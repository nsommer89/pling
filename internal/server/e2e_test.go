package server

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/goleak"

	"github.com/nsommer89/pling/internal/auth"
	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/hub"
	"github.com/nsommer89/pling/internal/protocol"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type testServer struct {
	url    string
	addr   string
	keys   map[string]ed25519.PrivateKey
	users  string
	cancel context.CancelFunc
	done   chan error
}

// startServer runs a server on a random port with the given users.
func startServer(t *testing.T, cfg Config, names ...string) *testServer {
	t.Helper()
	ts := &testServer{keys: map[string]ed25519.PrivateKey{}, done: make(chan error, 1)}
	var lines []string
	for _, n := range names {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		ts.keys[n] = priv
		lines = append(lines, n+": "+auth.EncodePublicKey(pub))
	}
	ts.users = filepath.Join(t.TempDir(), "users.yaml")
	if err := os.WriteFile(ts.users, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ts.addr = ln.Addr().String()
	ts.url = "ws://" + ts.addr + "/ws"
	cfg.UsersPath = ts.users
	if cfg.Host == "" {
		cfg.Host = ts.addr
	}
	srv := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	ts.cancel = cancel
	go func() { ts.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(ts.stop)
	return ts
}

func (ts *testServer) stop() {
	ts.cancel()
	<-ts.done
	ts.done <- nil // allow a second stop
}

func (ts *testServer) dial(t *testing.T, name string) *client.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: name, Key: ts.keys[name]})
	if err != nil {
		t.Fatalf("dial %s: %v", name, err)
	}
	t.Cleanup(func() { _ = s.CloseNow() })
	return s
}

// next reads frames until one matches the wanted type, skipping others.
func next[T protocol.Message](t *testing.T, s *client.Session) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		m, err := s.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if v, ok := m.(T); ok {
			return v
		}
	}
}

func send(t *testing.T, s *client.Session, m protocol.Message) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Send(ctx, m); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestEndToEndRoomAndDM(t *testing.T) {
	ts := startServer(t, Config{MOTD: "velkommen"}, "anders", "nikolaj", "carl")
	a := ts.dial(t, "anders")
	if a.Welcome.MOTD != "velkommen" || !slices.Equal(a.Welcome.Users, []string{"anders"}) {
		t.Fatalf("welcome %#v", a.Welcome)
	}
	n := ts.dial(t, "nikolaj")
	if !slices.Equal(n.Welcome.Users, []string{"anders", "nikolaj"}) {
		t.Fatalf("welcome users %v", n.Welcome.Users)
	}
	if p := next[protocol.Presence](t, a); p.Name != "nikolaj" || p.Event != protocol.PresenceJoin {
		t.Fatalf("presence %#v", p)
	}

	send(t, a, protocol.Msg{Text: "hej alle"})
	for _, s := range []*client.Session{a, n} {
		m := next[protocol.Msg](t, s)
		if m.From != "anders" || m.Text != "hej alle" || m.To != "" || m.ID == "" || m.TS == 0 {
			t.Errorf("room message %#v", m)
		}
	}

	c := ts.dial(t, "carl")
	next[protocol.Presence](t, a)
	next[protocol.Presence](t, n)

	send(t, n, protocol.Msg{To: "anders", Text: "kun til dig", Me: true})
	if m := next[protocol.Msg](t, a); m.From != "nikolaj" || m.To != "anders" || !m.Me {
		t.Errorf("dm %#v", m)
	}
	if m := next[protocol.Msg](t, n); m.To != "anders" {
		t.Errorf("dm echo %#v", m)
	}
	// carl must not see the DM; the next thing he gets is the room message.
	send(t, a, protocol.Msg{Text: "carl?"})
	if m := next[protocol.Msg](t, c); m.Text != "carl?" {
		t.Errorf("carl got %#v", m)
	}

	send(t, a, protocol.Msg{To: "nobody", Text: "hallo"})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeNotOnline || e.Detail != "nobody" {
		t.Errorf("error %#v", e)
	}
	send(t, a, protocol.Msg{Text: strings.Repeat("x", protocol.MaxTextBytes+1)})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeTooLarge {
		t.Errorf("error %#v", e)
	}

	send(t, a, protocol.Typing{})
	if ty := next[protocol.Typing](t, n); ty.From != "anders" {
		t.Errorf("typing %#v", ty)
	}

	_ = c.Close()
	if p := next[protocol.Presence](t, a); p.Name != "carl" || p.Event != protocol.PresenceLeave {
		t.Errorf("leave %#v", p)
	}
}

func TestEndToEndAuthFailures(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	_, wrongKey, _ := ed25519.GenerateKey(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, tc := range []struct {
		name string
		key  ed25519.PrivateKey
	}{
		{"anders", wrongKey},  // wrong key
		{"mallory", wrongKey}, // unknown user
	} {
		_, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: tc.name, Key: tc.key})
		if !client.IsServerCode(err, protocol.ErrCodeAuthFailed) {
			t.Errorf("%s: got %v, want auth_failed", tc.name, err)
		}
	}

	// Wrong host: the client signs for a host the server does not use.
	alias := strings.Replace(ts.url, "127.0.0.1", "localhost", 1)
	_, err := client.Dial(ctx, client.DialOptions{Server: alias, Name: "anders", Key: ts.keys["anders"]})
	if !client.IsServerCode(err, protocol.ErrCodeAuthFailed) {
		t.Errorf("wrong host: got %v, want auth_failed", err)
	}

	// Users are re-read on each login: add bob without a restart.
	pub, priv, _ := ed25519.GenerateKey(nil)
	f, _ := os.OpenFile(ts.users, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("bob: " + auth.EncodePublicKey(pub) + "\n")
	f.Close()
	s, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: "bob", Key: priv})
	if err != nil {
		t.Fatalf("bob after users edit: %v", err)
	}
	_ = s.Close()
}

func TestEndToEndDuplicateLoginKicksOld(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	old := ts.dial(t, "anders")
	_ = ts.dial(t, "anders")
	if e := next[protocol.Error](t, old); e.Code != protocol.ErrCodeReplaced {
		t.Fatalf("old got %#v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := old.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Errorf("old connection: %v", err)
	}
}

func TestEndToEndRateLimit(t *testing.T) {
	ts := startServer(t, Config{Hub: hub.NewWithRateLimit(3, time.Minute)}, "anders")
	a := ts.dial(t, "anders")
	for i := 0; i < 3; i++ {
		send(t, a, protocol.Msg{Text: "spam"})
		next[protocol.Msg](t, a)
	}
	send(t, a, protocol.Msg{Text: "spam"})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeRateLimited {
		t.Errorf("got %#v", e)
	}
}

func TestEndToEndShutdownClosesWithGoingAway(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	a := ts.dial(t, "anders")
	go ts.stop()
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeShutdown {
		t.Fatalf("got %#v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("close status: %v", err)
	}
}

func TestHTTPEndpoints(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	resp, err := http.Get("http://" + ts.addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz %d", resp.StatusCode)
	}
	resp, err = http.Get("http://" + ts.addr + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("plain GET /ws: %d", resp.StatusCode)
	}
}

func TestMaxConnections(t *testing.T) {
	ts := startServer(t, Config{MaxConns: 1}, "anders", "bob")
	_ = ts.dial(t, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: "bob", Key: ts.keys["bob"]})
	var he *client.HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusServiceUnavailable {
		t.Errorf("got %v, want HTTP 503", err)
	}
}

func TestClientReconnectsAndEmitsEvents(t *testing.T) {
	ts := startServer(t, Config{}, "anders", "bob")
	c := client.NewClient(client.DialOptions{Server: ts.url, Name: "anders", Key: ts.keys["anders"]})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor := func(match func(any) bool) {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case ev, ok := <-c.Events():
				if !ok {
					t.Fatal("events closed")
				}
				if match(ev) {
					return
				}
			case <-timeout:
				t.Fatal("timed out waiting for event")
			}
		}
	}
	waitFor(func(ev any) bool { _, ok := ev.(client.Connected); return ok })
	if err := c.Send(ctx, protocol.Msg{Text: "via client"}); err != nil {
		t.Fatal(err)
	}
	waitFor(func(ev any) bool {
		r, ok := ev.(client.Received)
		return ok && r.Message.(protocol.Msg).Text == "via client"
	})

	// Another login as anders replaces this one: fatal, no reconnect.
	_ = ts.dial(t, "anders")
	waitFor(func(ev any) bool {
		d, ok := ev.(client.Disconnected)
		return ok && d.Fatal && client.IsServerCode(d.Err, protocol.ErrCodeReplaced)
	})
}

func TestEndToEndBadFrames(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	readErr := func(ws *websocket.Conn) protocol.Error {
		t.Helper()
		for {
			_, b, err := ws.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			m, err := protocol.Decode(b)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if e, ok := m.(protocol.Error); ok {
				return e
			}
		}
	}

	// Garbage instead of hello: bad_frame, then the connection closes.
	ws, _, err := websocket.Dial(ctx, ts.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.Write(ctx, websocket.MessageText, []byte("not json"))
	if e := readErr(ws); e.Code != protocol.ErrCodeBadFrame {
		t.Errorf("login garbage: %#v", e)
	}
	if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("login garbage close: %v", err)
	}
	ws.CloseNow()

	// A future protocol version during login.
	ws, _, err = websocket.Dial(ctx, ts.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"v":2,"kind":"hello"}`))
	if e := readErr(ws); e.Code != protocol.ErrCodeUnsupportedVersion {
		t.Errorf("login v2: %#v", e)
	}
	ws.CloseNow()

	// After login, bad frames are reported but the connection stays up.
	s := ts.dial(t, "anders")
	for _, raw := range []string{"not json", `{"v":2,"kind":"msg","text":"x"}`, `{"v":1,"kind":"msg","text":""}`} {
		if err := s.SendRaw(ctx, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{protocol.ErrCodeBadFrame, protocol.ErrCodeUnsupportedVersion, protocol.ErrCodeBadFrame}
	for _, code := range want {
		if e := next[protocol.Error](t, s); e.Code != code {
			t.Errorf("got %q, want %q", e.Code, code)
		}
	}
	send(t, s, protocol.Msg{Text: "still here"})
	if m := next[protocol.Msg](t, s); m.Text != "still here" {
		t.Errorf("got %#v", m)
	}
}
