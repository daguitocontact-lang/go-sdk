package daguito

// EventType is the wire-level event name for a StreamEvent. Mirrors the
// Python `StreamEvent` literal union.
type EventType string

const (
	EventReady          EventType = "ready"
	EventClosed         EventType = "closed"
	EventNodeStarted    EventType = "node.started"
	EventNodeToken      EventType = "node.token"
	EventNodeCompleted  EventType = "node.completed"
	EventNodeFailed     EventType = "node.failed"
	EventNodeEmit       EventType = "node.emit"
	EventFlowCompleted  EventType = "flow.completed"
	EventFlowFailed     EventType = "flow.failed"
	EventError          EventType = "error"
)

// StreamEvent is the sum type delivered on WebhookStreamSession.Events().
// The Type field selects which of the typed payload pointers is populated;
// at most one is non-nil. Switch on Type when handling, or use the helper
// accessor methods.
type StreamEvent struct {
	Type EventType

	Ready         *ReadyEvent
	Closed        *ClosedEvent
	NodeStarted   *NodeStartedEvent
	NodeToken     *NodeTokenEvent
	NodeCompleted *NodeCompletedEvent
	NodeFailed    *NodeFailedEvent
	NodeEmit      *NodeEmitEvent
	FlowCompleted *FlowCompletedEvent
	FlowFailed    *FlowFailedEvent
	Error         *ErrorEvent
}

// ReadyEvent fires after the WS upgrade and bearer-token auth succeed.
type ReadyEvent struct {
	WebhookID string
}

// ClosedEvent fires when the transport closes. Code/Reason are best-effort —
// some closures (peer drop, network) leave them empty.
type ClosedEvent struct {
	Code   int
	Reason string
}

// NodeStartedEvent fires when the engine enters a flow node.
type NodeStartedEvent struct {
	NodeID string
}

// NodeTokenEvent carries a single LLM token (or the smallest unit the model
// streamed). Concatenate Text across consecutive events to reconstruct the
// full response.
type NodeTokenEvent struct {
	NodeID string
	Text   string
}

// NodeCompletedEvent fires when a node finishes successfully. DurationMs is
// populated when the server reports it; Output may be any JSON-shaped value.
type NodeCompletedEvent struct {
	NodeID     string
	DurationMs int
	Output     any
}

// NodeFailedEvent fires when a node errors out.
type NodeFailedEvent struct {
	NodeID string
	Error  string
}

// NodeEmitEvent carries custom telemetry. Kind discriminates the payload;
// common values: "tool_progress" (use ParseToolProgress), "intent",
// "agent.tool_call_started".
type NodeEmitEvent struct {
	NodeID string
	Kind   string
	Data   map[string]any
}

// FlowCompletedEvent fires when the engine finishes the run. ElapsedMs is
// measured client-side from session.started.
type FlowCompletedEvent struct {
	ElapsedMs int
	Output    any
}

// FlowFailedEvent fires when the engine errors out.
type FlowFailedEvent struct {
	Error string
}

// ErrorEvent carries a protocol-level error (auth failure, malformed frame,
// etc.). It is not the same as NodeFailedEvent / FlowFailedEvent — those are
// flow-execution failures.
type ErrorEvent struct {
	Message string
}

// ToolProgressResource describes what a tool is operating on. Every field is
// optional — the server only fills what's relevant for the stage.
type ToolProgressResource struct {
	Kind     string
	Name     string
	MediaKey string
	URL      string
}

// ToolProgressResult carries the data the tool produced. Summary is the raw
// caption/transcript text for a single-resource tool; Items lists multiple
// hits for search-like tools.
type ToolProgressResult struct {
	Summary string
	Items   []ToolProgressItem
}

// ToolProgressItem is one hit in a search-like tool result.
type ToolProgressItem struct {
	Title   string
	Snippet string
	URL     string
}

// ToolProgressEvent is the typed view over a NodeEmitEvent whose Kind is
// "tool_progress". Returned by ParseToolProgress. Consumers compose the
// user-facing copy from (Tool, Stage, Resource, Result) via their own i18n
// layer — the SDK never localises strings.
type ToolProgressEvent struct {
	Tool     string
	Stage    string
	Progress float64 // 0..1; -1 when not reported
	Resource *ToolProgressResource
	Result   *ToolProgressResult
	TraceID  string
	Attempt  int
}

// ParseToolProgress narrows a NodeEmitEvent into a ToolProgressEvent when
// Kind is "tool_progress". Returns nil otherwise so callers can use it as a
// discriminator inside their node.emit handler.
func ParseToolProgress(evt *NodeEmitEvent) *ToolProgressEvent {
	if evt == nil || evt.Kind != "tool_progress" {
		return nil
	}
	data := evt.Data
	out := &ToolProgressEvent{
		Tool:     stringField(data, "tool"),
		Stage:    stringField(data, "stage"),
		Progress: -1,
		TraceID:  stringField(data, "trace_id"),
	}
	if p, ok := numberField(data, "progress"); ok {
		out.Progress = p
	}
	if a, ok := intField(data, "attempt"); ok {
		out.Attempt = a
	}
	if raw, ok := data["resource"].(map[string]any); ok {
		out.Resource = &ToolProgressResource{
			Kind:     stringField(raw, "kind"),
			Name:     stringField(raw, "name"),
			MediaKey: stringField(raw, "media_key"),
			URL:      stringField(raw, "url"),
		}
	}
	if raw, ok := data["result"].(map[string]any); ok {
		res := &ToolProgressResult{Summary: stringField(raw, "summary")}
		if items, ok := raw["items"].([]any); ok {
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				res.Items = append(res.Items, ToolProgressItem{
					Title:   stringField(m, "title"),
					Snippet: stringField(m, "snippet"),
					URL:     stringField(m, "url"),
				})
			}
		}
		out.Result = res
	}
	return out
}

func stringField(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func numberField(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func intField(m map[string]any, k string) (int, bool) {
	switch v := m[k].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}
