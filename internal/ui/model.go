// Package ui is the Bubble Tea chat interface. The look is described in
// docs/design.md; all user-facing text lives in strings.go.
package ui

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/protocol"
)

// Layout and behaviour constants.
const (
	listWidth       = 18 // online list column, including its left gap
	minListTerminal = 80 // the list is hidden below this terminal width
	tallHeader      = 18 // terminals at least this tall get the 3-line banner
	maxEntries      = 2000
	typingSendEvery = 3 * time.Second
	typingShowFor   = 5 * time.Second
	wheelLines      = 3
)

var banner = [3]string{
	"┌─┐┬  ┬┌┐┌┌─┐",
	"├─┘│  │││││ ┬",
	"┴  ┴─┘┴┘└┘└─┘",
}

// Conn is what the UI needs from the network side; *client.Client
// implements it.
type Conn interface {
	Events() <-chan any
	Send(ctx context.Context, m protocol.Message) error
}

// Options configure the model.
type Options struct {
	Conn       Conn
	Store      *client.Store // local history; nil disables saving
	Config     client.Config
	Host       string // server host[:port], shown in the UI and stored with history
	History    []client.Entry
	HistoryErr error
	Now        func() time.Time // nil means time.Now
}

type connState int

const (
	stateConnecting connState = iota
	stateConnected
	stateDisconnected
)

// Model is the chat UI.
type Model struct {
	o        Options
	st       styles
	notifier notifier
	rng      *rand.Rand

	width, height int
	vp            viewport.Model
	input         textinput.Model

	entries   []*entry
	self      string
	users     []string
	state     connState
	retryIn   time.Duration
	lastDrop  string // last disconnect text, to avoid repeating it
	unread    bool
	focused   bool
	lastPeer  string // for /r
	typing    map[string]time.Time
	lastTyped time.Time
	animating bool
	comp      completion
}

type completion struct {
	active  bool
	head    string // text before the completed word
	tail    string // text after the cursor
	matches []string
	idx     int
	suffix  string
}

// Messages internal to the UI.
type (
	eventMsg        struct{ ev any }
	eventsClosedMsg struct{}
	tickMsg         struct{}
	confettiMsg     struct{}
	sendResultMsg   struct{ err error }
)

// New returns the model.
func New(o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Model{
		o:        o,
		st:       newStyles(true),
		notifier: newNotifier(o.Config.Notify),
		rng:      rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x706c696e67)),
		self:     o.Config.Name,
		focused:  true,
		typing:   map[string]time.Time{},
	}
	m.vp = viewport.New()
	m.vp.FillHeight = true
	m.vp.MouseWheelEnabled = false // we scroll ourselves, see Update
	m.input = textinput.New()
	m.input.CharLimit = protocol.MaxTextBytes
	m.applyInputStyle()

	for _, h := range o.History {
		m.addEntry(&entry{kind: kindMsg, at: time.UnixMilli(h.TS), from: sanitize(h.From),
			to: sanitize(h.To), text: sanitize(h.Text), me: h.Me, frame: confettiFrames})
	}
	if o.HistoryErr != nil {
		m.system(kindError, strHistoryLoadFail+": "+o.HistoryErr.Error())
	}
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.input.Focus(), m.waitEvent(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) waitEvent() tea.Cmd {
	ch := m.o.Conn.Events()
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventsClosedMsg{}
		}
		return eventMsg{ev}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.BackgroundColorMsg:
		m.st = newStyles(msg.IsDark())
		m.applyInputStyle()
		m.restyle()
		return m, nil
	case tea.FocusMsg:
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseWheelMsg:
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			m.vp.ScrollUp(wheelLines)
		case tea.MouseWheelDown:
			m.vp.ScrollDown(wheelLines)
		}
		m.afterScroll()
		return m, nil
	case eventMsg:
		return m, tea.Batch(m.handleEvent(msg.ev), m.waitEvent())
	case eventsClosedMsg:
		return m, nil
	case tickMsg:
		now := m.o.Now()
		for name, t := range m.typing {
			if now.Sub(t) > typingShowFor {
				delete(m.typing, name)
			}
		}
		return m, tick()
	case confettiMsg:
		return m, m.advanceConfetti()
	case sendResultMsg:
		if msg.err != nil {
			if errors.Is(msg.err, client.ErrNotConnected) {
				m.system(kindError, strNotConnected)
			} else {
				m.system(kindError, strSendFailed+": "+msg.err.Error())
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() != "tab" {
		m.comp.active = false
	}
	switch k.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		return m, m.submit()
	case "pgup":
		m.vp.PageUp()
		m.afterScroll()
		return m, nil
	case "pgdown":
		m.vp.PageDown()
		m.afterScroll()
		return m, nil
	case "tab":
		m.complete()
		return m, nil
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	if m.input.Value() != before {
		cmd = tea.Batch(cmd, m.maybeSendTyping())
	}
	return m, cmd
}

func (m *Model) maybeSendTyping() tea.Cmd {
	if m.state != stateConnected {
		return nil
	}
	to, ok := typingTarget(m.input.Value(), m.lastPeer)
	now := m.o.Now()
	if !ok || now.Sub(m.lastTyped) < typingSendEvery {
		return nil
	}
	m.lastTyped = now
	return m.send(protocol.Typing{To: to}, true)
}

// send returns a command that writes m; quiet suppresses error reporting
// (for typing hints).
func (m *Model) send(msg protocol.Message, quiet bool) tea.Cmd {
	conn := m.o.Conn
	return func() tea.Msg {
		err := conn.Send(context.Background(), msg)
		if quiet {
			return nil
		}
		return sendResultMsg{err: err}
	}
}

// submit handles Enter.
func (m *Model) submit() tea.Cmd {
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return nil
	}
	cmd, rest := parseInput(line)
	done := func() tea.Cmd { m.input.Reset(); return nil }
	switch cmd {
	case "":
		return m.sendChat(protocol.Msg{Text: rest})
	case "/msg":
		name, text, ok := splitTarget(rest)
		if !ok {
			m.system(kindError, strMsgUsage)
			return nil
		}
		return m.sendChat(protocol.Msg{To: name, Text: text})
	case "/r":
		switch {
		case m.lastPeer == "":
			m.system(kindError, strNobodyToReply)
			return nil
		case rest == "":
			m.system(kindError, strReplyUsage)
			return nil
		}
		return m.sendChat(protocol.Msg{To: m.lastPeer, Text: rest})
	case "/me":
		if rest == "" {
			m.system(kindError, strMeUsage)
			return nil
		}
		return m.sendChat(protocol.Msg{Text: rest, Me: true})
	case "/roll":
		n, sides, err := parseDice(rest)
		if err != nil {
			m.system(kindError, strRollUsage(err))
			return nil
		}
		rolls, total := roll(n, sides, m.rng)
		return m.sendChat(protocol.Msg{Text: strRolls(fmt.Sprintf("%dd%d", n, sides), rolls, total), Me: true})
	case "/shrug":
		return m.sendChat(protocol.Msg{Text: shrug(rest)})
	case "/who":
		m.system(kindSystem, strWho(m.users))
		return done()
	case "/clear":
		m.entries = nil
		m.unread = false
		m.refresh(false)
		return done()
	case "/help":
		for _, l := range helpLines {
			m.system(kindSystem, l)
		}
		m.system(kindSystem, strSelectHint)
		return done()
	case "/quit", "/exit":
		return tea.Quit
	}
	m.system(kindError, strUnknownCommand(cmd))
	return nil
}

// sendChat sends a chat message. The message shows up when the server
// echoes it back. On failure the input is kept so nothing typed is lost.
func (m *Model) sendChat(msg protocol.Msg) tea.Cmd {
	if m.state != stateConnected {
		m.system(kindError, strNotConnected)
		return nil
	}
	if len(msg.Text) > protocol.MaxTextBytes {
		m.system(kindError, strTooLarge)
		return nil
	}
	m.input.Reset()
	m.lastTyped = time.Time{}
	return m.send(msg, false)
}

func (m *Model) handleEvent(ev any) tea.Cmd {
	switch ev := ev.(type) {
	case client.Connecting:
		m.state = stateConnecting
		if ev.Attempt == 1 && m.lastDrop == "" {
			m.system(kindSystem, strConnectingAttempt(m.o.Host, ev.Attempt))
		}
	case client.Connected:
		w := ev.Welcome
		m.state = stateConnected
		m.lastDrop = ""
		if name := sanitize(w.Name); protocol.ValidName(name) {
			m.self = name
		}
		m.users = m.users[:0]
		for _, u := range w.Users {
			if u = sanitize(u); protocol.ValidName(u) && !slices.Contains(m.users, u) {
				m.users = append(m.users, u)
			}
		}
		slices.Sort(m.users)
		m.applyInputStyle()
		m.system(kindSystem, strWelcome(m.o.Host, w.Name))
		for _, l := range strings.Split(strings.TrimSpace(w.MOTD), "\n") {
			if l = strings.TrimSpace(sanitize(l)); l != "" {
				m.addEntry(&entry{kind: kindMOTD, at: m.o.Now(), text: l})
			}
		}
		if len(m.users) <= 1 {
			m.system(kindSystem, strEmptyRoom)
		}
	case client.Disconnected:
		m.state = stateDisconnected
		m.users = nil
		m.typing = map[string]time.Time{}
		m.retryIn = ev.RetryIn
		m.reportDisconnect(ev)
	case client.Received:
		return m.handleFrame(ev.Message)
	}
	return nil
}

func (m *Model) reportDisconnect(ev client.Disconnected) {
	var lines []string
	var he *client.HTTPError
	switch {
	case client.IsServerCode(ev.Err, protocol.ErrCodeAuthFailed):
		lines = []string{strAuthFailed1, strAuthFailed2, strQuitHint}
	case client.IsServerCode(ev.Err, protocol.ErrCodeReplaced):
		lines = []string{strReplaced}
	case client.IsServerCode(ev.Err, protocol.ErrCodeUnsupportedVersion):
		lines = []string{strBadVersion, strQuitHint}
	case errors.As(ev.Err, &he) && he.Status == 404:
		lines = []string{strNotFound, strQuitHint}
	case client.IsServerCode(ev.Err, protocol.ErrCodeShutdown):
		lines = []string{strDisconnectedRetry(strServerRestart, seconds(ev.RetryIn))}
	default:
		lines = []string{strDisconnectedRetry(describeErr(ev.Err), seconds(ev.RetryIn))}
	}
	text := strings.Join(lines, "\n")
	if text == m.lastDrop {
		return // the status bar shows we are still retrying
	}
	m.lastDrop = text
	for _, l := range lines {
		m.system(kindError, l)
	}
}

// seconds rounds a (jittered) retry delay to whole seconds, at least 1.
func seconds(d time.Duration) int {
	return max(1, int(d.Round(time.Second)/time.Second))
}

// mentions reports whether text contains name as a whole word. Words are
// runs of the characters allowed in names, so "nikolaj:" and "@nikolaj"
// match but "nikolajsen" does not.
func mentions(text, name string) bool {
	if name == "" {
		return false
	}
	isNameRune := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !isNameRune(r) }) {
		if word == name {
			return true
		}
	}
	return false
}

// describeErr shortens network errors for the log line.
func describeErr(err error) string {
	if err == nil {
		return strDisconnected
	}
	var he *client.HTTPError
	if errors.As(err, &he) {
		return he.Error()
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 60 {
		s = s[i+2:]
	}
	return s
}

func (m *Model) handleFrame(f protocol.Message) tea.Cmd {
	switch f := f.(type) {
	case protocol.Msg:
		return m.handleMsg(f)
	case protocol.Presence:
		name := sanitize(f.Name)
		switch f.Event {
		case protocol.PresenceJoin:
			if !slices.Contains(m.users, name) {
				m.users = append(m.users, name)
				slices.Sort(m.users)
			}
			if name == m.self {
				return nil
			}
			e := &entry{kind: kindJoin, at: m.o.Now(), text: strJoined(name), frame: confettiFrames}
			if m.o.Config.Fun {
				e.frame = 0
			}
			m.addEntry(e)
			if m.o.Config.Fun && !m.animating {
				m.animating = true
				return confettiTick()
			}
		case protocol.PresenceLeave:
			m.users = slices.DeleteFunc(m.users, func(u string) bool { return u == name })
			delete(m.typing, name)
			m.system(kindSystem, strLeft(name))
		}
	case protocol.Typing:
		if f.From != m.self && f.From != "" {
			m.typing[sanitize(f.From)] = m.o.Now()
		}
	case protocol.Error:
		switch f.Code {
		case protocol.ErrCodeRateLimited:
			m.system(kindError, strRateLimited)
		case protocol.ErrCodeTooLarge:
			m.system(kindError, strTooLarge)
		case protocol.ErrCodeNotOnline:
			if f.Detail != "" {
				m.system(kindError, strNotOnline(sanitize(f.Detail)))
			} else {
				m.system(kindError, strUnknownTarget)
			}
		case protocol.ErrCodeBadFrame:
			m.system(kindError, strBadFrame)
		}
		// replaced, shutdown and timeout end the connection and are
		// reported by the Disconnected event.
	}
	return nil
}

func (m *Model) handleMsg(f protocol.Msg) tea.Cmd {
	e := &entry{kind: kindMsg, at: time.UnixMilli(f.TS), from: sanitize(f.From),
		to: sanitize(f.To), text: sanitize(f.Text), me: f.Me, frame: confettiFrames}
	if f.TS == 0 {
		e.at = m.o.Now()
	}
	delete(m.typing, e.from)
	if e.to != "" {
		if e.from == m.self {
			m.lastPeer = e.to
		} else {
			m.lastPeer = e.from
		}
	}
	m.addEntry(e)
	if m.o.Store != nil {
		err := m.o.Store.Append(client.Entry{TS: e.at.UnixMilli(), Server: m.o.Host,
			From: f.From, To: f.To, Text: f.Text, Me: f.Me})
		if err != nil {
			m.system(kindError, strHistorySaveFail+": "+err.Error())
		}
	}
	if e.from == m.self {
		return nil
	}
	private := e.to != ""
	mention := mentions(e.text, m.self)
	if private || mention || !m.focused {
		return m.notifier.notify(strNotifyTitle(e.from, private), e.text)
	}
	return nil
}

func confettiTick() tea.Cmd {
	return tea.Tick(confettiInterval, func(time.Time) tea.Msg { return confettiMsg{} })
}

func (m *Model) advanceConfetti() tea.Cmd {
	more := false
	for _, e := range m.entries {
		if e.kind == kindJoin && e.frame < confettiFrames {
			e.frame++
			e.invalidate()
			more = more || e.frame < confettiFrames
		}
	}
	m.refresh(false)
	if more {
		return confettiTick()
	}
	m.animating = false
	return nil
}

// complete handles Tab: command names after a leading slash, otherwise
// online nicks. Pressing Tab again cycles through the matches.
func (m *Model) complete() {
	if !m.comp.active {
		runes := []rune(m.input.Value())
		pos := min(m.input.Position(), len(runes))
		start := pos
		for start > 0 && runes[start-1] != ' ' {
			start--
		}
		word := strings.ToLower(string(runes[start:pos]))
		head := string(runes[:start])
		var cands []string
		suffix := " "
		switch {
		case start == 0 && strings.HasPrefix(word, "/"):
			cands = commandNames
		default:
			cands = m.users
			if start == 0 {
				suffix = ": " // "anders: hi" when addressing someone
			}
		}
		var matches []string
		for _, c := range cands {
			if strings.HasPrefix(c, word) && c != m.self {
				matches = append(matches, c)
			}
		}
		if len(matches) == 0 {
			return
		}
		m.comp = completion{active: true, head: head, tail: string(runes[pos:]), matches: matches, suffix: suffix}
	} else {
		m.comp.idx = (m.comp.idx + 1) % len(m.comp.matches)
	}
	choice := m.comp.matches[m.comp.idx] + m.comp.suffix
	m.input.SetValue(m.comp.head + choice + m.comp.tail)
	m.input.SetCursor(len([]rune(m.comp.head + choice)))
}

// system adds an info or error line.
func (m *Model) system(kind entryKind, text string) {
	m.addEntry(&entry{kind: kind, at: m.o.Now(), text: text, frame: confettiFrames})
}

func (m *Model) addEntry(e *entry) {
	if n := len(m.entries); n == 0 || !sameDay(m.entries[n-1].at, e.at) {
		m.entries = append(m.entries, &entry{kind: kindDay, at: e.at})
	}
	m.entries = append(m.entries, e)
	if len(m.entries) > maxEntries {
		m.entries = slices.Delete(m.entries, 0, len(m.entries)-maxEntries)
	}
	m.refresh(true)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// refresh re-renders the log into the viewport. When added is true and the
// user has scrolled up, the unread marker is set instead of jumping down.
func (m *Model) refresh(added bool) {
	if m.width == 0 {
		return
	}
	atBottom := m.vp.AtBottom()
	w := m.vp.Width()
	lines := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		lines = append(lines, e.render(m.st, m.self, w))
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
	if atBottom {
		m.vp.GotoBottom()
	} else if added {
		m.unread = true
	}
}

func (m *Model) afterScroll() {
	if m.vp.AtBottom() {
		m.unread = false
	}
}

func (m *Model) restyle() {
	for _, e := range m.entries {
		e.invalidate()
	}
	m.refresh(false)
}

func (m *Model) showList() bool { return m.width >= minListTerminal }

func (m *Model) headerHeight() int {
	if m.height >= tallHeader {
		return len(banner) + 1
	}
	return 2
}

func (m *Model) layout() {
	w := m.width
	if m.showList() {
		w -= listWidth
	}
	h := max(1, m.height-m.headerHeight()-2) // status bar + input
	atBottom := m.vp.AtBottom()
	m.vp.SetWidth(max(10, w))
	m.vp.SetHeight(h)
	m.input.SetWidth(max(1, m.width-xansi.StringWidth(m.input.Prompt)-1))
	m.refresh(false)
	if atBottom {
		m.vp.GotoBottom()
	}
}

func (m *Model) applyInputStyle() {
	name := m.self
	if name == "" {
		name = "pling"
	}
	m.input.Prompt = name + "› "
	s := textinput.DefaultStyles(m.st.isDark)
	s.Focused.Prompt = m.st.nick(name)
	s.Blurred.Prompt = m.st.nick(name)
	m.input.SetStyles(s)
	if m.width > 0 {
		m.input.SetWidth(max(1, m.width-xansi.StringWidth(m.input.Prompt)-1))
	}
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.WindowTitle = "pling"
	if m.width == 0 || m.height == 0 {
		return v
	}
	v.SetContent(strings.Join([]string{m.headerView(), m.bodyView(), m.statusView(), m.input.View()}, "\n"))
	return v
}

func (m *Model) connLine() string {
	switch m.state {
	case stateConnected:
		return m.st.dimText.Render(strWelcome(m.o.Host, m.self))
	case stateDisconnected:
		return m.st.errText.Render(strDisconnected)
	default:
		return m.st.dimText.Render(strConnecting)
	}
}

func (m *Model) headerView() string {
	rule := m.st.rule.Render(strings.Repeat("─", m.width))
	if m.height < tallHeader {
		line := m.st.banner.Render("pling") + "  " + m.st.tagline.Render(strTagline)
		return xansi.Truncate(line, m.width, "") + "\n" + rule
	}
	lines := []string{
		" " + m.st.banner.Render(banner[0]),
		" " + m.st.banner.Render(banner[1]) + "   " + m.st.tagline.Render(strTagline),
		" " + m.st.banner.Render(banner[2]) + "   " + m.connLine(),
	}
	for i, l := range lines {
		lines[i] = xansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n") + "\n" + rule
}

func (m *Model) bodyView() string {
	log := lipgloss.NewStyle().Width(m.vp.Width()).Height(m.vp.Height()).MaxHeight(m.vp.Height()).Render(m.vp.View())
	if !m.showList() {
		return log
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, log, m.listView())
}

func (m *Model) listView() string {
	h := m.vp.Height()
	inner := listWidth - 2
	lines := []string{m.st.listHead.Render(xansi.Truncate(strOnline+fmt.Sprintf(" (%d)", len(m.users)), inner, ""))}
	now := m.o.Now()
	for i, u := range m.users {
		if len(lines) == h-1 && len(m.users)-i > 1 {
			lines = append(lines, m.st.dimText.Render(fmt.Sprintf("+%d", len(m.users)-i)))
			break
		}
		name := m.st.nick(u).Render(xansi.Truncate(u, inner-1, "…"))
		if t, ok := m.typing[u]; ok && now.Sub(t) <= typingShowFor {
			name += m.st.dimText.Render("…")
		}
		lines = append(lines, name)
	}
	return lipgloss.NewStyle().PaddingLeft(2).Width(listWidth).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) statusView() string {
	var dot string
	switch m.state {
	case stateConnected:
		dot = lipgloss.NewStyle().Foreground(m.st.green).Render("●")
	case stateDisconnected:
		dot = lipgloss.NewStyle().Foreground(m.st.errC).Render("●")
	default:
		dot = lipgloss.NewStyle().Foreground(m.st.amber).Render("○")
	}
	sep := m.st.statusDim.Render(" · ")
	left := " " + dot + " " + m.st.statusDim.Render(m.o.Host) + sep + m.st.nick(m.self).Render(m.self) + sep
	switch m.state {
	case stateConnected:
		left += m.st.statusDim.Render(strOnlineCount(len(m.users)))
	case stateDisconnected:
		label := strDisconnected
		if m.retryIn > 0 {
			label = fmt.Sprintf("%s, retrying", strDisconnected)
		}
		left += m.st.errText.Render(label)
	default:
		left += m.st.statusDim.Render(strConnecting)
	}
	if t := m.typingNames(); len(t) > 0 {
		left += "   " + m.st.statusDim.Italic(true).Render(strTyping(t))
	}

	var right string
	switch {
	case m.unread:
		right = lipgloss.NewStyle().Foreground(m.st.amber).Bold(true).Render(strNewMessages)
	case !m.vp.AtBottom():
		right = m.st.statusDim.Render(strScrolledUp)
	case m.vp.TotalLineCount() > m.vp.Height():
		right = m.st.statusDim.Render(strScrollHint)
	}
	right += " "
	gap := m.width - xansi.StringWidth(left) - xansi.StringWidth(right)
	if gap < 1 {
		if m.unread {
			// The unread marker matters more than the left part.
			left = xansi.Truncate(left, max(0, m.width-xansi.StringWidth(right)-1), "…")
			gap = 1
		} else {
			return xansi.Truncate(left, m.width, "…")
		}
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) typingNames() []string {
	now := m.o.Now()
	var names []string
	for name, t := range m.typing {
		if now.Sub(t) <= typingShowFor {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
