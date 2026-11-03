// Package client holds everything the pling client needs apart from the
// TUI: config file, key pair, local history and the WebSocket session.
package client

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultServer is the server `pling init` suggests.
const DefaultServer = "wss://pling.viaxyz.net/ws"

// Notification modes.
const (
	NotifyBell    = "bell"
	NotifyDesktop = "desktop"
	NotifyBoth    = "both"
	NotifyOff     = "off"
)

// Config is ~/.config/pling/config.toml.
type Config struct {
	Server string `toml:"server"`
	Name   string `toml:"name"`
	Notify string `toml:"notify"`
	Fun    bool   `toml:"fun"`
}

// DefaultConfig returns the values used for keys missing from the file.
func DefaultConfig() Config {
	return Config{Server: DefaultServer, Notify: NotifyBell, Fun: true}
}

// ErrNoConfig is returned by LoadConfig when the file does not exist.
var ErrNoConfig = errors.New("no config file")

// ConfigDir returns $XDG_CONFIG_HOME/pling, or ~/.config/pling. The same
// location is used on every OS so the docs stay simple.
func ConfigDir() (string, error) {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// DataDir returns $XDG_DATA_HOME/pling, or ~/.local/share/pling.
func DataDir() (string, error) {
	return xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

func xdgDir(env, fallback string) (string, error) {
	if base := os.Getenv(env); base != "" && filepath.IsAbs(base) {
		return filepath.Join(base, "pling"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, "pling"), nil
}

// ConfigPath returns the path of config.toml.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// LoadConfig reads the config file. Missing keys get default values.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, ErrNoConfig
	}
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks the values.
func (c Config) Validate() error {
	switch c.Notify {
	case NotifyBell, NotifyDesktop, NotifyBoth, NotifyOff:
	default:
		return fmt.Errorf("notify must be \"bell\", \"desktop\", \"both\" or \"off\", not %q", c.Notify)
	}
	if _, err := ParseServerURL(c.Server); err != nil {
		return err
	}
	return nil
}

// SaveConfig writes cfg with comments, creating the directory (0700) and
// the file (0600).
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var body bytes.Buffer
	if err := toml.NewEncoder(&body).Encode(cfg); err != nil {
		return err
	}
	content := "# pling client config. See README.md for all options.\n" +
		"# server: WebSocket URL of the relay (wss://host/ws)\n" +
		"# name:   your user name, must match the server's users file\n" +
		"# notify: \"bell\", \"desktop\" (notify-send), \"both\" or \"off\"\n" +
		"# fun:    confetti when someone joins (true/false)\n\n" + body.String()
	return writeFileAtomic(path, []byte(content), 0o600)
}

// ErrInsecure is returned for a ws:// URL to a host that is not the local
// machine, unless insecure use was asked for.
var ErrInsecure = errors.New("unencrypted ws:// is only allowed to localhost; use wss:// or pass --insecure")

// CheckTransport refuses plain ws:// to anything but a loopback host
// unless insecure is set: without TLS anyone on the way could read the
// chat and relay your login.
func CheckTransport(u *url.URL, insecure bool) error {
	if u.Scheme != "ws" || insecure {
		return nil
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s: %w", u.Host, ErrInsecure)
}

// ParseServerURL checks that s is a ws:// or wss:// URL with a host.
func ParseServerURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("server URL %q: %w", s, err)
	}
	if (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return nil, fmt.Errorf("server URL %q must look like wss://host/ws", s)
	}
	return u, nil
}

// writeFileAtomic writes to a temp file in the same directory and renames
// it into place, so a crash never leaves a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
