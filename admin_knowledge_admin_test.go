package daguito

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestKnowledgeUploadFile(t *testing.T) {
	var (
		method      string
		path        string
		auth        string
		contentType string
		gotName     string
		gotBytes    []byte
	)
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")

		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		gotName = header.Filename
		gotBytes, err = io.ReadAll(file)
		if err != nil {
			t.Fatalf("read file part: %v", err)
		}

		writeJSON(t, w, 200, map[string]any{
			"source_id":   "src1",
			"chunk_count": 3,
			"token_count": 120,
		})
	})
	defer srv.Close()

	data := []byte("# Title\n\nhello world\n")
	result, err := client.Knowledge.UploadFile(context.Background(), "src1", UploadFileInput{
		Filename: "notes.md",
		MimeType: "text/markdown",
		Data:     data,
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	if result.SourceID != "src1" || result.ChunkCount != 3 || result.TokenCount != 120 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if method != "POST" {
		t.Fatalf("want POST, got %s", method)
	}
	if path != "/api/public/knowledge/sources/src1/upload" {
		t.Fatalf("unexpected path: %s", path)
	}
	if auth != "Bearer dgsk_acc_test" {
		t.Fatalf("missing/incorrect bearer: %q", auth)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("want multipart/form-data, got %q", contentType)
	}
	if gotName != "notes.md" {
		t.Fatalf("unexpected filename: %q", gotName)
	}
	if !bytes.Equal(gotBytes, data) {
		t.Fatalf("file bytes mismatch: got %q", gotBytes)
	}
}

func TestKnowledgeUploadFileSendsPathAsMetadata(t *testing.T) {
	var gotMetadata string
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		gotMetadata = r.FormValue("metadata")
		writeJSON(t, w, 200, map[string]any{
			"source_id": "src1", "chunk_count": 1, "token_count": 10,
		})
	})
	defer srv.Close()

	_, err := client.Knowledge.UploadFile(context.Background(), "src1", UploadFileInput{
		Filename: "notes.md",
		Data:     []byte("hello"),
		Path:     []string{"doctor_8", "Chile"},
		Metadata: map[string]any{"tag": "keep"},
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(gotMetadata), &parsed); err != nil {
		t.Fatalf("metadata field is not valid JSON (%q): %v", gotMetadata, err)
	}
	if parsed["l0"] != "doctor_8" {
		t.Fatalf("want l0=doctor_8, got %v", parsed["l0"])
	}
	if parsed["l1"] != "Chile" {
		t.Fatalf("want l1=Chile, got %v", parsed["l1"])
	}
	if parsed["tag"] != "keep" {
		t.Fatalf("caller metadata dropped: %v", parsed["tag"])
	}
}

func TestKnowledgeUploadFileOmitsMetadataWhenUnset(t *testing.T) {
	var hadMetadata bool
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		_, hadMetadata = r.MultipartForm.Value["metadata"]
		writeJSON(t, w, 200, map[string]any{
			"source_id": "src1", "chunk_count": 1, "token_count": 10,
		})
	})
	defer srv.Close()

	_, err := client.Knowledge.UploadFile(context.Background(), "src1", UploadFileInput{
		Filename: "notes.md",
		Data:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if hadMetadata {
		t.Fatalf("metadata field should be absent when no Path/Metadata is set")
	}
}

func TestKnowledgeUploadFileRejectsTooDeepPath(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called when Path exceeds the level cap")
	})
	defer srv.Close()

	_, err := client.Knowledge.UploadFile(context.Background(), "src1", UploadFileInput{
		Filename: "notes.md",
		Data:     []byte("hello"),
		Path:     []string{"a", "b", "c"},
	})
	if !errors.Is(err, ErrAdmin) {
		t.Fatalf("want ErrAdmin for over-deep Path, got %v", err)
	}
}

func TestKnowledgeUploadFileValidatesInput(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called on invalid input")
	})
	defer srv.Close()

	cases := []struct {
		name     string
		sourceID string
		in       UploadFileInput
	}{
		{"empty sourceID", "", UploadFileInput{Filename: "a.md", Data: []byte("x")}},
		{"empty filename", "src1", UploadFileInput{Data: []byte("x")}},
		{"empty data", "src1", UploadFileInput{Filename: "a.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.Knowledge.UploadFile(context.Background(), tc.sourceID, tc.in)
			if !errors.Is(err, ErrAdmin) {
				t.Fatalf("want ErrAdmin, got %v", err)
			}
		})
	}
}

func TestNewClientWiresKnowledge(t *testing.T) {
	client, err := NewClient(ClientOptions{
		APIURL: "https://x",
		APIKey: "dgsk_acc_test",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.Knowledge == nil {
		t.Fatalf("client.Knowledge is nil")
	}
}
