package daguito

import (
	"net/http"
	"strings"
	"testing"
)

func TestApplyClientHeaders(t *testing.T) {
	h := http.Header{}
	applyClientHeaders(h)
	if got := h.Get("X-Daguito-Client-Lang"); got != "go" {
		t.Fatalf("lang: want go, got %q", got)
	}
	if got := h.Get("X-Daguito-Client-Version"); got == "" {
		t.Fatalf("version: empty")
	}
	if got := h.Get("X-Daguito-Client"); !strings.HasPrefix(got, "daguito-sdk-go/") {
		t.Fatalf("client: bad prefix, got %q", got)
	}
}

func TestAppendClientQueryParams(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"no query", "wss://api.daguito.com/v1/stream"},
		{"with query", "wss://api.daguito.com/v1/stream?token=abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := appendClientQueryParams(tc.in)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !strings.Contains(out, "x_daguito_client_lang=go") {
				t.Fatalf("missing lang param: %q", out)
			}
			if !strings.Contains(out, "x_daguito_client_version=") {
				t.Fatalf("missing version param: %q", out)
			}
		})
	}
}
