package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFlowsUpsertAgentHappyPath(t *testing.T) {
	var captured *http.Request
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, map[string]any{
			"flow_id":    "flow_abc123",
			"slug":       "medical_chatbot",
			"name":       "Dr. Midulabs",
			"webhook_id": "wh_xyz789",
			"created":    true,
		})
	})
	defer srv.Close()

	temp := 0.3
	maxTok := 1200
	historyTurns := 20
	recentTurns := 20
	maxToolIter := 25
	result, err := client.Flows.UpsertAgent(context.Background(), AgentFlowSpec{
		Slug:              "medical_chatbot",
		Name:              "Dr. Midulabs",
		Provider:          "openrouter",
		Model:             "deepseek/deepseek-v4-flash:nitro",
		SystemPrompt:      "You are a careful medical assistant.",
		Temperature:       &temp,
		MaxTokens:         &maxTok,
		HistoryTurns:      &historyTurns,
		RecentTurns:       &recentTurns,
		MaxToolIterations: &maxToolIter,
		Tools: []FlowToolRef{
			{Kind: "handler", Name: "search_knowledge"},
			{Kind: "handler", Name: "web_search", Config: map[string]any{"max_results": 5}},
		},
		ContextMemoryKeys: []string{"patient_id", "consultation_id"},
	})
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	if result.FlowID != "flow_abc123" || result.WebhookID != "wh_xyz789" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !result.Created {
		t.Fatalf("expected created=true, got %+v", result)
	}
	if result.Slug != "medical_chatbot" || result.Name != "Dr. Midulabs" {
		t.Fatalf("unexpected echo fields: %+v", result)
	}

	if captured.Method != "POST" {
		t.Fatalf("want POST, got %s", captured.Method)
	}
	if !strings.HasSuffix(captured.URL.Path, "/v1/flows/upsert-agent") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer dgsk_acc_test" {
		t.Fatalf("missing/incorrect bearer: %q", got)
	}

	if capturedBody["slug"] != "medical_chatbot" {
		t.Fatalf("slug not posted: %+v", capturedBody)
	}
	if capturedBody["provider"] != "openrouter" {
		t.Fatalf("provider not posted: %+v", capturedBody)
	}
	if temp, _ := capturedBody["temperature"].(float64); temp != 0.3 {
		t.Fatalf("temperature not posted: %+v", capturedBody)
	}
	tools, ok := capturedBody["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("tools not posted correctly: %+v", capturedBody)
	}
	firstTool, _ := tools[0].(map[string]any)
	if firstTool["kind"] != "handler" || firstTool["name"] != "search_knowledge" {
		t.Fatalf("first tool wrong: %+v", firstTool)
	}
}

func TestFlowsUpsertAgentForbiddenMapsToInvalidToken(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 403, map[string]any{"error": "account key required"})
	})
	defer srv.Close()

	_, err := client.Flows.UpsertAgent(context.Background(), AgentFlowSpec{
		Slug:         "x",
		Name:         "x",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash",
		SystemPrompt: "x",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	var he *httpError
	if !errors.As(err, &he) || he.Status != 403 {
		t.Fatalf("want httpError with 403, got %v", err)
	}
}

func TestFlowsUpsertAgentOmitsNilPointers(t *testing.T) {
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, map[string]any{
			"flow_id":    "flow_min",
			"slug":       "minimal",
			"name":       "Minimal",
			"webhook_id": "wh_min",
			"created":    true,
		})
	})
	defer srv.Close()

	// First round: every optional pointer field is nil. omitempty must
	// drop them from the wire body entirely.
	if _, err := client.Flows.UpsertAgent(context.Background(), AgentFlowSpec{
		Slug:         "minimal",
		Name:         "Minimal",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash",
		SystemPrompt: "be brief",
	}); err != nil {
		t.Fatalf("UpsertAgent minimal: %v", err)
	}
	for _, key := range []string{
		"temperature", "max_tokens", "history_turns",
		"recent_turns", "max_tool_iterations",
		"tools", "memory_facts_schema", "memory_summary_config",
		"context_memory_keys",
	} {
		if _, exists := capturedBody[key]; exists {
			t.Fatalf("optional field %q must be omitted when nil: %+v", key, capturedBody)
		}
	}

	// Second round: same fields populated -> they must round-trip on the
	// wire.
	srv.Close()
	capturedBody = nil
	temp := 0.7
	maxTok := 800
	client, srv = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, map[string]any{
			"flow_id":    "flow_full",
			"slug":       "full",
			"name":       "Full",
			"webhook_id": "wh_full",
			"created":    false,
		})
	})
	defer srv.Close()

	result, err := client.Flows.UpsertAgent(context.Background(), AgentFlowSpec{
		Slug:         "full",
		Name:         "Full",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash",
		SystemPrompt: "be thorough",
		Temperature:  &temp,
		MaxTokens:    &maxTok,
	})
	if err != nil {
		t.Fatalf("UpsertAgent full: %v", err)
	}
	if got, _ := capturedBody["temperature"].(float64); got != 0.7 {
		t.Fatalf("temperature not posted: %+v", capturedBody)
	}
	if got, _ := capturedBody["max_tokens"].(float64); int(got) != 800 {
		t.Fatalf("max_tokens not posted: %+v", capturedBody)
	}
	if result.Created {
		t.Fatalf("expected created=false on second upsert, got %+v", result)
	}
}
