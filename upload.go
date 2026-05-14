package daguito

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// UploadInput is the input to UploadFile. Exactly one of Path or Data must
// be set. When Data is provided, Filename is required (used for both the
// extension-based MIME fallback and the storage key suffix).
type UploadInput struct {
	APIURL    string
	WebhookID string
	Token     string
	Kind      MediaKind
	Path      string
	Data      []byte
	Filename  string
	MimeType  string
	Timeout   time.Duration // optional; defaults to 30s
	// HTTPClient is an optional override. When nil, UploadFile uses
	// http.DefaultClient.
	HTTPClient *http.Client
}

// UploadResult is the output of a successful presign + PUT.
type UploadResult struct {
	MediaKey     string
	MimeType     string
	SizeBytes    int64
	ExpiresInSec int
}

// UploadFile uploads a local file (or in-memory bytes) to Daguito object
// storage and returns the media_key. Hand the key to MediaKeyMessage on a
// subsequent WebhookStreamSession.Send.
//
// The upload is a two-step dance: (1) POST /v1/webhooks/:id/upload to mint a
// presigned PUT URL, (2) PUT bytes straight to object storage. Bytes never
// stream through the Daguito API. The presigned signature embeds kind/mime/
// size so a client cannot upload a different file than it announced.
func UploadFile(ctx context.Context, input UploadInput) (*UploadResult, error) {
	if (input.Path == "") == (len(input.Data) == 0) {
		return nil, fmt.Errorf("%w: exactly one of Path or Data must be set", ErrUploadFailed)
	}
	if input.APIURL == "" || input.WebhookID == "" || input.Token == "" {
		return nil, fmt.Errorf("%w: APIURL, WebhookID and Token are required", ErrUploadFailed)
	}

	var payload []byte
	var filename string
	if input.Path != "" {
		raw, err := os.ReadFile(input.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUploadFailed, err)
		}
		payload = raw
		filename = input.Filename
		if filename == "" {
			filename = filepath.Base(input.Path)
		}
	} else {
		if input.Filename == "" {
			return nil, fmt.Errorf("%w: Filename is required when passing Data", ErrUploadFailed)
		}
		payload = input.Data
		filename = input.Filename
	}

	size := int64(len(payload))
	if size == 0 {
		return nil, fmt.Errorf("%w: payload is empty", ErrUploadFailed)
	}

	mimeType := input.MimeType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if mimeType == "" {
		mimeType = defaultMime(input.Kind)
	}

	timeout := input.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := input.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	presignURL := joinHTTP(input.APIURL, "/v1/webhooks/"+input.WebhookID+"/upload")
	presignBody, _ := json.Marshal(map[string]any{
		"kind":       string(input.Kind),
		"mime_type":  mimeType,
		"size_bytes": size,
		"filename":   filename,
	})

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, presignURL, bytes.NewReader(presignBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUploadFailed, err)
	}
	req.Header.Set("Authorization", "Bearer "+input.Token)
	req.Header.Set("Content-Type", "application/json")

	presignResp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: presign: %v", ErrUploadFailed, err)
	}
	defer presignResp.Body.Close()
	presignRaw, _ := io.ReadAll(io.LimitReader(presignResp.Body, 1<<20))
	if presignResp.StatusCode >= 400 {
		base := ErrUploadFailed
		if presignResp.StatusCode == http.StatusUnauthorized || presignResp.StatusCode == http.StatusForbidden {
			base = ErrInvalidToken
		}
		return nil, &httpError{
			base:    base,
			Status:  presignResp.StatusCode,
			Message: extractErrorMessage(presignRaw, "presign failed"),
		}
	}
	var presign struct {
		UploadURL       string            `json:"upload_url"`
		Key             string            `json:"key"`
		ExpiresInSec    int               `json:"expires_in_sec"`
		RequiredHeaders map[string]string `json:"required_headers"`
	}
	if err := json.Unmarshal(presignRaw, &presign); err != nil {
		return nil, fmt.Errorf("%w: parse presign: %v", ErrUploadFailed, err)
	}
	if presign.UploadURL == "" || presign.Key == "" {
		return nil, fmt.Errorf("%w: presign response missing upload_url or key", ErrUploadFailed)
	}

	// PUT directly to object storage. MUST NOT carry the bearer token — the
	// presigned URL embeds its own auth in the query string and S3/R2 reject
	// requests with extra credentials.
	putReq, err := http.NewRequestWithContext(reqCtx, http.MethodPut, presign.UploadURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUploadFailed, err)
	}
	for k, v := range presign.RequiredHeaders {
		putReq.Header.Set(k, v)
	}
	if putReq.Header.Get("Content-Type") == "" {
		putReq.Header.Set("Content-Type", mimeType)
	}
	putReq.ContentLength = size
	if putReq.Header.Get("Content-Length") == "" {
		putReq.Header.Set("Content-Length", strconv.FormatInt(size, 10))
	}

	putResp, err := client.Do(putReq)
	if err != nil {
		return nil, fmt.Errorf("%w: PUT: %v", ErrUploadFailed, err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(putResp.Body, 1<<10))
		return nil, &httpError{
			base:    ErrUploadFailed,
			Status:  putResp.StatusCode,
			Message: "PUT to storage failed: " + string(body),
		}
	}

	return &UploadResult{
		MediaKey:     presign.Key,
		MimeType:     mimeType,
		SizeBytes:    size,
		ExpiresInSec: presign.ExpiresInSec,
	}, nil
}

func defaultMime(kind MediaKind) string {
	switch kind {
	case MediaKindImage:
		return "image/jpeg"
	case MediaKindAudio:
		return "audio/mpeg"
	case MediaKindVideo:
		return "video/mp4"
	}
	return "application/octet-stream"
}
