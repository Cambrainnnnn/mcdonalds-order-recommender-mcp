package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	productsapp "meal-recommendation/internal/application/products"
	app "meal-recommendation/internal/application/recommendation"
	productdomain "meal-recommendation/internal/domain/product"
	domain "meal-recommendation/internal/domain/recommendation"
)

type RecommendationUseCase interface {
	StartSession(context.Context, app.StartSessionCommand) (*domain.Session, error)
	GetSession(context.Context, string) (*domain.Session, error)
	UpdateRequirement(context.Context, string, domain.Requirement) (*domain.Session, error)
	GenerateRecommendations(context.Context, string) (*app.RecommendationResponse, error)
}

type ProductCatalog interface {
	List(context.Context, productsapp.Filter) ([]*productdomain.Product, error)
	Search(context.Context, productsapp.SearchRequest) (*productsapp.SearchResponse, error)
}

type startSessionInput struct {
	UserID       string           `json:"userId,omitempty" jsonschema:"Optional caller identifier."`
	OrderMode    domain.OrderMode `json:"orderMode,omitempty" jsonschema:"Optional maintained-offer filter: pickup, delivery, or drive_thru; omit when unspecified."`
	City         string           `json:"city,omitempty" jsonschema:"Reserved for future use; ignored in current catalog recommendations."`
	StoreCode    string           `json:"storeCode,omitempty" jsonschema:"Reserved for future use; ignored in current catalog recommendations."`
	StoreKeyword string           `json:"storeKeyword,omitempty" jsonschema:"Reserved for future use; ignored in current catalog recommendations."`
}

type sessionIDInput struct {
	SessionID string `json:"sessionId" jsonschema:"Recommendation session identifier returned by start_recommendation_session."`
}

type updateRequirementInput struct {
	SessionID   string             `json:"sessionId" jsonschema:"Recommendation session identifier."`
	Requirement domain.Requirement `json:"requirement" jsonschema:"Complete structured dining requirement. Existing values are replaced."`
}

type listProductsInput struct {
	Query    string `json:"query,omitempty" jsonschema:"Optional text matched against product ID, name, specification, and aliases."`
	Brand    string `json:"brand,omitempty" jsonschema:"Optional exact brand filter."`
	Category string `json:"category,omitempty" jsonschema:"Optional exact category filter."`
}

type sessionOutput struct {
	Session *domain.Session `json:"session"`
}

type recommendationsOutput struct {
	Result *app.RecommendationResponse `json:"result"`
}

type productsOutput struct {
	Products []*productdomain.Product `json:"products"`
}

func New(recommendations RecommendationUseCase, products ProductCatalog) *mcp.Server {
	if recommendations == nil {
		panic("mcpserver: recommendation use case is required")
	}
	if products == nil {
		panic("mcpserver: product catalog is required")
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "meal-recommendation",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "start_recommendation_session",
		Description: "Start a server-catalog meal recommendation session. City, store and order mode are not required; do not ask for location. Store the returned sessionId for later calls.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input startSessionInput) (*mcp.CallToolResult, sessionOutput, error) {
		session, err := recommendations.StartSession(ctx, app.StartSessionCommand{
			UserID:       input.UserID,
			OrderMode:    input.OrderMode,
			City:         input.City,
			StoreCode:    input.StoreCode,
			StoreKeyword: input.StoreKeyword,
		})
		return nil, sessionOutput{Session: session}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_recommendation_session",
		Description: "Read the current structured requirement and state of a recommendation session.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input sessionIDInput) (*mcp.CallToolResult, sessionOutput, error) {
		session, err := recommendations.GetSession(ctx, input.SessionID)
		return nil, sessionOutput{Session: session}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "update_meal_requirement",
		Description: "Replace the structured meal requirement for a session. Resolve product names with search_product_catalog first; only exact results supply resolvedProductId. Ask the user to clarify ambiguous results before selecting a product or treating candidates as alternatives.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input updateRequirementInput) (*mcp.CallToolResult, sessionOutput, error) {
		session, err := recommendations.UpdateRequirement(ctx, input.SessionID, input.Requirement)
		return nil, sessionOutput{Session: session}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "generate_meal_recommendations",
		Description: "Generate ranked recommendations for a complete session requirement using server-maintained published offers and priced standalone products. Returns offerTitle for offers and name, specification, brand and quantity for products, with choiceGroupName for named selections. Explain recommendations using these display fields; IDs are for tool calls, not user-facing names. Never invent missing names. No store lookup, stock check or official live pricing. City, store and order mode are not required.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input sessionIDInput) (*mcp.CallToolResult, recommendationsOutput, error) {
		result, err := recommendations.GenerateRecommendations(ctx, input.SessionID)
		return nil, recommendationsOutput{Result: result}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_product_catalog",
		Description: "Browse canonical products regardless of active/disabled status with optional substring and alias filters. Use search_product_catalog for batch name/specification resolution. Catalog status does not determine availability within offers; purchase eligibility is evaluated during recommendation.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listProductsInput) (*mcp.CallToolResult, productsOutput, error) {
		result, err := products.List(ctx, productsapp.Filter{
			Query:    input.Query,
			Brand:    input.Brand,
			Category: input.Category,
		})
		return nil, productsOutput{Products: result}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_product_catalog",
		Description: "Batch search canonical products regardless of active/disabled status by complete name and optional complete specification, ignoring case and surrounding whitespace. No alias or substring matching. This resolves product identity, not purchase availability; disabled products can still be selected within eligible offers. Returns exact (one candidate), ambiguous (multiple), or not_found (none) in input order, with all candidates sorted by brand, name, specification, and ID. Only exact returns resolvedProductId; ambiguous candidates require clarification and are not automatically allowed alternatives.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input productsapp.SearchRequest) (*mcp.CallToolResult, *productsapp.SearchResponse, error) {
		result, err := products.Search(ctx, input)
		return nil, result, err
	})

	return server
}
