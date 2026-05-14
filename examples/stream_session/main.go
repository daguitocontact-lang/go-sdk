// Streaming session example.
//
//	DAGUITO_API_URL=https://api.daguito.com \
//	DAGUITO_WEBHOOK_ID=wh_abc123 \
//	DAGUITO_WEBHOOK_TOKEN=sk_wh_xxx \
//	go run ./examples/stream_session
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/daguitocontact-lang/go-sdk"
)

func main() {
	apiURL := os.Getenv("DAGUITO_API_URL")
	webhookID := os.Getenv("DAGUITO_WEBHOOK_ID")
	token := os.Getenv("DAGUITO_WEBHOOK_TOKEN")
	if apiURL == "" || webhookID == "" || token == "" {
		log.Fatal("DAGUITO_API_URL, DAGUITO_WEBHOOK_ID and DAGUITO_WEBHOOK_TOKEN are required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	session := daguito.NewWebhookStreamSession(daguito.WebhookStreamOptions{
		APIURL:    apiURL,
		WebhookID: webhookID,
		Token:     token,
		Scope: map[string]any{
			"workspace_id": "ws_demo",
		},
	})

	// Register a client-side tool the agent can invoke.
	_ = session.RegisterTool(
		daguito.ToolSpec{
			Name:        "get_weather",
			Description: "Get the current weather for a city.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []string{"city"},
			},
		},
		func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				City string `json:"city"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, err
			}
			return map[string]any{"city": args.City, "temp_c": 22, "conditions": "sunny"}, nil
		},
	)

	if err := session.Connect(ctx); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer session.Close()

	if err := session.Send(ctx, daguito.TextMessage("Hello, agent!"), nil); err != nil {
		log.Fatalf("send: %v", err)
	}

	for evt := range session.Events() {
		switch evt.Type {
		case daguito.EventNodeToken:
			fmt.Print(evt.NodeToken.Text)
		case daguito.EventNodeEmit:
			if progress := daguito.ParseToolProgress(evt.NodeEmit); progress != nil {
				fmt.Printf("\n[tool] %s/%s\n", progress.Tool, progress.Stage)
			}
		case daguito.EventFlowCompleted:
			fmt.Printf("\nflow done in %dms\n", evt.FlowCompleted.ElapsedMs)
			return
		case daguito.EventFlowFailed:
			fmt.Printf("\nflow failed: %s\n", evt.FlowFailed.Error)
			return
		case daguito.EventError:
			fmt.Printf("\nerror: %s\n", evt.Error.Message)
			return
		case daguito.EventClosed:
			return
		}
	}
}
