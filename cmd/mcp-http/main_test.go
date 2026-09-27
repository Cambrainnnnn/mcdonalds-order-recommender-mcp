package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"meal-recommendation/internal/adapters/inbound/mcpserver"
	"meal-recommendation/internal/infrastructure/bootstrap"
	"meal-recommendation/internal/infrastructure/config"
)

func TestRequiredConfiguration(t *testing.T) {
	for _, token := range []string{"", " ", "bad token", "token\n"} {
		if err := validateConfig(config.Config{DatabaseURL: "postgres://db"}, token); err == nil {
			t.Errorf("accepted invalid token %q", token)
		}
	}
	if err := validateConfig(config.Config{}, "secret"); err == nil {
		t.Fatal("accepted missing database")
	}
	if err := validateConfig(config.Config{DatabaseURL: "postgres://db"}, "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAccessControl(t *testing.T) {
	handler := newHandler(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil), "secret")
	for _, method := range []string{"POST", "GET", "DELETE", "OPTIONS"} {
		for _, auth := range []string{"", "Bearer wrong", "Basic secret", "Bearer ", "Bearer secret extra"} {
			r := httptest.NewRequest(method, "/mcp", nil)
			r.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("%s %q: %d", method, auth, w.Code)
			}
		}
	}
	for _, tc := range []struct {
		path, origin string
		want         int
	}{
		{"/healthz", "", 200},
		{"/mcp", "https://untrusted.example", 403},
		{"/mcp", "", 405},         // Stateless MCP has no standalone SSE stream.
		{"/v1/products", "", 404}, // No unauthenticated admin API is mounted.
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.path != "/healthz" {
			r.Header.Set("Authorization", "bearer secret")
		}
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.path, w.Code, tc.want)
		}
	}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Add("Authorization", "Bearer secret")
	r.Header.Add("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate credentials: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "/mcp", strings.NewReader(strings.Repeat(" ", (1<<20)+1)))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request: %d %s", w.Code, w.Body.String())
	}
}

type bearerTransport struct{ base http.RoundTripper }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer secret")
	return b.base.RoundTrip(r)
}

func TestRemoteMCPToolsAndSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := bootstrap.New(ctx, config.Config{SessionTTL: time.Minute}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := httptest.NewServer(newHandler(mcpserver.New(runtime.Recommendations, runtime.Products), "secret"))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "remote-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   server.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{server.Client().Transport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 6 {
		t.Fatalf("got %d tools", len(list.Tools))
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("%s: %+v", name, result.Content)
		}
		return result
	}
	result := call("start_recommendation_session", map[string]any{})
	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &started); err != nil {
		t.Fatal(err)
	}
	if started.Session.ID == "" {
		t.Fatalf("missing session ID: %s", payload)
	}
	call("get_recommendation_session", map[string]any{"sessionId": started.Session.ID})
	call("list_product_catalog", map[string]any{})
}
