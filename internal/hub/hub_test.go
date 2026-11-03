package hub

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nsommer89/pling/internal/protocol"
)

type fakePeer struct {
	name string
	cap  int

	mu     sync.Mutex
	frames []protocol.Message
	kicked string
}

func newPeer(name string) *fakePeer { return &fakePeer{name: name, cap: 100} }

func (p *fakePeer) Name() string { return p.name }

func (p *fakePeer) Deliver(b []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.frames) >= p.cap {
		return false
	}
	m, err := protocol.Decode(b)
	if err != nil {
		panic(err)
	}
	p.frames = append(p.frames, m)
	return true
}

func (p *fakePeer) Kick(code string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kicked = code
}

func (p *fakePeer) take() []protocol.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.frames
	p.frames = nil
	return f
}

func TestJoinAnnouncesAndListsUsers(t *testing.T) {
	h := New()
	a, b := newPeer("anders"), newPeer("bob")
	if got := h.Join(a); !slices.Equal(got, []string{"anders"}) {
		t.Fatalf("users %v", got)
	}
	if got := h.Join(b); !slices.Equal(got, []string{"anders", "bob"}) {
		t.Fatalf("users %v", got)
	}
	frames := a.take()
	if len(frames) != 1 || frames[0] != (protocol.Presence{Event: protocol.PresenceJoin, Name: "bob"}) {
		t.Fatalf("anders got %#v", frames)
	}
	if len(b.take()) != 0 {
		t.Error("bob was told about his own join")
	}
	h.Leave(b)
	frames = a.take()
	if len(frames) != 1 || frames[0] != (protocol.Presence{Event: protocol.PresenceLeave, Name: "bob"}) {
		t.Fatalf("anders got %#v", frames)
	}
	if h.Count() != 1 {
		t.Errorf("count %d", h.Count())
	}
}

func TestBroadcastReachesEveryoneIncludingSender(t *testing.T) {
	h := New()
	h.Now = func() time.Time { return time.UnixMilli(1234) }
	a, b, c := newPeer("anders"), newPeer("bob"), newPeer("carl")
	h.Join(a)
	h.Join(b)
	h.Join(c)
	a.take()
	b.take()
	c.take()

	if err := h.Route("anders", protocol.Msg{Text: "hej", From: "spoofed"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*fakePeer{a, b, c} {
		frames := p.take()
		if len(frames) != 1 {
			t.Fatalf("%s got %d frames", p.name, len(frames))
		}
		m := frames[0].(protocol.Msg)
		if m.From != "anders" || m.Text != "hej" || m.TS != 1234 || m.ID == "" || m.To != "" {
			t.Errorf("%s got %#v", p.name, m)
		}
	}
}

func TestDirectMessage(t *testing.T) {
	h := New()
	a, b, c := newPeer("anders"), newPeer("bob"), newPeer("carl")
	h.Join(a)
	h.Join(b)
	h.Join(c)
	a.take()
	b.take()
	c.take()

	if err := h.Route("anders", protocol.Msg{To: "bob", Text: "psst"}); err != nil {
		t.Fatal(err)
	}
	if got := b.take(); len(got) != 1 || got[0].(protocol.Msg).Text != "psst" || got[0].(protocol.Msg).From != "anders" {
		t.Errorf("bob got %#v", got)
	}
	if got := a.take(); len(got) != 1 || got[0].(protocol.Msg).To != "bob" {
		t.Errorf("sender echo %#v", got)
	}
	if got := c.take(); len(got) != 0 {
		t.Errorf("carl saw a private message: %#v", got)
	}

	if err := h.Route("anders", protocol.Msg{To: "nobody", Text: "x"}); !errors.Is(err, ErrNotOnline) {
		t.Errorf("got %v, want ErrNotOnline", err)
	}

	// A DM to yourself arrives once.
	if err := h.Route("anders", protocol.Msg{To: "anders", Text: "note"}); err != nil {
		t.Fatal(err)
	}
	if got := a.take(); len(got) != 1 {
		t.Errorf("self DM delivered %d times", len(got))
	}
}

func TestDuplicateNameKicksOldConnection(t *testing.T) {
	h := New()
	watcher := newPeer("bob")
	old, fresh := newPeer("anders"), newPeer("anders")
	h.Join(watcher)
	h.Join(old)
	watcher.take()

	users := h.Join(fresh)
	if !slices.Equal(users, []string{"anders", "bob"}) {
		t.Errorf("users %v", users)
	}
	if old.kicked != protocol.ErrCodeReplaced {
		t.Errorf("old connection kicked with %q", old.kicked)
	}
	if got := watcher.take(); len(got) != 0 {
		t.Errorf("replacement was announced: %#v", got)
	}
	// The old connection leaving must not remove the new one.
	h.Leave(old)
	if h.Count() != 2 {
		t.Errorf("count %d after old left", h.Count())
	}
	if got := watcher.take(); len(got) != 0 {
		t.Errorf("stale leave was announced: %#v", got)
	}
	// Messages go to the new connection.
	_ = h.Route("bob", protocol.Msg{To: "anders", Text: "hi"})
	if len(fresh.take()) != 1 || len(old.take()) != 0 {
		t.Error("DM went to the wrong connection")
	}
}

func TestRouteRateLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	h := NewWithRateLimit(3, 10*time.Second)
	h.Now = func() time.Time { return now }
	h.Join(newPeer("anders"))
	for i := 0; i < 3; i++ {
		if err := h.Route("anders", protocol.Msg{Text: "x"}); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	if err := h.Route("anders", protocol.Msg{Text: "x"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
	now = now.Add(10 * time.Second)
	if err := h.Route("anders", protocol.Msg{Text: "x"}); err != nil {
		t.Fatalf("after window: %v", err)
	}
}

func TestRateLimiterSlidingWindow(t *testing.T) {
	r := NewRateLimiter(20, 10*time.Second)
	base := time.Unix(0, 0)
	for i := 0; i < 20; i++ {
		if !r.Allow(base.Add(time.Duration(i) * 100 * time.Millisecond)) {
			t.Fatalf("event %d rejected", i)
		}
	}
	if r.Allow(base.Add(5 * time.Second)) {
		t.Fatal("21st event within window allowed")
	}
	// The first event falls out of the window at 10 s; one more is allowed.
	if !r.Allow(base.Add(10 * time.Second)) {
		t.Fatal("event after oldest expired rejected")
	}
	if r.Allow(base.Add(10*time.Second + 50*time.Millisecond)) {
		t.Fatal("second event allowed although only one slot freed")
	}
	if !r.Allow(base.Add(10*time.Second + 100*time.Millisecond)) {
		t.Fatal("event after second-oldest expired rejected")
	}
}

func TestTypingThrottledAndScoped(t *testing.T) {
	now := time.Unix(1000, 0)
	h := New()
	h.Now = func() time.Time { return now }
	a, b, c := newPeer("anders"), newPeer("bob"), newPeer("carl")
	h.Join(a)
	h.Join(b)
	h.Join(c)
	a.take()
	b.take()
	c.take()

	h.Typing("anders", protocol.Typing{})
	h.Typing("anders", protocol.Typing{}) // throttled
	if got := b.take(); len(got) != 1 || got[0].(protocol.Typing).From != "anders" {
		t.Errorf("bob got %#v", got)
	}
	if got := a.take(); len(got) != 0 {
		t.Errorf("sender got own typing hint %#v", got)
	}
	c.take()

	now = now.Add(2 * time.Second)
	h.Typing("anders", protocol.Typing{To: "bob"})
	if len(b.take()) != 1 || len(c.take()) != 0 {
		t.Error("private typing hint misrouted")
	}
}

func TestSlowPeerIsDropped(t *testing.T) {
	h := New()
	slow := newPeer("slow")
	slow.cap = 0
	h.Join(newPeer("anders"))
	h.Join(slow)
	_ = h.Route("anders", protocol.Msg{Text: "x"})
	if slow.kicked != protocol.ErrCodeTimeout {
		t.Errorf("slow peer kicked with %q", slow.kicked)
	}
}

func TestKickAll(t *testing.T) {
	h := New()
	a, b := newPeer("anders"), newPeer("bob")
	h.Join(a)
	h.Join(b)
	h.KickAll(protocol.ErrCodeShutdown)
	if a.kicked != protocol.ErrCodeShutdown || b.kicked != protocol.ErrCodeShutdown {
		t.Error("not everybody was kicked")
	}
}
