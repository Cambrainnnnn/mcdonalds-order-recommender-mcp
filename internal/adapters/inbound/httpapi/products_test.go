package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"meal-recommendation/internal/adapters/outbound/memory"
	app "meal-recommendation/internal/application/products"
	domain "meal-recommendation/internal/domain/product"
	"meal-recommendation/internal/infrastructure/ids"
)

func TestProductHTTPWorkflow(t *testing.T) {
	service := app.NewService(memory.NewProductRepository(), ids.Default, time.Now)
	handler := NewHandler(sessionUseCaseStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithProductAdmin(service)).Routes()
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d body=%s", method, path, response.Code, response.Body.String())
		}
		return response
	}
	if result := call("GET", "/v1/admin/products", "", 200); strings.TrimSpace(result.Body.String()) != "[]" {
		t.Fatal(result.Body.String())
	}
	body := `{"brand":"mcd","name":"薯条","specification":"中份","category":"小食","aliases":["中薯"],"price":{"amount":999900,"currency":"cny"},"salesScopes":["offer_component"]}`
	created := call("POST", "/v1/admin/products", body, 201)
	var p domain.Product
	if err := json.Unmarshal(created.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/products/" + p.ID
	if created.Header().Get("Location") != path {
		t.Fatal("missing resource location")
	}
	if p.Price == nil || p.Price.Amount != 999900 || p.Price.Currency != "CNY" || len(p.SalesScopes) != 1 || p.SalesScopes[0] != domain.SalesScopeOfferComponent {
		t.Fatalf("sales fields: %+v", p)
	}
	call("GET", path, "", 200)
	call("POST", "/v1/admin/products", body, 409)
	matched := call("GET", "/v1/products?brand=mcd&q=%E4%B8%AD%E8%96%AF", "", 200)
	if !strings.Contains(matched.Body.String(), p.ID) {
		t.Fatal("alias search missed product")
	}
	disabled := `{"brand":"mcd","name":"薯条","specification":"中份","status":"disabled"}`
	call("PUT", path, disabled, 200)
	if result := call("GET", "/v1/products", "", 200); strings.TrimSpace(result.Body.String()) != "[]" {
		t.Fatal("picker returned disabled product")
	}
	if result := call("GET", "/v1/admin/products?status=disabled", "", 200); !strings.Contains(result.Body.String(), p.ID) {
		t.Fatal("admin omitted disabled product")
	}
	call("PUT", path, body, 200)
	if result := call("GET", "/v1/products", "", 200); strings.TrimSpace(result.Body.String()) != "[]" {
		t.Fatal("omitting status reactivated product")
	}
	call("PUT", path, `{"brand":"mcd","name":"薯条","specification":"中份","status":"active"}`, 200)
	call("GET", "/v1/admin/products/missing", "", 404)
	call("PUT", "/v1/admin/products/missing", body, 404)
	call("POST", "/v1/admin/products", `{"brand":"mcd"}`, 400)
	call("POST", "/v1/admin/products", `{"brand":"mcd","unknown":true}`, 400)
	call("POST", "/v1/admin/products", `{"brand":"mcd","name":"非法","specification":"1个","salesScopes":[]}`, 400)
	call("PUT", path, `{"id":"changed","brand":"mcd","name":"薯条","specification":"中份"}`, 400)
	call("POST", "/v1/admin/products", body+body, 400)
	call("GET", "/v1/admin/products?status=draft", "", 400)
	call("GET", "/v1/products?status=disabled", "", 400)
	call("GET", "/v1/products?limit=10", "", 400)
	call("GET", "/v1/products?brand=mcd&brand=other", "", 400)
	if result := call("DELETE", path, "", 405); result.Header().Get("Allow") != "GET, HEAD, PUT" {
		t.Fatal("invalid Allow header")
	}
}
