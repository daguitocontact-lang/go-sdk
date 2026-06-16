package daguito

import (
	"context"
	"encoding/json"
	"fmt"
)

// ModelsService wraps the `/v1/models` endpoint. The endpoint returns the
// curated LLM picker list — the same rows the dashboard's flow builder
// shows — so SDK / MCP integrations can render a model selector and pass
// the chosen id into flow node configs (e.g. `ai_agent.model`,
// pre-recorded / realtime LLM nodes inside graph flows).
//
// Auth is the same `dgsk_acc_…` bearer key used by the other admin
// services — the list is scoped to the caller's org.
type ModelsService struct {
	transport *adminTransport
}

// Model is one curated LLM entry. ID is the `<provider>/<model>` string
// that flow node configs expect (e.g. `google/gemini-3.5-flash`).
// DisplayName + Tier are UI hints — Tier is the free-form bucket the
// dashboard surfaces ("fast", "balanced", "smart"), empty when the
// operator hasn't tagged it.
type Model struct {
	Provider    string `json:"provider"`
	ID          string `json:"model_id"`
	DisplayName string `json:"display_name"`
	Tier        string `json:"tier,omitempty"`
}

type modelsListResponse struct {
	OrgID  string  `json:"org_id"`
	Models []Model `json:"models"`
}

// List returns the curated LLM models available to the caller's org.
func (s *ModelsService) List(ctx context.Context) ([]Model, error) {
	raw, err := s.transport.requestJSON(ctx, "GET", "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	var parsed modelsListResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return parsed.Models, nil
}
