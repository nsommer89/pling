package client

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nsommer89/pling/internal/protocol"
)

// Events emitted by Client.Run.
type (
	// Connecting is sent before each connection attempt.
	Connecting struct{ Attempt int }
	// Connected is sent after a successful login.
	Connected struct{ Welcome protocol.Welcome }
	// Received carries a frame from the server.
	Received struct{ Message protocol.Message }
	// Disconnected is sent when the connection ends or an attempt fails.
	// RetryIn is zero when the client gives up (see Fatal).
	Disconnected struct {
		Err     error
		RetryIn time.Duration
		Fatal   bool
	}
)

// ErrNotConnected is returned by Send while there is no connection.
var ErrNotConnected = errors.New("not connected")

// backoff is the wait before reconnect attempt n (1-based).
var backoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// Client keeps a session alive, reconnecting with backoff, and turns
// everything into events for the UI.
type Client struct {
	opts   DialOptions
	events chan any

	mu   sync.Mutex
	sess *Session
}

// NewClient returns a client; call Run to start it.
func NewClient(o DialOptions) *Client {
	return &Client{opts: o, events: make(chan any, 64)}
}

// Events returns the event stream. It is closed when Run returns.
func (c *Client) Events() <-chan any { return c.events }

// IsFatal reports whether reconnecting cannot help: the login was refused,
// another login replaced this one, or the protocol versions differ.
func IsFatal(err error) bool {
	var he *HTTPError
	return IsServerCode(err, protocol.ErrCodeAuthFailed) ||
		IsServerCode(err, protocol.ErrCodeReplaced) ||
		IsServerCode(err, protocol.ErrCodeUnsupportedVersion) ||
		(errors.As(err, &he) && he.Status == 404)
}

// Run connects and reconnects until ctx is cancelled or a fatal error.
func (c *Client) Run(ctx context.Context) {
	defer close(c.events)
	failures := 0
	for {
		if !c.emit(ctx, Connecting{Attempt: failures + 1}) {
			return
		}
		sess, err := Dial(ctx, c.opts)
		if err == nil {
			failures = 0
			err = c.serve(ctx, sess)
		}
		failures++
		if ctx.Err() != nil {
			return
		}
		if IsFatal(err) {
			c.emit(ctx, Disconnected{Err: err, Fatal: true})
			return
		}
		wait := backoff[min(failures-1, len(backoff)-1)]
		if !c.emit(ctx, Disconnected{Err: err, RetryIn: wait}) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// serve pumps frames from one session until it ends. An error frame that
// ends the connection (replaced, shutdown, ...) is returned as the error.
func (c *Client) serve(ctx context.Context, sess *Session) error {
	c.setSession(sess)
	defer c.setSession(nil)
	stop := context.AfterFunc(ctx, func() { _ = sess.Close() })
	defer stop()

	if !c.emit(ctx, Connected{Welcome: sess.Welcome}) {
		_ = sess.Close()
		return ctx.Err()
	}
	var lastErr *ServerError
	for {
		m, err := sess.Read(context.Background()) // ended by Close via AfterFunc
		if err != nil {
			if errors.Is(err, protocol.ErrMalformed) || errors.Is(err, protocol.ErrUnsupportedVersion) {
				continue // a newer server; skip what we do not understand
			}
			_ = sess.CloseNow()
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		if e, ok := m.(protocol.Error); ok {
			switch e.Code {
			case protocol.ErrCodeReplaced, protocol.ErrCodeShutdown, protocol.ErrCodeTimeout:
				lastErr = &ServerError{Code: e.Code, Detail: e.Detail}
			}
		}
		if _, ok := m.(protocol.Unknown); ok {
			continue
		}
		if !c.emit(ctx, Received{Message: m}) {
			return ctx.Err()
		}
	}
}

func (c *Client) setSession(s *Session) {
	c.mu.Lock()
	c.sess = s
	c.mu.Unlock()
}

func (c *Client) emit(ctx context.Context, ev any) bool {
	select {
	case c.events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// Send writes a frame on the current session.
func (c *Client) Send(ctx context.Context, m protocol.Message) error {
	c.mu.Lock()
	s := c.sess
	c.mu.Unlock()
	if s == nil {
		return ErrNotConnected
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.Send(ctx, m)
}
