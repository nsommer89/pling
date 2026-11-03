// Command pling is the terminal chat client.
//
//	pling            connect (runs `pling init` first if there is no config)
//	pling init       choose a name and server, create your key pair
//	pling pubkey     print the line to send to the server admin
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nsommer89/pling/internal/client"
	"github.com/nsommer89/pling/internal/protocol"
	"github.com/nsommer89/pling/internal/ui"
)

var version = "" // set by GoReleaser

func getVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "dev"
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pling:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pling", flag.ContinueOnError)
	fs.SetOutput(stderr)
	serverFlag := fs.String("server", "", "server URL, e.g. wss://pling.example.com/ws (overrides the config file)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage:\n"+
			"  pling [--server URL]   connect to the chat\n"+
			"  pling init             choose a name and server, create your key pair\n"+
			"  pling pubkey           print the line to send to the server admin\n\n"+
			"flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Fprintln(stdout, "pling", getVersion())
		return nil
	}
	cfgPath, err := client.ConfigPath()
	if err != nil {
		return err
	}
	in := bufio.NewReader(stdin)

	switch fs.Arg(0) {
	case "init":
		return runInit(cfgPath, *serverFlag, in, stdout)
	case "pubkey":
		return runPubkey(cfgPath, stdout)
	case "":
	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", fs.Arg(0))
	}

	cfg, err := client.LoadConfig(cfgPath)
	if errors.Is(err, client.ErrNoConfig) {
		fmt.Fprintln(stdout, "No config yet, let's set you up first.")
		fmt.Fprintln(stdout)
		if err := runInit(cfgPath, *serverFlag, in, stdout); err != nil {
			return err
		}
		fmt.Fprint(stdout, "\nPress Enter to connect (you will be let in once the admin has added your key)… ")
		_, _ = in.ReadString('\n')
		cfg, err = client.LoadConfig(cfgPath)
	}
	if err != nil {
		return err
	}
	if *serverFlag != "" {
		cfg.Server = *serverFlag
	}
	return chat(cfg)
}

func chat(cfg client.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !protocol.ValidName(cfg.Name) {
		return fmt.Errorf("config has no valid name; run `pling init`")
	}
	u, err := client.ParseServerURL(cfg.Server)
	if err != nil {
		return err
	}
	dir, err := client.ConfigDir()
	if err != nil {
		return err
	}
	key, err := client.LoadKey(dir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no key found in %s; run `pling init`", dir)
	}
	if err != nil {
		return err
	}

	var store *client.Store
	var history []client.Entry
	var historyErr error
	if dataDir, err := client.DataDir(); err == nil {
		store, historyErr = client.OpenStore(filepath.Join(dataDir, client.HistoryFile))
		if historyErr == nil {
			defer store.Close()
			history, historyErr = store.Load(u.Host, client.HistoryLoad)
		}
	} else {
		historyErr = err
	}

	c := client.NewClient(client.DialOptions{
		Server:    cfg.Server,
		Name:      cfg.Name,
		Key:       key,
		UserAgent: "pling/" + getVersion(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	model := ui.New(ui.Options{
		Conn:       c,
		Store:      store,
		Config:     cfg,
		Host:       u.Host,
		History:    history,
		HistoryErr: historyErr,
	})
	_, err = tea.NewProgram(model).Run()

	cancel() // closes the connection politely
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}

func prompt(in *bufio.Reader, out io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", errors.New("input ended")
		}
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

func runInit(cfgPath, serverFlag string, in *bufio.Reader, out io.Writer) error {
	cfg, err := client.LoadConfig(cfgPath)
	if err != nil && !errors.Is(err, client.ErrNoConfig) {
		fmt.Fprintf(out, "Ignoring the old config (%v).\n", err)
		cfg = client.DefaultConfig()
	}
	if serverFlag != "" {
		cfg.Server = serverFlag
	}

	fmt.Fprintln(out, "pling setup")
	fmt.Fprintln(out)
	for {
		name, err := prompt(in, out, "Your name (2-16 of a-z, 0-9, _ and -)", cfg.Name)
		if err != nil {
			return err
		}
		name = strings.ToLower(name)
		if protocol.ValidName(name) {
			cfg.Name = name
			break
		}
		fmt.Fprintln(out, "  That name does not work. Use 2-16 characters: a-z, 0-9, _ or -.")
	}
	for {
		server, err := prompt(in, out, "Server", cfg.Server)
		if err != nil {
			return err
		}
		if _, err := client.ParseServerURL(server); err == nil {
			cfg.Server = server
			break
		}
		fmt.Fprintln(out, "  That does not look like a server address. Example: wss://pling.example.com/ws")
	}
	if err := client.SaveConfig(cfgPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nSaved %s\n", cfgPath)

	dir := filepath.Dir(cfgPath)
	key, err := client.GenerateKey(dir)
	privPath, _ := client.KeyPaths(dir)
	switch {
	case errors.Is(err, client.ErrKeyExists):
		key, err = client.LoadKey(dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Using your existing key %s\n", privPath)
	case err != nil:
		return err
	default:
		fmt.Fprintf(out, "Created your key %s (keep it private, it is your login)\n", privPath)
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Your public key line:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  "+client.PublicKeyLine(cfg.Name, key))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Send this line to whoever runs the server. Once they have added it, run: pling")
	return nil
}

func runPubkey(cfgPath string, out io.Writer) error {
	cfg, err := client.LoadConfig(cfgPath)
	if err != nil && !errors.Is(err, client.ErrNoConfig) {
		return err
	}
	name := cfg.Name
	if name == "" {
		name = "your-name"
	}
	key, err := client.LoadKey(filepath.Dir(cfgPath))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("no key yet; run `pling init` first")
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, client.PublicKeyLine(name, key))
	return nil
}
