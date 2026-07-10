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

// KnowledgeSessionOptions configures a KnowledgeSession.
type KnowledgeSessionOptions struct {
	APIURL          string
	APIKey          string // sk_dgt_... — kb:read / kb:write scoped key
	DefaultSourceID string // optional default sourceId for IngestText
	Timeout         time.Duration
	HTTPClient      *http.Client
}

// IngestTextInput is the input to KnowledgeSession.IngestText.
type IngestTextInput struct {
	Text     string
	Metadata map[string]any
	SourceID string // overrides DefaultSourceID when set
	// Path routes the chunk into a subtree of the knowledge base. Each entry
	// is expanded positionally to a reserved metadata key (Path[0]→l0,
	// Path[1]→l1) and merged into Metadata (Path wins on conflict). Capped at
	// two category levels — a longer Path is rejected, never truncated.
	Path []string
}

// IngestTextResult is the response of a successful text-ingest.
type IngestTextResult struct {
	SourceID   string
	ChunkCount int
	TokenCount int
}

// SearchInput is the input to KnowledgeSession.Search. Set pointers via the
// helper constructors below — the SDK omits nil pointers from the wire body
// so server defaults apply.
type SearchInput struct {
	Query     string
	TopK      *int
	SourceIDs []string
	// MetadataEquals filters retrieval to chunks whose ingest-time metadata
	// matches every given key/value (AND). Combine with SourceIDs to scope a
	// search to a structured slice of the knowledge base.
	MetadataEquals map[string]any
	// Path routes the search to a subtree — expanded positionally to the
	// reserved level keys (Path[0]→l0, Path[1]→l1) and merged into
	// MetadataEquals (Path wins on conflict). Capped at two category levels.
	Path          []string
	EnableRewrite *bool
	EnableRerank  *bool
	EnableCache   *bool
}

// SearchHit is a single chunk returned by the KB search.
type SearchHit struct {
	ID       string
	SourceID string
	Content  string
	Score    float64
	Metadata map[string]any
}

// SearchResult is the response of a KB search.
type SearchResult struct {
	Hits           []SearchHit
	QueryOriginal  string
	QueryRewritten string
	Raw            map[string]any
}

// KnowledgeSession is an HTTP client for the Daguito KB ingest + search
// endpoints. It is safe to reuse across goroutines.
type KnowledgeSession struct {
	opts   KnowledgeSessionOptions
	client *http.Client
}

// NewKnowledgeSession constructs a KB client. No I/O happens here.
func NewKnowledgeSession(opts KnowledgeSessionOptions) *KnowledgeSession {
	client := opts.HTTPClient
	if client == nil {
		timeout := opts.Timeout
		if timeout == 0 {
			timeout = 60 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	return &KnowledgeSession{opts: opts, client: client}
}

// IngestText ingests a text blob into the configured source. The server
// chunks, embeds, and indexes the text. Returns the chunk + token counts so
// the caller can audit cost.
func (s *KnowledgeSession) IngestText(ctx context.Context, in IngestTextInput) (*IngestTextResult, error) {
	sourceID := in.SourceID
	if sourceID == "" {
		sourceID = s.opts.DefaultSourceID
	}
	if sourceID == "" {
		return nil, fmt.Errorf("%w: SourceID is required (pass to method or set DefaultSourceID)", ErrKnowledge)
	}

	metadata, err := expandKnowledgePath(in.Metadata, in.Path, ErrKnowledge)
	if err != nil {
		return nil, err
	}

	endpoint := joinHTTP(s.opts.APIURL, "/api/sdk/knowledge/sources/"+url.PathEscape(sourceID)+"/text")
	body := map[string]any{"text": in.Text}
	if metadata != nil {
		body["metadata"] = metadata
	}

	parsed, err := s.post(ctx, endpoint, body)
	if err != nil {
		return nil, err
	}
	res := &IngestTextResult{SourceID: sourceID}
	if v, ok := parsed["source_id"].(string); ok {
		res.SourceID = v
	}
	if v, ok := numberField(parsed, "chunk_count"); ok {
		res.ChunkCount = int(v)
	}
	if v, ok := numberField(parsed, "token_count"); ok {
		res.TokenCount = int(v)
	}
	return res, nil
}

// Search runs a hybrid (BM25 + vector) search against the KB.
func (s *KnowledgeSession) Search(ctx context.Context, in SearchInput) (*SearchResult, error) {
	metadataEquals, err := expandKnowledgePath(in.MetadataEquals, in.Path, ErrKnowledge)
	if err != nil {
		return nil, err
	}

	endpoint := joinHTTP(s.opts.APIURL, "/api/sdk/knowledge/search")
	body := map[string]any{"query": in.Query}
	if in.TopK != nil {
		body["top_k"] = *in.TopK
	}
	if in.SourceIDs != nil {
		body["source_ids"] = in.SourceIDs
	}
	if metadataEquals != nil {
		body["metadata_equals"] = metadataEquals
	}
	if in.EnableRewrite != nil {
		body["enable_rewrite"] = *in.EnableRewrite
	}
	if in.EnableRerank != nil {
		body["enable_rerank"] = *in.EnableRerank
	}
	if in.EnableCache != nil {
		body["enable_cache"] = *in.EnableCache
	}

	parsed, err := s.post(ctx, endpoint, body)
	if err != nil {
		return nil, err
	}
	out := &SearchResult{
		QueryOriginal: stringField(parsed, "query_original"),
		Raw:           parsed,
	}
	if out.QueryOriginal == "" {
		out.QueryOriginal = in.Query
	}
	if v, ok := parsed["query_rewritten"].(string); ok {
		out.QueryRewritten = v
	}
	if rawChunks, ok := parsed["chunks"].([]any); ok {
		for _, item := range rawChunks {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			hit := SearchHit{
				ID:       stringField(m, "id"),
				SourceID: stringField(m, "source_id"),
				Content:  stringField(m, "content"),
			}
			if v, ok := numberField(m, "score"); ok {
				hit.Score = v
			}
			if md, ok := m["metadata"].(map[string]any); ok {
				hit.Metadata = md
			}
			out.Hits = append(out.Hits, hit)
		}
	}
	return out, nil
}

func (s *KnowledgeSession) post(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	if s.opts.APIKey == "" {
		return nil, fmt.Errorf("%w: APIKey is required", ErrInvalidToken)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrKnowledge, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKnowledge, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.opts.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKnowledge, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrKnowledge, err)
	}
	if resp.StatusCode >= 400 {
		base := ErrKnowledge
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
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
		return nil, fmt.Errorf("%w: invalid JSON response: %v", ErrKnowledge, err)
	}
	return parsed, nil
}
