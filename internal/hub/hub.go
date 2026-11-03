// Package hub keeps track of who is online and routes frames between them.
// It is transport-agnostic: a connection is anything implementing Peer.
// The hub keeps no message history; a message is encoded once, handed to
// the recipients' queues and forgotten.
package hub

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/nsommer89/pling/internal/protocol"
)

// Rate limit defaults: 20 messages per 10 seconds per user.
const (
	DefaultRateLimit  = 20
	DefaultRateWindow = 10 * time.Second
	// typingInterval is the minimum gap between relayed typing frames from
	// one user; extra frames are dropped silently.
	typingInterval = time.Second
)

// Errors returned by Route.
var (
	ErrNotOnline   = errors.New("hub: recipient is not online")
	ErrRateLimited = errors.New("hub: rate limited")
)

// Peer is one connected, authenticated user.
type Peer interface {
	// Name is the authenticated user name.
	Name() string
	// Deliver queues an encoded frame without blocking. It returns false
	// when the queue is full; the hub then drops the peer.
	Deliver(frame []byte) bool
	// Kick tells the peer (best effort) why with an error code and closes
	// it. It must not block.
	Kick(code string)
}

// Hub is safe for concurrent use.
type Hub struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	rateLimit  int
	rateWindow time.Duration

	mu         sync.Mutex
	peers      map[string]Peer
	limiters   map[string]*RateLimiter
	lastTyping map[string]time.Time
}

// New returns an empty hub with the default rate limit.
func New() *Hub {
	return NewWithRateLimit(DefaultRateLimit, DefaultRateWindow)
}

// NewWithRateLimit returns an empty hub allowing limit messages per window
// per user.
func NewWithRateLimit(limit int, window time.Duration) *Hub {
	return &Hub{
		rateLimit:  limit,
		rateWindow: window,
		peers:      make(map[string]Peer),
		limiters:   make(map[string]*RateLimiter),
		lastTyping: make(map[string]time.Time),
	}
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Join registers p and returns the sorted list of online users (including
// p). If the name is already online, the old connection is kicked with
// "replaced" and no join/leave is announced, since the name never left.
func (h *Hub) Join(p Peer) []string {
	h.mu.Lock()
	old, replaced := h.peers[p.Name()]
	h.peers[p.Name()] = p
	users := h.usersLocked()
	var drop []Peer
	if !replaced {
		drop = h.broadcastLocked(protocol.Presence{Event: protocol.PresenceJoin, Name: p.Name()}, p.Name())
	}
	h.mu.Unlock()

	if replaced && old != p {
		old.Kick(protocol.ErrCodeReplaced)
	}
	h.dropSlow(drop)
	return users
}

// Leave unregisters p if it is still the current connection for its name
// and tells the others.
func (h *Hub) Leave(p Peer) {
	h.mu.Lock()
	if h.peers[p.Name()] != p {
		h.mu.Unlock()
		return
	}
	delete(h.peers, p.Name())
	delete(h.lastTyping, p.Name())
	drop := h.broadcastLocked(protocol.Presence{Event: protocol.PresenceLeave, Name: p.Name()}, "")
	h.mu.Unlock()
	h.dropSlow(drop)
}

// Route delivers a message from the authenticated user from. An empty
// m.To means the room. The server sets ID, From and TS. The sender gets
// the message echoed back so all clients show the same version.
func (h *Hub) Route(from string, m protocol.Msg) error {
	now := h.now()
	m.ID = newID()
	m.From = from
	m.TS = now.UnixMilli()

	h.mu.Lock()
	lim := h.limiters[from]
	if lim == nil {
		lim = NewRateLimiter(h.rateLimit, h.rateWindow)
		h.limiters[from] = lim
	}
	if !lim.Allow(now) {
		h.mu.Unlock()
		return ErrRateLimited
	}
	var drop []Peer
	if m.To == "" {
		drop = h.broadcastLocked(m, "")
	} else {
		target, ok := h.peers[m.To]
		if !ok {
			h.mu.Unlock()
			return ErrNotOnline
		}
		frame, err := protocol.Encode(m)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		if !target.Deliver(frame) {
			drop = append(drop, target)
		}
		if self, ok := h.peers[from]; ok && m.To != from {
			if !self.Deliver(frame) {
				drop = append(drop, self)
			}
		}
	}
	h.mu.Unlock()
	h.dropSlow(drop)
	return nil
}

// Typing relays a typing hint from the user from, at most once per second.
// Hints to users who are not online are dropped silently.
func (h *Hub) Typing(from string, t protocol.Typing) {
	now := h.now()
	t.From = from
	h.mu.Lock()
	if last, ok := h.lastTyping[from]; ok && now.Sub(last) < typingInterval {
		h.mu.Unlock()
		return
	}
	h.lastTyping[from] = now
	var drop []Peer
	if t.To == "" {
		drop = h.broadcastLocked(t, from)
	} else if target, ok := h.peers[t.To]; ok && t.To != from {
		if frame, err := protocol.Encode(t); err == nil && !target.Deliver(frame) {
			drop = append(drop, target)
		}
	}
	h.mu.Unlock()
	h.dropSlow(drop)
}

// Users returns the sorted list of online users.
func (h *Hub) Users() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.usersLocked()
}

// Count returns how many users are online.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.peers)
}

// KickAll kicks every peer with code, used on shutdown.
func (h *Hub) KickAll(code string) {
	h.mu.Lock()
	peers := make([]Peer, 0, len(h.peers))
	for _, p := range h.peers {
		peers = append(peers, p)
	}
	h.mu.Unlock()
	for _, p := range peers {
		p.Kick(code)
	}
}

func (h *Hub) usersLocked() []string {
	users := make([]string, 0, len(h.peers))
	for name := range h.peers {
		users = append(users, name)
	}
	slices.Sort(users)
	return users
}

// broadcastLocked sends m to everybody except skip and returns the peers
// whose queues were full.
func (h *Hub) broadcastLocked(m protocol.Message, skip string) []Peer {
	frame, err := protocol.Encode(m)
	if err != nil {
		return nil
	}
	var drop []Peer
	for name, p := range h.peers {
		if name == skip {
			continue
		}
		if !p.Deliver(frame) {
			drop = append(drop, p)
		}
	}
	return drop
}

// dropSlow kicks peers that could not keep up. Called without the lock.
func (h *Hub) dropSlow(peers []Peer) {
	for _, p := range peers {
		p.Kick(protocol.ErrCodeTimeout)
	}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RateLimiter is a sliding-window limiter: at most limit events per window.
// It is not safe for concurrent use on its own.
type RateLimiter struct {
	limit  int
	window time.Duration
	hits   []time.Time // ring buffer of the last limit event times
	next   int
}

// NewRateLimiter returns a limiter allowing limit events per window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: make([]time.Time, 0, limit)}
}

// Allow records an event at now and reports whether it is within the limit.
// Rejected events are not recorded.
func (r *RateLimiter) Allow(now time.Time) bool {
	if len(r.hits) < r.limit {
		r.hits = append(r.hits, now)
		return true
	}
	oldest := r.hits[r.next]
	if now.Sub(oldest) < r.window {
		return false
	}
	r.hits[r.next] = now
	r.next = (r.next + 1) % r.limit
	return true
}
