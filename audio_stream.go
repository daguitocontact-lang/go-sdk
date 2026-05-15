package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ErrAudioStream is returned for any failure on the audio upstream channel.
var ErrAudioStream = errors.New("audio stream")

// SupportedAudioCodecs mirrors the server's allowedCodecs set in
// apps/api/.../audio-ws.ts. Sending anything outside this list makes the
// server reject the socket on `open`.
var SupportedAudioCodecs = []string{"pcm16", "opus", "webm-opus", "mulaw", "flac"}

// AudioStreamOptions configures an upstream-only audio session that pushes
// raw PCM (or other supported codec) chunks to Daguito's /v1/audio/:session_id
// WebSocket. Transcript / flow events arrive over the companion
// WebhookStreamSession on /v1/webhooks/:id/stream.
type AudioStreamOptions struct {
	APIURL string
	Token  string

	// SessionID ties this audio stream to a conversation. Pass the same
	// value to the companion WebhookStreamSession so the flow sees them
	// as one session_key. Empty → SDK mints a random one.
	SessionID string

	// Codec must be one of SupportedAudioCodecs. Defaults to "pcm16".
	Codec string

	// SampleRate is required when Codec == "pcm16" so the STT provider
	// knows the rate of the frames. Leave 0 for container codecs.
	SampleRate int

	// ReadyTimeout caps how long Connect blocks waiting for the server's
	// `ready` JSON frame. Default 10s.
	ReadyTimeout time.Duration
}

// AudioStreamReady carries the server-confirmed session metadata returned
// in the first JSON frame after a successful handshake.
type AudioStreamReady struct {
	SessionKey string         `json:"session_key"`
	Codec      string         `json:"codec"`
	Guards     map[string]any `json:"guards,omitempty"`
}

// AudioStreamSession is a long-lived upstream audio channel.
//
// Lifecycle:
//
//	s := NewAudioStreamSession(opts)
//	if err := s.Connect(ctx); err != nil { ... }
//	defer s.Close()
//	for chunk := range mic.Chunks() {
//	    if err := s.SendAudio(ctx, chunk); err != nil { ... }
//	}
//
// SendAudio is safe to call from a single goroutine; concurrent senders
// must serialize themselves. Connect / Close are safe for concurrent use.
type AudioStreamSession struct {
	opts      AudioStreamOptions
	sessionID string

	mu     sync.Mutex
	conn   *websocket.Conn
	closed bool
	ready  *AudioStreamReady

	readyCh   chan struct{}
	errs      []string
	bg        sync.WaitGroup
	startedAt time.Time
}

// NewAudioStreamSession constructs a session. No I/O happens here — call
// Connect to open the socket.
func NewAudioStreamSession(opts AudioStreamOptions) *AudioStreamSession {
	if opts.Codec == "" {
		opts.Codec = "pcm16"
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = 10 * time.Second
	}
	sid := opts.SessionID
	if sid == "" {
		sid = randomSessionID("go")
	}
	return &AudioStreamSession{
		opts:      opts,
		sessionID: sid,
		readyCh:   make(chan struct{}),
	}
}

// SessionID returns the id used for this audio session. When opened with
// an empty SessionID, this is the random id the SDK minted — callers
// should read it back to use the same id on the companion event WS.
func (s *AudioStreamSession) SessionID() string { return s.sessionID }

// Ready returns the server-confirmed handshake metadata, or nil if the
// session is not yet open.
func (s *AudioStreamSession) Ready() *AudioStreamReady {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// Connect opens the WebSocket and blocks until the server's `ready` frame
// arrives (or ReadyTimeout fires).
func (s *AudioStreamSession) Connect(ctx context.Context) error {
	if err := s.validateOpts(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("%w: session is closed", ErrAudioStream)
	}
	if s.conn != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	wsURL, err := s.buildURL()
	if err != nil {
		return fmt.Errorf("%w: build url: %v", ErrAudioStream, err)
	}

	header := http.Header{}
	applyClientHeaders(header)

	dialCtx, cancel := context.WithTimeout(ctx, s.opts.ReadyTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		return fmt.Errorf("%w: dial: %v", ErrAudioStream, err)
	}
	conn.SetReadLimit(4 << 20)

	s.mu.Lock()
	s.conn = conn
	s.startedAt = time.Now()
	s.mu.Unlock()

	s.bg.Add(1)
	go s.controlLoop(conn)

	select {
	case <-s.readyCh:
	case <-time.After(s.opts.ReadyTimeout):
		_ = s.Close()
		return fmt.Errorf(
			"%w: timed out waiting for ready frame after %s",
			ErrAudioStream, s.opts.ReadyTimeout,
		)
	case <-ctx.Done():
		_ = s.Close()
		return fmt.Errorf("%w: %v", ErrAudioStream, ctx.Err())
	}

	s.mu.Lock()
	r := s.ready
	errs := append([]string(nil), s.errs...)
	s.mu.Unlock()
	if r == nil {
		_ = s.Close()
		msg := "server rejected audio session"
		if len(errs) > 0 {
			msg = msg + ": " + errs[len(errs)-1]
		}
		return fmt.Errorf("%w: %s", ErrAudioStream, msg)
	}
	return nil
}

// SendAudio pushes a single binary chunk. Caller decides chunk size — the
// server rejects frames over 256 KiB so 20–100 ms of audio per chunk is a
// reasonable target.
func (s *AudioStreamSession) SendAudio(ctx context.Context, chunk []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("%w: session is closed", ErrAudioStream)
	}
	conn := s.conn
	r := s.ready
	s.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("%w: not connected (call Connect first)", ErrAudioStream)
	}
	if r == nil {
		return fmt.Errorf("%w: handshake not complete", ErrAudioStream)
	}
	if len(chunk) == 0 {
		return nil
	}
	return conn.Write(ctx, websocket.MessageBinary, chunk)
}

// Close tears down the session. Server XADDs eof=1 to the audio Redis
// stream automatically on its end. Safe to call multiple times.
func (s *AudioStreamSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	s.conn = nil
	// Wake any waiter still blocked on Connect.
	select {
	case <-s.readyCh:
	default:
		close(s.readyCh)
	}
	s.mu.Unlock()

	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "client closing")
	}
	s.bg.Wait()
	return nil
}

// ─── internal ─────────────────────────────────────────────────────────

func (s *AudioStreamSession) validateOpts() error {
	if s.opts.APIURL == "" || s.opts.Token == "" {
		return fmt.Errorf("%w: APIURL and Token are required", ErrAudioStream)
	}
	if !codecSupported(s.opts.Codec) {
		return fmt.Errorf(
			"%w: unsupported codec %q (allowed: %v)",
			ErrAudioStream, s.opts.Codec, SupportedAudioCodecs,
		)
	}
	if s.opts.Codec == "pcm16" && s.opts.SampleRate <= 0 {
		return fmt.Errorf("%w: SampleRate is required when codec=pcm16", ErrAudioStream)
	}
	return nil
}

func (s *AudioStreamSession) buildURL() (string, error) {
	query := map[string]string{
		"token": s.opts.Token,
		"codec": s.opts.Codec,
	}
	if s.opts.SampleRate > 0 {
		query["sr"] = strconv.Itoa(s.opts.SampleRate)
	}
	wsURL, err := toWSURL(s.opts.APIURL, "/v1/audio/"+s.sessionID, query)
	if err != nil {
		return "", err
	}
	return appendClientQueryParams(wsURL)
}

func codecSupported(codec string) bool {
	for _, c := range SupportedAudioCodecs {
		if c == codec {
			return true
		}
	}
	return false
}

// controlLoop consumes the server → client JSON control channel. Surfaces
// `ready` to release Connect and logs subsequent `error` frames. Binary
// downstream is server-never; we ignore it defensively.
func (s *AudioStreamSession) controlLoop(conn *websocket.Conn) {
	defer s.bg.Done()
	defer func() {
		// If the loop exited before `ready` arrived (auth fail, server
		// closed early), wake the waiter so Connect doesn't hang.
		s.mu.Lock()
		select {
		case <-s.readyCh:
		default:
			close(s.readyCh)
		}
		s.mu.Unlock()
	}()
	for {
		ctx := context.Background()
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var payload map[string]any
		if jerr := json.Unmarshal(data, &payload); jerr != nil {
			continue
		}
		msgType, _ := payload["type"].(string)
		switch msgType {
		case "ready":
			r := &AudioStreamReady{
				Codec: s.opts.Codec,
			}
			if sk, ok := payload["session_key"].(string); ok {
				r.SessionKey = sk
			}
			if c, ok := payload["codec"].(string); ok {
				r.Codec = c
			}
			if g, ok := payload["guards"].(map[string]any); ok {
				r.Guards = g
			}
			s.mu.Lock()
			s.ready = r
			select {
			case <-s.readyCh:
			default:
				close(s.readyCh)
			}
			s.mu.Unlock()
		case "error":
			msg, _ := payload["message"].(string)
			s.mu.Lock()
			s.errs = append(s.errs, msg)
			// Handshake-time error: wake Connect with the actual reason
			// instead of timing out. Mid-session errors don't flip the
			// `ready` flag so the channel keeps running.
			if s.ready == nil {
				select {
				case <-s.readyCh:
				default:
					close(s.readyCh)
				}
			}
			s.mu.Unlock()
		}
	}
}
