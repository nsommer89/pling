# pling

A small chat for the terminal, with a retro BBS/IRC feel. You get one shared room, private messages, an online list, a "typing…" indicator, notifications, dice, and a little confetti when somebody joins.

```
 ┌─┐┬  ┬┌┐┌┌─┐
 ├─┘│  │││││ ┬   a tiny terminal chat · /help for help
 ┴  ┴─┘┴┘└┘└─┘   connected to pling.viaxyz.net as anders
──────────────────────────────────────────────────────────────────────────────────
── Tuesday 3 November 2026 ────────────                          online (2)
15:58 -!- nikolaj joined                                         anders
15:58 <anders> hey, finally a chat that doesn't need a browser   nikolaj…
15:58 ← nikolaj: psst, did you see the roadmap?
15:58 * nikolaj rolls 2d6: 5 + 4 = 9
15:58 <nikolaj> works on my machine ¯\_(ツ)_/¯
 ● pling.viaxyz.net · anders · 2 online   nikolaj is typing…
anders›
```

- **No passwords.** On first run you get an Ed25519 key pair, much like an SSH key. You send your public key to whoever runs the server, they add one line to a file, and you're in.
- **The server stores nothing.** `plingd` only relays messages between the people who are online. It keeps no history, writes nothing to disk and never logs message text. Your history lives only on your own machine.
- **Easy to run yourself.** It's one binary, or a tiny Docker image, plus one users file.

---

## Setup

You need a 64-bit Linux, or a Mac with Apple silicon, and a terminal that understands UTF-8 (any modern one does).

### 1. Install pling

Pick the line for your machine. It puts `pling` in `~/.local/bin`.

```sh
# Linux, x86-64 (most PCs and laptops)
mkdir -p ~/.local/bin && curl -fsSL https://github.com/nsommer89/pling/releases/latest/download/pling_linux_amd64.tar.gz | tar -xz -C ~/.local/bin pling

# Linux, ARM (Raspberry Pi 4/5 with a 64-bit OS, ARM laptops)
mkdir -p ~/.local/bin && curl -fsSL https://github.com/nsommer89/pling/releases/latest/download/pling_linux_arm64.tar.gz | tar -xz -C ~/.local/bin pling

# macOS, Apple silicon
mkdir -p ~/.local/bin && curl -fsSL https://github.com/nsommer89/pling/releases/latest/download/pling_darwin_arm64.tar.gz | tar -xz -C ~/.local/bin pling
```

Check that it works with `pling --version`. If your shell says `command not found`, add `~/.local/bin` to your PATH (for example, put `export PATH="$HOME/.local/bin:$PATH"` in `~/.bashrc` or `~/.zshrc` and open a new terminal).

If you have Go installed, you can build it yourself instead:

```sh
go install github.com/nsommer89/pling/cmd/pling@latest
```

### 2. Create your name and key

```sh
pling init
```

pling asks two questions:

1. **Your name.** Use 2–16 characters: `a-z`, `0-9`, `_` or `-`. This is the name others see.
2. **The server.** Press Enter to accept `wss://pling.viaxyz.net/ws`, or type the address you were given.

pling then creates your key pair in `~/.config/pling/` (the private key `id_ed25519` is readable only by you) and prints a line like this. Yours has your name and 44 characters of random-looking key; the `A`s here are just a placeholder:

```
anders: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
```

### 3. Send your public key line to the admin

Send that whole line to whoever runs the server, by mail, chat or whatever you like. The public key isn't secret, so it's safe to send. **Never send the file `id_ed25519` itself.** That's your private key.

Lost the line? `pling pubkey` prints it again.

Once the admin has added the line, you can connect. They don't need to restart anything.

---

## Connecting to the chat

### First login

```sh
pling
```

That's it. If you haven't run `pling init` yet, `pling` starts it for you first.

### What you see

- **Top:** the banner, and whether you're connected.
- **Middle:** the messages. Each line has the time, the name in its own colour (the same colour for everyone, everywhere) and the text. Long messages wrap.
  - `<anders> hi`: a message in the room
  - `← anders: hi`: a private message to you
  - `→ anders: hi`: a private message you sent
  - `* anders waves`: an action (`/me`)
  - `-!- …`: info from pling itself
- **Right:** who's online. A `…` after a name means that person is typing. The list is hidden when the terminal is narrower than 80 columns.
- **Bottom:** a status bar (server, your name, how many are online, who's typing, `↓ new messages` when you've scrolled up), and the input line with your name as the prompt.

Your messages are saved locally in `~/.local/share/pling/history.jsonl`, and the last 500 from the same server are shown when you start pling.

### Commands

| Command | What it does |
|---|---|
| `/msg <name> <text>` | Private message to one person |
| `/r <text>` | Reply to the last private message |
| `/me <action>` | Action, e.g. `/me makes coffee` |
| `/roll [NdM]` | Roll dice, e.g. `/roll 2d6` (default `1d6`, at most 20 dice of 2–1000 sides) |
| `/shrug [text]` | Sends your text followed by ¯\\\_(ツ)\_/¯ |
| `/who` | Who's online |
| `/clear` | Clear the screen (your saved history is kept) |
| `/help` | Show the commands |
| `/quit` | Quit (Ctrl-C works too) |

To send a line that starts with `/`, start it with `//`.

### Keys

| Key | What it does |
|---|---|
| Enter | Send |
| Tab | Complete a name or command; press it again for the next match |
| PgUp / PgDn, mouse wheel | Scroll |
| Ctrl-C | Quit |

pling uses the mouse wheel, so to select text with the mouse you hold **Shift** while you drag (that works in most terminals).

### Config file

`~/.config/pling/config.toml` (`pling init` writes it for you):

```toml
server = "wss://pling.viaxyz.net/ws"  # the server's address
name = "anders"                       # your name; must match the server's users file
notify = "bell"                       # "bell", "desktop", "both" or "off"
fun = true                            # confetti when someone joins
```

If `$XDG_CONFIG_HOME` or `$XDG_DATA_HOME` is set, pling uses those instead of `~/.config` and `~/.local/share`.

### Notifications

pling notifies you about private messages and messages that mention your name. When the terminal window isn't focused, it also notifies you about everything else said in the room.

- `bell`: rings the terminal bell. Most terminals then flash or mark the tab.
- `desktop`: shows a desktop notification through `notify-send`. On Debian and Ubuntu you install it with `sudo apt install libnotify-bin`. Without `notify-send`, pling falls back to the bell.
- `both`: does both.
- `off`: no notifications.

pling knows whether the window is focused only if your terminal reports focus changes. Most do. In tmux you need `set -g focus-events on`.

### Switching servers

Use `--server` for a single run:

```sh
pling --server wss://chat.example.com/ws
```

To switch for good, change `server` in the config file, or run `pling init` again (it keeps your key). Your key works on every server, but each server's admin has to add your line to their own users file.

Addresses start with `wss://` (encrypted). pling refuses unencrypted `ws://` except to `localhost`, because anyone on the way could read along. For a test server without TLS on your own network, add `--insecure` (to `pling` and to `pling init`).

### Troubleshooting

| You see | What to do |
|---|---|
| `login refused: the server does not know this name and key` | The admin hasn't added your line yet, or the name in your config doesn't match the line they added. Run `pling pubkey` and send them the line again. |
| `disconnected (…), retrying in N s` | The server is unreachable or restarting. pling keeps retrying on its own. If it never connects, check `server` in your config. |
| `there is no pling server at that address (HTTP 404)` | The address is wrong. It usually ends in `/ws`. |
| `you logged in from another window` | You can only be logged in once per name. The newest login wins. |
| `slow down: too many messages` | The server allows 20 messages per 10 seconds. Wait a moment. |
| `… is readable by other users; fix it with: chmod 600 …` | Your private key file has the wrong permissions. Run the `chmod` command shown. |
| `config has no valid name` / `no key found` | Run `pling init`. |
| `unencrypted ws:// is only allowed to localhost` | Use the `wss://` address. Only for a test server without TLS: add `--insecure`. |
| `server answered HTTP 503` | The server is full, or too many logins are in progress at once. pling retries on its own. |
| Garbled lines or boxes instead of `┌─┐` | Your terminal isn't using UTF-8. Set a UTF-8 locale, e.g. `export LANG=en_US.UTF-8`. |

---

## Running your own server

You need a machine that can be reached from the internet and a reverse proxy that does TLS (HTTPS). The examples use Caddy, but nginx or Traefik work too, as long as they pass WebSockets through. `plingd` itself speaks plain HTTP on port 8080: the WebSocket at `/ws`, and a health check at `/healthz`.

`plingd` is configured with environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `PLING_LISTEN` | `:8080` | Address to listen on |
| `PLING_USERS` | `./users.yaml` | The users file, re-read on every login |
| `PLING_HOST` | (required) | The public host name, e.g. `pling.example.com`. Clients sign it when they log in, so it must match the host in the address they use (including `:port` if that address has one). A login signature made for another server is then useless here. plingd refuses to start without it, unless you pass `-insecure-any-host` (local testing only: it trusts each request's Host header instead). |
| `PLING_MOTD` | (empty) | Message of the day, shown after login. `\n` starts a new line. |

Flags: `-v` logs more detail (frame types, never message text), `-version`, `-healthcheck` (checks the local server, for container health checks) and `-insecure-any-host` (see `PLING_HOST`).

Built-in limits: 100 logged-in users; at most 64 connections that are still logging in (2 per client address, where all IPv6 addresses in one /64 count as one address), each with 5 seconds to finish. Behind a reverse proxy on the same machine or Docker network, plingd takes the client address from `X-Forwarded-For`. Repeated warnings (failed logins, server full) are logged at most once per second, with a count of the ones skipped.

### The users file

One line per user, exactly as people send them to you (the keys below are placeholders):

```yaml
anders: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
nikolaj: BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=
```

To add someone, paste their line into the file. To remove them, delete it. Changes apply on the next login, with no restart. If a line is broken, every login fails and the log says why, so check the log after editing. See [deploy/users.example.yaml](deploy/users.example.yaml).

### Option A: one binary

1. Download `plingd` from the [releases](https://github.com/nsommer89/pling/releases) (`plingd_linux_amd64.tar.gz` and friends), or build it with `make build`.
2. Create `users.yaml` next to it.
3. Start it: `PLING_HOST=pling.example.com ./plingd`
4. Point your reverse proxy at `127.0.0.1:8080`. In Caddy:

   ```
   pling.example.com {
   	reverse_proxy 127.0.0.1:8080
   }
   ```

5. Connect with `pling --server wss://pling.example.com/ws`.

To keep it running, use a systemd service, or Docker (option B).

### Option B: Docker Compose

[deploy/compose.yaml](deploy/compose.yaml) builds a small image (about 10 MB, distroless, non-root, read-only). It publishes no ports on the host. Instead, it joins an external Docker network called `proxy`, where your reverse proxy reaches it as `plingd:8080`.

```sh
git clone https://github.com/nsommer89/pling.git && cd pling/deploy
mkdir -p config && cp users.example.yaml config/users.yaml && chmod 644 config/users.yaml
# edit config/users.yaml and add your users
docker network create proxy        # only if your proxy hasn't created it already
printf 'PLING_HOST=pling.example.com\nPLING_VERSION=%s\n' "$(git describe --tags --always)" > .env
docker compose up -d --build
docker compose logs -f             # one line per login/logout, never message text
```

Compose reads `.env` for every command, so `logs` and `down` work without repeating the settings. `PLING_VERSION` is the version clients see after login. Docker keeps at most 3 × 10 MB of logs.

Caddy, if it runs on the same `proxy` network: see [deploy/Caddyfile.example](deploy/Caddyfile.example).

The server keeps nothing, so a restart only costs a few seconds. Clients reconnect on their own.

---

## Contributing

Ideas, fixes and new features are welcome. [ROADMAP.md](ROADMAP.md) has a list of ideas with a rough size, and where in the code each one belongs.

1. **Fork** the repo on GitHub and clone your fork:
   ```sh
   git clone https://github.com/<you>/pling.git && cd pling
   ```
2. **Make a branch:** `git checkout -b my-feature`
3. **Build and test.** You need Go 1.26 or newer and `make`:
   ```sh
   make check        # gofmt, go vet and all tests; this is what CI runs
   make build        # bin/pling and bin/plingd
   make run-server   # local server on :8080 with ./users.yaml (signed host localhost:8080)
   make run-client   # client against the local server
   ```
   For a local test, put your own line from `pling pubkey` in `./users.yaml` (git ignores it).
4. **Keep the look:** [docs/design.md](docs/design.md) describes the TUI rules: colours, layout, and what not to add. All user-facing text lives in `internal/ui/strings.go`.
5. **Open a pull request** against `main`, with a short description of what changed and why. CI must be green.

**The protocol version rule.** Every frame carries `"v": 1`. Adding a new frame type (`kind`) or a new optional field is **not** a breaking change. Old clients and servers ignore what they don't know, so `v` stays the same. Bump `v` only for a breaking change (renaming or removing a field, changing what one means, or changing the login). If you do, update [docs/protocol.md](docs/protocol.md) in the same pull request. The full protocol is described there.

Code layout:

| Package | What's in it |
|---|---|
| `cmd/pling` | The client command (`init`, `pubkey`, chat) |
| `cmd/plingd` | The server command (environment config, signals) |
| `internal/protocol` | Frame types, encoding and decoding, limits |
| `internal/auth` | Users file, challenge nonces, signature checks |
| `internal/hub` | Who's online, room and DM routing, rate limit |
| `internal/server` | HTTP/WebSocket server, login handshake, ping/pong |
| `internal/client` | Config, keys, local history, the reconnecting connection |
| `internal/ui` | The Bubble Tea interface |

## License

MIT, see [LICENSE](LICENSE).
