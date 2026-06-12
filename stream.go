package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ToolSpec is the OpenAI-shaped declaration the LLM sees. Parameters is a
// JSON Schema fragment (typically `{"type":"object","properties":{...}}`).
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	// TimeoutMS caps how long the SDK waits for the local handler before
	// reporting a tool error back to the LLM. Defaults to 30s.
	TimeoutMS int `json:"client_timeout_ms,omitempty"`
}

// ToolHandler runs locally when the agent invokes a registered tool. Return
// any JSON-serialisable value as the tool result. Return an error to surface
// a failure to the LLM.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// WebhookStreamOptions configures a WebhookStreamSession.
type WebhookStreamOptions struct {
	APIURL    string
	WebhookID string
	Token     string

	// SessionKey is an optional caller-supplied id for resuming a session.
	// When empty, the SDK mints a random one.
	SessionKey string

	// BaseInput is applied to every send when the caller doesn't pass one
	// explicitly. Useful for static metadata (user_id, locale, …).
	BaseInput map[string]any

	// Scope is the server-enforced metadata filter for KB searches in this
	// session. Only primitive values (string, int, float, bool) are
	// forwarded — arrays/objects are silently dropped at the wire boundary.
	// The LLM never sees these values; Daguito injects them at the tool
	// boundary so a hallucinated id can't widen scope or leak across
	// conversations.
	Scope map[string]any

	// AutoReconnect re-opens the socket transparently on unexpected drops.
	// Reuses the same SessionKey so the daguito server resumes the running
	// flow execution — consumers don't see an EventClosed and Events() keeps
	// delivering. The new handshake re-sends session.start, then drains any
	// `pending` sends queued during the outage. Default false (opt-in) to
	// preserve the historical one-shot semantics for chatbot turns; long-
	// lived bridges (audio/video consultations, monitoring loops) should
	// flip this on.
	AutoReconnect bool
}

// WebhookStreamSession is a long-lived bidirectional session for streaming
// webhooks (WS /v1/webhooks/:id/stream).
//
// Lifecycle:
//
//	s := NewWebhookStreamSession(opts)
//	if err := s.Connect(ctx); err != nil { ... }
//	defer s.Close()
//	for evt := range s.Events() {
//	    switch evt.Type {
//	    case EventNodeToken: ...
//	    case EventFlowCompleted: return
//	    }
//	}
//
// All methods are safe for concurrent use. Events() is single-consumer — call
// it once per session.
type WebhookStreamSession struct {
	opts       WebhookStreamOptions
	sessionKey string

	mu        sync.Mutex
	conn      *websocket.Conn
	closed    bool
	opened    bool
	startedAt time.Time
	pending   []pendingSend

	events chan StreamEvent
	done   chan struct{}

	toolMu       sync.RWMutex
	toolHandlers map[string]ToolHandler
	toolSpecs    map[string]ToolSpec

	writeMu sync.Mutex // serialises Write/JSON encoding on the conn
	bg      sync.WaitGroup
}

type pendingSend struct {
	message   SendableMessage
	baseInput map[string]any
}

// NewWebhookStreamSession constructs a session. No I/O happens here — call
// Connect to open the socket.
func NewWebhookStreamSession(opts WebhookStreamOptions) *WebhookStreamSession {
	sk := opts.SessionKey
	if sk == "" {
		sk = randomSessionID("go")
	}
	return &WebhookStreamSession{
		opts:         opts,
		sessionKey:   sk,
		events:       make(chan StreamEvent, 64),
		done:         make(chan struct{}),
		toolHandlers: map[string]ToolHandler{},
		toolSpecs:    map[string]ToolSpec{},
	}
}

// Events returns the channel of typed StreamEvents. The channel is closed
// when the session ends (either Close was called or the server closed the
// socket). Range over it in a single goroutine.
func (s *WebhookStreamSession) Events() <-chan StreamEvent { return s.events }

// RegisterTool registers a client-side function tool the LLM may invoke. The
// spec is auto-injected into base_input.client_tools on every send; when the
// server emits agent.tool_call_started for this tool, handler runs locally
// and its return value is fed back via a tool_result frame.
//
// RegisterTool can be called before or after Connect. Registering twice with
// the same name replaces the previous handler.
func (s *WebhookStreamSession) RegisterTool(spec ToolSpec, handler ToolHandler) error {
	if spec.Name == "" {
		return fmt.Errorf("%w: tool spec missing Name", ErrStream)
	}
	if handler == nil {
		return fmt.Errorf("%w: tool handler is nil", ErrStream)
	}
	if spec.TimeoutMS <= 0 {
		spec.TimeoutMS = 30_000
	}
	s.toolMu.Lock()
	s.toolHandlers[spec.Name] = handler
	s.toolSpecs[spec.Name] = spec
	s.toolMu.Unlock()
	return nil
}

// Connect opens the WebSocket and runs the auth + session.start handshake.
// Subsequent calls are no-ops. The provided context governs only the dial;
// the long-lived recv loop runs until Close.
func (s *WebhookStreamSession) Connect(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("%w: session is closed", ErrStream)
	}
	if s.conn != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if s.opts.APIURL == "" || s.opts.WebhookID == "" || s.opts.Token == "" {
		return fmt.Errorf("%w: APIURL, WebhookID and Token are required", ErrStream)
	}

	wsURL, err := toWSURL(s.opts.APIURL, "/v1/webhooks/"+s.opts.WebhookID+"/stream", map[string]string{
		"token": s.opts.Token,
	})
	if err != nil {
		return fmt.Errorf("%w: build url: %v", ErrStream, err)
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return fmt.Errorf("%w: dial: %v", ErrStream, err)
	}
	// 4 MiB ceiling for a single frame is generous for token streams and
	// tool_progress payloads but still bounded.
	conn.SetReadLimit(4 << 20)

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	s.bg.Add(1)
	go s.recvLoop(conn)
	return nil
}

// Send pushes a message into the active session. If the session has not
// completed its handshake yet, the message is queued and dispatched once
// session.started arrives — callers do not need to wait themselves.
//
// baseInput overrides the session-level BaseInput for this single send.
// Pass nil to fall back to the session default.
func (s *WebhookStreamSession) Send(ctx context.Context, msg SendableMessage, baseInput map[string]any) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("%w: session is closed", ErrStream)
	}
	if s.conn == nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: not connected (call Connect first)", ErrStream)
	}
	if !s.opened {
		s.pending = append(s.pending, pendingSend{message: msg, baseInput: baseInput})
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.dispatch(ctx, msg, baseInput)
}

// SendRaw transmits an arbitrary control frame. Escape hatch for protocol
// extensions (session.open for stream-trigger flows, etc.). Most callers
// should use Send.
func (s *WebhookStreamSession) SendRaw(ctx context.Context, frame map[string]any) error {
	return s.writeJSON(ctx, frame)
}

// Close tears down the session. Safe to call multiple times.
func (s *WebhookStreamSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()

	if conn != nil {
		// Best-effort end frame so the server tears down its bridge promptly.
		endCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.writeJSONOn(endCtx, conn, map[string]any{"type": "session.end"})
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	close(s.done)
	s.bg.Wait()
	// recvLoop sends the final ClosedEvent before exiting; close events
	// channel last so consumers see it.
	close(s.events)
	return nil
}

func (s *WebhookStreamSession) dispatch(ctx context.Context, msg SendableMessage, baseInput map[string]any) error {
	inbound := toInbound(msg)
	envelope := map[string]any{
		"type":    "message",
		"message": inbound,
	}
	base := computeBaseInput(msg)
	override := baseInput
	if override == nil {
		override = s.opts.BaseInput
	}
	for k, v := range override {
		base[k] = v
	}

	// Inject client_tools when any are registered. The flow's ai_agent node
	// merges these with the tools declared statically in its config.
	s.toolMu.RLock()
	if len(s.toolSpecs) > 0 {
		specs := make([]ToolSpec, 0, len(s.toolSpecs))
		for _, spec := range s.toolSpecs {
			specs = append(specs, spec)
		}
		if existing, ok := base["client_tools"].([]any); ok {
			merged := append([]any{}, existing...)
			for _, sp := range specs {
				merged = append(merged, sp)
			}
			base["client_tools"] = merged
		} else {
			base["client_tools"] = specs
		}
	}
	s.toolMu.RUnlock()

	// Server-enforced scope filter, only set when not already in base_input.
	if len(s.opts.Scope) > 0 {
		if _, exists := base["scope"]; !exists {
			scope := map[string]any{}
			for k, v := range s.opts.Scope {
				switch v.(type) {
				case string, bool, int, int32, int64, float32, float64:
					scope[k] = v
				}
			}
			if len(scope) > 0 {
				base["scope"] = scope
			}
		}
	}

	if len(base) > 0 {
		envelope["base_input"] = base
	}
	return s.writeJSON(ctx, envelope)
}

// toInbound converts the user-facing SendableMessage into the server-side
// InboundMessage shape. The streaming WS surface uses kind=text on the
// inbound and moves modal data (image_url, image_urls, form_response) onto
// base_input — except pre-uploaded media, which travels on `media`.
func toInbound(msg SendableMessage) map[string]any {
	if msg.Kind != "form-response" && msg.MediaKey != "" {
		media := map[string]any{
			"key":        msg.MediaKey,
			"mime_type":  msg.MimeType,
			"size_bytes": msg.SizeBytes,
		}
		// Client-owned media: Daguito fetches the bytes from this presigned URL
		// instead of signing the key against its own storage. Mirrors the JS
		// SDK's mediaUrl path (MediaRefSchema.url).
		if msg.MediaURL != "" {
			media["url"] = msg.MediaURL
		}
		return map[string]any{
			"kind":  msg.Kind,
			"text":  msg.Text,
			"media": media,
		}
	}
	if msg.Kind == "form-response" {
		return map[string]any{"kind": "text", "text": "[form-response]"}
	}
	if msg.Kind == "text" {
		return map[string]any{"kind": "text", "text": msg.Text}
	}
	return map[string]any{"kind": "text", "text": msg.Text}
}

func computeBaseInput(msg SendableMessage) map[string]any {
	out := map[string]any{}
	if msg.Kind == "image" && msg.ImageURL != "" {
		out["image_url"] = msg.ImageURL
	}
	if msg.Kind == "image-multi" && len(msg.ImageURLs) > 0 {
		out["image_urls"] = msg.ImageURLs
	}
	if msg.Kind == "form-response" {
		out["form_response"] = msg.Payload
		out["form_response_id"] = msg.FormID
		out["is_form_response"] = true
	}
	return out
}

// recvLoop drains frames from the socket and emits typed events. Exits on
// context cancellation, socket close, or Close().
func (s *WebhookStreamSession) recvLoop(conn *websocket.Conn) {
	defer s.bg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Stop reading when Close fires.
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	dec := frameDecoder{}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			// Decide whether to reconnect silently or surface a closed event.
			// Don't reconnect when the caller asked us to close, or when
			// auto-reconnect is off.
			s.mu.Lock()
			callerClosed := s.closed
			s.mu.Unlock()
			if !callerClosed && s.opts.AutoReconnect {
				s.bg.Add(1)
				go s.reconnectLoop(err)
				return
			}
			closeErr := websocket.CloseError{}
			if errors.As(err, &closeErr) {
				s.emit(StreamEvent{
					Type:   EventClosed,
					Closed: &ClosedEvent{Code: int(closeErr.Code), Reason: closeErr.Reason},
				})
			} else {
				s.emit(StreamEvent{
					Type:   EventClosed,
					Closed: &ClosedEvent{Reason: err.Error()},
				})
			}
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		frame, err := dec.decode(data)
		if err != nil {
			continue
		}
		s.handleFrame(frame)
	}
}

// reconnectLoop re-opens the socket after an unexpected drop. Backoff 1s..30s.
// The new connection sends `session.start` with the same SessionKey, so the
// daguito server resumes the running execution's Redis channel and Events()
// keeps delivering without surfacing the seam.
func (s *WebhookStreamSession) reconnectLoop(cause error) {
	defer s.bg.Done()
	s.mu.Lock()
	s.opened = false
	if s.conn != nil {
		_ = s.conn.Close(websocket.StatusGoingAway, "reconnecting")
		s.conn = nil
	}
	s.mu.Unlock()

	delay := time.Second
	for attempt := 1; ; attempt++ {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}

		wsURL, err := toWSURL(s.opts.APIURL, "/v1/webhooks/"+s.opts.WebhookID+"/stream", map[string]string{
			"token": s.opts.Token,
		})
		if err == nil {
			dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			conn, _, dialErr := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{})
			cancel()
			if dialErr == nil {
				conn.SetReadLimit(4 << 20)
				s.mu.Lock()
				s.conn = conn
				s.mu.Unlock()
				s.bg.Add(1)
				go s.recvLoop(conn)
				return
			}
			err = dialErr
		}
		_ = err // best-effort; loop until close or success
		select {
		case <-s.done:
			return
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

// frameDecoder wraps a reusable json.Decoder-style scratch slot. json.Unmarshal
// into a fresh map keeps allocations bounded to one map per frame, which is
// the minimum we can do while still surfacing arbitrary `data` payloads.
type frameDecoder struct{}

func (frameDecoder) decode(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *WebhookStreamSession) handleFrame(frame map[string]any) {
	typ, _ := frame["type"].(string)
	switch typ {
	case "ready":
		webhookID, _ := frame["webhook_id"].(string)
		if webhookID == "" {
			webhookID = s.opts.WebhookID
		}
		s.emit(StreamEvent{Type: EventReady, Ready: &ReadyEvent{WebhookID: webhookID}})
		// Server is now expecting session.start.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.writeJSON(ctx, map[string]any{
			"type":        "session.start",
			"session_key": s.sessionKey,
		})
		return

	case "session.started":
		s.mu.Lock()
		s.opened = true
		s.startedAt = time.Now()
		queued := s.pending
		s.pending = nil
		s.mu.Unlock()
		for _, p := range queued {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.dispatch(ctx, p.message, p.baseInput)
			cancel()
		}
		return

	case "session.ended":
		s.mu.Lock()
		s.opened = false
		s.mu.Unlock()
		return

	case "pong", "ping":
		return

	case "error":
		msg, _ := frame["message"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		s.emit(StreamEvent{Type: EventError, Error: &ErrorEvent{Message: msg}})
		return
	}

	data, _ := frame["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	nodeID, _ := frame["node_id"].(string)

	switch typ {
	case "node.token":
		text := pickToken(data)
		if text == "" {
			return
		}
		s.emit(StreamEvent{Type: EventNodeToken, NodeToken: &NodeTokenEvent{NodeID: nodeID, Text: text}})

	case "node.started", "merge.progress":
		s.emit(StreamEvent{Type: EventNodeStarted, NodeStarted: &NodeStartedEvent{NodeID: nodeID}})

	case "node.completed":
		dur := 0
		if v, ok := intField(data, "duration_ms"); ok {
			dur = v
		} else if v, ok := intField(data, "elapsed_ms"); ok {
			dur = v
		}
		s.emit(StreamEvent{
			Type: EventNodeCompleted,
			NodeCompleted: &NodeCompletedEvent{
				NodeID:     nodeID,
				DurationMs: dur,
				Output:     data["output"],
			},
		})

	case "node.failed":
		errMsg, _ := data["error"].(string)
		s.emit(StreamEvent{Type: EventNodeFailed, NodeFailed: &NodeFailedEvent{NodeID: nodeID, Error: errMsg}})

	case "node.emit":
		emitKind, _ := data["kind"].(string)
		s.emit(StreamEvent{
			Type:     EventNodeEmit,
			NodeEmit: &NodeEmitEvent{NodeID: nodeID, Kind: emitKind, Data: data},
		})
		if emitKind == "agent.tool_call_started" {
			s.maybeRunTool(data)
		}

	case "flow.completed":
		elapsed := 0
		s.mu.Lock()
		if !s.startedAt.IsZero() {
			elapsed = int(time.Since(s.startedAt) / time.Millisecond)
		}
		s.mu.Unlock()
		s.emit(StreamEvent{
			Type:          EventFlowCompleted,
			FlowCompleted: &FlowCompletedEvent{ElapsedMs: elapsed, Output: data["output"]},
		})

	case "flow.failed":
		errMsg, _ := data["error"].(string)
		if errMsg == "" {
			errMsg = "flow failed"
		}
		s.emit(StreamEvent{Type: EventFlowFailed, FlowFailed: &FlowFailedEvent{Error: errMsg}})
	}
}

func pickToken(data map[string]any) string {
	for _, key := range []string{"token", "text", "delta", "content"} {
		if v, ok := data[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (s *WebhookStreamSession) maybeRunTool(data map[string]any) {
	name, _ := data["tool_name"].(string)
	callID, _ := data["call_id"].(string)
	if name == "" || callID == "" {
		return
	}

	s.toolMu.RLock()
	handler, ok := s.toolHandlers[name]
	spec := s.toolSpecs[name]
	s.toolMu.RUnlock()
	if !ok {
		return
	}

	var argsRaw json.RawMessage
	if rawArgs, ok := data["args"]; ok {
		if encoded, err := json.Marshal(rawArgs); err == nil {
			argsRaw = encoded
		}
	}
	if argsRaw == nil {
		argsRaw = json.RawMessage("{}")
	}

	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result, err := handler(ctx, argsRaw)

		frame := map[string]any{
			"type":    "tool_result",
			"call_id": callID,
			"ok":      err == nil,
		}
		if err != nil {
			frame["error"] = err.Error()
		} else {
			frame["result"] = result
		}
		sendCtx, sendCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sendCancel()
		_ = s.writeJSON(sendCtx, frame)
	}()
}

func (s *WebhookStreamSession) writeJSON(ctx context.Context, v any) error {
	s.mu.Lock()
	conn := s.conn
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("%w: session is closed", ErrStream)
	}
	if conn == nil {
		return fmt.Errorf("%w: not connected", ErrStream)
	}
	return s.writeJSONOn(ctx, conn, v)
}

func (s *WebhookStreamSession) writeJSONOn(ctx context.Context, conn *websocket.Conn, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: marshal frame: %v", ErrStream, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("%w: write: %v", ErrStream, err)
	}
	return nil
}

func (s *WebhookStreamSession) emit(evt StreamEvent) {
	select {
	case s.events <- evt:
	case <-s.done:
	}
}
