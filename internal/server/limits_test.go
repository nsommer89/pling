package server

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/protocol"
)

// idleSocket opens a WebSocket that never says hello, appearing to come
// from ip (the test server sees a loopback peer, so it trusts the header).
func idleSocket(ctx context.Context, url, ip string) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"X-Forwarded-For": {ip}}})
}

func TestIdleSocketsDoNotBlockOtherLogins(t *testing.T) {
	ts := startServer(t, Config{HandshakeTimeout: 3 * time.Second}, "anders") // defaults: 64 total, 2 per address
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	accepted, rejected := 0, 0
	for i := 0; i < 10; i++ {
		ws, resp, err := idleSocket(ctx, ts.url, "203.0.113.5")
		if err != nil {
			if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("idle socket %d: %v", i, err)
			}
			rejected++
			continue
		}
		accepted++
		defer ws.CloseNow()
	}
	if accepted != DefaultMaxPendingPerIP || rejected != 10-DefaultMaxPendingPerIP {
		t.Errorf("per-address cap: %d accepted, %d rejected; want %d and %d",
			accepted, rejected, DefaultMaxPendingPerIP, 10-DefaultMaxPendingPerIP)
	}

	// A real login from another address still gets in.
	s, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: "anders", Key: ts.keys["anders"],
		Header: http.Header{"X-Forwarded-For": {"198.51.100.7"}}})
	if err != nil {
		t.Fatalf("valid login blocked by idle sockets: %v", err)
	}
	_ = s.Close()
}

func TestPendingCapIsGlobalToo(t *testing.T) {
	ts := startServer(t, Config{MaxPending: 3, MaxPendingPerIP: 3, HandshakeTimeout: 3 * time.Second}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, ip := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
		ws, _, err := idleSocket(ctx, ts.url, ip)
		if err != nil {
			t.Fatalf("socket %d: %v", i, err)
		}
		defer ws.CloseNow()
	}
	_, resp, err := idleSocket(ctx, ts.url, "203.0.113.4")
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("4th pending socket: %v", err)
	}
}

func TestHandshakeTimeoutClosesIdleSockets(t *testing.T) {
	ts := startServer(t, Config{MaxPending: 2, MaxPendingPerIP: 2, HandshakeTimeout: 200 * time.Millisecond}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var socks []*websocket.Conn
	for i := 0; i < 2; i++ {
		ws, _, err := idleSocket(ctx, ts.url, "203.0.113.9")
		if err != nil {
			t.Fatal(err)
		}
		socks = append(socks, ws)
	}
	start := time.Now()
	for _, ws := range socks {
		if _, _, err := ws.Read(ctx); err == nil {
			t.Fatal("idle socket got a frame instead of being closed")
		}
		ws.CloseNow()
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("idle sockets closed after %v", d)
	}
	// The slots are free again.
	deadline := time.Now().Add(2 * time.Second)
	for ts.srv.pending.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d pending slots still taken", ts.srv.pending.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
	ws, _, err := idleSocket(ctx, ts.url, "203.0.113.9")
	if err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	ws.CloseNow()
}

func TestUpgradeDuringShutdownDoesNotPanic(t *testing.T) {
	for round := 0; round < 5; round++ {
		ts := startServer(t, Config{}, "anders")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := client.Dial(ctx, client.DialOptions{Server: ts.url, Name: "anders", Key: ts.keys["anders"]})
				if err == nil {
					_ = s.CloseNow()
				}
			}()
		}
		ts.stop()
		wg.Wait()
		cancel()
	}
}

func TestShutdownDuringLogin(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, ts.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	pub := ts.keys["anders"].Public().(ed25519.PublicKey)
	hello, _ := protocol.Encode(protocol.Hello{Name: "anders", PubKey: pub})
	if err := ws.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); err != nil { // the challenge
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { ts.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown hung on a half-finished login")
	}
	if _, _, err := ws.Read(ctx); err == nil {
		t.Error("connection still open after shutdown")
	}
}

func TestServeRequiresHost(t *testing.T) {
	err := New(Config{}).Serve(context.Background(), nil)
	if !errors.Is(err, ErrNoHost) {
		t.Errorf("got %v", err)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, xff, want string
	}{
		{"198.51.100.1:5000", "", "198.51.100.1"},
		{"198.51.100.1:5000", "10.0.0.1", "198.51.100.1"},          // public peer: header ignored
		{"127.0.0.1:5000", "203.0.113.5", "203.0.113.5"},           // local proxy
		{"172.18.0.4:5000", "6.6.6.6, 203.0.113.5", "203.0.113.5"}, // Docker proxy: last entry wins
		{"172.18.0.4:5000", "garbage", "172.18.0.4"},               // unparsable header
		{"[::1]:5000", "2001:db8::1", "2001:db8::1"},               // IPv6
		{"192.168.1.10:5000", "::ffff:203.0.113.7", "203.0.113.7"}, // mapped address
	}
	for _, c := range cases {
		r, _ := http.NewRequest("GET", "/ws", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestLogLimiter(t *testing.T) {
	now := time.Unix(100, 0)
	l := newLogLimiter(time.Second)
	l.now = func() time.Time { return now }
	if ok, n := l.allow("auth"); !ok || n != 0 {
		t.Fatal("first line held back")
	}
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow("auth"); ok {
			t.Fatal("flood not limited")
		}
	}
	if ok, _ := l.allow("full"); !ok {
		t.Error("keys are not independent")
	}
	now = now.Add(time.Second)
	if ok, n := l.allow("auth"); !ok || n != 5 {
		t.Errorf("after a second: ok=%v suppressed=%d, want true 5", ok, n)
	}
}

func TestInvalidUTF8IsBadFrame(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := ts.dial(t, "anders")
	if err := s.SendRaw(ctx, []byte("{\"v\":1,\"kind\":\"msg\",\"text\":\"bad \xff\xfe\"}")); err != nil {
		t.Fatal(err)
	}
	if e := next[protocol.Error](t, s); e.Code != protocol.ErrCodeBadFrame {
		t.Errorf("got %#v", e)
	}

	// During login it is bad_frame too, and the connection closes.
	ws, _, err := websocket.Dial(ctx, ts.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_ = ws.Write(ctx, websocket.MessageText, []byte("{\"v\":1,\"kind\":\"hello\",\"name\":\"\xff\"}"))
	_, b, err := ws.Read(ctx)
	if err != nil || !strings.Contains(string(b), protocol.ErrCodeBadFrame) {
		t.Errorf("login: %s %v", b, err)
	}
}

func TestOversizeFrames(t *testing.T) {
	ts := startServer(t, Config{}, "anders")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := ts.dial(t, "anders")

	// Text over 4 KB inside a normal frame: too_large, connection stays.
	send(t, a, protocol.Msg{Text: strings.Repeat("x", protocol.MaxTextBytes+1)})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeTooLarge {
		t.Errorf("got %#v", e)
	}
	// A frame over the read limit: closed with 1009 (message too big).
	big := `{"v":1,"kind":"msg","text":"` + strings.Repeat("y", protocol.MaxFrameBytes) + `"}`
	if err := a.SendRaw(ctx, []byte(big)); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := a.Read(ctx)
		if err == nil {
			continue
		}
		if s := websocket.CloseStatus(err); s != websocket.StatusMessageTooBig {
			t.Errorf("close status %v (%v), want 1009", s, err)
		}
		break
	}
}

func TestNotOnline(t *testing.T) {
	ts := startServer(t, Config{}, "anders", "bob")
	a := ts.dial(t, "anders")
	b := ts.dial(t, "bob")
	next[protocol.Presence](t, a) // bob joined
	_ = b.Close()
	if p := next[protocol.Presence](t, a); p.Event != protocol.PresenceLeave {
		t.Fatalf("got %#v", p)
	}
	send(t, a, protocol.Msg{To: "bob", Text: "are you there?"})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeNotOnline || e.Detail != "bob" {
		t.Errorf("got %#v", e)
	}
	send(t, a, protocol.Msg{To: "Not A Name", Text: "x"})
	if e := next[protocol.Error](t, a); e.Code != protocol.ErrCodeNotOnline {
		t.Errorf("invalid name: %#v", e)
	}
}

func TestAddressKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5":          "203.0.113.5",
		"::ffff:203.0.113.5":   "203.0.113.5",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2::ffff":   "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
		"fe80::1%eth0":         "fe80::/64",
		"not-an-ip":            "not-an-ip",
	}
	for in, want := range cases {
		if got := addressKey(in); got != want {
			t.Errorf("addressKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPendingLimiterGroupsIPv6By64(t *testing.T) {
	p := newPendingLimiter(64, 2)
	r1, ok1 := p.acquire("2001:db8:1:2::1")
	_, ok2 := p.acquire("2001:db8:1:2::2")
	_, ok3 := p.acquire("2001:db8:1:2:ffff:ffff:ffff:ffff") // same /64: third one
	_, ok4 := p.acquire("2001:db8:1:3::1")                  // another /64
	if !ok1 || !ok2 || ok3 || !ok4 {
		t.Fatalf("got %v %v %v %v, want true true false true", ok1, ok2, ok3, ok4)
	}
	r1()
	r1() // releasing twice frees one slot only
	if _, ok := p.acquire("2001:db8:1:2::3"); !ok {
		t.Error("slot in the /64 not freed")
	}
	if _, ok := p.acquire("2001:db8:1:2::4"); ok {
		t.Error("double release freed two slots")
	}
	if p.count() != 3 { // ::2, ::3 and the other /64
		t.Errorf("count %d, want 3", p.count())
	}

	// The total cap still applies across addresses.
	q := newPendingLimiter(3, 2)
	for i, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		if _, ok := q.acquire(ip); !ok {
			t.Fatalf("acquire %d refused", i)
		}
	}
	if _, ok := q.acquire("198.51.100.4"); ok {
		t.Error("total cap ignored")
	}
}
