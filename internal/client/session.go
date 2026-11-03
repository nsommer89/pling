package client

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/nsommer89/pling/internal/protocol"
)

// ServerError is an error frame received from the server.
type ServerError struct {
	Code   string
	Detail string
}

func (e *ServerError) Error() string {
	if e.Detail != "" {
		return "server: " + e.Code + " (" + e.Detail + ")"
	}
	return "server: " + e.Code
}

// HTTPError is a failed WebSocket handshake (e.g. 404 or 503).
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("server answered HTTP %d %s", e.Status, http.StatusText(e.Status))
}

// IsServerCode reports whether err is a ServerError with the given code.
func IsServerCode(err error, code string) bool {
	var se *ServerError
	return errors.As(err, &se) && se.Code == code
}

// Session is one authenticated connection.
type Session struct {
	ws      *websocket.Conn
	Welcome protocol.Welcome
}

// DialOptions configure Dial.
type DialOptions struct {
	Server    string // ws:// or wss:// URL
	Name      string
	Key       ed25519.PrivateKey
	UserAgent string
}

// Dial connects and logs in. The signed host is the host[:port] part of
// the server URL, which must match the server's PLING_HOST.
func Dial(ctx context.Context, o DialOptions) (*Session, error) {
	u, err := ParseServerURL(o.Server)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	if o.UserAgent != "" {
		hdr.Set("User-Agent", o.UserAgent)
	}
	ws, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, &HTTPError{Status: resp.StatusCode}
		}
		return nil, err
	}
	ws.SetReadLimit(protocol.MaxFrameBytes)
	s := &Session{ws: ws}
	if err := s.login(ctx, u.Host, o.Name, o.Key); err != nil {
		ws.CloseNow()
		return nil, err
	}
	return s, nil
}

func (s *Session) login(ctx context.Context, host, name string, key ed25519.PrivateKey) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pub := key.Public().(ed25519.PublicKey)
	if err := s.Send(ctx, protocol.Hello{Name: name, PubKey: pub}); err != nil {
		return err
	}
	m, err := s.Read(ctx)
	if err != nil {
		return err
	}
	ch, ok := m.(protocol.Challenge)
	if !ok {
		return unexpected(m)
	}
	if len(ch.Nonce) != protocol.NonceSize {
		return fmt.Errorf("server sent a %d-byte nonce", len(ch.Nonce))
	}
	sig := ed25519.Sign(key, protocol.SignedMessage(ch.Nonce, host, name))
	if err := s.Send(ctx, protocol.Auth{Sig: sig}); err != nil {
		return err
	}
	m, err = s.Read(ctx)
	if err != nil {
		return err
	}
	w, ok := m.(protocol.Welcome)
	if !ok {
		return unexpected(m)
	}
	s.Welcome = w
	return nil
}

func unexpected(m protocol.Message) error {
	if e, ok := m.(protocol.Error); ok {
		return &ServerError{Code: e.Code, Detail: e.Detail}
	}
	return fmt.Errorf("unexpected %q frame from server", m.Kind())
}

// Send writes one frame.
func (s *Session) Send(ctx context.Context, m protocol.Message) error {
	b, err := protocol.Encode(m)
	if err != nil {
		return err
	}
	return s.ws.Write(ctx, websocket.MessageText, b)
}

// Read returns the next frame. Unknown kinds are returned as
// protocol.Unknown and should be ignored by the caller.
func (s *Session) Read(ctx context.Context) (protocol.Message, error) {
	for {
		typ, b, err := s.ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ != websocket.MessageText {
			continue
		}
		return protocol.Decode(b)
	}
}

// Close closes the connection politely.
func (s *Session) Close() error {
	return s.ws.Close(websocket.StatusNormalClosure, "bye")
}

// CloseNow drops the connection without the close handshake.
func (s *Session) CloseNow() error {
	return s.ws.CloseNow()
}
