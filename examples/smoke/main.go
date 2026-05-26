// Smoke test: connect to local Daguito API on :4001 against the midulabs flow,
// send a text message, print every event until flow.completed.
//
//	go run ./examples/smoke
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	daguito "github.com/daguitocontact-lang/go-sdk"
)

func main() {
	apiURL := envOr("DAGUITO_API_URL", "http://localhost:4001")
	webhookID := envOr("DAGUITO_WEBHOOK_ID", "wh_61f03d57798a6cf9")
	token := envOr("DAGUITO_WEBHOOK_TOKEN", "sk_wh_LreutzjnoYBjM78WaDJ2dno6o9oyU4SH")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	defer cancel2()

	session := daguito.NewWebhookStreamSession(daguito.WebhookStreamOptions{
		APIURL:     apiURL,
		WebhookID:  webhookID,
		Token:      token,
		SessionKey: "smoke-test-" + fmt.Sprint(time.Now().UnixNano()),
		Scope:      map[string]any{"consultation_uuid": "smoke-test"},
	})

	if err := session.Connect(ctx); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer session.Close()
	fmt.Println("→ connected")

	if err := session.Send(ctx, daguito.TextMessage("hola, contame un chiste corto"), nil); err != nil {
		log.Fatalf("send: %v", err)
	}
	fmt.Println("→ sent text")

	for evt := range session.Events() {
		switch evt.Type {
		case daguito.EventReady:
			fmt.Println("✓ ready")
		case daguito.EventNodeStarted:
			fmt.Printf("→ node.started: %s\n", evt.NodeStarted.NodeID)
		case daguito.EventNodeToken:
			fmt.Print(evt.NodeToken.Text)
		case daguito.EventNodeCompleted:
			fmt.Printf("\n✓ node.completed: %s (%dms)\n", evt.NodeCompleted.NodeID, evt.NodeCompleted.DurationMs)
		case daguito.EventNodeEmit:
			fmt.Printf("⊙ node.emit kind=%s\n", evt.NodeEmit.Kind)
		case daguito.EventFlowCompleted:
			fmt.Printf("\n✓ flow.completed elapsed=%dms\n", evt.FlowCompleted.ElapsedMs)
			return
		case daguito.EventFlowFailed:
			fmt.Printf("\n✗ flow.failed: %s\n", evt.FlowFailed.Error)
			return
		case daguito.EventError:
			fmt.Printf("✗ error: %s\n", evt.Error.Message)
		case daguito.EventClosed:
			fmt.Printf("⊙ closed: code=%d reason=%q\n", evt.Closed.Code, evt.Closed.Reason)
			return
		}
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
