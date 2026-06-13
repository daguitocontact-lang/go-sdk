// Declare flows as code and idempotently apply them to Daguito.
//
// Each AgentFlowSpec below is the source of truth for one agent. The first
// run creates the flow + webhook on Daguito; subsequent runs update the
// agent config. Slug is the org-scoped identity — never change it.
//
// Run:
//
//	DAGUITO_API_URL=https://ingest.daguito.com \
//	DAGUITO_API_KEY=dgsk_acc_xxxxxxxxxxxx \
//	go run ./examples/flows_migrate
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

func float64Ptr(v float64) *float64 { return &v }
func intPtr(v int) *int             { return &v }

var flows = []daguito.AgentFlowSpec{
	{
		Slug:         "test_flow_1",
		Name:         "Test Flow 1",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash:nitro",
		SystemPrompt: "You are a friendly assistant. Keep replies short.",
		Temperature:  float64Ptr(0.3),
		MaxTokens:    intPtr(800),
		Tools: []daguito.FlowToolRef{
			{Kind: "handler", Name: "search_knowledge"},
		},
	},
	{
		Slug:         "test_flow_2",
		Name:         "Test Flow 2",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash:nitro",
		SystemPrompt: "You are a summarizer. Reply with bullet points only.",
		Temperature:  float64Ptr(0.2),
		MaxTokens:    intPtr(600),
	},
}

func main() {
	apiURL := os.Getenv("DAGUITO_API_URL")
	if apiURL == "" {
		apiURL = daguito.DefaultAPIURL
	}
	apiKey := os.Getenv("DAGUITO_API_KEY")
	if apiKey == "" {
		log.Fatal("missing env var: DAGUITO_API_KEY")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelTimeout()

	client, err := daguito.NewClient(daguito.ClientOptions{APIURL: apiURL, APIKey: apiKey})
	if err != nil {
		log.Fatalf("NewClient: %v", err)
	}

	failed := 0
	for _, spec := range flows {
		result, err := client.Flows.UpsertAgent(ctx, spec)
		if err != nil {
			failed++
			fmt.Printf("x %s upsert FAILED: %v\n", spec.Slug, err)
			continue
		}
		fmt.Printf(
			"OK %s upserted (flow=%s, webhook=%s, created=%t)\n",
			result.Slug, result.FlowID, result.WebhookID, result.Created,
		)
	}
	if failed > 0 {
		os.Exit(1)
	}
}
