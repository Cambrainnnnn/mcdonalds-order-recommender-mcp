package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"meal-recommendation/internal/adapters/outbound/memory"
	productsapp "meal-recommendation/internal/application/products"
	app "meal-recommendation/internal/application/recommendation"
	productdomain "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
	"meal-recommendation/internal/infrastructure/ids"
)

type fakeRecommendations struct {
	session *domain.Session
}

func (f *fakeRecommendations) StartSession(_ context.Context, command app.StartSessionCommand) (*domain.Session, error) {
	f.session = &domain.Session{
		ID:    "session-1",
		State: domain.SessionCollecting,
		Requirement: domain.Requirement{
			OrderMode: command.OrderMode,
			Location: domain.Location{
				City:         command.City,
				StoreCode:    command.StoreCode,
				StoreKeyword: command.StoreKeyword,
			},
		},
	}
	return f.session, nil
}

func (f *fakeRecommendations) GetSession(context.Context, string) (*domain.Session, error) {
	return f.session, nil
}

func (f *fakeRecommendations) UpdateRequirement(_ context.Context, _ string, requirement domain.Requirement) (*domain.Session, error) {
	f.session.Requirement = requirement
	f.session.State = domain.SessionReady
	return f.session, nil
}

func (f *fakeRecommendations) GenerateRecommendations(context.Context, string) (*app.RecommendationResponse, error) {
	return &app.RecommendationResponse{SessionID: f.session.ID, Status: string(domain.SessionCompleted)}, nil
}

type fakeProducts struct{ *productsapp.Service }

func (fakeProducts) List(_ context.Context, filter productsapp.Filter) ([]*productdomain.Product, error) {
	return []*productdomain.Product{{
		ID:        "product-1",
		Brand:     filter.Brand,
		Name:      "薯条",
		Status:    productdomain.StatusActive,
		CreatedAt: time.Unix(0, 0).UTC(),
		UpdatedAt: time.Unix(0, 0).UTC(),
	}}, nil
}

func TestServerDiscoversToolsAndStartsSession(t *testing.T) {
	ctx := context.Background()
	catalog := productsapp.NewService(memory.NewProductRepository(), ids.Default, time.Now)
	for _, specification := range []string{"小杯", "大杯"} {
		if _, err := catalog.Create(ctx, productsapp.CreateCommand{Fields: productsapp.Fields{Brand: "mcd", Name: "豆浆", Specification: specification}}); err != nil {
			t.Fatal(err)
		}
	}
	server := New(&fakeRecommendations{}, fakeProducts{Service: catalog})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := map[string]bool{
		"start_recommendation_session":  false,
		"get_recommendation_session":    false,
		"update_meal_requirement":       false,
		"generate_meal_recommendations": false,
		"list_product_catalog":          false,
		"search_product_catalog":        false,
	}
	for _, tool := range tools.Tools {
		if _, exists := wantTools[tool.Name]; exists {
			wantTools[tool.Name] = true
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("tool %q was not discovered", name)
		}
	}

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "start_recommendation_session",
		Arguments: map[string]any{
			"orderMode":    "pickup",
			"city":         "上海市",
			"storeKeyword": "中山公园",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned an error: %#v", result.Content)
	}

	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output sessionOutput
	if err := json.Unmarshal(payload, &output); err != nil {
		t.Fatal(err)
	}
	if output.Session == nil || output.Session.ID != "session-1" {
		t.Fatalf("unexpected session output: %#v", output.Session)
	}
	if output.Session.Requirement.Location.StoreKeyword != "中山公园" {
		t.Fatalf("unexpected requirement: %#v", output.Session.Requirement)
	}

	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "search_product_catalog",
		Arguments: map[string]any{
			"brand": "mcd",
			"queries": []map[string]any{
				{"name": "豆浆", "specification": "小杯"},
				{"name": "豆浆"},
				{"name": "豆浆", "specification": "中杯"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("search tool error: %#v", result.Content)
	}
	payload, err = json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var search productsapp.SearchResponse
	if err := json.Unmarshal(payload, &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 3 || search.Results[0].Status != productsapp.SearchExact || search.Results[0].ResolvedProductID == "" || search.Results[1].Status != productsapp.SearchAmbiguous || len(search.Results[1].Candidates) != 2 || search.Results[2].Status != productsapp.SearchNotFound {
		t.Fatalf("unexpected search output: %s", payload)
	}
	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "search_product_catalog", Arguments: map[string]any{"queries": []any{}}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("empty batch should produce a tool error")
	}
}

func TestCatalogToolsResolveDisabledProduct(t *testing.T) {
	ctx := context.Background()
	catalog := productsapp.NewService(memory.NewProductRepository(), ids.Default, time.Now)
	p, err := catalog.Create(ctx, productsapp.CreateCommand{Fields: productsapp.Fields{
		Brand: "mcd", Name: "东南亚风味椰香鸡扒堡", Specification: "1个", Status: productdomain.StatusDisabled,
	}})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(&fakeRecommendations{}, catalog).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_product_catalog", map[string]any{"query": "椰香"}},
		{"search_product_catalog", map[string]any{"queries": []map[string]any{{"name": p.Name, "specification": p.Specification}}}},
	} {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("%s: %+v", tc.name, result.Content)
		}
		payload, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if tc.name == "list_product_catalog" {
			var out productsOutput
			if err := json.Unmarshal(payload, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Products) != 1 || out.Products[0].ID != p.ID || out.Products[0].Status != productdomain.StatusDisabled {
				t.Fatalf("catalog=%s", payload)
			}
		} else {
			var out productsapp.SearchResponse
			if err := json.Unmarshal(payload, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Results) != 1 || out.Results[0].Status != productsapp.SearchExact || out.Results[0].ResolvedProductID != p.ID {
				t.Fatalf("search=%s", payload)
			}
		}
	}
}
