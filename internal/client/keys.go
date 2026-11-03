package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nsommer89/pling/internal/auth"
)

// KeyFile is the private key's file name inside the config directory; the
// public key sits next to it with a ".pub" suffix.
const KeyFile = "id_ed25519"

// ErrKeyExists is returned by GenerateKey when a key is already there.
var ErrKeyExists = errors.New("a key already exists")

// KeyPaths returns the private and public key paths in dir.
func KeyPaths(dir string) (private, public string) {
	private = filepath.Join(dir, KeyFile)
	return private, private + ".pub"
}

// GenerateKey creates a new Ed25519 key pair in dir. The private key is a
// PKCS#8 PEM file with mode 0600; the public key file holds the base64
// key on one line. It never overwrites an existing key.
func GenerateKey(dir string) (ed25519.PrivateKey, error) {
	privPath, pubPath := KeyPaths(dir)
	if _, err := os.Stat(privPath); err == nil {
		return nil, ErrKeyExists
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { // the directory is ours alone
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	f, err := os.OpenFile(privPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(block); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pubPath, []byte(auth.EncodePublicKey(pub)+"\n"), 0o644); err != nil {
		return nil, err
	}
	return priv, nil
}

// LoadKey reads the private key from dir. Like ssh, it refuses a key file
// that other users can read.
func LoadKey(dir string) (ed25519.PrivateKey, error) {
	privPath, _ := KeyPaths(dir)
	info, err := os.Stat(privPath)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by other users (mode %o); fix it with: chmod 600 %s",
			privPath, info.Mode().Perm(), privPath)
	}
	b, err := os.ReadFile(privPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PEM private key", privPath)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", privPath, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", privPath)
	}
	return priv, nil
}

// PublicKeyLine returns the line to add to the server's users file.
func PublicKeyLine(name string, priv ed25519.PrivateKey) string {
	pub := priv.Public().(ed25519.PublicKey)
	return strings.TrimSpace(name) + ": " + auth.EncodePublicKey(pub)
}
