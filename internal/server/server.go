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
	DefaultPingInterval     = 15 * time.Second
	DefaultPongTimeout      = 30 * time.Second // ping interval + pong timeout = 45 s to detect a dead peer
	DefaultHandshakeTimeout = 10 * time.Second
	writeTimeout            = 10 * time.Second
	sendQueueSize           = 64
)

// Config configures a Server.
type Config struct {
	// Host is the public host name clients sign during login, e.g.
	// "pling.viaxyz.net". Empty means the Host header of each request.
	Host string
	// MOTD is shown to users after login.
	MOTD string
	// UsersPath is the users file; it is re-read on every login attempt.
	UsersPath string
	// MaxConns caps concurrent WebSocket connections.
	MaxConns int
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
	cfg   Config
	log   *slog.Logger
	hub   *hub.Hub
	auth  *auth.Authenticator
	conns atomic.Int64

	stopOnce sync.Once
	stopCh   chan struct{} // closed when the server shuts down
	wg       sync.WaitGroup
}

// New returns a server for cfg.
func New(cfg Config) *Server {
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = DefaultMaxConns
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
		cfg:    cfg,
		log:    cfg.Logger,
		hub:    h,
		auth:   &auth.Authenticator{LoadUsers: func() (auth.Users, error) { return auth.LoadUsers(usersPath) }},
		stopCh: make(chan struct{}),
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
	s.stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdownCtx) // hijacked WebSockets are not tracked by Shutdown,
	s.wg.Wait()                      // so wait for our own connection goroutines too.
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return err
}

func (s *Server) stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// remoteAddr returns the client address for logs, preferring the address a
// reverse proxy put in X-Forwarded-For.
func remoteAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	return r.RemoteAddr
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.log.Warn("rejected non-websocket request", "remote", remoteAddr(r), "method", r.Method)
		w.Header().Set("Upgrade", "websocket")
		http.Error(w, "this endpoint only speaks WebSocket", http.StatusUpgradeRequired)
		return
	}
	select {
	case <-s.stopCh:
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	default:
	}
	if n := s.conns.Add(1); n > int64(s.cfg.MaxConns) {
		s.conns.Add(-1)
		s.log.Warn("rejected connection: server full", "remote", remoteAddr(r), "max", s.cfg.MaxConns)
		http.Error(w, "server full", http.StatusServiceUnavailable)
		return
	}
	defer s.conns.Add(-1)
	s.wg.Add(1)
	defer s.wg.Done()

	host := s.cfg.Host
	if host == "" {
		host = r.Host
	}
	c, err := accept(w, r, s, host)
	if err != nil {
		s.log.Debug("websocket accept failed", "remote", remoteAddr(r), "err", err)
		return
	}
	c.run()
}
