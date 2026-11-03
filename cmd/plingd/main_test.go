package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nsommer89/pling/internal/auth"
	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/protocol"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// TestRunPlingd starts plingd through its real entry point (env config,
// users file) on a random port, connects two clients over WebSocket, sends
// a room message and a DM, and checks the logs never contain the text.
func TestRunPlingd(t *testing.T) {
	addr := freePort(t)
	pubA, privA, _ := ed25519.GenerateKey(nil)
	pubB, privB, _ := ed25519.GenerateKey(nil)
	users := filepath.Join(t.TempDir(), "users.yaml")
	os.WriteFile(users, []byte("anders: "+auth.EncodePublicKey(pubA)+"\nbob: "+auth.EncodePublicKey(pubB)+"\n"), 0o600)
	env := map[string]string{
		"PLING_LISTEN": addr,
		"PLING_USERS":  users,
		"PLING_HOST":   addr,
		"PLING_MOTD":   `hello\nworld`,
	}
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, func(k string) string { return env[k] }, &bytes.Buffer{}, &logs) }()

	deadline := time.Now().Add(5 * time.Second)
	for checkHealth(addr) != nil {
		if time.Now().After(deadline) {
			t.Fatal("plingd did not become healthy")
		}
		time.Sleep(20 * time.Millisecond)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	url := "ws://" + addr + "/ws"
	a, err := client.Dial(dctx, client.DialOptions{Server: url, Name: "anders", Key: privA})
	if err != nil {
		t.Fatal(err)
	}
	if a.Welcome.MOTD != "hello\nworld" {
		t.Errorf("motd %q", a.Welcome.MOTD)
	}
	b, err := client.Dial(dctx, client.DialOptions{Server: url, Name: "bob", Key: privB})
	if err != nil {
		t.Fatal(err)
	}
	readMsg := func(s *client.Session) protocol.Msg {
		for {
			m, err := s.Read(dctx)
			if err != nil {
				t.Fatal(err)
			}
			if msg, ok := m.(protocol.Msg); ok {
				return msg
			}
		}
	}
	if err := a.Send(dctx, protocol.Msg{Text: "secret room text"}); err != nil {
		t.Fatal(err)
	}
	if m := readMsg(b); m.Text != "secret room text" || m.From != "anders" {
		t.Errorf("bob got %#v", m)
	}
	if err := b.Send(dctx, protocol.Msg{To: "anders", Text: "secret dm text"}); err != nil {
		t.Fatal(err)
	}
	if m := readMsg(a); m.Text != "secret room text" { // anders' own echo first
		t.Errorf("echo %#v", m)
	}
	if m := readMsg(a); m.Text != "secret dm text" || m.From != "bob" || m.To != "anders" {
		t.Errorf("anders got %#v", m)
	}
	a.Close()
	b.Close()

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	out := logs.String()
	if strings.Contains(out, "secret") {
		t.Errorf("message content leaked into logs:\n%s", out)
	}
	for _, want := range []string{"plingd started", "connect", "name=anders", "disconnect", "plingd stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestRunFailsWithoutUsersFile(t *testing.T) {
	env := map[string]string{"PLING_USERS": filepath.Join(t.TempDir(), "missing.yaml"), "PLING_LISTEN": freePort(t), "PLING_HOST": "x.example"}
	err := run(context.Background(), nil, func(k string) string { return env[k] }, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "users file") {
		t.Errorf("got %v", err)
	}
}

func TestRunRequiresHost(t *testing.T) {
	users := filepath.Join(t.TempDir(), "users.yaml")
	os.WriteFile(users, []byte("# empty\n"), 0o600)
	env := map[string]string{"PLING_USERS": users, "PLING_LISTEN": freePort(t)}
	err := run(context.Background(), nil, func(k string) string { return env[k] }, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "PLING_HOST is not set") {
		t.Fatalf("got %v", err)
	}

	// With -insecure-any-host it starts (and stops when the context ends).
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-insecure-any-host"}, func(k string) string { return env[k] }, &bytes.Buffer{}, &logs)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for checkHealth(env["PLING_LISTEN"]) != nil {
		if time.Now().After(deadline) {
			t.Fatal("plingd did not start with -insecure-any-host")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "insecure-any-host") {
		t.Errorf("no warning logged:\n%s", logs.String())
	}
}
