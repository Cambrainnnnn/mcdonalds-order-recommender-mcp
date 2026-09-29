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

// Exercise the real application and engine through MCP, not a fake recommendation
// response: offer titles and selected SKU metadata must survive the entire path.
type namedOffers struct{}

func (namedOffers) ListEligible(context.Context, domain.Requirement) ([]app.OfferVersion, error) {
	return []app.OfferVersion{{
		ID: "offer-version", Title: "早餐随心配", Source: "maintained",
		Price:        domain.PriceQuote{Amount: domain.Money{Amount: 1000, Currency: "CNY"}},
		ChoiceGroups: []app.OfferChoiceGroup{{ID: "drink", Name: "饮品二选一", MinSelect: 1, MaxSelect: 1}},
		Components: []app.OfferComponent{
			{CanonicalProductID: "burger", Quantity: 1},
			{CanonicalProductID: "soy-small", Quantity: 1, ChoiceGroupID: "drink", ExtraPrice: 100},
			{CanonicalProductID: "soy-large", Quantity: 1, ChoiceGroupID: "drink", ExtraPrice: 200},
		},
	}}, nil
}

type namedProducts []*productdomain.Product

func (p namedProducts) List(context.Context) ([]*productdomain.Product, error) { return p, nil }

func TestRecommendationNamesOverMCP(t *testing.T) {
	products := namedProducts{
		{ID: "burger", Brand: "mcd", Name: "麦香鸡", Specification: "1个", Status: productdomain.StatusDisabled, SalesScopes: []productdomain.SalesScope{productdomain.SalesScopeOfferComponent}},
		{ID: "soy-small", Brand: "mcd", Name: "豆浆", Specification: "小杯", Status: productdomain.StatusActive, SalesScopes: []productdomain.SalesScope{productdomain.SalesScopeOfferComponent}},
		{ID: "soy-large", Brand: "mcd", Name: "豆浆", Specification: "大杯", Status: productdomain.StatusActive, SalesScopes: []productdomain.SalesScope{productdomain.SalesScopeOfferComponent}},
		{ID: "fries", Brand: "mcd", Name: "薯条", Specification: "中份", Status: productdomain.StatusActive, SalesScopes: []productdomain.SalesScope{productdomain.SalesScopeStandalone}, Price: &productdomain.Money{Amount: 500, Currency: "CNY"}},
	}
	for _, standalone := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected offer plus supplement", true: "standalone"}[standalone], func(t *testing.T) {
			ctx := context.Background()
			service := app.NewService(memory.NewSessionRepository(), ids.Default, time.Now, time.Hour, app.WithLocalCatalogDependencies(namedOffers{}, products, domain.NewEngine(), domain.ScoringPolicy{}))
			catalog := productsapp.NewService(memory.NewProductRepository(), ids.Default, time.Now)
			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			serverSession, err := New(service, catalog).Connect(ctx, serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()
			call := func(name string, args any, output any) {
				t.Helper()
				result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				if result.IsError {
					t.Fatalf("%s: %+v", name, result.Content)
				}
				payload, err := json.Marshal(result.StructuredContent)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(payload, output); err != nil {
					t.Fatal(err)
				}
			}
			var session sessionOutput
			call("start_recommendation_session", map[string]any{}, &session)
			wants := []domain.ProductRequest{{ResolvedProductID: "fries", Quantity: 2}}
			if !standalone {
				wants = append(wants, domain.ProductRequest{ResolvedProductID: "burger", Quantity: 1}, domain.ProductRequest{ResolvedProductID: "soy-small", Quantity: 1})
			}
			call("update_meal_requirement", updateRequirementInput{SessionID: session.Session.ID, Requirement: domain.Requirement{MustHave: wants}}, &session)
			// Decode into generic JSON to verify public field names rather than only Go fields.
			var output struct {
				Result struct {
					Recommendations []struct {
						Candidate struct {
							TotalPrice domain.Money `json:"totalPrice"`
							Legs       []struct {
								Title    string           `json:"offerTitle"`
								Version  string           `json:"offerVersionId"`
								Products []map[string]any `json:"products"`
							} `json:"purchaseLegs"`
						} `json:"candidate"`
					} `json:"recommendations"`
				} `json:"result"`
			}
			call("generate_meal_recommendations", sessionIDInput{SessionID: session.Session.ID}, &output)
			if len(output.Result.Recommendations) != 1 {
				t.Fatalf("recommendations=%+v", output)
			}
			candidate := output.Result.Recommendations[0].Candidate
			wantPrice, wantCount := int64(2100), 3
			if standalone {
				wantPrice, wantCount = 1000, 1
			}
			if candidate.TotalPrice.Amount != wantPrice {
				t.Fatalf("price=%d", candidate.TotalPrice.Amount)
			}
			seen := map[string]bool{}
			offerCount := 0
			for _, leg := range candidate.Legs {
				if leg.Version != "" {
					offerCount++
				}
				if leg.Version != "" && leg.Title != "早餐随心配" {
					t.Fatalf("offer title=%q", leg.Title)
				}
				for _, item := range leg.Products {
					id := item["canonicalProductId"].(string)
					seen[id] = true
					var expected *productdomain.Product
					for _, p := range products {
						if p.ID == id {
							expected = p
						}
					}
					if expected == nil || item["name"] != expected.Name || item["specification"] != expected.Specification || item["brand"] != expected.Brand {
						t.Fatalf("display fields=%+v", item)
					}
					if id == "fries" && item["quantity"] != float64(2) {
						t.Fatalf("quantity=%+v", item)
					}
					if id == "soy-small" && (item["choiceGroupName"] != "饮品二选一" || item["extraPrice"] != float64(100)) {
						t.Fatalf("selection=%+v", item)
					}
				}
			}
			wantOffers := 1
			if standalone {
				wantOffers = 0
			}
			if offerCount != wantOffers {
				t.Fatalf("offer count=%d, want %d", offerCount, wantOffers)
			}
			if len(seen) != wantCount || seen["soy-large"] {
				t.Fatalf("selected products=%v", seen)
			}
		})
	}
}
