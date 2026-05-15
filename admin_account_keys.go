package daguito

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// AccountKeysService manages `dgsk_acc_…` keys for the caller's org.
// Returned by Client.AccountKeys; never construct one yourself.
type AccountKeysService struct {
	transport *adminTransport
}

// List returns every account key in the caller's org (active + revoked).
func (s *AccountKeysService) List(ctx context.Context) ([]AccountKey, error) {
	raw, err := s.transport.requestJSON(ctx, "GET", "/v1/account/api-keys", nil)
	if err != nil {
		return nil, err
	}
	var parsed keysListResponse[AccountKey]
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return parsed.Keys, nil
}

// Create mints a new account key. The returned Plaintext is the only chance
// to capture the full token — store it, then discard the value.
func (s *AccountKeysService) Create(
	ctx context.Context, in CreateAccountKeyInput,
) (*AccountKeyCreated, error) {
	raw, err := s.transport.requestJSON(ctx, "POST", "/v1/account/api-keys", in)
	if err != nil {
		return nil, err
	}
	out := &AccountKeyCreated{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// Revoke soft-deletes the key. Subsequent calls using it will fail auth.
// 204 No Content on success.
func (s *AccountKeysService) Revoke(ctx context.Context, keyID string) error {
	path := "/v1/account/api-keys/" + url.PathEscape(keyID)
	_, err := s.transport.requestJSON(ctx, "DELETE", path, nil)
	return err
}

// SetBudget updates the per-key spending cap. Pass nil to clear the cap.
//
// The PATCH /budget endpoint only echoes {id, monthly_budget_micro_usd};
// the SDK re-fetches the canonical row via List so the caller still gets
// the full shape.
func (s *AccountKeysService) SetBudget(
	ctx context.Context, keyID string, monthlyBudgetMicroUsd *int64,
) (*AccountKey, error) {
	body := map[string]any{"monthly_budget_micro_usd": monthlyBudgetMicroUsd}
	path := "/v1/account/api-keys/" + url.PathEscape(keyID) + "/budget"
	if _, err := s.transport.requestJSON(ctx, "PATCH", path, body); err != nil {
		return nil, err
	}
	keys, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].ID == keyID {
			return &keys[i], nil
		}
	}
	return nil, fmt.Errorf("%w: key not found after budget update", ErrAdmin)
}
