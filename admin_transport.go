package daguito

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
		if resp.StatusCode == http.StatusNotFound {
			base = ErrNotFound
		}
		return nil, &httpError{
			base:    base,
			Status:  resp.StatusCode,
			Message: extractErrorMessage(raw, resp.Status),
		}
	}
	return raw, nil
}

// requestMultipart POSTs a single-file multipart/form-data body to path and
// returns the raw response bytes on success. Success/error handling mirrors
// requestJSON (8MB LimitReader, 204→nil, >=400 → ErrAdmin/ErrInvalidToken).
// mimeType sets the file part's Content-Type when non-empty; otherwise the
// server sniffs by filename extension. When metadata is non-empty it rides
// alongside the file as a JSON-encoded `metadata` form field; nil/empty omits
// the field entirely (backward-compatible with servers that ignore it).
func (t *adminTransport) requestMultipart(
	ctx context.Context, path, fileFieldName, filename, mimeType string, data []byte,
	metadata map[string]any,
) ([]byte, error) {
	endpoint := joinHTTP(t.apiURL, path)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	var part io.Writer
	var err error
	if mimeType == "" {
		part, err = writer.CreateFormFile(fileFieldName, filename)
	} else {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name=%q; filename=%q`, fileFieldName, filename))
		header.Set("Content-Type", mimeType)
		part, err = writer.CreatePart(header)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: build multipart part: %v", ErrAdmin, err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("%w: write multipart part: %v", ErrAdmin, err)
	}
	if len(metadata) > 0 {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal metadata: %v", ErrAdmin, err)
		}
		if err := writer.WriteField("metadata", string(encoded)); err != nil {
			return nil, fmt.Errorf("%w: write metadata field: %v", ErrAdmin, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: close multipart writer: %v", ErrAdmin, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAdmin, err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
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
		if resp.StatusCode == http.StatusNotFound {
			base = ErrNotFound
		}
		return nil, &httpError{
			base:    base,
			Status:  resp.StatusCode,
			Message: extractErrorMessage(raw, resp.Status),
		}
	}
	return raw, nil
}
