package client

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestStoreSkipsDamageAndRepairsLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	good := func(ts int) string {
		return `{"ts":` + strconv.Itoa(ts) + `,"server":"a.example","from":"x","text":"ok"}`
	}
	content := good(1) + "\n" +
		"{not json\n" +
		strings.Repeat("z", 200*1024) + "\n" + // over-long line
		good(2) + "\n" +
		`{"ts":3,"server":"a.exa` // cut off mid-write, no newline
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("existing file kept mode %o", info.Mode().Perm())
	}
	if err := s.Append(Entry{TS: 4, Server: "a.example", From: "x", Text: "after"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("a.example", 10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var ts []int64
	for _, e := range got {
		ts = append(ts, e.TS)
	}
	if !reflect.DeepEqual(ts, []int64{1, 2, 4}) {
		t.Errorf("loaded %v, want [1 2 4]", ts)
	}
}

func TestCheckTransport(t *testing.T) {
	cases := map[string]bool{ // URL → allowed without --insecure
		"wss://pling.example.com/ws": true,
		"ws://localhost:8080/ws":     true,
		"ws://127.0.0.1:8080/ws":     true,
		"ws://[::1]:8080/ws":         true,
		"ws://pling.example.com/ws":  false,
		"ws://192.168.1.5:8080/ws":   false,
	}
	for raw, ok := range cases {
		u, err := ParseServerURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		err = CheckTransport(u, false)
		if (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
		if !ok && !errors.Is(err, ErrInsecure) {
			t.Errorf("%s: error %v is not ErrInsecure", raw, err)
		}
		if err := CheckTransport(u, true); err != nil {
			t.Errorf("%s with insecure: %v", raw, err)
		}
	}
	if !IsFatal(CheckTransport(must(ParseServerURL("ws://x.example/ws")), false)) {
		t.Error("insecure transport should not be retried")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestRetryDelay(t *testing.T) {
	for failures, base := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 5: 30 * time.Second, 50: 30 * time.Second} {
		lo, hi := retryDelay(failures, 0), retryDelay(failures, 0.999999)
		if lo != base*8/10 || hi < base*119/100 || hi > base*12/10 {
			t.Errorf("failures=%d: range %v..%v around %v", failures, lo, hi, base)
		}
	}
	if retryDelay(0, 0.5) != time.Second {
		t.Errorf("failures=0: %v", retryDelay(0, 0.5))
	}
}
