package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nsommer89/pling/internal/client"
)

func TestInitCreatesPrivateFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	var out bytes.Buffer
	in := strings.NewReader("Not Valid!\nanders\n\n")
	if err := run([]string{"init"}, in, &out, &out); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	dir := filepath.Join(home, ".config", "pling")
	for path, want := range map[string]os.FileMode{
		dir:                                  0o700,
		filepath.Join(dir, "config.toml"):    0o600,
		filepath.Join(dir, client.KeyFile):   0o600,
		filepath.Join(dir, "id_ed25519.pub"): 0o644,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
	text := out.String()
	if !strings.Contains(text, "That name does not work") {
		t.Error("invalid name not rejected")
	}
	if !strings.Contains(text, "Send this line to whoever runs the server") {
		t.Error("no instruction printed")
	}
	cfg, err := client.LoadConfig(filepath.Join(dir, "config.toml"))
	if err != nil || cfg.Name != "anders" || cfg.Server != client.DefaultServer {
		t.Errorf("config %#v %v", cfg, err)
	}

	// pubkey prints the same line init printed.
	var pk bytes.Buffer
	if err := run([]string{"pubkey"}, strings.NewReader(""), &pk, &pk); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(pk.String())
	if !strings.HasPrefix(line, "anders: ") || !strings.Contains(text, line) {
		t.Errorf("pubkey %q not in init output", line)
	}

	// Running init again keeps the key.
	var again bytes.Buffer
	if err := run([]string{"init"}, strings.NewReader("\nwss://other.example/ws\n"), &again, &again); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again.String(), "Using your existing key") || !strings.Contains(again.String(), line) {
		t.Errorf("second init:\n%s", again.String())
	}
}

func TestPubkeyWithoutKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	var out bytes.Buffer
	err := run([]string{"pubkey"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "pling init") {
		t.Errorf("got %v", err)
	}
}

func TestInitRefusesPlainWSToRemoteHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	var out bytes.Buffer
	in := strings.NewReader("anders\nws://chat.example.com/ws\nwss://chat.example.com/ws\n")
	if err := run([]string{"init"}, in, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "only works for localhost") {
		t.Errorf("ws:// to a remote host accepted:\n%s", out.String())
	}
	var ok bytes.Buffer
	in = strings.NewReader("anders\nws://chat.example.com/ws\n")
	if err := run([]string{"--insecure", "init"}, in, &ok, &ok); err != nil {
		t.Fatalf("--insecure init: %v\n%s", err, ok.String())
	}
}
