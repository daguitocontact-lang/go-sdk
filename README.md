<p align="center">
  <a href="https://daguito.com" target="_blank">
    <img src="https://raw.githubusercontent.com/daguitocontact-lang/go-sdk/main/assets/logo.png" alt="Daguito" width="160" />
  </a>
</p>

<h1 align="center">daguito (Go SDK)</h1>

<p align="center">
  Official Go SDK for the
  <a href="https://daguito.com">Daguito</a>
  conversational AI platform — text, voice, image, audio, document and video agent flows.
</p>

---

Standard-library HTTP, `github.com/coder/websocket` for the streaming session. Go 1.22+. Mirrors the [Python](https://github.com/daguitocontact-lang/python-sdk) and [TypeScript](https://github.com/daguitocontact-lang/js-sdk) SDKs feature-for-feature.

```bash
go get github.com/daguitocontact-lang/go-sdk
```

```go
import "github.com/daguitocontact-lang/go-sdk"
```

## What's in the box

| Symbol                  | Use it for                                                              |
| ----------------------- | ----------------------------------------------------------------------- |
| `RunWebhook`            | One-shot HTTP call to a flow. Wait, get the result.                     |
| `WebhookStreamSession`  | Long-lived WebSocket. Streams tokens, node lifecycle, custom emits.     |
| `UploadFile`            | Presigned upload for image / audio / document / video attachments.      |
| `session.RegisterTool`  | Register OpenAI-style function tools the LLM can invoke on your code.   |
| `WebhookStreamOptions.Scope` | Server-enforced metadata filter for KB searches (data isolation).  |
| `KnowledgeSession`      | Ingest + search a Knowledge Base with a `sk_dgt_...` org key.           |

Every WebSocket event is a typed struct delivered on `session.Events()` — switch on `evt.Type` and read the matching pointer.

## Authentication

| Surface          | Key shape       | Best for                                        |
| ---------------- | --------------- | ----------------------------------------------- |
| Webhook          | `sk_wh_...`     | Server-to-server, your own backend, scripts     |
| Knowledge Base   | `sk_dgt_...`    | Ingest + search against your own KB             |

Create both from the Daguito dashboard.

## Quick start

### One-shot webhook

```go
ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
defer cancel()

result, err := daguito.RunWebhook(ctx, daguito.WebhookRunInput{
    APIURL: "https://ingest.daguito.com",
    Token:  "sk_wh_...",
    Input:  map[string]any{"question": "What is the capital of France?"},
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Output)
```

### Streaming a chat agent

```go
session := daguito.NewWebhookStreamSession(daguito.WebhookStreamOptions{
    APIURL:    "https://ingest.daguito.com",
    WebhookID: "wh_abc123",
    Token:     "sk_wh_...",
})
if err := session.Connect(ctx); err != nil { log.Fatal(err) }
defer session.Close()

_ = session.Send(ctx, daguito.TextMessage("Hello!"), nil)

for evt := range session.Events() {
    switch evt.Type {
    case daguito.EventNodeToken:
        fmt.Print(evt.NodeToken.Text)
    case daguito.EventFlowCompleted:
        return
    case daguito.EventFlowFailed:
        log.Printf("failed: %s", evt.FlowFailed.Error)
        return
    }
}
```

### Sending attachments

**Pre-uploaded media key**:

```go
up, err := daguito.UploadFile(ctx, daguito.UploadInput{
    APIURL:    "https://ingest.daguito.com",
    WebhookID: "wh_abc123",
    Token:     "sk_wh_...",
    Kind:      daguito.MediaKindDocument,
    Path:      "/tmp/report.pdf",
})
if err != nil { log.Fatal(err) }

_ = session.Send(ctx, daguito.MediaKeyMessage(
    daguito.MediaKindDocument,
    up.MediaKey,
    up.MimeType,
    up.SizeBytes,
    "Summarize this report",
), nil)
```

**Public image URL** (no upload, fastest path):

```go
_ = session.Send(ctx, daguito.ImageURLMessage("https://example.com/photo.jpg", "What's in this image?"), nil)

_ = session.Send(ctx, daguito.ImageMultiMessage(
    []string{"https://example.com/a.jpg", "https://example.com/b.jpg"},
    "Compare these two",
), nil)
```

### Per-session scope (server-enforced KB filter)

When your KB holds data for many users / workspaces / documents, you want each chat to only see chunks tagged with the right key. Set `Scope` on the session — Daguito **forces** every KB search the agent makes to apply it as a metadata filter, server-side. The LLM never sees the values, so it can't widen the search or leak across tenants.

```go
session := daguito.NewWebhookStreamSession(daguito.WebhookStreamOptions{
    APIURL:    "https://ingest.daguito.com",
    WebhookID: "wh_abc123",
    Token:     "sk_wh_...",
    Scope: map[string]any{
        "workspace_id": "ws_42",
        "document_id":  "doc_abc",
    },
})
```

Scope values must be primitives (`string`, `int`, `float64`, `bool`); arrays and maps are silently dropped at the wire boundary.

### Client-side tools (function calling)

Tools registered on the session run locally — Go code, in your process — and their return value is fed back to the LLM as the tool result. Same shape as OpenAI function calling.

```go
err := session.RegisterTool(
    daguito.ToolSpec{
        Name:        "get_weather",
        Description: "Get the current weather for a city.",
        Parameters: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "city":  map[string]any{"type": "string"},
                "units": map[string]any{"type": "string", "enum": []string{"c", "f"}},
            },
            "required": []string{"city"},
        },
    },
    func(ctx context.Context, raw json.RawMessage) (any, error) {
        var args struct {
            City  string `json:"city"`
            Units string `json:"units"`
        }
        if err := json.Unmarshal(raw, &args); err != nil { return nil, err }
        data, err := myWeather.Fetch(ctx, args.City, args.Units)
        if err != nil { return nil, err }
        return map[string]any{"temp": data.Temp, "conditions": data.Summary}, nil
    },
)
```

Return an error to surface a failure to the LLM. Tools are merged with whatever the flow already declares server-side — the LLM picks the best fit.

### Tool progress events (data-only)

When a server-side tool runs (KB search, media analysis, web search), the engine emits `tool_progress` events. They're **data-only** — no localised strings — so your client renders whatever copy/UI you want.

```go
for evt := range session.Events() {
    if evt.Type != daguito.EventNodeEmit { continue }
    progress := daguito.ParseToolProgress(evt.NodeEmit)
    if progress == nil { continue }
    fmt.Printf("[%s] %s\n", progress.Tool, progress.Stage)
}
```

### Knowledge Base

```go
kb := daguito.NewKnowledgeSession(daguito.KnowledgeSessionOptions{
    APIURL:          "https://ingest.daguito.com",
    APIKey:          "sk_dgt_...",
    DefaultSourceID: "src_abc123",
})

_, err := kb.IngestText(ctx, daguito.IngestTextInput{
    Text:     "Daguito is a conversational AI platform...",
    Metadata: map[string]any{"workspace_id": "ws_42", "kind": "doc"},
})

topK := 5
result, err := kb.Search(ctx, daguito.SearchInput{Query: "what is daguito", TopK: &topK})
for _, h := range result.Hits {
    fmt.Println(h.Score, h.Content)
}
```

`APIKey` scopes (`kb:read`, `kb:write`) are configured in the dashboard.

## Event reference

| `evt.Type`            | Pointer field         | When                          |
| --------------------- | --------------------- | ----------------------------- |
| `EventReady`          | `evt.Ready`           | Socket authenticated          |
| `EventClosed`         | `evt.Closed`          | Transport closed              |
| `EventNodeStarted`    | `evt.NodeStarted`     | Engine entered a node         |
| `EventNodeToken`      | `evt.NodeToken`       | LLM streaming token           |
| `EventNodeCompleted`  | `evt.NodeCompleted`   | Node finished                 |
| `EventNodeFailed`     | `evt.NodeFailed`      | Node errored                  |
| `EventNodeEmit`       | `evt.NodeEmit`        | Tool progress / custom emits  |
| `EventFlowCompleted`  | `evt.FlowCompleted`   | Engine finished               |
| `EventFlowFailed`     | `evt.FlowFailed`      | Engine errored                |
| `EventError` | `evt.Error`           | Protocol-level error          |

## Errors

The SDK exports sentinel errors so callers branch with `errors.Is`:

```go
if errors.Is(err, daguito.ErrInvalidToken) { /* re-auth */ }
if errors.Is(err, daguito.ErrUploadFailed) { /* retry with backoff */ }
```

Sentinels: `ErrInvalidToken`, `ErrWebhook`, `ErrUploadFailed`, `ErrKnowledge`, `ErrStream`.

## Examples

Runnable programs under [`examples/`](./examples):

- `examples/run_webhook` — one-shot HTTP call.
- `examples/stream_session` — streaming session with a client-side tool.

```bash
DAGUITO_API_URL=https://ingest.daguito.com \
DAGUITO_WEBHOOK_ID=wh_abc123 \
DAGUITO_WEBHOOK_TOKEN=sk_wh_... \
go run ./examples/stream_session
```

## Resources

- [daguito.com](https://daguito.com) — landing & dashboard
- [docs.daguito.com](https://docs.daguito.com) — full API + flow reference
- [Python SDK](https://github.com/daguitocontact-lang/python-sdk) — same surface, different runtime
- [TypeScript SDK](https://github.com/daguitocontact-lang/js-sdk) — same surface, different runtime
- [Issues](https://github.com/daguitocontact-lang/go-sdk/issues)

## License

MIT © [Daguito, LLC](https://daguito.com)
