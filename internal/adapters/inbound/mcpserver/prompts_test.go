package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRecommendationPromptProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Prompt retrieval must work without a session or catalog access.
	server := New(&fakeRecommendations{}, fakeProducts{})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "prompt-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	init := cs.InitializeResult()
	if init.Capabilities.Prompts == nil || !strings.Contains(init.Instructions, "recommend_meal") {
		t.Fatalf("missing prompt capability or workflow instructions: %+v", init)
	}
	listed, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "recommend_meal" || len(listed.Prompts[0].Arguments) != 2 {
		t.Fatalf("unexpected prompts: %+v", listed)
	}
	baseline, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "recommend_meal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Messages) != 1 || baseline.Messages[0].Role != "user" {
		t.Fatalf("unexpected messages: %+v", baseline)
	}
	workflow, ok := baseline.Messages[0].Content.(*mcp.TextContent)
	if !ok || !strings.Contains(workflow.Text, "no eligible recommendation candidates") {
		t.Fatal("missing troubleshooting workflow")
	}
	args := map[string]string{"request": "汉堡+鸡翅\n含\"引号\"", "session_id": "session-123"}
	rendered, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "recommend_meal", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered.Messages) != 2 || rendered.Messages[0].Content.(*mcp.TextContent).Text != workflow.Text {
		t.Fatal("arguments changed the maintained workflow")
	}
	contextText := rendered.Messages[1].Content.(*mcp.TextContent).Text
	_, payload, found := strings.Cut(contextText, "\n")
	var got map[string]string
	if !found || json.Unmarshal([]byte(payload), &got) != nil || got["request"] != args["request"] || got["session_id"] != args["session_id"] {
		t.Fatalf("lost caller context: %s", contextText)
	}
	for _, params := range []*mcp.GetPromptParams{
		{Name: "unknown"},
		{Name: "recommend_meal", Arguments: map[string]string{"typo": "value"}},
	} {
		if _, err := cs.GetPrompt(ctx, params); err == nil {
			t.Fatalf("accepted invalid parameters: %+v", params)
		}
	}
}
