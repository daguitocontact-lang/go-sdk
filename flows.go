package daguito

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// FlowsService administers flow definitions on Daguito. Today exposes the
// "agent" preset (system_prompt + tools + model + memory + a trigger →
// agent graph built server-side). Full flow CRUD with custom graphs is
// Phase 3.
type FlowsService struct {
	transport *adminTransport
}

// AgentFlowSpec is the high-level declaration of a conversational agent
// flow. Each call to UpsertAgent uses Slug as the org-scoped identity:
// the first call creates the flow + webhook, subsequent calls update the
// agent config. The webhook id stays stable across upserts.
type AgentFlowSpec struct {
	Slug              string        `json:"slug"`
	Name              string        `json:"name"`
	Provider          string        `json:"provider"`
	Model             string        `json:"model"`
	SystemPrompt      string        `json:"system_prompt"`
	Temperature       *float64      `json:"temperature,omitempty"`
	MaxTokens         *int          `json:"max_tokens,omitempty"`
	HistoryTurns      *int          `json:"history_turns,omitempty"`
	RecentTurns       *int          `json:"recent_turns,omitempty"`
	MaxToolIterations *int          `json:"max_tool_iterations,omitempty"`
	Tools             []FlowToolRef `json:"tools,omitempty"`
	MemoryFactsSchema   any      `json:"memory_facts_schema,omitempty"`
	MemorySummaryConfig any      `json:"memory_summary_config,omitempty"`
	ContextMemoryKeys   []string `json:"context_memory_keys,omitempty"`
}

// FlowToolRef declares a single tool the agent can call. Today only the
// `handler` kind (server-side registered tools like `search_knowledge`,
// `web_search`) is supported via this endpoint.
type FlowToolRef struct {
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

// AgentFlowResult is what the server returns from UpsertAgent.
type AgentFlowResult struct {
	FlowID    string `json:"flow_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	WebhookID string `json:"webhook_id"`
	Created   bool   `json:"created"`
}

// UpsertAgent creates a new agent flow on first call, updates it on
// subsequent calls. Identity is (org_id, slug). The org is resolved from
// the client's API key. Returns the canonical flow/webhook ids — store
// WebhookID and pass it to NewWebhookStreamSession.
func (s *FlowsService) UpsertAgent(
	ctx context.Context, spec AgentFlowSpec,
) (*AgentFlowResult, error) {
	raw, err := s.transport.requestJSON(ctx, "POST", "/v1/flows/upsert-agent", spec)
	if err != nil {
		return nil, err
	}
	out := &AgentFlowResult{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// ResolvedFlowWebhook is the streaming webhook a flow resolves to.
type ResolvedFlowWebhook struct {
	FlowID       string `json:"flow_id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	WebhookID    string `json:"webhook_id"`
	WebhookToken string `json:"webhook_token"`
}

// ResolveWebhook resolves a flow by slug (in the org the API key belongs
// to) and returns its streaming webhook id + a usable sk_wh_… token, so a
// client can open an AudioStream / WebhookStream session without hardcoding
// webhook credentials. The token is reused when the flow already has one.
func (s *FlowsService) ResolveWebhook(
	ctx context.Context, slug string,
) (*ResolvedFlowWebhook, error) {
	path := "/api/sdk/flows?slug=" + url.QueryEscape(slug)
	raw, err := s.transport.requestJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	out := &ResolvedFlowWebhook{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
