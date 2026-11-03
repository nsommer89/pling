package ui

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/protocol"
)

func TestNickColourStable(t *testing.T) {
	// Pinned values: changing the hash would recolour everybody.
	want := map[string]int{"anders": nickIndex("anders"), "nikolaj": nickIndex("nikolaj")}
	for i := 0; i < 3; i++ {
		for name, idx := range want {
			if got := nickIndex(name); got != idx {
				t.Fatalf("nickIndex(%q) changed: %d vs %d", name, got, idx)
			}
		}
	}
	if nickIndex("anders") != 6 || nickIndex("nikolaj") != 1 {
		t.Errorf("FNV-1a mapping changed: anders=%d nikolaj=%d", nickIndex("anders"), nickIndex("nikolaj"))
	}
	seen := map[int]bool{}
	for _, n := range []string{"a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10", "k11", "l12", "m13", "n14", "o15", "p16"} {
		idx := nickIndex(n)
		if idx < 0 || idx > 7 {
			t.Fatalf("index %d out of range", idx)
		}
		seen[idx] = true
	}
	if len(seen) < 5 {
		t.Errorf("poor spread: only %d of 8 colours used", len(seen))
	}
	for _, dark := range []bool{true, false} {
		if n := len(newStyles(dark).nicks); n != 8 {
			t.Errorf("palette has %d colours", n)
		}
	}
}

func TestParseDice(t *testing.T) {
	good := map[string][2]int{"": {1, 6}, "2d6": {2, 6}, "d20": {1, 20}, " 3D8 ": {3, 8}, "20d1000": {20, 1000}}
	for in, want := range good {
		n, s, err := parseDice(in)
		if err != nil || n != want[0] || s != want[1] {
			t.Errorf("parseDice(%q) = %d, %d, %v", in, n, s, err)
		}
	}
	for _, in := range []string{"6", "0d6", "21d6", "2d1", "2d1001", "xd6", "2dx", "2d", "-1d6", "2d6d6"} {
		if _, _, err := parseDice(in); err == nil {
			t.Errorf("parseDice(%q) accepted", in)
		}
	}
}

func TestRoll(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200; i++ {
		rolls, total := roll(3, 6, rng)
		sum := 0
		for _, r := range rolls {
			if r < 1 || r > 6 {
				t.Fatalf("roll %d out of range", r)
			}
			sum += r
		}
		if sum != total || len(rolls) != 3 {
			t.Fatalf("total %d, sum %d", total, sum)
		}
	}
	if got := strRolls("2d6", []int{3, 5}, 8); got != "rolls 2d6: 3 + 5 = 8" {
		t.Errorf("got %q", got)
	}
	if got := strRolls("1d20", []int{17}, 17); got != "rolls 1d20: 17" {
		t.Errorf("got %q", got)
	}
}

func TestParseInput(t *testing.T) {
	cases := []struct{ in, cmd, rest string }{
		{"hej", "", "hej"},
		{"/msg anders hi there", "/msg", "anders hi there"},
		{"/ME waves", "/me", "waves"},
		{"//not a command", "", "/not a command"},
		{"/quit", "/quit", ""},
	}
	for _, c := range cases {
		cmd, rest := parseInput(c.in)
		if cmd != c.cmd || rest != c.rest {
			t.Errorf("parseInput(%q) = %q, %q", c.in, cmd, rest)
		}
	}
	if name, text, ok := splitTarget("Anders  hej med dig"); !ok || name != "anders" || text != "hej med dig" {
		t.Errorf("splitTarget: %q %q %v", name, text, ok)
	}
	if _, _, ok := splitTarget("anders"); ok {
		t.Error("splitTarget without text accepted")
	}
	if got := shrug(""); got != `¯\_(ツ)_/¯` {
		t.Errorf("shrug %q", got)
	}
	if got := shrug("tja"); got != `tja ¯\_(ツ)_/¯` {
		t.Errorf("shrug %q", got)
	}
}

func TestTypingTarget(t *testing.T) {
	cases := []struct {
		in, to string
		ok     bool
	}{
		{"hej", "", true},
		{"", "", false},
		{"/who", "", false},
		{"/me waves", "", true},
		{"/msg bob hi", "bob", true},
		{"/msg bob", "", false},
		{"/r hi", "carl", true},
	}
	for _, c := range cases {
		to, ok := typingTarget(c.in, "carl")
		if to != c.to || ok != c.ok {
			t.Errorf("typingTarget(%q) = %q, %v", c.in, to, ok)
		}
	}
}

func TestSanitize(t *testing.T) {
	in := "hi\x1b[31mred\x07\tthere‮evil\nline\u0085"
	got := sanitize(in)
	if strings.ContainsAny(got, "\x1b\x07\t\n‮\u0085") {
		t.Errorf("control characters left in %q", got)
	}
	if got != "hi[31mred thereevil line" {
		t.Errorf("got %q", got)
	}
	if sanitize("æøå ¯\\_(ツ)_/¯") != "æøå ¯\\_(ツ)_/¯" {
		t.Error("normal text changed")
	}
}

func TestConfetti(t *testing.T) {
	l1, r1 := confetti("anders joined", 0)
	l2, r2 := confetti("anders joined", 0)
	if l1 != l2 || r1 != r2 {
		t.Error("confetti not deterministic")
	}
	if len(l1) != confettiWidth || strings.TrimSpace(l1+r1) == "" {
		t.Errorf("frame 0 looks empty: %q %q", l1, r1)
	}
	for f := 0; f < confettiFrames; f++ {
		l, r := confetti("x", f)
		for _, c := range l + r {
			if c != ' ' && !strings.ContainsRune(string(confettiChars), c) {
				t.Errorf("non-ASCII confetti %q", c)
			}
		}
	}
}

func TestRenderWrapsWithinWidth(t *testing.T) {
	st := newStyles(true)
	at := time.Date(2026, 11, 3, 12, 34, 0, 0, time.Local)
	long := strings.Repeat("ord ", 40)
	cases := []*entry{
		{kind: kindMsg, at: at, from: "anders", text: long},
		{kind: kindMsg, at: at, from: "anders", to: "nikolaj", text: long},
		{kind: kindMsg, at: at, from: "nikolaj", to: "anders", text: "psst"},
		{kind: kindMsg, at: at, from: "anders", text: "waves", me: true},
		{kind: kindSystem, at: at, text: long},
		{kind: kindJoin, at: at, text: "anders joined", frame: 0},
		{kind: kindDay, at: at},
	}
	for _, width := range []int{20, 40, 61} {
		for _, e := range cases {
			e.invalidate()
			out := e.render(st, "nikolaj", width)
			for _, l := range strings.Split(out, "\n") {
				if w := xansi.StringWidth(l); w > width {
					t.Errorf("width %d: line %q is %d wide", width, xansi.Strip(l), w)
				}
			}
		}
	}
	plain := func(e *entry) string { e.invalidate(); return xansi.Strip(e.render(st, "nikolaj", 80)) }
	if got := plain(cases[2]); got != "12:34 → anders: psst" {
		t.Errorf("outgoing DM %q", got)
	}
	if got := plain(&entry{kind: kindMsg, at: at, from: "anders", to: "nikolaj", text: "hej"}); got != "12:34 ← anders: hej" {
		t.Errorf("incoming DM %q", got)
	}
	if got := plain(&entry{kind: kindMsg, at: at, from: "anders", text: "hej"}); got != "12:34 <anders> hej" {
		t.Errorf("room %q", got)
	}
	if got := plain(cases[3]); got != "12:34 * anders waves" {
		t.Errorf("me %q", got)
	}
}

// fakeConn records sent frames.
type fakeConn struct {
	events chan any
	mu     sync.Mutex
	sent   []protocol.Message
}

func (f *fakeConn) Events() <-chan any { return f.events }
func (f *fakeConn) Send(_ context.Context, m protocol.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

// run executes a command and feeds resulting messages back, ignoring
// commands that block (waitEvent, ticks).
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runCmd(c)
			}
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func newTestModel(t *testing.T) (*Model, *fakeConn) {
	t.Helper()
	conn := &fakeConn{events: make(chan any)}
	cfg := client.DefaultConfig()
	cfg.Name = "nikolaj"
	cfg.Notify = client.NotifyOff
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local)
	m := New(Options{Conn: conn, Config: cfg, Host: "pling.example", Now: func() time.Time { return now }})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.handleEvent(client.Connected{Welcome: protocol.Welcome{Name: "nikolaj", Users: []string{"nikolaj", "anders", "anna"}, MOTD: "hej"}})
	return m, conn
}

func typeLine(m *Model, s string) {
	m.input.SetValue(s)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runCmd(cmd)
}

func (f *fakeConn) last() protocol.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return nil
	}
	return f.sent[len(f.sent)-1]
}

func TestModelSendsCommands(t *testing.T) {
	m, conn := newTestModel(t)

	typeLine(m, "hej alle")
	if got := conn.last(); got != (protocol.Msg{Text: "hej alle"}) {
		t.Errorf("room: %#v", got)
	}
	if m.input.Value() != "" {
		t.Error("input not cleared after send")
	}
	typeLine(m, "/msg anders psst")
	if got := conn.last(); got != (protocol.Msg{To: "anders", Text: "psst"}) {
		t.Errorf("/msg: %#v", got)
	}
	typeLine(m, "/me vinker")
	if got := conn.last(); got != (protocol.Msg{Text: "vinker", Me: true}) {
		t.Errorf("/me: %#v", got)
	}
	typeLine(m, "/shrug")
	if got := conn.last(); got != (protocol.Msg{Text: `¯\_(ツ)_/¯`}) {
		t.Errorf("/shrug: %#v", got)
	}
	typeLine(m, "/roll 2d6")
	if got, ok := conn.last().(protocol.Msg); !ok || !got.Me || !strings.HasPrefix(got.Text, "rolls 2d6: ") {
		t.Errorf("/roll: %#v", conn.last())
	}

	// /r replies to whoever last sent a DM.
	m.handleFrame(protocol.Msg{ID: "1", From: "anna", To: "nikolaj", Text: "hej", TS: 1})
	typeLine(m, "/r hej anna")
	if got := conn.last(); got != (protocol.Msg{To: "anna", Text: "hej anna"}) {
		t.Errorf("/r: %#v", got)
	}

	n := len(conn.sent)
	typeLine(m, "/bogus")
	typeLine(m, "/msg")
	if len(conn.sent) != n {
		t.Error("invalid command sent something")
	}
	if !strings.Contains(m.vp.GetContent(), "unknown command /bogus") {
		t.Error("no error shown for unknown command")
	}
}

func TestModelKeepsInputWhenOffline(t *testing.T) {
	m, conn := newTestModel(t)
	m.handleEvent(client.Disconnected{Err: context.DeadlineExceeded, RetryIn: 5 * time.Second})
	typeLine(m, "kommer det frem?")
	if len(conn.sent) != 0 {
		t.Error("sent while offline")
	}
	if m.input.Value() != "kommer det frem?" {
		t.Errorf("input lost: %q", m.input.Value())
	}
	if !strings.Contains(m.vp.GetContent(), strNotConnected) {
		t.Error("no offline notice")
	}
}

func TestModelPresenceAndCompletion(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleFrame(protocol.Presence{Event: protocol.PresenceJoin, Name: "bob"})
	if !strings.Contains(xansi.Strip(m.vp.GetContent()), "bob joined") {
		t.Error("join not shown")
	}
	if len(m.users) != 4 {
		t.Errorf("users %v", m.users)
	}

	m.input.SetValue("an")
	m.input.CursorEnd()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "anders: " {
		t.Errorf("tab 1: %q", m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "anna: " {
		t.Errorf("tab 2: %q", m.input.Value())
	}
	m.input.SetValue("/msg b")
	m.input.CursorEnd()
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}) // resets completion
	m.input.SetValue("/msg b")
	m.input.CursorEnd()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/msg bob " {
		t.Errorf("tab after /msg: %q", m.input.Value())
	}
	m.input.SetValue("/ro")
	m.input.CursorEnd()
	m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	m.input.SetValue("/ro")
	m.input.CursorEnd()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/roll " {
		t.Errorf("command completion: %q", m.input.Value())
	}

	m.handleFrame(protocol.Presence{Event: protocol.PresenceLeave, Name: "bob"})
	if len(m.users) != 3 {
		t.Errorf("users after leave %v", m.users)
	}
}

func TestModelUnreadMarkerWhenScrolledUp(t *testing.T) {
	m, _ := newTestModel(t)
	for i := 0; i < 60; i++ {
		m.handleFrame(protocol.Msg{From: "anders", Text: "fyld", TS: 1})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.vp.AtBottom() {
		t.Fatal("PgUp did not scroll")
	}
	m.handleFrame(protocol.Msg{From: "anders", Text: "ny", TS: 1})
	if !m.unread || !strings.Contains(m.statusView(), strNewMessages) {
		t.Error("no unread marker")
	}
	for !m.vp.AtBottom() {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if m.unread {
		t.Error("unread marker stayed after scrolling down")
	}
}

func TestModelViewFitsTerminal(t *testing.T) {
	m, _ := newTestModel(t)
	m.handleFrame(protocol.Typing{From: "anders"})
	for _, size := range [][2]int{{100, 30}, {60, 12}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View()
		lines := strings.Split(v.Content, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if w := xansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d wide: %q", size[0], size[1], w, xansi.Strip(l))
			}
		}
	}
	if !strings.Contains(m.statusView(), "anders is typing") {
		t.Error("typing indicator missing")
	}
}

func TestModelStoresHistory(t *testing.T) {
	m, _ := newTestModel(t)
	store, err := client.OpenStore(t.TempDir() + "/history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m.o.Store = store
	m.handleFrame(protocol.Msg{From: "anders", Text: "gem mig", TS: 42})
	got, _ := store.Load("pling.example", 10)
	if len(got) != 1 || got[0].Text != "gem mig" || got[0].TS != 42 {
		t.Errorf("history %#v", got)
	}
}
