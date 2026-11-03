package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// HistoryFile is the history file name inside the data directory.
const HistoryFile = "history.jsonl"

// HistoryLoad is how many entries the client shows from history on start.
const HistoryLoad = 500

// Entry is one stored message. History lives only on the user's machine;
// the server keeps nothing.
type Entry struct {
	TS     int64  `json:"ts"` // unix milliseconds
	Server string `json:"server,omitempty"`
	From   string `json:"from"`
	To     string `json:"to,omitempty"` // empty for the room
	Text   string `json:"text"`
	Me     bool   `json:"me,omitempty"`
}

// maxLineBytes is the longest history line Load accepts; longer lines
// (damage, or a file that is not ours) are skipped. A 4 KB message with
// worst-case JSON escaping stays far below it.
const maxLineBytes = 64 * 1024

// Store is an append-only JSON Lines history file. Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	f    *os.File
	// needNewline is set when the file does not end with a newline (say,
	// after a crash mid-write), so the next entry starts on its own line.
	needNewline bool
}

// OpenStore opens (or creates) the history file. The directory gets mode
// 0700 and the file 0600, also when they already existed.
func OpenStore(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	s := &Store{path: path, f: f}
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			s.needNewline = true
		}
	}
	return s, nil
}

// Append writes one entry.
func (s *Store) Append(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.needNewline {
		b = append([]byte{'\n'}, b...)
	}
	if _, err := s.f.Write(b); err != nil {
		return err
	}
	s.needNewline = false
	return nil
}

// Load returns the last n entries for server (all servers if empty), oldest
// first. Lines that do not parse or are too long are skipped, so a damaged
// file never blocks start-up.
func (s *Store) Load(server string, n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ring := make([]Entry, 0, n)
	next := 0
	add := func(line []byte) {
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			return
		}
		if server != "" && e.Server != server {
			return
		}
		if len(ring) < n {
			ring = append(ring, e)
			return
		}
		ring[next] = e
		next = (next + 1) % n
	}

	r := bufio.NewReaderSize(f, maxLineBytes)
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Too long: drop the rest of this line.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		if len(line) > 0 {
			add(line)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return append(ring[next:], ring[:next]...), nil
}

// Close closes the file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}
