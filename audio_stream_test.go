package daguito

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeAudioServer captures URL/headers, sends a configurable JSON frame
// after upgrade, and records all binary frames the client sends.
type fakeAudioServer struct {
	mu        sync.Mutex
	openedURL string
	headers   http.Header
	received  [][]byte
	controls  []string

	// initialFrame is the JSON frame the server sends right after upgrade.
	initialFrame []byte
	// skipInitialFrame: if true the server holds the socket open without
	// sending anything. Used to exercise the ready-timeout path.
	skipInitialFrame bool
}

func (f *fakeAudioServer) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.openedURL = r.URL.String()
	f.headers = r.Header.Clone()
	f.mu.Unlock()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if !f.skipInitialFrame && len(f.initialFrame) > 0 {
		_ = conn.Write(r.Context(), websocket.MessageText, f.initialFrame)
	}

	for {
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			cp := make([]byte, len(data))
			copy(cp, data)
			f.mu.Lock()
			f.received = append(f.received, cp)
			f.mu.Unlock()
		} else if typ == websocket.MessageText {
			f.mu.Lock()
			f.controls = append(f.controls, string(data))
			f.mu.Unlock()
		}
	}
}

func newFakeAudioServer(t *testing.T, initial map[string]any, skipInitial bool) (*fakeAudioServer, *httptest.Server) {
	t.Helper()
	body, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("marshal initial: %v", err)
	}
	f := &fakeAudioServer{initialFrame: body, skipInitialFrame: skipInitial}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	return f, srv
}

func TestAudioStream_ValidateOpts(t *testing.T) {
	cases := []struct {
		name    string
		opts    AudioStreamOptions
		wantErr string
	}{
		{
			"missing api url",
			AudioStreamOptions{Token: "sk_wh_x"},
			"APIURL",
		},
		{
			"missing token",
			AudioStreamOptions{APIURL: "http://x"},
			"Token",
		},
		{
			"unsupported codec",
			AudioStreamOptions{APIURL: "http://x", Token: "t", Codec: "mp3", SampleRate: 16000},
			"unsupported codec",
		},
		{
			"pcm16 without sample rate",
			AudioStreamOptions{APIURL: "http://x", Token: "t", Codec: "pcm16"},
			"SampleRate is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewAudioStreamSession(tc.opts)
			err := s.Connect(context.Background())
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !errors.Is(err, ErrAudioStream) {
				t.Fatalf("want ErrAudioStream, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestAudioStream_ConnectHandshake_SendsExpectedURLAndHeaders(t *testing.T) {
	f, srv := newFakeAudioServer(t, map[string]any{
		"type":        "ready",
		"session_key": "k",
		"codec":       "pcm16",
	}, false)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_TEST",
		SessionID:    "sess123",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 2 * time.Second,
	})
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer s.Close()

	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.openedURL, "/v1/audio/sess123") {
		t.Fatalf("opened URL %q missing /v1/audio/sess123", f.openedURL)
	}
	if !strings.Contains(f.openedURL, "token=sk_wh_TEST") {
		t.Fatalf("opened URL missing token: %q", f.openedURL)
	}
	if !strings.Contains(f.openedURL, "codec=pcm16") {
		t.Fatalf("opened URL missing codec: %q", f.openedURL)
	}
	if !strings.Contains(f.openedURL, "sr=16000") {
		t.Fatalf("opened URL missing sr: %q", f.openedURL)
	}
	if !strings.Contains(f.openedURL, "x_daguito_client_lang=go") {
		t.Fatalf("opened URL missing sdk client lang: %q", f.openedURL)
	}
	if got := f.headers.Get("X-Daguito-Client-Lang"); got != "go" {
		t.Fatalf("X-Daguito-Client-Lang header: want go, got %q", got)
	}
	if r := s.Ready(); r == nil || r.SessionKey != "k" {
		t.Fatalf("ready snapshot mismatch: %+v", r)
	}
}

func TestAudioStream_ConnectTimesOutWithoutReady(t *testing.T) {
	_, srv := newFakeAudioServer(t, nil, true)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_x",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 250 * time.Millisecond,
	})
	err := s.Connect(context.Background())
	if err == nil {
		t.Fatalf("want timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timed out, got %q", err.Error())
	}
}

func TestAudioStream_ConnectSurfacesServerErrorFrame(t *testing.T) {
	_, srv := newFakeAudioServer(t, map[string]any{
		"type":    "error",
		"message": "unsupported codec: mp3",
	}, false)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_x",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 1 * time.Second,
	})
	err := s.Connect(context.Background())
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported codec") {
		t.Fatalf("want unsupported codec, got %q", err.Error())
	}
}

func TestAudioStream_SendAudioPushesBinaryChunk(t *testing.T) {
	f, srv := newFakeAudioServer(t, map[string]any{
		"type":        "ready",
		"session_key": "k",
		"codec":       "pcm16",
	}, false)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_x",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 2 * time.Second,
	})
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := s.SendAudio(context.Background(), []byte{0, 1, 2, 3}); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if err := s.SendAudio(context.Background(), []byte{4, 5, 6, 7}); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	_ = s.Close()

	// Allow the server time to drain the read loop.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.received)
		f.mu.Unlock()
		if got >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) < 2 {
		t.Fatalf("server received %d binary frames, want 2", len(f.received))
	}
	if string(f.received[0]) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("frame 0: %v", f.received[0])
	}
	if string(f.received[1]) != string([]byte{4, 5, 6, 7}) {
		t.Fatalf("frame 1: %v", f.received[1])
	}
}

func TestAudioStream_PauseResumeSendsTextControlFrames(t *testing.T) {
	f, srv := newFakeAudioServer(t, map[string]any{
		"type":        "ready",
		"session_key": "k",
		"codec":       "pcm16",
	}, false)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_x",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 2 * time.Second,
	})
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := s.Pause(context.Background()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := s.Resume(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	_ = s.Close()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.controls)
		f.mu.Unlock()
		if got >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.controls) < 2 {
		t.Fatalf("server received %d control frames, want 2", len(f.controls))
	}
	if f.controls[0] != "pause" || f.controls[1] != "resume" {
		t.Fatalf("controls = %v, want [pause resume]", f.controls)
	}
}

func TestAudioStream_ConcurrentAudioAndControlDoesNotDropFrames(t *testing.T) {
	// Regression: SendAudio (pump goroutine) and SendControl (mute goroutine)
	// write to the same socket. coder/websocket forbids concurrent writers, so
	// without the session's write mutex a control frame sent mid-audio is lost
	// — which is exactly how a real `pause` went missing. Hammer both paths
	// concurrently and assert NO errors and EVERY control frame arrives.
	f, srv := newFakeAudioServer(t, map[string]any{
		"type":        "ready",
		"session_key": "k",
		"codec":       "pcm16",
	}, false)
	defer srv.Close()

	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:       srv.URL,
		Token:        "sk_wh_x",
		Codec:        "pcm16",
		SampleRate:   16000,
		ReadyTimeout: 2 * time.Second,
	})
	if err := s.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	const audioWriters = 100
	const controlWriters = 20
	var wg sync.WaitGroup
	errs := make(chan error, audioWriters+controlWriters)
	for i := 0; i < audioWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SendAudio(context.Background(), []byte{1, 2, 3, 4}); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < controlWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Pause(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write error (race not serialized): %v", err)
	}

	// Wait for the server to drain, then assert every control frame landed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.controls)
		f.mu.Unlock()
		if got >= controlWriters {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = s.Close()

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.controls) != controlWriters {
		t.Fatalf("server got %d control frames, want %d (frames dropped by the race)", len(f.controls), controlWriters)
	}
	for _, c := range f.controls {
		if c != "pause" {
			t.Fatalf("unexpected control frame %q", c)
		}
	}
}

func TestAudioStream_ControlBeforeConnect(t *testing.T) {
	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:     "http://x",
		Token:      "t",
		Codec:      "pcm16",
		SampleRate: 16000,
	})
	err := s.Pause(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("want not-connected error, got %v", err)
	}
}

func TestAudioStream_SendBeforeConnect(t *testing.T) {
	s := NewAudioStreamSession(AudioStreamOptions{
		APIURL:     "http://x",
		Token:      "t",
		Codec:      "pcm16",
		SampleRate: 16000,
	})
	err := s.SendAudio(context.Background(), []byte{1, 2, 3})
	if err == nil {
		t.Fatalf("want not-connected error, got nil")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("want not connected, got %q", err.Error())
	}
}
