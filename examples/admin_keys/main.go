// Administer API keys + budgets programmatically with daguito.NewClient.
//
//  1. Connect with a dgsk_acc_ org-admin key.
//  2. List existing account keys.
//  3. Create a public flow key for DAGUITO_FLOW_ID,
//     allowed_origins=["https://example.com"].
//  4. Patch its budget to $50/month (50_000_000 micro-USD).
//  5. List public keys to confirm the change.
//  6. Revoke it.
//
// Run:
//
//	DAGUITO_API_URL=https://api.daguito.com \
//	DAGUITO_API_KEY=dgsk_acc_xxxxxxxxxxxx \
//	DAGUITO_FLOW_ID=flow_abc123 \
//	go run ./examples/admin_keys
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/daguitocontact-lang/go-sdk"
)

func env(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("missing env var: %s", name)
	}
	return value
}

func main() {
	apiURL := os.Getenv("DAGUITO_API_URL")
	if apiURL == "" {
		apiURL = "https://api.daguito.com"
	}
	apiKey := env("DAGUITO_API_KEY")
	flowID := env("DAGUITO_FLOW_ID")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTimeout()

	// 1. Single client, three sub-services.
	client, err := daguito.NewClient(daguito.ClientOptions{APIURL: apiURL, APIKey: apiKey})
	if err != nil {
		log.Fatalf("NewClient: %v", err)
	}

	// 2. List existing org-wide account keys.
	keys, err := client.AccountKeys.List(ctx)
	if err != nil {
		log.Fatalf("list account keys: %v", err)
	}
	fmt.Printf("[account-keys] %d key(s) active\n", len(keys))
	for _, key := range keys {
		fmt.Printf("  - %s  name=%q  mtd=%dμ$\n", key.KeyPrefix, key.Name, key.CurrentMtdMicroUsd)
	}

	// 3. Mint a public flow key for embedding in a browser.
	created, err := client.PublicKeys.Create(ctx, flowID, daguito.CreatePublicKeyInput{
		Name:           "example-com integration",
		AllowedOrigins: []string{"https://example.com"},
	})
	if err != nil {
		log.Fatalf("create public key: %v", err)
	}
	fmt.Printf("[public-key]  created %s\n", created.KeyPrefix)
	fmt.Printf("               plaintext (shown ONCE): %s\n", created.Plaintext)

	// 4. Cap the new key at $50/month (5e7 micro-USD).
	updated, err := client.PublicKeys.SetBudget(ctx, flowID, created.ID, daguito.Int64Ptr(50_000_000))
	if err != nil {
		log.Fatalf("set budget: %v", err)
	}
	if updated.MonthlyBudgetMicroUsd != nil {
		fmt.Printf("[public-key]  budget set to %dμ$\n", *updated.MonthlyBudgetMicroUsd)
	}

	// 5. Confirm via list.
	listing, err := client.PublicKeys.List(ctx, flowID)
	if err != nil {
		log.Fatalf("list public keys: %v", err)
	}
	for _, key := range listing {
		if key.ID == created.ID && key.MonthlyBudgetMicroUsd != nil {
			fmt.Printf("[verify]     %s budget=%dμ$\n", key.KeyPrefix, *key.MonthlyBudgetMicroUsd)
		}
	}

	// 6. Revoke (soft delete — never deleted in the DB).
	if err := client.PublicKeys.Revoke(ctx, flowID, created.ID); err != nil {
		log.Fatalf("revoke: %v", err)
	}
	fmt.Printf("[public-key]  revoked %s\n", created.KeyPrefix)
}
