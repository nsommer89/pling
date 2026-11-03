// Command plingd is the pling relay server. It keeps nothing on disk and
// never logs message contents. Configuration is by environment variables:
//
//	PLING_LISTEN  listen address (default ":8080")
//	PLING_USERS   users file (default "./users.yaml"), re-read on every login
//	PLING_HOST    public host name clients sign, e.g. "pling.example.com"
//	PLING_MOTD    message of the day ("\n" starts a new line)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/nsommer89/pling/internal/auth"
	"github.com/nsommer89/pling/internal/server"
)

var version = "" // set by GoReleaser

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "plingd:", err)
		os.Exit(1)
	}
}

func getVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plingd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	verbose := fs.Bool("v", false, "debug logging (frame kinds, never contents)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	healthcheck := fs.Bool("healthcheck", false, "check /healthz of the local server and exit (for container health checks)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: plingd [-v] [-version] [-healthcheck]\n\n"+
			"environment:\n"+
			"  PLING_LISTEN  listen address (default :8080)\n"+
			"  PLING_USERS   users file (default ./users.yaml)\n"+
			"  PLING_HOST    public host name clients sign, e.g. pling.example.com\n"+
			"  PLING_MOTD    message of the day\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintln(stdout, "plingd", getVersion())
		return nil
	}

	listen := envOr(getenv, "PLING_LISTEN", ":8080")
	if *healthcheck {
		return checkHealth(listen)
	}
	usersPath := envOr(getenv, "PLING_USERS", "./users.yaml")
	host := getenv("PLING_HOST")
	motd := strings.ReplaceAll(getenv("PLING_MOTD"), `\n`, "\n")

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	users, err := auth.LoadUsers(usersPath)
	if err != nil {
		return fmt.Errorf("users file: %w (see deploy/users.example.yaml)", err)
	}
	if host == "" {
		log.Warn("PLING_HOST is not set; using each request's Host header as the signed host name")
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	log.Info("plingd started", "version", getVersion(), "listen", ln.Addr().String(),
		"users_file", usersPath, "users", len(users), "host", host)

	srv := server.New(server.Config{
		Host:      host,
		MOTD:      motd,
		UsersPath: usersPath,
		Version:   getVersion(),
		Logger:    log,
	})
	err = srv.Serve(ctx, ln)
	log.Info("plingd stopped")
	return err
}

func envOr(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

// checkHealth calls /healthz on the local listener. Distroless images have
// no curl, so the binary checks itself.
func checkHealth(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz answered %d", resp.StatusCode)
	}
	return nil
}
