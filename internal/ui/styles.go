package ui

import (
	"hash/fnv"
	"image/color"

	"charm.land/lipgloss/v2"
)

// styles holds every Lip Gloss style of the UI. They are rebuilt when the
// terminal reports its background colour, so the palette adapts to light
// terminals too; dark is assumed until then. See docs/design.md.
type styles struct {
	isDark bool

	amber, cyan, green, dm, dim, errC color.Color
	nicks                             []color.Color

	banner, tagline, rule   lipgloss.Style
	time, dimText, system   lipgloss.Style
	errText, motd, dayLine  lipgloss.Style
	bracket, dmMark, meText lipgloss.Style
	statusBar, statusDim    lipgloss.Style
	listHead, prompt        lipgloss.Style
}

func newStyles(isDark bool) styles {
	ld := lipgloss.LightDark(isDark)
	s := styles{isDark: isDark}
	// Accents. Hex values are downsampled by Bubble Tea to the terminal's
	// colour profile, so in a 16-colour terminal they become yellow, cyan,
	// green and magenta.
	s.amber = ld(lipgloss.Color("#B07D00"), lipgloss.Color("#FFB000"))
	s.cyan = ld(lipgloss.Color("#00808A"), lipgloss.Color("#5FD7D7"))
	s.green = ld(lipgloss.Color("#2E7D32"), lipgloss.Color("#87D787"))
	s.dm = ld(lipgloss.Color("#9C2B8C"), lipgloss.Color("#E38FD8"))
	s.dim = ld(lipgloss.Color("#767676"), lipgloss.Color("#8A8A8A"))
	s.errC = ld(lipgloss.Color("#C62828"), lipgloss.Color("#FF8787"))
	// Nick colours use the 16 ANSI colours directly, so they stay distinct
	// in a 16-colour terminal and follow the user's terminal theme. Blue
	// and black are left out: hard to read on dark backgrounds.
	if isDark {
		s.nicks = ansi("11", "14", "10", "13", "12", "9", "3", "6")
	} else {
		s.nicks = ansi("3", "6", "2", "5", "4", "1", "12", "9")
	}

	s.banner = lipgloss.NewStyle().Foreground(s.amber).Bold(true)
	s.tagline = lipgloss.NewStyle().Foreground(s.dim)
	s.rule = lipgloss.NewStyle().Foreground(s.dim)
	s.time = lipgloss.NewStyle().Foreground(s.dim)
	s.dimText = lipgloss.NewStyle().Foreground(s.dim)
	s.system = lipgloss.NewStyle().Foreground(s.dim).Italic(true)
	s.errText = lipgloss.NewStyle().Foreground(s.errC).Italic(true)
	s.motd = lipgloss.NewStyle().Foreground(s.amber)
	s.dayLine = lipgloss.NewStyle().Foreground(s.cyan)
	s.bracket = lipgloss.NewStyle().Foreground(s.dim)
	s.dmMark = lipgloss.NewStyle().Foreground(s.dm).Bold(true)
	s.meText = lipgloss.NewStyle().Italic(true)
	s.statusBar = lipgloss.NewStyle()
	s.statusDim = lipgloss.NewStyle().Foreground(s.dim)
	s.listHead = lipgloss.NewStyle().Foreground(s.dim)
	s.prompt = lipgloss.NewStyle().Bold(true)
	return s
}

func ansi(codes ...string) []color.Color {
	out := make([]color.Color, len(codes))
	for i, c := range codes {
		out[i] = lipgloss.Color(c)
	}
	return out
}

// nickIndex maps a name to one of the 8 nick colours. It is a stable hash
// (FNV-1a), so a name has the same colour for everybody, everywhere.
func nickIndex(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return int(h.Sum32() % 8)
}

func (s styles) nick(name string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(s.nicks[nickIndex(name)]).Bold(true)
}

func (s styles) confettiColor(i int) color.Color {
	switch i % 4 {
	case 0:
		return s.amber
	case 1:
		return s.cyan
	case 2:
		return s.green
	default:
		return s.dm
	}
}
