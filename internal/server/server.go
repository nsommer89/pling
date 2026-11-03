// Package server is the plingd relay: an HTTP server with a WebSocket
// endpoint at /ws and a health check at /healthz. It authenticates users
// against the users file and hands authenticated connections to the hub.
// Nothing is written to disk and message contents are never logged.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsommer89/pling/internal/auth"
	"github.com/nsommer89/pling/internal/hub"
)

// Defaults for Config fields left at zero.
const (
	DefaultMaxConns         = 100
	DefaultMaxPending       = 64 // connections that have not finished logging in
	DefaultMaxPendingPerIP  = 2  // per IPv4 address or IPv6 /64
	DefaultPingInterval     = 15 * time.Second
	DefaultPongTimeout      = 30 * time.Second // ping interval + pong timeout = 45 s to detect a dead peer
	DefaultHandshakeTimeout = 5 * time.Second
	writeTimeout            = 10 * time.Second
	sendQueueSize           = 64
)

// Config configures a Server.
type Config struct {
	// Host is the public host name clients sign during login, e.g.
	// "pling.example.com". It is required unless AllowAnyHost is set.
	Host string
	// AllowAnyHost accepts an empty Host and uses each request's Host
	// header instead. Then a signature made for another server that
	// shares this users file can be relayed here, so it is for local
	// testing only.
	AllowAnyHost bool
	// MOTD is shown to users after login.
	MOTD string
	// UsersPath is the users file; it is re-read on every login attempt.
	UsersPath string
	// MaxConns caps logged-in connections.
	MaxConns int
	// MaxPending caps connections that are still logging in, in total and
	// per client IP, so idle sockets cannot crowd out real logins.
	MaxPending      int
	MaxPendingPerIP int
	// Version is reported to clients in the welcome frame.
	Version string
	// Logger receives one line per connect, disconnect and auth failure.
	Logger *slog.Logger

	PingInterval     time.Duration
	PongTimeout      time.Duration
	HandshakeTimeout time.Duration

	// Hub lets tests inject a hub with a different rate limit.
	Hub *hub.Hub
}

// Server is a pling relay.
type Server struct {
	cfg     Config
	log     *slog.Logger
	hub     *hub.Hub
	auth    *auth.Authenticator
	authed  atomic.Int64 // logged-in connections
	pending *pendingLimiter
	warns   *logLimiter

	mu      sync.Mutex
	stopped bool
	stopCh  chan struct{} // closed when the server shuts down
	wg      sync.WaitGroup
}

// New returns a server for cfg.
func New(cfg Config) *Server {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = DefaultMaxConns
	}
	if cfg.MaxPending <= 0 {
		cfg.MaxPending = DefaultMaxPending
	}
	if cfg.MaxPendingPerIP <= 0 {
		cfg.MaxPendingPerIP = DefaultMaxPendingPerIP
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = DefaultPingInterval
	}
	if cfg.PongTimeout <= 0 {
		cfg.PongTimeout = DefaultPongTimeout
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	h := cfg.Hub
	if h == nil {
		h = hub.New()
	}
	usersPath := cfg.UsersPath
	return &Server{
		cfg:     cfg,
		log:     cfg.Logger,
		hub:     h,
		auth:    &auth.Authenticator{LoadUsers: func() (auth.Users, error) { return auth.LoadUsers(usersPath) }},
		pending: newPendingLimiter(cfg.MaxPending, cfg.MaxPendingPerIP),
		warns:   newLogLimiter(time.Second),
		stopCh:  make(chan struct{}),
	}
}

// Handler returns the HTTP handler with /ws and /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// Serve accepts connections on ln until ctx is cancelled. It then closes
// every WebSocket with status 1001 (going away), waits for the connection
// goroutines to finish and returns.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.cfg.Host == "" && !s.cfg.AllowAnyHost {
		return ErrNoHost
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		s.stop()
		s.wg.Wait()
		return err
	case <-ctx.Done():
	}

	s.log.Info("shutting down", "online", s.hub.Count())
	// WebSockets are hijacked, so the http.Server does not track them: our
	// own stop signal makes each one send "shutdown" and close with 1001.
	s.stop()
	// Plain HTTP is only /healthz and rejections, so there is nothing worth
	// draining. Close instead of Shutdown, which can wait 5 s for idle
	// keep-alive or half-open connections.
	closeErr := srv.Close()
	s.wg.Wait()
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return closeErr
}

// ErrNoHost is returned by Serve when Config.Host is empty and
// AllowAnyHost is not set.
var ErrNoHost = errors.New("server: no public host name configured")

func (s *Server) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
}

// track registers a connection handler with the wait group, unless the
// server is stopping. The mutex orders every Add before stop's Wait.
func (s *Server) track() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.wg.Add(1)
	return true
}

// warn logs a warning at most once per second per key, with the number of
// lines suppressed since the last one, so floods cannot fill the disk.
func (s *Server) warn(key, msg string, args ...any) {
	ok, suppressed := s.warns.allow(key)
	if !ok {
		return
	}
	if suppressed > 0 {
		args = append(args, "suppressed", suppressed)
	}
	s.log.Warn(msg, args...)
}

// clientIP returns the client's IP address. X-Forwarded-For is trusted only
// when the TCP peer is a loopback or private address (a reverse proxy on
// the same host or Docker network); then the last entry, the one our proxy
// added, is used. Anyone else could put anything in that header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !(peer.IsLoopback() || peer.IsPrivate()) {
		return host
	}
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if xff == "" {
		return host
	}
	parts := strings.Split(xff, ",")
	if ip, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
		return ip.Unmap().String()
	}
	return host
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.log.Debug("rejected non-websocket request", "remote", ip, "method", r.Method)
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "this endpoint only speaks WebSocket", http.StatusUpgradeRequired)
		return
	}
	if !s.track() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}
	defer s.wg.Done()
	if s.authed.Load() >= int64(s.cfg.MaxConns) {
		s.warn("full", "rejected connection: server full", "remote", ip, "max", s.cfg.MaxConns)
		http.Error(w, "server full", http.StatusServiceUnavailable)
		return
	}
	release, ok := s.pending.acquire(ip)
	if !ok {
		s.warn("pending", "rejected connection: too many logins in progress", "remote", ip)
		http.Error(w, "too many logins in progress, try again", http.StatusServiceUnavailable)
		return
	}
	defer release()

	host := s.cfg.Host
	if host == "" {
		host = r.Host
	}
	c, err := accept(w, r, s, host, ip)
	if err != nil {
		s.log.Debug("websocket accept failed", "remote", ip, "err", err)
		return
	}
	c.loginDone = release
	c.run()
}
