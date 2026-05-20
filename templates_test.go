package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func okPreviewResponse() map[string]any {
	return map[string]any{
		"template_schema": map[string]any{
			"name": "SOAP_27",
			"schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"motivo": map[string]any{"type": "string"}},
			},
		},
		"field_names": []string{"motivo", "alergias"},
		"field_count": 2,
		"fields_detail": []map[string]any{
			{"name": "motivo", "description": "razón de la consulta", "type": "string"},
			{
				"name":        "severidad",
				"description": "severidad reportada",
				"type":        "enum",
				"enum_values": []string{"leve", "moderada", "grave"},
			},
		},
		"example": map[string]any{
			"transcript":        "Doctor, me duele la cabeza",
			"transcript_origin": "default",
			"extracted":         map[string]any{"motivo": "cefalea", "alergias": nil},
			"model":             "deepseek-v4-flash",
		},
		"warnings": []map[string]any{
			{"code": "placeholder_empty", "field": "extra", "message": "no body"},
		},
		"body_hash": "sha256:abc123",
	}
}

func TestTemplatesPreviewHappyPath(t *testing.T) {
	var captured *http.Request
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		captured = r
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, okPreviewResponse())
	})
	defer srv.Close()

	result, err := client.Templates.Preview(context.Background(), TemplatePreviewInput{
		TemplateBody:    "[[razón de la consulta]] [[alergias del paciente]]",
		Vertical:        "medical",
		Model:           "deepseek-v4-flash",
		ForceRegenerate: true,
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	if captured.Method != "POST" {
		t.Fatalf("want POST, got %s", captured.Method)
	}
	if !strings.HasSuffix(captured.URL.Path, "/v1/templates/preview") {
		t.Fatalf("unexpected path: %s", captured.URL.Path)
	}
	if got := captured.Header.Get("Authorization"); got != "Bearer dgsk_acc_test" {
		t.Fatalf("missing bearer: %q", got)
	}

	if capturedBody["template_body"] != "[[razón de la consulta]] [[alergias del paciente]]" {
		t.Fatalf("template_body not posted: %+v", capturedBody)
	}
	if capturedBody["vertical"] != "medical" {
		t.Fatalf("vertical not posted: %+v", capturedBody)
	}
	if capturedBody["force_regenerate"] != true {
		t.Fatalf("force_regenerate not posted: %+v", capturedBody)
	}

	if result.FieldCount != 2 || len(result.FieldNames) != 2 {
		t.Fatalf("field metadata wrong: %+v", result)
	}
	if result.TemplateSchema.Name != "SOAP_27" {
		t.Fatalf("schema.name wrong: %+v", result.TemplateSchema)
	}
	if result.FieldsDetail[1].Type != "enum" {
		t.Fatalf("enum type wrong: %+v", result.FieldsDetail[1])
	}
	if len(result.FieldsDetail[1].EnumValues) != 3 {
		t.Fatalf("enum_values wrong: %+v", result.FieldsDetail[1])
	}
	if result.Example == nil || result.Example.TranscriptOrigin != "default" {
		t.Fatalf("example wrong: %+v", result.Example)
	}
	if result.Example.Extracted["motivo"] != "cefalea" {
		t.Fatalf("extracted wrong: %+v", result.Example.Extracted)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "placeholder_empty" {
		t.Fatalf("warnings wrong: %+v", result.Warnings)
	}
	if result.BodyHash != "sha256:abc123" {
		t.Fatalf("body_hash wrong: %q", result.BodyHash)
	}
}

func TestTemplatesPreviewOmitsOptionalFields(t *testing.T) {
	var capturedBody map[string]any
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		writeJSON(t, w, 200, okPreviewResponse())
	})
	defer srv.Close()

	if _, err := client.Templates.Preview(context.Background(), TemplatePreviewInput{
		TemplateBody: "[[motivo]]",
	}); err != nil {
		t.Fatalf("Preview minimal: %v", err)
	}
	for _, key := range []string{"vertical", "model", "force_regenerate"} {
		if _, exists := capturedBody[key]; exists {
			t.Fatalf("optional field %q must be omitted when zero: %+v", key, capturedBody)
		}
	}
}

func TestTemplatesPreviewRejectsEmptyBody(t *testing.T) {
	called := false
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		writeJSON(t, w, 200, okPreviewResponse())
	})
	defer srv.Close()

	_, err := client.Templates.Preview(context.Background(), TemplatePreviewInput{})
	if err == nil {
		t.Fatalf("expected error for empty TemplateBody")
	}
	if !errors.Is(err, ErrAdmin) {
		t.Fatalf("want ErrAdmin, got %v", err)
	}
	if called {
		t.Fatalf("HTTP must not be called when TemplateBody is empty")
	}
}

func TestTemplatesPreviewForbiddenMapsToInvalidToken(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 403, map[string]any{"error": "account key required"})
	})
	defer srv.Close()

	_, err := client.Templates.Preview(context.Background(), TemplatePreviewInput{
		TemplateBody: "[[a]]",
	})
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestTemplatesPreviewNullExample(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{
			"template_schema": map[string]any{"name": "X", "schema": map[string]any{}},
			"field_names":     []string{},
			"field_count":     0,
			"fields_detail":   []map[string]any{},
			"example":         nil,
			"warnings":        []map[string]any{},
			"body_hash":       "sha256:empty",
		})
	})
	defer srv.Close()

	result, err := client.Templates.Preview(context.Background(), TemplatePreviewInput{
		TemplateBody: "no placeholders",
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if result.Example != nil {
		t.Fatalf("want nil example, got %+v", result.Example)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("want no warnings, got %+v", result.Warnings)
	}
	if result.BodyHash != "sha256:empty" {
		t.Fatalf("body_hash wrong: %q", result.BodyHash)
	}
}
