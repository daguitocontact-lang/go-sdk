package daguito

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// reconnectServer simulates a webhook streaming endpoint that drops the
// first connection mid-session, then accepts the reconnect. It records
// each new URL the client opens so the test can assert SessionKey reuse.
type reconnectServer struct {
	mu       sync.Mutex
	urls     []string
	starts   []string // session.start frames received per connection
	closeOne chan struct{}
	conns    int32
}

func newReconnectServer() *reconnectServer {
	return &reconnectServer{closeOne: make(chan struct{}, 1)}
}

func (s *reconnectServer) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.urls = append(s.urls, r.URL.String())
	s.mu.Unlock()
	connIdx := atomic.AddInt32(&s.conns, 1)

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")

	ctx := r.Context()
	// Send `ready` so the client emits session.start.
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ready","webhook_id":"wh_1"}`))

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		t, _ := frame["type"].(string)
		if t == "session.start" {
			s.mu.Lock()
			if sk, ok := frame["session_key"].(string); ok {
				s.starts = append(s.starts, sk)
			}
			s.mu.Unlock()
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"session.started"}`))
			// On the FIRST connection, close as soon as we're done with the
			// handshake — the client should reconnect transparently.
			if connIdx == 1 {
				// Give the handshake reply a moment to flush.
				time.Sleep(20 * time.Millisecond)
				_ = conn.Close(websocket.StatusGoingAway, "simulated drop")
				return
			}
		}
	}
}

func TestWebhookStreamSession_AutoReconnect_SameSessionKey(t *testing.T) {
	srv := newReconnectServer()
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()

	apiURL := "http://" + strings.TrimPrefix(httpSrv.URL, "http://")
	sess := NewWebhookStreamSession(WebhookStreamOptions{
		APIURL:        apiURL,
		WebhookID:     "wh_1",
		Token:         "sk_wh_TEST",
		SessionKey:    "resume-me",
		AutoReconnect: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sess.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	// Drain events; the test only cares that the session stays alive.
	// If the SDK had given up, the events channel would close.
	gotClosed := make(chan struct{}, 1)
	go func() {
		for evt := range sess.Events() {
			if evt.Type == EventClosed {
				gotClosed <- struct{}{}
				return
			}
		}
	}()

	// Wait for two distinct connections to land on the fake server.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		n := len(srv.urls)
		srv.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	srv.mu.Lock()
	urls := append([]string(nil), srv.urls...)
	starts := append([]string(nil), srv.starts...)
	srv.mu.Unlock()

	if len(urls) < 2 {
		t.Fatalf("expected ≥2 server connections (reconnect), got %d urls=%v", len(urls), urls)
	}
	// Both URLs target the same webhook path.
	for _, u := range urls {
		if !strings.Contains(u, "/v1/webhooks/wh_1/stream") {
			t.Errorf("unexpected url %q", u)
		}
	}
	// Both session.start frames carry the same SessionKey so the server
	// resumes the same execution.
	if len(starts) < 2 {
		t.Fatalf("expected ≥2 session.start frames, got %d: %v", len(starts), starts)
	}
	for _, sk := range starts {
		if sk != "resume-me" {
			t.Errorf("session.start key = %q, want resume-me", sk)
		}
	}

	select {
	case <-gotClosed:
		t.Fatal("EventClosed was dispatched during auto-reconnect")
	case <-time.After(100 * time.Millisecond):
		// expected — no closed event surfaced
	}
}

func TestWebhookStreamSession_NoReconnect_EmitsClosed(t *testing.T) {
	srv := newReconnectServer()
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()

	apiURL := "http://" + strings.TrimPrefix(httpSrv.URL, "http://")
	sess := NewWebhookStreamSession(WebhookStreamOptions{
		APIURL:     apiURL,
		WebhookID:  "wh_1",
		Token:      "sk_wh_TEST",
		SessionKey: "one-shot",
		// AutoReconnect default false.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sess.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	gotClosed := make(chan struct{}, 1)
	go func() {
		for evt := range sess.Events() {
			if evt.Type == EventClosed {
				gotClosed <- struct{}{}
				return
			}
		}
	}()

	select {
	case <-gotClosed:
		// expected
	case <-time.After(3 * time.Second):
		t.Fatal("expected EventClosed when AutoReconnect is off")
	}
}
