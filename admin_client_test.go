package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client, err := NewClient(ClientOptions{
		APIURL:     srv.URL,
		APIKey:     "dgsk_acc_test",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		srv.Close()
		t.Fatalf("NewClient: %v", err)
	}
	return client, srv
}

func okAccountKey() map[string]any {
	return map[string]any{
		"id":                       "ak_1",
		"name":                     "prod",
		"key_prefix":               "dgsk_acc_abc12",
		"monthly_budget_micro_usd": nil,
		"current_mtd_micro_usd":    0,
		"created_at":               "2026-05-14T00:00:00.000Z",
		"last_used_at":             nil,
		"revoked_at":               nil,
	}
}

func okPublicKey() map[string]any {
	return map[string]any{
		"id":                       "pk_1",
		"flow_id":                  "flow_abc",
		"name":                     "browser",
		"key_prefix":               "dgpk_flow_x9",
		"allowed_origins":          []string{"https://example.com"},
		"monthly_budget_micro_usd": nil,
		"current_mtd_micro_usd":    0,
		"created_at":               "2026-05-14T00:00:00.000Z",
		"last_used_at":             nil,
		"revoked_at":               nil,
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestAccountKeysList(t *testing.T) {
	var captured *http.Request
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		writeJSON(t, w, 200, map[string]any{
			"keys": []map[string]any{okAccountKey()},
		})
	})
	defer srv.Close()

	keys, err := client.AccountKeys.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("want 1 key, got %d", len(keys))
	}
	if keys[0].ID != "ak_1" || keys[0].KeyPrefix != "dgsk_acc_abc12" {
		t.Fatalf("unexpected key: %+v", keys[0])
	}
	if captured.Method != "GET" {
		t.Fatalf("want GET, got %s", captured.Method)
	}
	if !strings.HasSuffix(captured.URL.Path, "/v1/account/api-keys") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer dgsk_acc_test" {
		t.Fatalf("missing/incorrect bearer: %q", got)
	}
}

func TestAccountKeysCreate(t *testing.T) {
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		body := okAccountKey()
		body["plaintext"] = "dgsk_acc_FULL"
		writeJSON(t, w, 201, body)
	})
	defer srv.Close()

	budget := int64(5_000_000)
	created, err := client.AccountKeys.Create(context.Background(), CreateAccountKeyInput{
		Name:                  "prod",
		MonthlyBudgetMicroUsd: &budget,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Plaintext != "dgsk_acc_FULL" {
		t.Fatalf("want plaintext echoed, got %q", created.Plaintext)
	}
	if capturedBody["name"] != "prod" {
		t.Fatalf("name not posted: %+v", capturedBody)
	}
	if got, _ := capturedBody["monthly_budget_micro_usd"].(float64); int64(got) != 5_000_000 {
		t.Fatalf("budget not posted: %+v", capturedBody)
	}
}

func TestAccountKeysRevoke(t *testing.T) {
	var captured *http.Request
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(204)
	})
	defer srv.Close()

	if err := client.AccountKeys.Revoke(context.Background(), "ak_1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if captured.Method != "DELETE" {
		t.Fatalf("want DELETE, got %s", captured.Method)
	}
	if !strings.HasSuffix(captured.URL.Path, "/v1/account/api-keys/ak_1") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
}

func TestAccountKeysSetBudget(t *testing.T) {
	stage := 0
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		stage++
		switch stage {
		case 1:
			if r.Method != "PATCH" {
				t.Fatalf("stage 1 want PATCH, got %s", r.Method)
			}
			writeJSON(t, w, 200, map[string]any{
				"id":                       "ak_1",
				"monthly_budget_micro_usd": 1_000_000,
			})
		case 2:
			if r.Method != "GET" {
				t.Fatalf("stage 2 want GET, got %s", r.Method)
			}
			key := okAccountKey()
			key["monthly_budget_micro_usd"] = 1_000_000
			writeJSON(t, w, 200, map[string]any{"keys": []map[string]any{key}})
		}
	})
	defer srv.Close()

	budget := int64(1_000_000)
	updated, err := client.AccountKeys.SetBudget(context.Background(), "ak_1", &budget)
	if err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if updated.MonthlyBudgetMicroUsd == nil || *updated.MonthlyBudgetMicroUsd != 1_000_000 {
		t.Fatalf("unexpected budget: %+v", updated)
	}
	if stage != 2 {
		t.Fatalf("want 2 calls, got %d", stage)
	}
}

func TestAccountKeysUnauthorizedMapsToInvalidToken(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 401, map[string]any{"error": "missing bearer"})
	})
	defer srv.Close()

	_, err := client.AccountKeys.List(context.Background())
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	var he *httpError
	if !errors.As(err, &he) || he.Status != 401 {
		t.Fatalf("want httpError with 401, got %v", err)
	}
}

func TestAccountKeysForbiddenMapsToInvalidToken(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 403, map[string]any{"error": "not an admin"})
	})
	defer srv.Close()

	_, err := client.AccountKeys.List(context.Background())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestPublicKeysList(t *testing.T) {
	var captured *http.Request
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		writeJSON(t, w, 200, map[string]any{"keys": []map[string]any{okPublicKey()}})
	})
	defer srv.Close()

	keys, err := client.PublicKeys.List(context.Background(), "flow_abc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0].FlowID != "flow_abc" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	if !strings.Contains(captured.URL.Path, "/v1/flows/flow_abc/public-keys") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
}

func TestPublicKeysCreate(t *testing.T) {
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		body := okPublicKey()
		body["plaintext"] = "dgpk_flow_FULL"
		writeJSON(t, w, 201, body)
	})
	defer srv.Close()

	created, err := client.PublicKeys.Create(context.Background(), "flow_abc", CreatePublicKeyInput{
		Name:           "browser",
		AllowedOrigins: []string{"https://example.com"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Plaintext != "dgpk_flow_FULL" {
		t.Fatalf("plaintext mismatch: %q", created.Plaintext)
	}
	origins, _ := capturedBody["allowed_origins"].([]any)
	if len(origins) != 1 || origins[0] != "https://example.com" {
		t.Fatalf("allowed_origins not posted: %+v", capturedBody)
	}
	// MonthlyBudgetMicroUsd is nil -> omitempty drops it from the wire body.
	if _, exists := capturedBody["monthly_budget_micro_usd"]; exists {
		t.Fatalf("budget should be omitted: %+v", capturedBody)
	}
}

func TestPublicKeysRevoke(t *testing.T) {
	var captured *http.Request
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(204)
	})
	defer srv.Close()

	if err := client.PublicKeys.Revoke(context.Background(), "flow_abc", "pk_1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !strings.HasSuffix(captured.URL.Path, "/v1/flows/flow_abc/public-keys/pk_1") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
}

func TestBudgetsGetOrg(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{
			"monthly_budget_micro_usd": 100_000_000,
			"current_mtd_micro_usd":    25_000_000,
			"mtd_reset_at":             "2026-06-01T00:00:00.000Z",
		})
	})
	defer srv.Close()

	budget, err := client.Budgets.GetOrg(context.Background())
	if err != nil {
		t.Fatalf("GetOrg: %v", err)
	}
	if budget.MonthlyBudgetMicroUsd == nil || *budget.MonthlyBudgetMicroUsd != 100_000_000 {
		t.Fatalf("budget: %+v", budget)
	}
	if budget.CurrentMtdMicroUsd != 25_000_000 {
		t.Fatalf("mtd: %d", budget.CurrentMtdMicroUsd)
	}
}

func TestBudgetsSetOrgNullClears(t *testing.T) {
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, map[string]any{
			"monthly_budget_micro_usd": nil,
			"current_mtd_micro_usd":    0,
			"mtd_reset_at":             "2026-06-01T00:00:00.000Z",
		})
	})
	defer srv.Close()

	budget, err := client.Budgets.SetOrg(context.Background(), nil)
	if err != nil {
		t.Fatalf("SetOrg: %v", err)
	}
	if budget.MonthlyBudgetMicroUsd != nil {
		t.Fatalf("want nil budget, got %+v", budget.MonthlyBudgetMicroUsd)
	}
	if _, exists := capturedBody["monthly_budget_micro_usd"]; !exists {
		t.Fatalf("body missing key: %+v", capturedBody)
	}
	if capturedBody["monthly_budget_micro_usd"] != nil {
		t.Fatalf("want explicit null on the wire, got %+v", capturedBody)
	}
}

func TestNewClientValidatesArgs(t *testing.T) {
	if _, err := NewClient(ClientOptions{APIKey: "x"}); err == nil {
		t.Fatalf("want error for empty APIURL")
	}
	if _, err := NewClient(ClientOptions{APIURL: "https://x"}); err == nil {
		t.Fatalf("want error for empty APIKey")
	}
}
