package daguito

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// KnowledgeService is the management surface for knowledge bases and
// sources. It uses the `dgsk_acc_…` / `sk_dgt_…` account credential
// bound to the org — every call resolves the org server-side.
//
// The data-plane KB session (text ingest + search via a per-source
// `sk_dgt_…` key) lives separately in KnowledgeSession; both can be used
// independently or together.
type KnowledgeService struct {
	transport *adminTransport
}

// KnowledgeBase is the wire shape of a knowledge_bases row returned by
// GET /api/public/knowledge/bases.
type KnowledgeBase struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	Dim               int     `json:"dim"`
	TenancyTier       string  `json:"tenancy_tier"`
	EmbeddingProvider string  `json:"embedding_provider"`
	EmbeddingModel    string  `json:"embedding_model"`
}

// KnowledgeSource is the rolled-up "one card per kb" shape the dashboard
// endpoint returns. ID is the latest underlying source row id; SourceCount
// reveals how many raw sources were merged.
type KnowledgeSource struct {
	ID          string  `json:"id"`
	OrgID       string  `json:"org_id"`
	KbID        string  `json:"kb_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	ChunkCount  int     `json:"chunk_count"`
	TokenCount  int     `json:"token_count"`
	SourceCount int     `json:"source_count,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// ListKnowledgeOptions is the input to ListBases / ListSources. OrgID is
// required — the server enforces it against the calling key.
type ListKnowledgeOptions struct {
	OrgID string
}

// CreateKnowledgeSourceInput is the request body for CreateSource. Kind
// is one of "url", "file", "text", "sitemap"; the server defaults to
// "text" when empty.
type CreateKnowledgeSourceInput struct {
	OrgID       string `json:"org_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// IngestURLInput is the request body for IngestURL.
type IngestURLInput struct {
	URL      string         `json:"url"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// IngestTextAdminInput is the request body for IngestText on the admin
// surface. Identical shape to KnowledgeSession.IngestText but routed
// through `/api/public/knowledge/sources/:id/text`.
type IngestTextAdminInput struct {
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// IngestJob is the response of IngestURL / IngestText admin calls plus
// GetIngestJob. The shape mirrors the server's ingest-queue snapshot;
// extra fields (`progress`, `result`, …) ride through as Raw.
type IngestJob struct {
	JobID  string         `json:"job_id"`
	Status string         `json:"status"`
	OrgID  string         `json:"org_id,omitempty"`
	Raw    map[string]any `json:"-"`
}

// UnmarshalJSON keeps the canonical fields typed and stuffs the rest into
// Raw, so callers can read provider-specific extras without losing them
// to a static struct.
func (j *IngestJob) UnmarshalJSON(data []byte) error {
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	j.Raw = generic
	if v, ok := generic["job_id"].(string); ok {
		j.JobID = v
	}
	if v, ok := generic["status"].(string); ok {
		j.Status = v
	}
	if v, ok := generic["org_id"].(string); ok {
		j.OrgID = v
	}
	return nil
}

// ListBases lists every knowledge base owned by the org. OrgID is
// optional — when omitted, the server falls back to the org the API key
// belongs to (account keys live in exactly one org).
func (s *KnowledgeService) ListBases(
	ctx context.Context, opts ListKnowledgeOptions,
) ([]KnowledgeBase, error) {
	path := "/api/public/knowledge/bases"
	if opts.OrgID != "" {
		path += "?org_id=" + url.QueryEscape(opts.OrgID)
	}
	raw, err := s.transport.requestJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Bases []KnowledgeBase `json:"bases"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return envelope.Bases, nil
}

// ListSources lists the org's knowledge sources, already rolled up to one
// entry per knowledge base by the server. OrgID is optional — when
// omitted, the server falls back to the org the API key belongs to.
func (s *KnowledgeService) ListSources(
	ctx context.Context, opts ListKnowledgeOptions,
) ([]KnowledgeSource, error) {
	path := "/api/public/knowledge/sources"
	if opts.OrgID != "" {
		path += "?org_id=" + url.QueryEscape(opts.OrgID)
	}
	raw, err := s.transport.requestJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Sources []KnowledgeSource `json:"sources"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return envelope.Sources, nil
}

// CreateSource provisions a new KB + default source in one call.
func (s *KnowledgeService) CreateSource(
	ctx context.Context, in CreateKnowledgeSourceInput,
) (*KnowledgeSource, error) {
	if in.OrgID == "" || in.Name == "" {
		return nil, fmt.Errorf("%w: OrgID and Name are required", ErrAdmin)
	}
	raw, err := s.transport.requestJSON(ctx, "POST", "/api/public/knowledge/sources", in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Source KnowledgeSource `json:"source"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return &envelope.Source, nil
}

// IngestURL kicks off an asynchronous URL ingest against the given source.
// Poll the returned IngestJob via GetIngestJob until Status == "ready".
func (s *KnowledgeService) IngestURL(
	ctx context.Context, sourceID string, in IngestURLInput,
) (*IngestJob, error) {
	if sourceID == "" || in.URL == "" {
		return nil, fmt.Errorf("%w: sourceID and URL are required", ErrAdmin)
	}
	path := "/api/public/knowledge/sources/" + url.PathEscape(sourceID) + "/url"
	raw, err := s.transport.requestJSON(ctx, "POST", path, in)
	if err != nil {
		return nil, err
	}
	out := &IngestJob{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// IngestText kicks off a text ingest against the given source. The
// data-plane KB session has its own IngestText for the per-source key
// flow; this one uses the org account credential.
func (s *KnowledgeService) IngestText(
	ctx context.Context, sourceID string, in IngestTextAdminInput,
) (*IngestJob, error) {
	if sourceID == "" || in.Text == "" {
		return nil, fmt.Errorf("%w: sourceID and Text are required", ErrAdmin)
	}
	path := "/api/public/knowledge/sources/" + url.PathEscape(sourceID) + "/text"
	raw, err := s.transport.requestJSON(ctx, "POST", path, in)
	if err != nil {
		return nil, err
	}
	out := &IngestJob{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// GetIngestJob fetches the status of an asynchronous ingest job.
func (s *KnowledgeService) GetIngestJob(
	ctx context.Context, jobID string,
) (*IngestJob, error) {
	if jobID == "" {
		return nil, fmt.Errorf("%w: jobID is required", ErrAdmin)
	}
	path := "/api/public/knowledge/ingest-jobs/" + url.PathEscape(jobID)
	raw, err := s.transport.requestJSON(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	out := &IngestJob{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// DeleteSource permanently removes the source row + its vectors.
func (s *KnowledgeService) DeleteSource(ctx context.Context, sourceID string) error {
	if sourceID == "" {
		return fmt.Errorf("%w: sourceID is required", ErrAdmin)
	}
	path := "/api/public/knowledge/sources/" + url.PathEscape(sourceID)
	_, err := s.transport.requestJSON(ctx, "DELETE", path, nil)
	return err
}

// DeleteChunksByMetadataInput targets every chunk in a source whose
// metadata key equals the given value. Idempotent — DeletedCount=0 when
// nothing matches.
type DeleteChunksByMetadataInput struct {
	MetadataKey   string
	MetadataValue string
}

// DeleteChunksByMetadataResult is the response of DeleteChunksByMetadata.
type DeleteChunksByMetadataResult struct {
	DeletedCount int `json:"deleted_count"`
}

// UpdateChunksMetadataMatch is the matcher passed to UpdateChunksMetadata.
type UpdateChunksMetadataMatch struct {
	MetadataKey   string
	MetadataValue string
}

// UpdateChunksMetadataInput patches every chunk in a source that matches
// Match. Patch keys overwrite, others are preserved (shallow merge).
type UpdateChunksMetadataInput struct {
	Match UpdateChunksMetadataMatch
	Patch map[string]any
}

// UpdateChunksMetadataResult is the response of UpdateChunksMetadata.
type UpdateChunksMetadataResult struct {
	UpdatedCount int `json:"updated_count"`
}

// DeleteChunksByMetadata deletes every chunk in `sourceID` whose
// metadata key/value matches `in`. Returns 0 + nil error when nothing
// matches (idempotent).
func (s *KnowledgeService) DeleteChunksByMetadata(
	ctx context.Context, sourceID string, in DeleteChunksByMetadataInput,
) (*DeleteChunksByMetadataResult, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("%w: sourceID is required", ErrAdmin)
	}
	if in.MetadataKey == "" {
		return nil, fmt.Errorf("%w: MetadataKey is required", ErrAdmin)
	}
	q := url.Values{}
	q.Set("metadata_key", in.MetadataKey)
	q.Set("metadata_value", in.MetadataValue)
	path := "/api/public/knowledge/sources/" + url.PathEscape(sourceID) + "/chunks?" + q.Encode()
	raw, err := s.transport.requestJSON(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, err
	}
	out := &DeleteChunksByMetadataResult{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}

// UpdateChunksMetadata patches metadata of every chunk in `sourceID`
// matching `in.Match`. The wire body uses snake_case keys
// (`metadata_key`, `metadata_value`); this method translates from the
// CamelCase Go input.
func (s *KnowledgeService) UpdateChunksMetadata(
	ctx context.Context, sourceID string, in UpdateChunksMetadataInput,
) (*UpdateChunksMetadataResult, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("%w: sourceID is required", ErrAdmin)
	}
	if in.Match.MetadataKey == "" {
		return nil, fmt.Errorf("%w: Match.MetadataKey is required", ErrAdmin)
	}
	if in.Patch == nil {
		return nil, fmt.Errorf("%w: Patch is required", ErrAdmin)
	}
	body := map[string]any{
		"match": map[string]any{
			"metadata_key":   in.Match.MetadataKey,
			"metadata_value": in.Match.MetadataValue,
		},
		"patch": in.Patch,
	}
	path := "/api/public/knowledge/sources/" + url.PathEscape(sourceID) + "/chunks"
	raw, err := s.transport.requestJSON(ctx, "PATCH", path, body)
	if err != nil {
		return nil, err
	}
	out := &UpdateChunksMetadataResult{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrAdmin, err)
	}
	return out, nil
}
