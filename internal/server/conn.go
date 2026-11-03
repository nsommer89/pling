package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/nsommer89/pling/internal/auth"
	"github.com/nsommer89/pling/internal/hub"
	"github.com/nsommer89/pling/internal/protocol"
)

// conn is one WebSocket connection. Goroutines per connection: the reader
// (the HTTP handler goroutine), a writer and a pinger. All of them end when
// the connection stops.
type conn struct {
	s      *Server
	ws     *websocket.Conn
	host   string
	remote string
	name   string
	since  time.Time

	// loginDone frees the pending-login slot; called when login ends.
	loginDone func()

	out chan []byte

	stopOnce sync.Once
	stopped  chan struct{}
	mu       sync.Mutex
	reason   string // error code sent to the client before closing, if any
}

func accept(w http.ResponseWriter, r *http.Request, s *Server, host, ip string) (*conn, error) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// No browsers are involved, and the origin check would only get in
		// the way of non-browser clients that send an Origin header.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(protocol.MaxFrameBytes)
	return &conn{
		s:       s,
		ws:      ws,
		host:    host,
		remote:  ip,
		since:   time.Now(),
		out:     make(chan []byte, sendQueueSize),
		stopped: make(chan struct{}),
	}, nil
}

// Name implements hub.Peer.
func (c *conn) Name() string { return c.name }

// Deliver implements hub.Peer.
func (c *conn) Deliver(frame []byte) bool {
	select {
	case <-c.stopped:
		return true // going away anyway; not a slow consumer
	default:
	}
	select {
	case c.out <- frame:
		return true
	default:
		return false
	}
}

// Kick implements hub.Peer.
func (c *conn) Kick(code string) { c.stop(code) }

func (c *conn) stop(code string) {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.reason = code
		c.mu.Unlock()
		close(c.stopped)
	})
}

func (c *conn) stopReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

// run performs the login and then relays frames until the connection ends.
func (c *conn) run() {
	defer c.ws.CloseNow()

	// Shut down with the server.
	go func() {
		select {
		case <-c.s.stopCh:
			c.stop(protocol.ErrCodeShutdown)
		case <-c.stopped:
		}
	}()

	err := c.login()
	if c.loginDone != nil {
		c.loginDone()
	}
	if err != nil {
		c.stop("")
		return
	}
	if c.s.authed.Add(1) > int64(c.s.cfg.MaxConns) {
		c.s.authed.Add(-1)
		c.s.warn("full", "rejected login: server full", "name", c.name, "remote", c.remote, "max", c.s.cfg.MaxConns)
		c.fail(protocol.ErrCodeServerFull, websocket.StatusTryAgainLater)
		c.stop("")
		return
	}
	defer c.s.authed.Add(-1)

	users := c.s.hub.Join(c)
	c.s.log.Info("connect", "name", c.name, "remote", c.remote, "online", len(users))
	welcome, _ := protocol.Encode(protocol.Welcome{
		Name: c.name, MOTD: c.s.cfg.MOTD, Users: users, Server: c.s.cfg.Version,
	})
	if err := c.write(welcome); err != nil {
		c.stop("")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.writeLoop() }()
	go func() { defer wg.Done(); c.pingLoop() }()

	err = c.readLoop()
	c.stop("")
	c.s.hub.Leave(c)
	wg.Wait()

	reason := c.stopReason()
	if reason == "" && err != nil {
		reason = closeReason(err)
	}
	c.s.log.Info("disconnect", "name", c.name, "remote", c.remote,
		"duration", time.Since(c.since).Round(time.Second), "reason", reason)
}

// login runs hello → challenge → auth → (welcome is sent by run).
func (c *conn) login() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.s.cfg.HandshakeTimeout)
	defer cancel()
	go func() { // abort the handshake on shutdown
		select {
		case <-c.stopped:
			cancel()
		case <-ctx.Done():
		}
	}()

	m, err := c.readMessage(ctx)
	if err != nil {
		switch {
		case errors.Is(err, protocol.ErrUnsupportedVersion):
			c.fail(protocol.ErrCodeUnsupportedVersion, websocket.StatusPolicyViolation)
		case errors.Is(err, protocol.ErrMalformed):
			c.fail(protocol.ErrCodeBadFrame, websocket.StatusPolicyViolation)
		}
		c.s.log.Debug("handshake: no hello", "remote", c.remote)
		return err
	}
	hello, ok := m.(protocol.Hello)
	if !ok {
		c.fail(protocol.ErrCodeBadFrame, websocket.StatusPolicyViolation)
		return fmt.Errorf("expected hello, got %s", m.Kind())
	}

	nonce, err := c.s.auth.NewChallenge()
	if err != nil {
		return err
	}
	defer c.s.auth.Forget(nonce) // no-op once Verify has consumed it
	frame, _ := protocol.Encode(protocol.Challenge{Nonce: nonce})
	if err := c.ws.Write(ctx, websocket.MessageText, frame); err != nil {
		return err
	}

	authCtx, authCancel := context.WithTimeout(ctx, auth.NonceTTL)
	defer authCancel()
	m, err = c.readMessage(authCtx)
	if err != nil {
		if errors.Is(err, protocol.ErrMalformed) || errors.Is(err, protocol.ErrUnsupportedVersion) {
			c.fail(protocol.ErrCodeBadFrame, websocket.StatusPolicyViolation)
		}
		return err
	}
	answer, ok := m.(protocol.Auth)
	if !ok {
		c.fail(protocol.ErrCodeBadFrame, websocket.StatusPolicyViolation)
		return fmt.Errorf("expected auth, got %s", m.Kind())
	}
	if err := c.s.auth.Verify(c.host, hello.Name, hello.PubKey, nonce, answer.Sig); err != nil {
		c.s.warn("auth", "auth failed", "name", logName(hello.Name), "remote", c.remote, "err", err)
		c.fail(protocol.ErrCodeAuthFailed, websocket.StatusPolicyViolation)
		return err
	}
	c.name = hello.Name
	return nil
}

// logName keeps garbage out of the log when someone sends a nonsense name.
func logName(name string) string {
	if protocol.ValidName(name) {
		return name
	}
	return "(invalid)"
}

func (c *conn) readMessage(ctx context.Context) (protocol.Message, error) {
	typ, b, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, fmt.Errorf("%w: binary frame", protocol.ErrMalformed)
	}
	// encoding/json would quietly turn invalid UTF-8 into U+FFFD.
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("%w: invalid UTF-8", protocol.ErrMalformed)
	}
	return protocol.Decode(b)
}

// fail sends an error frame and closes the connection.
func (c *conn) fail(code string, status websocket.StatusCode) {
	frame, _ := protocol.Encode(protocol.Error{Code: code})
	_ = c.write(frame)
	_ = c.ws.Close(status, code)
}

func (c *conn) write(frame []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, frame)
}

func (c *conn) sendError(code, detail string) {
	frame, _ := protocol.Encode(protocol.Error{Code: code, Detail: detail})
	if !c.Deliver(frame) {
		c.stop(protocol.ErrCodeTimeout)
	}
}

func (c *conn) readLoop() error {
	for {
		m, err := c.readMessage(context.Background())
		if err != nil {
			switch {
			case errors.Is(err, protocol.ErrUnsupportedVersion):
				c.s.log.Debug("bad frame version", "name", c.name)
				c.sendError(protocol.ErrCodeUnsupportedVersion, "")
				continue
			case errors.Is(err, protocol.ErrMalformed):
				// No error details: JSON errors can quote bits of the text.
				c.s.log.Debug("bad frame", "name", c.name)
				c.sendError(protocol.ErrCodeBadFrame, "")
				continue
			}
			return err
		}
		switch m := m.(type) {
		case protocol.Msg:
			c.handleMsg(m)
		case protocol.Typing:
			if m.To == "" || protocol.ValidName(m.To) {
				c.s.hub.Typing(c.name, m)
			}
		default:
			c.s.log.Debug("ignored frame", "name", c.name, "kind", m.Kind())
		}
	}
}

func (c *conn) handleMsg(m protocol.Msg) {
	if len(m.Text) > protocol.MaxTextBytes {
		c.sendError(protocol.ErrCodeTooLarge, "")
		return
	}
	if err := protocol.ValidText(m.Text); err != nil {
		c.sendError(protocol.ErrCodeBadFrame, "")
		return
	}
	if m.To != "" && !protocol.ValidName(m.To) {
		c.sendError(protocol.ErrCodeNotOnline, "")
		return
	}
	switch err := c.s.hub.Route(c.name, m); {
	case errors.Is(err, hub.ErrRateLimited):
		c.s.log.Debug("rate limited", "name", c.name)
		c.sendError(protocol.ErrCodeRateLimited, "")
	case errors.Is(err, hub.ErrNotOnline):
		c.sendError(protocol.ErrCodeNotOnline, m.To)
	case err != nil:
		c.s.log.Error("route failed", "name", c.name, "err", err)
	}
}

// writeLoop sends queued frames. When the connection stops it sends the
// stop reason (if any) and closes the WebSocket, which also ends readLoop.
func (c *conn) writeLoop() {
	for {
		select {
		case frame := <-c.out:
			if err := c.write(frame); err != nil {
				c.stop("")
				c.ws.CloseNow() // also unblocks readLoop
				return
			}
		case <-c.stopped:
			reason := c.stopReason()
			status := websocket.StatusNormalClosure
			if reason != "" {
				frame, _ := protocol.Encode(protocol.Error{Code: reason})
				_ = c.write(frame)
			}
			if reason == protocol.ErrCodeShutdown {
				status = websocket.StatusGoingAway
			}
			_ = c.ws.Close(status, reason)
			return
		}
	}
}

func (c *conn) pingLoop() {
	t := time.NewTicker(c.s.cfg.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stopped:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), c.s.cfg.PongTimeout)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				c.s.log.Debug("ping failed", "name", c.name, "err", err)
				c.stop(protocol.ErrCodeTimeout)
				return
			}
		}
	}
}

func closeReason(err error) string {
	if s := websocket.CloseStatus(err); s != -1 {
		return s.String()
	}
	return "connection lost"
}
