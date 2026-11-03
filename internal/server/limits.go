package server

import (
	"net/netip"
	"sync"
	"time"
)

// addressKey groups client addresses for the per-address limit: an IPv4
// address counts on its own, an IPv6 address by its /64 prefix, because
// one home or server usually gets a whole /64 and can pick any address in
// it. Unparsable input is used as is.
func addressKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr.WithZone(""), 64).Masked().String()
}

// pendingLimiter caps connections that have not finished logging in, in
// total and per client address (see addressKey).
type pendingLimiter struct {
	max, perIP int

	mu    sync.Mutex
	total int
	byIP  map[string]int
}

func newPendingLimiter(max, perIP int) *pendingLimiter {
	return &pendingLimiter{max: max, perIP: perIP, byIP: make(map[string]int)}
}

// acquire takes a slot for ip. The returned release may be called more
// than once; only the first call frees the slot.
func (p *pendingLimiter) acquire(ip string) (release func(), ok bool) {
	ip = addressKey(ip)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= p.max || p.byIP[ip] >= p.perIP {
		return nil, false
	}
	p.total++
	p.byIP[ip]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.total--
			if p.byIP[ip]--; p.byIP[ip] <= 0 {
				delete(p.byIP, ip)
			}
		})
	}, true
}

func (p *pendingLimiter) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

// logLimiter lets one log line per key through per interval and counts
// the ones it held back.
type logLimiter struct {
	every time.Duration
	now   func() time.Time

	mu         sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}

func newLogLimiter(every time.Duration) *logLimiter {
	return &logLimiter{every: every, now: time.Now, last: map[string]time.Time{}, suppressed: map[string]int{}}
}

// allow reports whether a line for key may be logged now, and how many
// were suppressed since the last one that was.
func (l *logLimiter) allow(key string) (ok bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if last, seen := l.last[key]; seen && now.Sub(last) < l.every {
		l.suppressed[key]++
		return false, 0
	}
	l.last[key] = now
	n := l.suppressed[key]
	delete(l.suppressed, key)
	return true, n
}
