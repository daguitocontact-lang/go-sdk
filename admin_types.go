package daguito

import "errors"

// ErrAdmin is the sentinel for admin-client failures (account keys, public
// flow keys, budgets). Use errors.Is to branch on it; errors.As against
// *httpError reveals the HTTP status + server message when available.
var ErrAdmin = errors.New("daguito: admin request failed")

// AccountKey is the wire shape of a `dgsk_acc_…` org-wide credential.
//
// JSON tags mirror the Python and JS SDKs exactly (key_prefix,
// monthly_budget_micro_usd, …) so admin payloads are interchangeable
// across the three SDKs.
type AccountKey struct {
	ID                    string  `json:"id"`
	Name                  string  `json:"name"`
	KeyPrefix             string  `json:"key_prefix"`
	MonthlyBudgetMicroUsd *int64  `json:"monthly_budget_micro_usd"`
	CurrentMtdMicroUsd    int64   `json:"current_mtd_micro_usd"`
	CreatedAt             string  `json:"created_at"`
	LastUsedAt            *string `json:"last_used_at"`
	RevokedAt             *string `json:"revoked_at"`
}

// AccountKeyCreated is the response of AccountKeys.Create. The Plaintext
// token is returned exactly once — persist it now or revoke and recreate.
type AccountKeyCreated struct {
	AccountKey
	Plaintext string `json:"plaintext"`
}

// PublicKey is the wire shape of a `dgpk_flow_…` browser-safe credential
// bound to a single flow + an allowed-origins allowlist.
type PublicKey struct {
	ID                    string   `json:"id"`
	FlowID                string   `json:"flow_id"`
	Name                  string   `json:"name"`
	KeyPrefix             string   `json:"key_prefix"`
	AllowedOrigins        []string `json:"allowed_origins"`
	MonthlyBudgetMicroUsd *int64   `json:"monthly_budget_micro_usd"`
	CurrentMtdMicroUsd    int64    `json:"current_mtd_micro_usd"`
	CreatedAt             string   `json:"created_at"`
	LastUsedAt            *string  `json:"last_used_at"`
	RevokedAt             *string  `json:"revoked_at"`
}

// PublicKeyCreated is the response of PublicKeys.Create.
type PublicKeyCreated struct {
	PublicKey
	Plaintext string `json:"plaintext"`
}

// OrgBudget is the org-level spending state returned by Budgets.GetOrg.
type OrgBudget struct {
	MonthlyBudgetMicroUsd *int64  `json:"monthly_budget_micro_usd"`
	CurrentMtdMicroUsd    int64   `json:"current_mtd_micro_usd"`
	MtdResetAt            *string `json:"mtd_reset_at"`
}

// CreateAccountKeyInput is the request body for AccountKeys.Create.
//
// Pass a pointer for MonthlyBudgetMicroUsd so callers can distinguish "no
// cap" (nil) from "explicit zero" — the wire shape needs the same
// distinction.
type CreateAccountKeyInput struct {
	Name                  string `json:"name"`
	MonthlyBudgetMicroUsd *int64 `json:"monthly_budget_micro_usd,omitempty"`
}

// CreatePublicKeyInput is the request body for PublicKeys.Create.
type CreatePublicKeyInput struct {
	Name                  string   `json:"name"`
	AllowedOrigins        []string `json:"allowed_origins"`
	MonthlyBudgetMicroUsd *int64   `json:"monthly_budget_micro_usd,omitempty"`
}

// keysListResponse is the {keys: […]} envelope every GET endpoint returns.
type keysListResponse[T any] struct {
	Keys []T `json:"keys"`
}

// Int64Ptr is a tiny convenience for callers building optional budget
// fields without having to declare a local variable first.
func Int64Ptr(v int64) *int64 { return &v }
