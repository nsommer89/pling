package auth

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nsommer89/pling/internal/protocol"
)

const host = "pling.example"

type fixture struct {
	auth *Authenticator
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{pub: pub, priv: priv, now: time.Unix(1_700_000_000, 0)}
	f.auth = &Authenticator{
		LoadUsers: func() (Users, error) { return Users{"anders": pub}, nil },
		Now:       func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) sign(nonce []byte, name string) []byte {
	return ed25519.Sign(f.priv, protocol.SignedMessage(nonce, host, name))
}

func TestVerifyHappyPath(t *testing.T) {
	f := newFixture(t)
	nonce, err := f.auth.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != protocol.NonceSize {
		t.Fatalf("nonce size %d", len(nonce))
	}
	if err := f.auth.Verify(host, "anders", f.pub, nonce, f.sign(nonce, "anders")); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyWrongKey(t *testing.T) {
	f := newFixture(t)
	otherPub, otherPriv, _ := ed25519.GenerateKey(nil)
	nonce, _ := f.auth.NewChallenge()
	sig := ed25519.Sign(otherPriv, protocol.SignedMessage(nonce, host, "anders"))
	err := f.auth.Verify(host, "anders", otherPub, nonce, sig)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
	// Right public key, but signed by somebody else.
	nonce, _ = f.auth.NewChallenge()
	sig = ed25519.Sign(otherPriv, protocol.SignedMessage(nonce, host, "anders"))
	if err := f.auth.Verify(host, "anders", f.pub, nonce, sig); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyUnknownUserSameError(t *testing.T) {
	f := newFixture(t)
	nonce, _ := f.auth.NewChallenge()
	err := f.auth.Verify(host, "mallory", f.pub, nonce, f.sign(nonce, "mallory"))
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyWrongHost(t *testing.T) {
	f := newFixture(t)
	nonce, _ := f.auth.NewChallenge()
	sig := ed25519.Sign(f.priv, protocol.SignedMessage(nonce, "evil.example", "anders"))
	if err := f.auth.Verify(host, "anders", f.pub, nonce, sig); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyReplayedNonce(t *testing.T) {
	f := newFixture(t)
	nonce, _ := f.auth.NewChallenge()
	sig := f.sign(nonce, "anders")
	if err := f.auth.Verify(host, "anders", f.pub, nonce, sig); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if err := f.auth.Verify(host, "anders", f.pub, nonce, sig); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("replay: got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyExpiredNonce(t *testing.T) {
	f := newFixture(t)
	nonce, _ := f.auth.NewChallenge()
	f.now = f.now.Add(NonceTTL + time.Second)
	if err := f.auth.Verify(host, "anders", f.pub, nonce, f.sign(nonce, "anders")); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyNonceNotIssued(t *testing.T) {
	f := newFixture(t)
	nonce := make([]byte, protocol.NonceSize)
	if err := f.auth.Verify(host, "anders", f.pub, nonce, f.sign(nonce, "anders")); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("got %v, want ErrAuthFailed", err)
	}
}

func TestVerifyFailedAttemptBurnsNonce(t *testing.T) {
	f := newFixture(t)
	nonce, _ := f.auth.NewChallenge()
	_ = f.auth.Verify(host, "anders", f.pub, nonce, []byte("garbage"))
	if err := f.auth.Verify(host, "anders", f.pub, nonce, f.sign(nonce, "anders")); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("nonce was reusable after a failed attempt: %v", err)
	}
}

func TestParseUsers(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	good := "anders: " + EncodePublicKey(pub) + "\n"
	users, err := ParseUsers([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if !users["anders"].Equal(pub) {
		t.Error("key mismatch")
	}
	bad := map[string]string{
		"bad name": "Anders: " + EncodePublicKey(pub),
		"bad b64":  "anders: not-base64!!",
		"short":    "anders: AAAA",
		"not yaml": "- [",
	}
	for label, in := range bad {
		if _, err := ParseUsers([]byte(in)); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
	empty, err := ParseUsers([]byte("# nobody yet\n"))
	if err != nil || len(empty) != 0 {
		t.Errorf("empty file: %v %v", empty, err)
	}
	if enc := EncodePublicKey(pub); len(enc) != 44 || !strings.HasSuffix(enc, "=") {
		t.Errorf("unexpected encoding %q", enc)
	}
}
