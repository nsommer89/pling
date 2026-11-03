// Package protocol defines the wire format spoken between the pling client
// and the plingd relay. Every frame is one WebSocket text message holding a
// JSON object with a protocol version "v" and a "kind" plus kind-specific
// fields. See docs/protocol.md for the full description.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// Version is the protocol version spoken by this build. It is only bumped
// for breaking changes; new kinds and new optional fields keep the version.
const Version = 1

// Limits shared by client and server.
const (
	// MaxTextBytes is the largest allowed message text in bytes (UTF-8).
	MaxTextBytes = 4096
	// MaxFrameBytes is the WebSocket read limit. It leaves room for JSON
	// escaping on top of MaxTextBytes.
	MaxFrameBytes = 32 * 1024
	// NonceSize is the size of an auth challenge nonce in bytes.
	NonceSize = 32
)

// Kind names a frame type.
type Kind string

// Frame kinds. Kinds marked "reserved" are not implemented in v0.1 but the
// names are taken so later versions can add them without a version bump.
const (
	KindHello     Kind = "hello"     // client → server: start of login
	KindChallenge Kind = "challenge" // server → client: nonce to sign
	KindAuth      Kind = "auth"      // client → server: signature
	KindWelcome   Kind = "welcome"   // server → client: login accepted
	KindError     Kind = "error"     // server → client: something went wrong
	KindMsg       Kind = "msg"       // both: a room message or a DM
	KindTyping    Kind = "typing"    // both: "is typing" hint
	KindPresence  Kind = "presence"  // server → client: join/leave
	KindCipher    Kind = "cipher"    // reserved for v0.2 (end-to-end encryption)
	KindBlob      Kind = "blob"      // reserved for v0.3 (file transfer)
)

// Error codes sent in Error frames. Clients map them to readable text.
const (
	ErrCodeBadFrame           = "bad_frame"
	ErrCodeUnsupportedVersion = "unsupported_version"
	ErrCodeAuthFailed         = "auth_failed"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeTooLarge           = "too_large"
	ErrCodeNotOnline          = "not_online"
	ErrCodeReplaced           = "replaced"
	ErrCodeServerFull         = "server_full"
	ErrCodeShutdown           = "shutdown"
	ErrCodeTimeout            = "timeout"
)

// Presence events.
const (
	PresenceJoin  = "join"
	PresenceLeave = "leave"
)

// Message is implemented by every frame payload type.
type Message interface {
	Kind() Kind
}

// Hello opens a login. PubKey is the raw 32-byte Ed25519 public key
// (base64 in JSON).
type Hello struct {
	Name   string `json:"name"`
	PubKey []byte `json:"pubkey"`
}

// Challenge carries the nonce the client must sign.
type Challenge struct {
	Nonce []byte `json:"nonce"`
}

// Auth carries the Ed25519 signature over SignedMessage(nonce, host, name).
type Auth struct {
	Sig []byte `json:"sig"`
}

// Welcome confirms the login.
type Welcome struct {
	Name   string   `json:"name"`
	MOTD   string   `json:"motd,omitempty"`
	Users  []string `json:"users"`
	Server string   `json:"server,omitempty"` // server software version, informational
}

// Error reports a problem. Detail is optional machine-readable context
// (for not_online it is the target name).
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// Msg is a chat message. A client sends To (empty means the room), Text and
// Me. The server fills in ID, From and TS before delivering it, and echoes
// it back to the sender.
type Msg struct {
	ID   string `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Text string `json:"text"`
	Me   bool   `json:"me,omitempty"`
	TS   int64  `json:"ts,omitempty"` // unix milliseconds, server clock
}

// Typing says that From is typing, in the room (To empty) or to To.
type Typing struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Presence tells that Name joined or left.
type Presence struct {
	Event string `json:"event"`
	Name  string `json:"name"`
}

// Unknown is returned by Decode for kinds this build does not know. Callers
// should ignore it; that is what keeps new kinds backwards compatible.
type Unknown struct {
	K Kind
}

func (Hello) Kind() Kind     { return KindHello }
func (Challenge) Kind() Kind { return KindChallenge }
func (Auth) Kind() Kind      { return KindAuth }
func (Welcome) Kind() Kind   { return KindWelcome }
func (Error) Kind() Kind     { return KindError }
func (Msg) Kind() Kind       { return KindMsg }
func (Typing) Kind() Kind    { return KindTyping }
func (Presence) Kind() Kind  { return KindPresence }
func (u Unknown) Kind() Kind { return u.K }

// Decoding errors.
var (
	ErrMalformed          = errors.New("protocol: malformed frame")
	ErrUnsupportedVersion = errors.New("protocol: unsupported version")
)

type header struct {
	V    int  `json:"v"`
	Kind Kind `json:"kind"`
}

// Encode serialises m as a frame with the current version.
func Encode(m Message) ([]byte, error) {
	if _, ok := m.(Unknown); ok {
		return nil, fmt.Errorf("protocol: cannot encode unknown kind %q", m.Kind())
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	head, err := json.Marshal(header{V: Version, Kind: m.Kind()})
	if err != nil {
		return nil, err
	}
	// Splice the header fields in front of the payload fields:
	// {"v":1,"kind":"x"} + {"a":1} → {"v":1,"kind":"x","a":1}
	var buf bytes.Buffer
	buf.Grow(len(head) + len(body))
	buf.Write(head[:len(head)-1])
	if len(body) > 2 {
		buf.WriteByte(',')
		buf.Write(body[1:])
	} else {
		buf.WriteByte('}')
	}
	return buf.Bytes(), nil
}

// Decode parses a frame. Frames with a different major version return
// ErrUnsupportedVersion; unknown kinds return Unknown and no error.
func Decode(b []byte) (Message, error) {
	var h header
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if h.V != Version {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, h.V)
	}
	if h.Kind == "" {
		return nil, fmt.Errorf("%w: missing kind", ErrMalformed)
	}
	var m Message
	switch h.Kind {
	case KindHello:
		m = &Hello{}
	case KindChallenge:
		m = &Challenge{}
	case KindAuth:
		m = &Auth{}
	case KindWelcome:
		m = &Welcome{}
	case KindError:
		m = &Error{}
	case KindMsg:
		m = &Msg{}
	case KindTyping:
		m = &Typing{}
	case KindPresence:
		m = &Presence{}
	default:
		return Unknown{K: h.Kind}, nil
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return deref(m), nil
}

// deref turns the pointer used for unmarshalling back into a value so that
// callers can type-switch on plain struct types.
func deref(m Message) Message {
	switch v := m.(type) {
	case *Hello:
		return *v
	case *Challenge:
		return *v
	case *Auth:
		return *v
	case *Welcome:
		return *v
	case *Error:
		return *v
	case *Msg:
		return *v
	case *Typing:
		return *v
	case *Presence:
		return *v
	}
	return m
}

var nameRE = regexp.MustCompile(`^[a-z0-9_-]{2,16}$`)

// ValidName reports whether s is a valid user name: 2–16 characters of
// lowercase a–z, digits, '_' and '-'.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// ValidText reports whether s is acceptable message text: non-empty, valid
// UTF-8 and at most MaxTextBytes.
func ValidText(s string) error {
	switch {
	case s == "":
		return errors.New("empty text")
	case len(s) > MaxTextBytes:
		return errors.New("text too large")
	case !utf8.ValidString(s):
		return errors.New("text is not valid UTF-8")
	}
	return nil
}

// SignedMessage builds the bytes a client signs during login:
// nonce || host || name. The nonce is fixed-size and the host is fixed per
// server, so the concatenation is unambiguous.
func SignedMessage(nonce []byte, host, name string) []byte {
	out := make([]byte, 0, len(nonce)+len(host)+len(name))
	out = append(out, nonce...)
	out = append(out, host...)
	out = append(out, name...)
	return out
}
