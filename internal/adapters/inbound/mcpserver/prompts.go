package mcpserver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const recommendationPromptName = "recommend_meal"

const recommendationInstructions = "Use the recommend_meal prompt for the standard recommendation and troubleshooting workflow when supported by the client. Resolve product identity before updating a requirement; ambiguous specifications require clarification. Catalog matches and ready sessions do not establish purchase availability. Candidates use eligible published offers and priced standalone products. No eligible candidates does not prove offers are absent. Preserve the saved requirement and distinguish observed tool results from unverified causes."

//go:embed recommendation_prompt.md
var recommendationPrompt string

func registerRecommendationPrompt(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        recommendationPromptName,
		Title:       "标准餐品推荐与排查",
		Description: "Resolve products, recommend maintained meals, and diagnose empty results without guessing server state (v1).",
		Arguments: []*mcp.PromptArgument{
			{Name: "request", Description: "Optional user meal request; otherwise use the current conversation."},
			{Name: "session_id", Description: "Optional existing session to inspect before continuing or diagnosing a failed recommendation."},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		for name := range req.Params.Arguments {
			if name != "request" && name != "session_id" {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown recommend_meal argument %q", name)}
			}
		}
		messages := []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: recommendationPrompt}}}
		// Keep caller input separate from the maintained workflow. JSON escaping
		// preserves arbitrary text without interpolating it into instructions.
		if len(req.Params.Arguments) > 0 {
			data, err := json.Marshal(req.Params.Arguments)
			if err != nil {
				return nil, err
			}
			messages = append(messages, &mcp.PromptMessage{Role: "user", Content: &mcp.TextContent{Text: "用户提供的上下文（JSON）：\n" + string(data)}})
		}
		return &mcp.GetPromptResult{Description: "标准餐品推荐与排查 v1", Messages: messages}, nil
	})
}
