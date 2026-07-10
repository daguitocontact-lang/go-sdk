package daguito

import "errors"

// Sentinel errors. Use errors.Is to branch on the well-known failure modes.
var (
	// ErrInvalidToken is returned when a webhook or KB key is rejected.
	ErrInvalidToken = errors.New("daguito: invalid token")
	// ErrNotFound is returned when a request targets a resource that no longer
	// exists (HTTP 404) — e.g. uploading to a knowledge source that was deleted.
	// Callers can errors.Is on it to re-provision and retry.
	ErrNotFound = errors.New("daguito: not found")
	// ErrUploadFailed is returned when either the presign POST or the
	// follow-up PUT to object storage fails.
	ErrUploadFailed = errors.New("daguito: upload failed")
	// ErrKnowledge is returned for KB ingest/search failures.
	ErrKnowledge = errors.New("daguito: knowledge request failed")
	// ErrWebhook is returned when a one-shot webhook call fails (network or
	// non-2xx response).
	ErrWebhook = errors.New("daguito: webhook request failed")
	// ErrStream is returned for protocol-level streaming session errors.
	ErrStream = errors.New("daguito: stream session error")
)

// httpError wraps a sentinel with HTTP status and server message so callers
// can both branch via errors.Is and inspect details via errors.As.
type httpError struct {
	base    error
	Status  int
	Message string
}

func (e *httpError) Error() string {
	if e.Message == "" {
		return e.base.Error()
	}
	return e.base.Error() + ": " + e.Message
}

func (e *httpError) Unwrap() error { return e.base }
