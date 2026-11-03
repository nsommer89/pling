package ui

import (
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

type entryKind int

const (
	kindMsg    entryKind = iota // chat message (room or DM)
	kindSystem                  // dim italic info line
	kindError                   // something went wrong
	kindMOTD                    // message of the day from the server
	kindDay                     // date separator
	kindJoin                    // "x joined", possibly with confetti
)

// entry is one item in the message log.
type entry struct {
	kind entryKind
	at   time.Time
	from string
	to   string // DM recipient; empty for the room
	text string
	me   bool
	// frame is the confetti frame for kindJoin; >= confettiFrames means
	// settled (no confetti).
	frame int

	cache      string
	cacheWidth int
}

// sanitize removes control characters, ANSI escapes and bidi overrides
// from text received over the network, so nobody can repaint your terminal.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			// dropped: C0, DEL and C1 (this includes ESC)
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			// dropped: bidirectional overrides and isolates
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// render returns the entry as one or more terminal lines of at most width
// cells, with continuation lines indented under the text column.
func (e *entry) render(st styles, self string, width int) string {
	if e.cacheWidth == width && e.cache != "" {
		return e.cache
	}
	e.cache = e.renderUncached(st, self, width)
	e.cacheWidth = width
	return e.cache
}

func (e *entry) invalidate() { e.cache = "" }

func (e *entry) renderUncached(st styles, self string, width int) string {
	if width < 10 {
		width = 10
	}
	stamp := st.time.Render(e.at.Format("15:04")) + " "
	switch e.kind {
	case kindDay:
		label := "── " + e.at.Format("Monday 2 January 2006") + " "
		if w := xansi.StringWidth(label); w < width {
			label += strings.Repeat("─", min(width-w, 12))
		}
		return st.dayLine.Render(xansi.Truncate(label, width, ""))
	case kindSystem:
		return hang(stamp+st.dimText.Render("-!- "), e.text, st.system, width)
	case kindError:
		return hang(stamp+st.errText.Render("-!- "), e.text, st.errText, width)
	case kindMOTD:
		return hang(stamp+st.motd.Render("*** "), e.text, st.motd, width)
	case kindJoin:
		prefix := stamp + st.dimText.Render("-!- ")
		if e.frame < confettiFrames {
			l, r := confetti(e.text, e.frame)
			line := prefix + paintConfetti(st, l) + " " + st.system.Render(e.text) + " " + paintConfetti(st, r)
			return xansi.Truncate(line, width, "")
		}
		return hang(prefix, e.text, st.system, width)
	}

	// Chat message.
	var prefix strings.Builder
	prefix.WriteString(stamp)
	private := e.to != ""
	outgoing := private && e.from == self && e.to != self
	if private {
		if outgoing {
			prefix.WriteString(st.dmMark.Render("→ "))
		} else {
			prefix.WriteString(st.dmMark.Render("← "))
		}
	}
	textStyle := lipgloss.NewStyle()
	who := e.from
	if outgoing {
		who = e.to
	}
	switch {
	case e.me:
		textStyle = st.meText
		prefix.WriteString(st.meText.Render("* "))
		prefix.WriteString(st.nick(e.from).Italic(true).Render(e.from) + " ")
	case private:
		prefix.WriteString(st.dmMark.Render(who+":") + " ")
	default:
		prefix.WriteString(st.bracket.Render("<") + st.nick(who).Render(who) + st.bracket.Render(">") + " ")
	}
	return hang(prefix.String(), e.text, textStyle, width)
}

// hang word-wraps text after prefix and indents continuation lines to the
// width of the prefix (or a little when the prefix is very wide).
func hang(prefix, text string, style lipgloss.Style, width int) string {
	pw := xansi.StringWidth(prefix)
	indent := pw
	if width-pw < 12 {
		indent = min(6, width/2)
	}
	first := width - pw
	if first < 1 {
		return prefix
	}
	words := xansi.Wrap(text, first, "")
	lines := strings.Split(words, "\n")
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(style.Render(lines[0]))
	rest := strings.Join(lines[1:], " ")
	if strings.TrimSpace(rest) == "" {
		return b.String()
	}
	pad := strings.Repeat(" ", indent)
	for _, l := range strings.Split(xansi.Wrap(rest, width-indent, ""), "\n") {
		b.WriteString("\n" + pad + style.Render(l))
	}
	return b.String()
}
