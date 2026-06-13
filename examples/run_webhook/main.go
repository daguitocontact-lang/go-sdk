// One-shot webhook example.
//
//	DAGUITO_API_URL=https://ingest.daguito.com \
//	DAGUITO_WEBHOOK_TOKEN=sk_wh_xxx \
//	go run ./examples/run_webhook
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/daguitocontact-lang/go-sdk"
)

func main() {
	apiURL := envOr("DAGUITO_API_URL", daguito.DefaultAPIURL)
	token := os.Getenv("DAGUITO_WEBHOOK_TOKEN")
	if token == "" {
		log.Fatal("DAGUITO_WEBHOOK_TOKEN is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := daguito.RunWebhook(ctx, daguito.WebhookRunInput{
		APIURL: apiURL,
		Token:  token,
		Input: map[string]any{
			"question": "What is the capital of France?",
		},
	})
	if err != nil {
		log.Fatalf("run webhook: %v", err)
	}

	fmt.Printf("execution_id=%s status=%s ok=%v\n", result.ExecutionID, result.Status, result.OK)
	fmt.Printf("output: %+v\n", result.Output)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
