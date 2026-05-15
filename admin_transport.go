package daguito

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// adminTransport is the shared HTTP plumbing for the admin services. It
// adds Bearer auth, JSON content-type, and maps non-2xx into ErrAdmin /
// ErrInvalidToken with the same envelope as the runtime sentinels.
type adminTransport struct {
	apiURL string
	apiKey string
	client *http.Client
}

func newAdminTransport(apiURL, apiKey string, client *http.Client) *adminTransport {
	if client == nil {
		client = http.DefaultClient
	}
	return &adminTransport{apiURL: apiURL, apiKey: apiKey, client: client}
}

// requestJSON sends method/path with an optional JSON body, returns the
// raw response bytes on success. 204 maps to a nil body. Callers Unmarshal
// into the typed shape themselves.
func (t *adminTransport) requestJSON(
	ctx context.Context, method, path string, body any,
) ([]byte, error) {
	endpoint := joinHTTP(t.apiURL, path)

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal: %v", ErrAdmin, err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdmin, err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyClientHeaders(req.Header)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdmin, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrAdmin, err)
	}

	if resp.StatusCode >= 400 {
		base := ErrAdmin
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			base = ErrInvalidToken
		}
		return nil, &httpError{
			base:    base,
			Status:  resp.StatusCode,
			Message: extractErrorMessage(raw, resp.Status),
		}
	}
	return raw, nil
}
