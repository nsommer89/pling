package client

import (
	"bufio"
	"encoding/json"
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

// Store is an append-only JSON Lines history file. Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// OpenStore opens (or creates) the history file. The directory is created
// with mode 0700 and the file with 0600.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, f: f}, nil
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
	_, err = s.f.Write(b)
	return err
}

// Load returns the last n entries for server (all servers if empty), oldest
// first. Lines that do not parse are skipped, so a damaged line never
// blocks start-up.
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
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if server != "" && e.Server != server {
			continue
		}
		if len(ring) < n {
			ring = append(ring, e)
			continue
		}
		ring[next] = e
		next = (next + 1) % n
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return append(ring[next:], ring[:next]...), nil
}

// Close closes the file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}
