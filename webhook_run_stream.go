package daguito

import (
	"context"
	"fmt"
	"time"
)

// WebhookRunStreamInput is the input to RunWebhookStream. Mirrors
// WebhookRunInput but adds WebhookID (resolve via Client.Flows.ResolveWebhook)
// since the streaming surface lives at /v1/webhooks/:id/stream rather than
// /h/:token.
type WebhookRunStreamInput struct {
	APIURL string
	// WebhookID is the streaming webhook id ("wh_..."). Resolve via
	// Client.Flows.ResolveWebhook when only the slug is known.
	WebhookID string
	// Token is the webhook bearer ("sk_wh_...").
	Token string
	// Input is the free-form payload forwarded to the flow as base_input.
	Input map[string]any
	// Text is the inline message text sent on the WS. Defaults to a single
	// space when empty — webhook agents typically read everything from
	// base_input and don't need a user-facing message.
	Text string
	// SessionKey is an optional caller-supplied id. A fresh uuid is minted
	// when empty.
	SessionKey string
	// Timeout caps how long the SDK waits for flow.completed. No default —
	// WS has no edge timeout, so this is opt-in.
	Timeout time.Duration
}

// WebhookRunStreamResult mirrors WebhookRunResult plus ElapsedMs measured
// client-side from session.started to flow.completed.
type WebhookRunStreamResult struct {
	OK          bool
	ExecutionID string
	// Status is "completed", "failed" or "unknown".
	Status    string
	Output    any
	ElapsedMs int64
}

// RunWebhookStream opens a streaming WebSocket session, sends one message,
// waits for flow.completed, closes the session, and returns the final output.
// Same ergonomics as RunWebhook (single call, one return) without the 100s
// Cloudflare HTTP edge timeout — use it for flows that may run longer than a
// HTTP request is willing to wait (long generations, vision pipelines,
// multi-step tools).
//
// The shape of the returned value matches WebhookRunResult so callers can
// swap RunWebhook for RunWebhookStream without touching their parsing code,
// modulo the extra ElapsedMs field.
//
// The context controls the dial and the wait for flow.completed — cancel it
// to abort the in-flight run. Timeout, when set, applies on top via
// context.WithTimeout.
func RunWebhookStream(ctx context.Context, input WebhookRunStreamInput) (*WebhookRunStreamResult, error) {
	if input.APIURL == "" {
		return nil, fmt.Errorf("%w: APIURL is required", ErrWebhook)
	}
	if input.WebhookID == "" {
		return nil, fmt.Errorf("%w: WebhookID is required", ErrWebhook)
	}
	if input.Token == "" {
		return nil, fmt.Errorf("%w: Token is required", ErrInvalidToken)
	}

	runCtx := ctx
	if input.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, input.Timeout)
		defer cancel()
	}

	session := NewWebhookStreamSession(WebhookStreamOptions{
		APIURL:        input.APIURL,
		WebhookID:     input.WebhookID,
		Token:         input.Token,
		SessionKey:    input.SessionKey,
		BaseInput:     input.Input,
		AutoReconnect: false,
	})
	defer session.Close()

	if err := session.Connect(runCtx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWebhook, err)
	}

	text := input.Text
	if text == "" {
		text = " "
	}
	if err := session.Send(runCtx, TextMessage(text), input.Input); err != nil {
		return nil, fmt.Errorf("%w: send: %v", ErrWebhook, err)
	}

	var (
		tokenBuffer []string
		nodeOutputs []any
		executionID string
	)

	events := session.Events()
	for {
		select {
		case <-runCtx.Done():
			return nil, fmt.Errorf("%w: %v", ErrWebhook, runCtx.Err())

		case evt, ok := <-events:
			if !ok {
				return nil, fmt.Errorf("%w: session closed before flow.completed", ErrWebhook)
			}
			switch evt.Type {
			case EventNodeToken:
				if evt.NodeToken != nil && evt.NodeToken.Text != "" {
					tokenBuffer = append(tokenBuffer, evt.NodeToken.Text)
				}

			case EventNodeCompleted:
				if evt.NodeCompleted != nil && evt.NodeCompleted.Output != nil {
					nodeOutputs = append(nodeOutputs, evt.NodeCompleted.Output)
				}

			case EventNodeEmit:
				if evt.NodeEmit != nil {
					if id, ok := evt.NodeEmit.Data["execution_id"].(string); ok && id != "" {
						executionID = id
					}
				}

			case EventFlowCompleted:
				elapsed := int64(0)
				var output any
				if evt.FlowCompleted != nil {
					elapsed = int64(evt.FlowCompleted.ElapsedMs)
					output = evt.FlowCompleted.Output
				}
				if output == nil {
					if len(nodeOutputs) > 0 {
						output = nodeOutputs[len(nodeOutputs)-1]
					} else if len(tokenBuffer) > 0 {
						output = map[string]any{"content": joinStrings(tokenBuffer)}
					}
				}
				return &WebhookRunStreamResult{
					OK:          true,
					ExecutionID: executionID,
					Status:      "completed",
					Output:      output,
					ElapsedMs:   elapsed,
				}, nil

			case EventFlowFailed:
				msg := "flow failed"
				if evt.FlowFailed != nil && evt.FlowFailed.Error != "" {
					msg = evt.FlowFailed.Error
				}
				return nil, fmt.Errorf("%w: %s", ErrWebhook, msg)

			case EventError:
				msg := "transport error"
				if evt.Error != nil && evt.Error.Message != "" {
					msg = evt.Error.Message
				}
				return nil, fmt.Errorf("%w: %s", ErrWebhook, msg)

			case EventClosed:
				reason := ""
				if evt.Closed != nil {
					reason = evt.Closed.Reason
				}
				if reason == "" {
					reason = "session closed before flow.completed"
				}
				return nil, fmt.Errorf("%w: %s", ErrWebhook, reason)
			}
		}
	}
}

// joinStrings concatenates without importing strings for one call.
func joinStrings(parts []string) string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return string(out)
}
