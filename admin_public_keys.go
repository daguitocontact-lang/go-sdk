package daguito

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// PublicKeysService manages `dgpk_flow_…` browser-safe keys per flow.
// Returned by Client.PublicKeys.
type PublicKeysService struct {
	transport *adminTransport
}

// List returns every public flow key for the given flow.
func (s *PublicKeysService) List(ctx context.Context, flowID string) ([]PublicKey, error) {
	path := "/v1/flows/" + url.PathEscape(flowID) + "/public-keys"
	raw, err := s.transport.requestJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var parsed keysListResponse[PublicKey]
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return parsed.Keys, nil
}

// Create mints a CORS-locked browser key. AllowedOrigins must contain at
// least one entry — the server enforces the allowlist on every request.
func (s *PublicKeysService) Create(
	ctx context.Context, flowID string, in CreatePublicKeyInput,
) (*PublicKeyCreated, error) {
	path := "/v1/flows/" + url.PathEscape(flowID) + "/public-keys"
	raw, err := s.transport.requestJSON(ctx, "POST", path, in)
	if err != nil {
		return nil, err
	}
	out := &PublicKeyCreated{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// Revoke soft-deletes the key.
func (s *PublicKeysService) Revoke(ctx context.Context, flowID, keyID string) error {
	path := "/v1/flows/" + url.PathEscape(flowID) + "/public-keys/" + url.PathEscape(keyID)
	_, err := s.transport.requestJSON(ctx, "DELETE", path, nil)
	return err
}

// SetBudget updates the per-key spending cap. Pass nil to clear it.
func (s *PublicKeysService) SetBudget(
	ctx context.Context, flowID, keyID string, monthlyBudgetMicroUsd *int64,
) (*PublicKey, error) {
	body := map[string]any{"monthly_budget_micro_usd": monthlyBudgetMicroUsd}
	path := "/v1/flows/" + url.PathEscape(flowID) + "/public-keys/" + url.PathEscape(keyID) + "/budget"
	if _, err := s.transport.requestJSON(ctx, "PATCH", path, body); err != nil {
		return nil, err
	}
	keys, err := s.List(ctx, flowID)
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
