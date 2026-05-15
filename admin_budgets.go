package daguito

import (
	"context"
	"encoding/json"
	"fmt"
)

// BudgetsService manages the org-level monthly budget. Per-key budgets
// live on AccountKeysService.SetBudget / PublicKeysService.SetBudget.
type BudgetsService struct {
	transport *adminTransport
}

// GetOrg returns the org-wide budget + MTD spend.
func (s *BudgetsService) GetOrg(ctx context.Context) (*OrgBudget, error) {
	raw, err := s.transport.requestJSON(ctx, "GET", "/v1/account/budget", nil)
	if err != nil {
		return nil, err
	}
	out := &OrgBudget{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// SetOrg updates the org-wide monthly cap. Pass nil to clear it.
func (s *BudgetsService) SetOrg(
	ctx context.Context, monthlyBudgetMicroUsd *int64,
) (*OrgBudget, error) {
	body := map[string]any{"monthly_budget_micro_usd": monthlyBudgetMicroUsd}
	raw, err := s.transport.requestJSON(ctx, "PATCH", "/v1/account/budget", body)
	if err != nil {
		return nil, err
	}
	out := &OrgBudget{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
