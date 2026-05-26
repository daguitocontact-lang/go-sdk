package daguito

import (
	"context"
	"encoding/json"
	"fmt"
)

// AnalyticsService reads the caller's org usage snapshot from
// GET /v1/org/analytics. The org is resolved from the API key —
// there is no input.
type AnalyticsService struct {
	transport *adminTransport
}

// OrgLLMUsage is today's LLM activity for the caller's org.
type OrgLLMUsage struct {
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
}

// OrgMessagesUsage is the message volume snapshot returned by GetOrg.
// Inbound is keyed by channel (whatsapp, widget, instagram, …) — the SDK
// keeps it as a map so new channels show up without a code change.
type OrgMessagesUsage struct {
	Inbound          map[string]int `json:"inbound"`
	OutboundLast24h  int            `json:"outbound_last_24h"`
}

// OrgHTTPUsage is the HTTP request volume for the org over the current day.
type OrgHTTPUsage struct {
	Total  int            `json:"total"`
	ByPath map[string]int `json:"by_path"`
}

// OrgBudgetSnapshot is the budget portion of OrgAnalytics. Caps are
// micro-USD (1e-6 USD) — same unit as the Budgets surface. Cap fields
// are pointers because the server returns JSON `null` when the cap is
// unset.
type OrgBudgetSnapshot struct {
	OrgCapMicroUsd *int64 `json:"org_cap_micro_usd"`
	OrgMtdMicroUsd int64  `json:"org_mtd_micro_usd"`
	KeyCapMicroUsd *int64 `json:"key_cap_micro_usd"`
	KeyMtdMicroUsd int64  `json:"key_mtd_micro_usd"`
}

// OrgAnalytics is the wire shape of GET /v1/org/analytics.
type OrgAnalytics struct {
	OrgID         string             `json:"org_id"`
	LLMToday      OrgLLMUsage        `json:"llm_today"`
	MessagesToday OrgMessagesUsage   `json:"messages_today"`
	HTTPToday     OrgHTTPUsage       `json:"http_today"`
	Budget        OrgBudgetSnapshot  `json:"budget"`
}

// GetOrg returns the analytics snapshot for the caller's org.
func (s *AnalyticsService) GetOrg(ctx context.Context) (*OrgAnalytics, error) {
	raw, err := s.transport.requestJSON(ctx, "GET", "/v1/org/analytics", nil)
	if err != nil {
		return nil, err
	}
	out := &OrgAnalytics{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
