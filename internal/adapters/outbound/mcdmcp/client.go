package mcdmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

const protocolVersion = "2025-06-18"
const maxResponseBytes = 8 << 20

var (
	ErrNotConfigured = errors.New("McDonald's MCP token is not configured")
	ErrUnauthorized  = errors.New("McDonald's MCP authorization failed")
	ErrRateLimited   = errors.New("McDonald's MCP rate limit exceeded")
)

type Client struct {
	endpoint    string
	token       string
	httpClient  *http.Client
	nextID      atomic.Uint64
	callMu      sync.Mutex
	sessionMu   sync.Mutex
	sessionID   string
	initialized bool
}

func NewClient(endpoint, token string, httpClient *http.Client) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), token: strings.TrimSpace(token), httpClient: httpClient}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}
type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func (c *Client) CallTool(ctx context.Context, name string, arguments any, destination any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if c.token == "" {
		return ErrNotConfigured
	}
	if err := c.ensureInitialized(ctx); err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		response, status, err := c.post(ctx, rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: "tools/call", Params: map[string]any{"name": name, "arguments": arguments}}, true)
		if status == http.StatusNotFound && attempt == 0 {
			c.resetSession()
			if err := c.ensureInitialized(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if response.Error != nil {
			return fmt.Errorf("MCP tool %s: code %d: %s", name, response.Error.Code, response.Error.Message)
		}
		var result toolResult
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return fmt.Errorf("decode MCP tool result: %w", err)
		}
		if result.IsError {
			return fmt.Errorf("MCP tool %s returned an error: %s", name, toolText(result))
		}
		payload := result.StructuredContent
		if len(payload) == 0 {
			payload = extractJSON([]byte(toolText(result)))
		}
		if len(payload) == 0 {
			return fmt.Errorf("MCP tool %s returned no structured JSON", name)
		}
		if err := json.Unmarshal(payload, destination); err != nil {
			return fmt.Errorf("decode MCP tool %s payload: %w", name, err)
		}
		return nil
	}
	return errors.New("MCP session recovery failed")
}

func (c *Client) ensureInitialized(ctx context.Context) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.initialized {
		return nil
	}
	response, _, err := c.post(ctx, rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: "initialize", Params: map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "meal-recommendation", "version": "1.0.0"}}}, false)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("initialize MCP: code %d: %s", response.Error.Code, response.Error.Message)
	}
	c.initialized = true
	_, _, err = c.post(ctx, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}, true)
	return err
}

func (c *Client) resetSession() {
	c.sessionMu.Lock()
	c.initialized = false
	c.sessionID = ""
	c.sessionMu.Unlock()
}

func (c *Client) post(ctx context.Context, message rpcRequest, withSession bool) (rpcResponse, int, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return rpcResponse{}, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return rpcResponse{}, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", protocolVersion)
	if withSession && c.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return rpcResponse{}, 0, fmt.Errorf("call MCP: %w", err)
	}
	defer response.Body.Close()
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		c.sessionID = session
	}
	if response.StatusCode == http.StatusUnauthorized {
		return rpcResponse{}, response.StatusCode, ErrUnauthorized
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return rpcResponse{}, response.StatusCode, ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return rpcResponse{}, response.StatusCode, fmt.Errorf("MCP HTTP status %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusAccepted || message.ID == 0 {
		return rpcResponse{}, response.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return rpcResponse{}, response.StatusCode, err
	}
	if len(body) > maxResponseBytes {
		return rpcResponse{}, response.StatusCode, errors.New("MCP response too large")
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		body, err = lastSSEData(body)
		if err != nil {
			return rpcResponse{}, response.StatusCode, err
		}
	}
	var decoded rpcResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return rpcResponse{}, response.StatusCode, fmt.Errorf("decode MCP response: %w", err)
	}
	return decoded, response.StatusCode, nil
}

func lastSSEData(body []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxResponseBytes)
	var last []byte
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			last = append([]byte(nil), strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(last) == 0 {
		return nil, errors.New("MCP SSE response contained no data")
	}
	return last, nil
}

func toolText(result toolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}
func extractJSON(text []byte) []byte {
	startObj, endObj := bytes.IndexByte(text, '{'), bytes.LastIndexByte(text, '}')
	startArr, endArr := bytes.IndexByte(text, '['), bytes.LastIndexByte(text, ']')
	if startObj >= 0 && endObj > startObj && (startArr < 0 || startObj < startArr) {
		return text[startObj : endObj+1]
	}
	if startArr >= 0 && endArr > startArr {
		return text[startArr : endArr+1]
	}
	return nil
}
