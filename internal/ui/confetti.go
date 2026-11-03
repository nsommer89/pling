package ui

import (
	"hash/fnv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// The join confetti: 8 frames of 125 ms, so one second in total. Turn it
// off with `fun = false` in the config.
const (
	confettiFrames   = 8
	confettiInterval = 125 * time.Millisecond
	confettiWidth    = 6
)

var confettiChars = []byte(`*+.'o,~^`)

// confetti returns the left and right confetti strips for a frame. It is
// deterministic (seeded by the text and frame) and gets sparser as the
// frames go by, so it looks like it settles.
func confetti(seed string, frame int) (left, right string) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	x := h.Sum64() + uint64(frame)*0x9E3779B97F4A7C15
	next := func() uint64 { // xorshift64
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return x
	}
	// Chance of a piece in a cell, out of 8: dense first, sparse at the end.
	density := uint64(confettiFrames - frame)
	strip := func() string {
		var b strings.Builder
		for range confettiWidth {
			if next()%8 < density {
				b.WriteByte(confettiChars[next()%uint64(len(confettiChars))])
			} else {
				b.WriteByte(' ')
			}
		}
		return b.String()
	}
	return strip(), strip()
}

// paintConfetti colours each piece with one of the accent colours.
func paintConfetti(st styles, strip string) string {
	var b strings.Builder
	for i, c := range []byte(strip) {
		if c == ' ' {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(st.confettiColor(i + int(c))).Render(string(c)))
	}
	return b.String()
}
