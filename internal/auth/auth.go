// Package auth implements the key-based login: the users file, challenge
// nonces and signature verification. There are no passwords; a user is a
// name bound to an Ed25519 public key in users.yaml.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/nsommer89/pling/internal/protocol"
)

// NonceTTL is how long a challenge nonce stays valid.
const NonceTTL = 30 * time.Second

// ErrAuthFailed is the single error returned for every failed login, so a
// caller cannot tell an unknown name from a wrong key.
var ErrAuthFailed = errors.New("auth: authentication failed")

// Users maps a user name to its public key.
type Users map[string]ed25519.PublicKey

// LoadUsers reads a users file of the form
//
//	anders: <base64 ed25519 public key>
//	nikolaj: <base64 ed25519 public key>
//
// Invalid names or keys are reported as errors so a typo is noticed.
func LoadUsers(path string) (Users, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseUsers(b)
}

// ParseUsers parses the contents of a users file.
func ParseUsers(b []byte) (Users, error) {
	var raw map[string]string
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("users file: %w", err)
	}
	users := make(Users, len(raw))
	for name, enc := range raw {
		if !protocol.ValidName(name) {
			return nil, fmt.Errorf("users file: invalid name %q (use 2-16 of a-z, 0-9, _ and -)", name)
		}
		key, err := ParsePublicKey(enc)
		if err != nil {
			return nil, fmt.Errorf("users file: %s: %w", name, err)
		}
		users[name] = key
	}
	return users, nil
}

// ParsePublicKey decodes a base64 (standard encoding) Ed25519 public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("public key is not valid base64: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// EncodePublicKey returns the base64 form used in users.yaml.
func EncodePublicKey(k ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(k)
}

// Authenticator issues challenges and verifies answers. It is safe for
// concurrent use.
type Authenticator struct {
	// LoadUsers returns the current users. It is called on every login
	// attempt, so edits to the users file apply without a restart.
	LoadUsers func() (Users, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time // nonce → expiry
}

func (a *Authenticator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// NewChallenge returns a fresh random nonce, valid for NonceTTL and usable
// exactly once.
func (a *Authenticator) NewChallenge() ([]byte, error) {
	nonce := make([]byte, protocol.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.nonces == nil {
		a.nonces = make(map[string]time.Time)
	}
	for k, exp := range a.nonces { // drop expired ones; the map stays tiny
		if now.After(exp) {
			delete(a.nonces, k)
		}
	}
	a.nonces[string(nonce)] = now.Add(NonceTTL)
	return nonce, nil
}

// consume removes the nonce and reports whether it was issued and is still
// valid.
func (a *Authenticator) consume(nonce []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.nonces[string(nonce)]
	if !ok {
		return false
	}
	delete(a.nonces, string(nonce))
	return !a.now().After(exp)
}

// Verify checks a login: the nonce must be one we issued, unexpired and
// unused; the name must exist with exactly this public key; and sig must be
// a valid signature over protocol.SignedMessage(nonce, host, name).
// Every failure returns ErrAuthFailed (wrapped with a reason for logs only).
func (a *Authenticator) Verify(host, name string, pubkey, nonce, sig []byte) error {
	if !a.consume(nonce) {
		return fmt.Errorf("%w: unknown, used or expired nonce", ErrAuthFailed)
	}
	users, err := a.LoadUsers()
	if err != nil {
		return fmt.Errorf("%w: cannot load users: %v", ErrAuthFailed, err)
	}
	want, ok := users[name]
	if !ok {
		return fmt.Errorf("%w: unknown user", ErrAuthFailed)
	}
	if len(pubkey) != ed25519.PublicKeySize || subtle.ConstantTimeCompare(want, pubkey) != 1 {
		return fmt.Errorf("%w: key does not match", ErrAuthFailed)
	}
	if !ed25519.Verify(want, protocol.SignedMessage(nonce, host, name), sig) {
		return fmt.Errorf("%w: bad signature", ErrAuthFailed)
	}
	return nil
}
