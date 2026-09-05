package mcdmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

func TestClientInitializesSessionAndCallsTool(t *testing.T) {
	var mu sync.Mutex
	methods := make([]string, 0)
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json, text/event-stream" {
			t.Errorf("Accept=%q", got)
		}
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		switch request.Method {
		case "initialize":
			return rpcResultResponse(request.ID, map[string]any{"protocolVersion": protocolVersion}, "session-1"), nil
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				t.Errorf("missing session")
			}
			return emptyResponse(http.StatusAccepted), nil
		case "tools/call":
			return rpcResultResponse(request.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "prefix {\"success\":true,\"data\":{\"price\":1234}} suffix"}}}, ""), nil
		}
		return emptyResponse(http.StatusBadRequest), nil
	})}
	client := NewClient("https://mcp.test", "token", httpClient)
	var result mcdResponse[priceData]
	if err := client.CallTool(context.Background(), "calculate-price", map[string]any{}, &result); err != nil {
		t.Fatalf("CallTool() error=%v", err)
	}
	if result.Data.Price != 1234 {
		t.Fatalf("price=%d", result.Data.Price)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != "initialize" || methods[2] != "tools/call" {
		t.Fatalf("methods=%#v", methods)
	}
}

func TestClientMapsAuthenticationAndRateLimitErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, want: ErrUnauthorized},
		{name: "rate limited", status: http.StatusTooManyRequests, want: ErrRateLimited},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient("https://mcp.test", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return emptyResponse(test.status), nil
			})})
			var result any
			err := client.CallTool(context.Background(), "query-meals", map[string]any{}, &result)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestCatalogLoadsMenuDetailsCouponsAndUsesCache(t *testing.T) {
	toolCalls := map[string]int{}
	client := newMCPClient(t, func(name string, args map[string]any) any {
		toolCalls[name]++
		switch name {
		case "query-meals":
			return map[string]any{"success": true, "data": map[string]any{"meals": map[string]any{"P1": map[string]any{"name": "巨无霸", "currentPrice": "25.5"}}}}
		case "query-meal-detail":
			return map[string]any{"success": true, "data": map[string]any{"code": "P1", "rounds": []map[string]any{{"id": 1, "name": "配餐", "minQuantity": 1, "maxQuantity": 1, "choices": []map[string]any{{"code": "C1", "name": "薯条", "quantity": 1}}}}}}
		case "query-store-coupons":
			return map[string]any{
				"success": true,
				"data": []map[string]any{{
					"couponId": "ID1", "couponCode": "CP1",
					"products": []map[string]any{{"productCode": "P1"}},
				}},
			}
		}
		return nil
	})
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	catalog := NewCatalog(client, func() time.Time { return now }, time.Minute, time.Hour, 5)
	requirement := domain.Requirement{OrderMode: domain.OrderModePickup, Location: domain.Location{StoreCode: "S1"}, MustHave: []domain.ProductRequest{{Query: "巨无霸", Quantity: 1}}}
	supply, err := catalog.LoadSupply(context.Background(), requirement)
	if err != nil {
		t.Fatal(err)
	}
	if len(supply.Products) != 1 || supply.Products[0].Price.Amount != 2550 || len(supply.Products[0].ChoiceGroups) != 1 || len(supply.Coupons) != 1 {
		t.Fatalf("supply=%#v", supply)
	}
	if _, err = catalog.LoadSupply(context.Background(), requirement); err != nil {
		t.Fatal(err)
	}
	if toolCalls["query-meals"] != 1 {
		t.Fatalf("menu calls=%d want 1", toolCalls["query-meals"])
	}
}

func TestPricingUsesStaleCacheWhenLiveCallFails(t *testing.T) {
	fail := false
	client := newMCPClient(t, func(name string, args map[string]any) any {
		if fail {
			return map[string]any{"success": false, "message": "down"}
		}
		return map[string]any{"success": true, "data": map[string]any{"price": 2200}}
	})
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	pricing := NewPricing(client, func() time.Time { return now }, time.Minute, time.Hour)
	request := app.OfficialPriceRequest{StoreCode: "S1", OrderMode: domain.OrderModePickup, Items: []app.OfficialPriceItem{{ProductCode: "P1", Quantity: 1}}}
	quote, err := pricing.Calculate(context.Background(), request)
	if err != nil || quote.Amount.Amount != 2200 {
		t.Fatalf("quote=%#v err=%v", quote, err)
	}
	now = now.Add(2 * time.Minute)
	fail = true
	quote, err = pricing.Calculate(context.Background(), request)
	if err != nil || quote.Confidence != domain.PriceVerifiedPast {
		t.Fatalf("stale quote=%#v err=%v", quote, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func newMCPClient(t *testing.T, tool func(string, map[string]any) any) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "initialize":
			return rpcResultResponse(request.ID, map[string]any{"protocolVersion": protocolVersion}, "s"), nil
		case "notifications/initialized":
			return emptyResponse(http.StatusAccepted), nil
		case "tools/call":
			raw, _ := json.Marshal(request.Params)
			var decoded struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(raw, &decoded)
			return rpcResultResponse(request.ID, map[string]any{"structuredContent": tool(decoded.Name, decoded.Arguments)}, ""), nil
		}
		return emptyResponse(http.StatusBadRequest), nil
	})}
	return NewClient("https://mcp.test", "token", httpClient)
}

func rpcResultResponse(id uint64, result any, sessionID string) *http.Response {
	var body bytes.Buffer
	_ = json.NewEncoder(&body).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body.Bytes()))}
	response.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		response.Header.Set("Mcp-Session-Id", sessionID)
	}
	return response
}

func emptyResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}
}
