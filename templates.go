package daguito

import (
	"context"
	"encoding/json"
	"fmt"
)

// TemplatesService wraps the `/v1/templates/preview` endpoint. The
// endpoint inspects a template body that uses
// `[[natural-language description]]` placeholders, infers a JSON schema
// for it, and (when the server decides to run the extractor) asks the
// model to extract a sample payload. The placeholder body is plain
// description — there is no `| type` syntax, the model infers the field
// type downstream.
//
// Auth is the same `dgsk_acc_…` bearer key used by FlowsService —
// templates run in the org bound to the account key.
type TemplatesService struct {
	transport *adminTransport
}

// TemplatePreviewInput is the request body for Preview. TemplateBody is
// required; every other field is optional.
type TemplatePreviewInput struct {
	TemplateBody    string `json:"template_body"`
	Vertical        string `json:"vertical,omitempty"`
	Model           string `json:"model,omitempty"`
	ForceRegenerate bool   `json:"force_regenerate,omitempty"`
}

// TemplateSchema is the JSON schema inferred from the template body.
// Schema mirrors the JSON Schema object the API returns — kept as a
// generic map because callers compose it into LLM tool definitions.
type TemplateSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// TemplateFieldDetail describes a single placeholder in the template,
// with the inferred type and (for enum fields) the candidate values.
type TemplateFieldDetail struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	EnumValues  []string `json:"enum_values,omitempty"`
}

// TemplatePreviewExample is the model-extracted example payload the
// server returns when an example transcript is available. Extracted is
// a free-form map keyed by placeholder name — its values come from the
// LLM and reflect the inferred schema.
type TemplatePreviewExample struct {
	Transcript       string         `json:"transcript"`
	TranscriptOrigin string         `json:"transcript_origin"`
	Extracted        map[string]any `json:"extracted"`
	Model            string         `json:"model"`
}

// TemplatePreviewWarning is a non-fatal hint emitted by the parser
// (placeholder with no body, duplicate name, etc).
type TemplatePreviewWarning struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// TemplatePreviewResult is what the server returns from
// POST /v1/templates/preview. Example is nil when the API did not run
// the extractor (e.g. no transcript supplied + Generate disabled).
// BodyHash is a stable digest of the input template body — callers can
// use it as a cache key for downstream LLM prompts.
type TemplatePreviewResult struct {
	TemplateSchema TemplateSchema           `json:"template_schema"`
	FieldNames     []string                 `json:"field_names"`
	FieldCount     int                      `json:"field_count"`
	FieldsDetail   []TemplateFieldDetail    `json:"fields_detail"`
	Example        *TemplatePreviewExample  `json:"example"`
	Warnings       []TemplatePreviewWarning `json:"warnings"`
	BodyHash       string                   `json:"body_hash"`
}

// Preview wraps POST /v1/templates/preview. TemplateBody is required;
// returns the inferred schema, the typed field list, optional warnings,
// the body hash, and (when the API ran the extractor) an example payload.
func (s *TemplatesService) Preview(
	ctx context.Context, in TemplatePreviewInput,
) (*TemplatePreviewResult, error) {
	if in.TemplateBody == "" {
		return nil, fmt.Errorf("%w: template_body is required", ErrAdmin)
	}
	raw, err := s.transport.requestJSON(ctx, "POST", "/v1/templates/preview", in)
	if err != nil {
		return nil, err
	}
	out := &TemplatePreviewResult{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
