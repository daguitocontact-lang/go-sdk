//go:build integration

package daguito

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Integration tests for the admin surface against a real Daguito API.
//
// Skip-by-default. Run with:
//
//	DAGUITO_API_URL=https://api-local.daguito.com \
//	DAGUITO_API_KEY=sk_dgt_... \
//	DAGUITO_KB_SOURCE_ID=a63fa3c1-... \
//	go test -run Integration ./...
//
// The tests are read-mostly. Anything that writes (UpsertAgent) cleans
// up via t.Cleanup so failures don't leave junk in the org.

const seedKBSourceFallback = "a63fa3c1-63b1-4c09-92df-9599ba44f68c"

func integrationClient(t *testing.T) *Client {
	t.Helper()
	apiURL := os.Getenv("DAGUITO_API_URL")
	apiKey := os.Getenv("DAGUITO_API_KEY")
	if apiURL == "" || apiKey == "" {
		t.Skip("DAGUITO_API_URL + DAGUITO_API_KEY required for integration tests")
	}
	client, err := NewClient(ClientOptions{APIURL: apiURL, APIKey: apiKey})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func seedSourceID() string {
	if v := os.Getenv("DAGUITO_KB_SOURCE_ID"); v != "" {
		return v
	}
	return seedKBSourceFallback
}

func TestIntegrationAnalyticsGetOrg(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := client.Analytics.GetOrg(ctx)
	if err != nil {
		t.Fatalf("Analytics.GetOrg: %v", err)
	}
	if out.OrgID == "" {
		t.Fatalf("expected org_id, got %+v", out)
	}
	t.Logf("org_id=%s llm_requests_today=%d outbound_24h=%d",
		out.OrgID, out.LLMToday.Requests, out.MessagesToday.OutboundLast24h)
}

func TestIntegrationNodesGetCatalog(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	catalog, err := client.Nodes.GetCatalog(ctx)
	if err != nil {
		t.Fatalf("Nodes.GetCatalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatalf("expected non-empty catalog, got %+v", catalog)
	}
	if _, ok := catalog["nodes"]; !ok {
		t.Fatalf("expected `nodes` key in catalog, got keys %v", keysOf(catalog))
	}
}

func TestIntegrationKnowledgeListSources(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sources, err := client.Knowledge.ListSources(ctx, ListKnowledgeOptions{})
	if err != nil {
		t.Fatalf("Knowledge.ListSources: %v", err)
	}

	want := seedSourceID()
	found := false
	for _, src := range sources {
		if src.ID == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected seed source %s, got %d sources", want, len(sources))
	}
}

func TestIntegrationKnowledgeListBases(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	bases, err := client.Knowledge.ListBases(ctx, ListKnowledgeOptions{})
	if err != nil {
		t.Fatalf("Knowledge.ListBases: %v", err)
	}
	if len(bases) == 0 {
		t.Fatalf("expected at least one KB, got 0")
	}
}

func TestIntegrationTemplatesParseAndExample(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	body := "# SOAP Note\nS: [[motivo de consulta]]\nO: [[hallazgos del examen]]"
	parsed, err := client.Templates.Parse(ctx, TemplateParseInput{
		TemplateBody:    body,
		Vertical:        "medical",
		ForceRegenerate: false,
	})
	if err != nil {
		t.Fatalf("Templates.Parse: %v", err)
	}
	if parsed.FieldCount == 0 {
		t.Fatalf("expected fields, got %+v", parsed)
	}
	if parsed.BodyHash == "" {
		t.Fatalf("expected body_hash, got %+v", parsed)
	}

	example, err := client.Templates.Example(ctx, TemplateExampleInput{
		TemplateSchema: parsed.TemplateSchema,
		Vertical:       "medical",
	})
	if err != nil {
		t.Fatalf("Templates.Example: %v", err)
	}
	if example == nil {
		t.Fatalf("expected example result")
	}
}

func TestIntegrationFlowsUpsertResolveGetDelete(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slug := fmt.Sprintf("sdk-go-test-agent-%d", time.Now().UnixNano())
	spec := AgentFlowSpec{
		Slug:         slug,
		Name:         "SDK Go test agent",
		Provider:     "openrouter",
		Model:        "deepseek/deepseek-v4-flash",
		SystemPrompt: "You are a test agent created by the Go SDK integration suite.",
	}

	first, err := client.Flows.UpsertAgent(ctx, spec)
	if err != nil {
		t.Fatalf("first UpsertAgent: %v", err)
	}
	if !first.Created {
		t.Fatalf("first UpsertAgent: expected created=true, got %+v", first)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		if err := client.Flows.Delete(cleanupCtx, first.FlowID); err != nil {
			t.Logf("cleanup Delete(%s): %v", first.FlowID, err)
		}
	})

	second, err := client.Flows.UpsertAgent(ctx, spec)
	if err != nil {
		t.Fatalf("second UpsertAgent: %v", err)
	}
	if second.Created {
		t.Fatalf("second UpsertAgent: expected created=false, got %+v", second)
	}
	if second.FlowID != first.FlowID || second.WebhookID != first.WebhookID {
		t.Fatalf("upsert ids drifted: first=%+v second=%+v", first, second)
	}

	resolved, err := client.Flows.ResolveWebhook(ctx, slug)
	if err != nil {
		t.Fatalf("ResolveWebhook: %v", err)
	}
	if resolved.FlowID != first.FlowID || resolved.WebhookID != first.WebhookID {
		t.Fatalf("ResolveWebhook drift: upsert=%+v resolve=%+v", first, resolved)
	}
	if !strings.HasPrefix(resolved.WebhookToken, "sk_wh_") {
		t.Fatalf("expected sk_wh_ token prefix, got %q", resolved.WebhookToken)
	}

	flow, err := client.Flows.Get(ctx, first.FlowID)
	if err != nil {
		t.Fatalf("Flows.Get: %v", err)
	}
	if flow.ID != first.FlowID {
		t.Fatalf("Flows.Get returned wrong id: want=%s got=%s", first.FlowID, flow.ID)
	}
}

func TestIntegrationFlowsList(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flows, err := client.Flows.List(ctx, ListFlowsOptions{})
	if err != nil {
		t.Fatalf("Flows.List: %v", err)
	}
	t.Logf("found %d flows in caller's org", len(flows))
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestIntegrationKnowledgeChunkMetadataRoundTrip(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	sourceID := os.Getenv("DAGUITO_LAWYERS_SOURCE_ID")
	if sourceID == "" {
		sourceID = seedSourceID()
	}

	stamp := time.Now().UnixNano()
	idA := fmt.Sprintf("test-delete-by-metadata-A-%d", stamp)
	idB := fmt.Sprintf("test-delete-by-metadata-B-%d", stamp)

	_, err := client.Knowledge.IngestText(ctx, sourceID, IngestTextAdminInput{
		Text:     fmt.Sprintf("Lawyer profile A %d. Specializes in tax law.", stamp),
		Metadata: map[string]any{"lawyer_id": idA, "marker": "chunk-ops-suite"},
	})
	if err != nil {
		t.Fatalf("IngestText A: %v", err)
	}
	_, err = client.Knowledge.IngestText(ctx, sourceID, IngestTextAdminInput{
		Text:     fmt.Sprintf("Lawyer profile B %d. Specializes in maritime law.", stamp),
		Metadata: map[string]any{"lawyer_id": idB, "marker": "chunk-ops-suite"},
	})
	if err != nil {
		t.Fatalf("IngestText B: %v", err)
	}

	patched, err := client.Knowledge.UpdateChunksMetadata(ctx, sourceID, UpdateChunksMetadataInput{
		Match: UpdateChunksMetadataMatch{MetadataKey: "lawyer_id", MetadataValue: idA},
		Patch: map[string]any{"city": "Bogota"},
	})
	if err != nil {
		t.Fatalf("UpdateChunksMetadata: %v", err)
	}
	if patched.UpdatedCount < 1 {
		t.Fatalf("expected at least 1 updated chunk, got %d", patched.UpdatedCount)
	}

	deletedB, err := client.Knowledge.DeleteChunksByMetadata(
		ctx, sourceID, DeleteChunksByMetadataInput{MetadataKey: "lawyer_id", MetadataValue: idB},
	)
	if err != nil {
		t.Fatalf("DeleteChunksByMetadata B: %v", err)
	}
	if deletedB.DeletedCount < 1 {
		t.Fatalf("expected at least 1 deleted chunk for B, got %d", deletedB.DeletedCount)
	}

	missing, err := client.Knowledge.DeleteChunksByMetadata(
		ctx,
		sourceID,
		DeleteChunksByMetadataInput{
			MetadataKey:   "lawyer_id",
			MetadataValue: fmt.Sprintf("does-not-exist-%d", stamp),
		},
	)
	if err != nil {
		t.Fatalf("DeleteChunksByMetadata missing: %v", err)
	}
	if missing.DeletedCount != 0 {
		t.Fatalf("expected 0 deleted for missing metadata, got %d", missing.DeletedCount)
	}

	// Cleanup A.
	_, err = client.Knowledge.DeleteChunksByMetadata(
		ctx, sourceID, DeleteChunksByMetadataInput{MetadataKey: "lawyer_id", MetadataValue: idA},
	)
	if err != nil {
		t.Logf("cleanup DeleteChunksByMetadata A: %v", err)
	}
}

func TestIntegrationModelsList(t *testing.T) {
	client := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	models, err := client.Models.List(ctx)
	if err != nil {
		t.Fatalf("Models.List: %v", err)
	}
	if len(models) == 0 {
		t.Fatalf("expected at least one model in the catalog")
	}
	for _, m := range models {
		if m.Provider == "" || m.ID == "" {
			t.Fatalf("model row missing provider or id: %+v", m)
		}
	}
}
