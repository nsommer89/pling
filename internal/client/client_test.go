package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "history.jsonl")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	want := []Entry{
		{TS: 1, Server: "a.example", From: "anders", Text: "hej"},
		{TS: 2, Server: "a.example", From: "bob", To: "anders", Text: "psst", Me: true},
		{TS: 3, Server: "a.example", From: "anders", Text: "æøå ¯\\_(ツ)_/¯"},
	}
	for _, e := range want {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load("a.example", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("history mode %o, want 600", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("data dir mode %o, want 700", dirInfo.Mode().Perm())
	}
}

func TestStoreLoadKeepsLastNAndFiltersServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		server := "a.example"
		if i%2 == 0 {
			server = "b.example"
		}
		if err := s.Append(Entry{TS: int64(i), Server: server, From: "x", Text: strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// A damaged line must not break loading.
	if _, err := s.f.WriteString("{broken\n"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = OpenStore(path) // reopen: entries survive
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Load("a.example", 3)
	if err != nil {
		t.Fatal(err)
	}
	var ts []int64
	for _, e := range got {
		ts = append(ts, e.TS)
	}
	if !reflect.DeepEqual(ts, []int64{15, 17, 19}) {
		t.Errorf("got %v", ts)
	}
	all, _ := s.Load("", 100)
	if len(all) != 20 {
		t.Errorf("all servers: %d entries", len(all))
	}
	none, _ := s.Load("", 0)
	if len(none) != 0 {
		t.Errorf("n=0 returned %d", len(none))
	}
}

func TestGenerateAndLoadKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pling")
	priv, err := GenerateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	privPath, pubPath := KeyPaths(dir)
	info, _ := os.Stat(privPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode %o, want 600", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(dir)
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode %o, want 700", dirInfo.Mode().Perm())
	}
	loaded, err := LoadKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Equal(priv) {
		t.Error("loaded key differs")
	}
	pub, _ := os.ReadFile(pubPath)
	line := PublicKeyLine("anders", priv)
	if line != "anders: "+strings.TrimSpace(string(pub)) {
		t.Errorf("pubkey line %q vs file %q", line, pub)
	}
	if _, err := GenerateKey(dir); !errors.Is(err, ErrKeyExists) {
		t.Errorf("second GenerateKey: %v", err)
	}
	if err := os.Chmod(privPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(dir); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("world-readable key accepted: %v", err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "config.toml")
	if _, err := LoadConfig(path); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("missing file: %v", err)
	}
	cfg := Config{Server: "wss://chat.example/ws", Name: "anders", Notify: NotifyBoth, Fun: false}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Errorf("got %#v", got)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode %o", info.Mode().Perm())
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`name = "anders"`+"\n"), 0o600)
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultConfig()
	want.Name = "anders"
	if got != want {
		t.Errorf("got %#v want %#v", got, want)
	}
	os.WriteFile(path, []byte(`notify = "loud"`+"\n"), 0o600)
	if _, err := LoadConfig(path); err == nil {
		t.Error("bad notify accepted")
	}
	os.WriteFile(path, []byte(`server = "https://x/ws"`+"\n"), 0o600)
	if _, err := LoadConfig(path); err == nil {
		t.Error("https server URL accepted")
	}
}

func TestXDGDirs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/cfg")
	t.Setenv("XDG_DATA_HOME", "relative/ignored")
	t.Setenv("HOME", "/home/test")
	if d, _ := ConfigDir(); d != "/tmp/cfg/pling" {
		t.Errorf("config dir %s", d)
	}
	if d, _ := DataDir(); d != "/home/test/.local/share/pling" {
		t.Errorf("data dir %s", d)
	}
}
