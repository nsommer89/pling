package ui

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/nsommer89/pling/internal/protocol"
)

// Dice limits for /roll.
const (
	maxDice  = 20
	minSides = 2
	maxSides = 1000
)

// commandNames are completed by Tab after a leading slash.
var commandNames = []string{"/help", "/msg", "/r", "/who", "/me", "/roll", "/shrug", "/clear", "/quit"}

// parseInput splits a line into a command ("/msg") and the rest. Lines not
// starting with a single slash are plain text (cmd ""). "//x" sends "/x".
func parseInput(line string) (cmd, rest string) {
	if !strings.HasPrefix(line, "/") {
		return "", line
	}
	if strings.HasPrefix(line, "//") {
		return "", line[1:]
	}
	cmd, rest, _ = strings.Cut(line, " ")
	return strings.ToLower(cmd), strings.TrimSpace(rest)
}

// splitTarget splits "name text" for /msg.
func splitTarget(rest string) (name, text string, ok bool) {
	name, text, _ = strings.Cut(strings.TrimSpace(rest), " ")
	name = strings.ToLower(name)
	text = strings.TrimSpace(text)
	return name, text, protocol.ValidName(name) && text != ""
}

// parseDice parses "NdM", "dM" or "" (1d6).
func parseDice(spec string) (n, sides int, err error) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return 1, 6, nil
	}
	left, right, ok := strings.Cut(spec, "d")
	if !ok {
		return 0, 0, errors.New(strDiceFormat)
	}
	n = 1
	if left != "" {
		if n, err = strconv.Atoi(left); err != nil {
			return 0, 0, errors.New(strDiceCount)
		}
	}
	if sides, err = strconv.Atoi(right); err != nil {
		return 0, 0, errors.New(strDiceSides)
	}
	if n < 1 || n > maxDice {
		return 0, 0, errors.New(strDiceCountRange)
	}
	if sides < minSides || sides > maxSides {
		return 0, 0, errors.New(strDiceSidesRange)
	}
	return n, sides, nil
}

// roll throws n dice with the given number of sides.
func roll(n, sides int, rng *rand.Rand) (rolls []int, total int) {
	rolls = make([]int, n)
	for i := range rolls {
		rolls[i] = rng.IntN(sides) + 1
		total += rolls[i]
	}
	return rolls, total
}

// shrug appends the shrug to text.
func shrug(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return strShrug
	}
	return text + " " + strShrug
}

// typingTarget says whether a line being typed should send a typing hint,
// and to whom ("" is the room). lastPeer is the /r target.
func typingTarget(line, lastPeer string) (to string, ok bool) {
	if strings.TrimSpace(line) == "" {
		return "", false
	}
	cmd, rest := parseInput(line)
	switch cmd {
	case "":
		return "", true
	case "/me", "/shrug":
		return "", rest != ""
	case "/msg":
		name, text, _ := strings.Cut(rest, " ")
		name = strings.ToLower(name)
		if protocol.ValidName(name) && text != "" {
			return name, true
		}
	case "/r":
		if lastPeer != "" && rest != "" {
			return lastPeer, true
		}
	}
	return "", false
}
