package daguito

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// WebhookRunInput is the input to RunWebhook. ApiURL is the Daguito ingest
// gateway root (e.g. https://ingest.daguito.com); Token is the raw sk_wh_...
// bearer.
type WebhookRunInput struct {
	APIURL  string
	Token   string
	Input   map[string]any
	Timeout time.Duration // optional; defaults to 90s
	// HTTPClient is an optional override. When nil, RunWebhook uses
	// http.DefaultClient with the configured timeout applied via context.
	HTTPClient *http.Client
}

// WebhookRunResult is the typed response of /h/:token. Output is the JSON
// value the flow produced — its shape is flow-dependent.
type WebhookRunResult struct {
	OK          bool
	ExecutionID string
	Status      string
	Output      any
}

// RunWebhook fires a one-shot HTTP POST to /h/:token and returns the final
// flow output. Use WebhookStreamSession instead for token-by-token streaming.
//
// The context controls both connection and read deadlines — cancel it to
// abort an in-flight request.
func RunWebhook(ctx context.Context, input WebhookRunInput) (*WebhookRunResult, error) {
	if input.APIURL == "" {
		return nil, fmt.Errorf("%w: APIURL is required", ErrWebhook)
	}
	if input.Token == "" {
		return nil, fmt.Errorf("%w: Token is required", ErrInvalidToken)
	}

	endpoint := joinHTTP(input.APIURL, "/h/"+url.PathEscape(input.Token))
	body := input.Input
	if body == nil {
		body = map[string]any{}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal input: %v", ErrWebhook, err)
	}

	timeout := input.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWebhook, err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := input.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWebhook, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrWebhook, err)
	}

	if resp.StatusCode >= 400 {
		base := ErrWebhook
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			base = ErrInvalidToken
		}
		return nil, &httpError{
			base:    base,
			Status:  resp.StatusCode,
			Message: extractErrorMessage(raw, resp.Status),
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrWebhook, err)
	}
	if msg, ok := parsed["error"].(string); ok && msg != "" {
		return nil, &httpError{base: ErrWebhook, Status: resp.StatusCode, Message: msg}
	}

	result := &WebhookRunResult{Output: parsed["output"]}
	if v, ok := parsed["ok"].(bool); ok {
		result.OK = v
	}
	if v, ok := parsed["execution_id"].(string); ok {
		result.ExecutionID = v
	}
	if v, ok := parsed["status"].(string); ok {
		result.Status = v
	}
	return result, nil
}

func extractErrorMessage(body []byte, fallback string) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		if msg, ok := parsed["error"].(string); ok && msg != "" {
			return msg
		}
		if msg, ok := parsed["message"].(string); ok && msg != "" {
			return msg
		}
	}
	if len(body) > 0 {
		if len(body) > 256 {
			body = body[:256]
		}
		return string(body)
	}
	return fallback
}
