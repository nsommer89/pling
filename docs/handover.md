# pling handover

A short orientation for anyone picking up the project. The README covers installation and daily use; `docs/protocol.md` and `docs/design.md` are the reference. This page explains how the pieces fit, why they look the way they do, and where to start.

## What it is

A chat that runs in the terminal. `pling` is the TUI client (Bubble Tea), `plingd` a small relay server over WebSocket. The server stores nothing: a message is passed on and forgotten. History lives only on each client. Login uses an Ed25519 key pair, like ssh; there are no passwords.

**v0.1 does:** one shared room, private messages (`/msg`), who is online, "typing…", local history (500 lines per server), notifications (bell and `notify-send`), automatic reconnect, `/me`, `/roll`, `/shrug`, Tab completion of names, a little confetti when someone joins.

**v0.1 does not:** more rooms, end-to-end encryption, file transfer. See `ROADMAP.md`.

## How it is built

Go 1.27, one module `github.com/nsommer89/pling`, about 6,400 lines including tests. Small packages with one job each:

| Package | Responsibility |
|---|---|
| `cmd/pling` | Client command line: `init`, `pubkey`, `--server`, `--insecure`, `-version` |
| `cmd/plingd` | Server start-up: environment variables, `-healthcheck`, `-insecure-any-host`, graceful shutdown |
| `internal/protocol` | Frames (JSON with `v` and `kind`), name validation, limits |
| `internal/auth` | Ed25519 challenge-response, nonce registry (30 s, single use), the users file |
| `internal/hub` | Who is online, broadcast and DM, kick on duplicate login, send queues (64 frames per connection) |
| `internal/server` | HTTP/WebSocket (`/ws`, `/healthz`), connection lifecycle, limits and log throttling (`limits.go`) |
| `internal/client` | Connection with reconnect and backoff, config, keys, local history |
| `internal/ui` | The Bubble Tea model, rendering, commands, confetti, notifications, styles, and **every user-facing string in `strings.go`** |

## The protocol in short

JSON frames over WebSocket, each with `v: 1` and a `kind`. Unknown fields are ignored. A new frame type only needs a new `kind`; changing an existing frame needs a new `v`.

1. `hello{name, pubkey}` from the client.
2. `challenge{nonce}` from the server: 32 random bytes, valid 30 s, single use.
3. `auth{sig}`: a signature over `nonce || host || name`. The host name is part of it, so a signature for one server is useless on another.
4. `welcome{motd, users[], server}` or `error{code}`. An unknown name and a wrong key produce the same error, so nobody can probe which names exist.

After that: `msg` (room, or `to` for a DM; the server fills in `id`, `from`, `ts` and echoes to the sender), `typing`, `presence` (join/leave). Reserved for later: `cipher` (v0.2) and `blob` (v0.3). Full reference with an example session: `docs/protocol.md`.

## Security and limits

- TLS is terminated by the reverse proxy (Caddy). The client refuses `ws://` to anything but localhost unless `--insecure` is given.
- The server refuses to start without `PLING_HOST` (otherwise a hostile server could relay a challenge). `-insecure-any-host` is for tests only.
- Limits: 4 KB per message, 20 messages per 10 s per user, 100 logged-in users, 64 logins in progress (2 per address, IPv6 counted per /64), 5 s to finish a login, ping/pong within 45 s. A full server answers 503 before the upgrade.
- `X-Forwarded-For` is trusted only when the TCP peer is loopback or a private address (the proxy).
- The log never contains message text: only connect, disconnect and auth failures, and warnings are throttled to one line per second.
- Container: non-root, read-only, `cap_drop ALL`, 64 MB RAM, 64 PIDs, no host ports, log rotation.
- Two independent reviews before release; 79 tests including the race detector; `govulncheck` in CI.

Not covered in v0.1: the server can read messages in transit (they are only TLS-encrypted). v0.2 fixes that with end-to-end encryption.

## Running it

Production runs in Docker behind Caddy. Everything needed is in `deploy/`: `compose.yaml`, `users.example.yaml`, `Caddyfile.example`.

| Task | How |
|---|---|
| Add a user | Put the line from `pling pubkey` into `deploy/config/users.yaml` (mode 644). It is read on every login; no restart. |
| Remove a user | Delete the line. Takes effect at the next login; `docker compose restart plingd` closes an open connection. |
| New version | `git fetch --tags && git checkout vX.Y.Z`, set `PLING_VERSION` in `deploy/.env`, `docker compose up -d --build`. Clients reconnect by themselves. |
| Health | `GET /healthz` answers `ok`. Logs: `docker compose logs --tail 50`. |
| Your own server | One binary: `PLING_HOST=chat.example.com PLING_USERS=users.yaml plingd` behind a TLS proxy, or the compose file above. |

One person, several machines: the key pair is created per machine by `pling init`. Either copy `~/.config/pling/id_ed25519` (and `.pub`) to the other machine, or use one name per machine in the users file.

## Decisions and why

- **Go and Bubble Tea:** one static binary per platform, nothing to install, easy to contribute to.
- **WebSocket behind a proxy instead of raw TCP:** TLS and port 443 for free, works through firewalls, no new ports on the server.
- **Key pairs instead of passwords:** nothing to leak, nothing to remember, and the same key works against several servers.
- **The server keeps nothing:** simple operations, no personal data on the server, easy to move or self-host. History stays on the client.
- **Everything in English:** a public repo anyone can use. A Danish translation is on the roadmap because all strings already live in one file.
- **The users file is mounted as a directory:** editors replace the inode on save, and a single-file mount would keep showing the old version inside the container.
- **A versioned protocol from day one:** `v` and `kind` in every frame and reserved kinds for encryption and files, so v0.2 and v0.3 will not break old clients.

## Roadmap

**v0.2 end-to-end encryption** (L): messages encrypted between clients with the key pairs, so the server only sees `cipher` frames. **v0.3 files and images** (M–L): `blob` frames in chunks, relayed, never stored.

Ideas to pick up, with size and the place in the code, are listed in `ROADMAP.md`: more rooms, reactions, an emoji picker, themes, translations, history search, bots and webhooks, read receipts, a mobile or web client, LAN mode without a server, admin commands, away status, sounds, input history, history rotation, macOS notifications.

## Contributing

1. Fork and clone. Go 1.27 or newer.
2. `make build` builds both binaries into `bin/`. `make run-server` and `make run-client` run locally.
3. `make check` must be green before a PR (gofmt, go vet, go test). `go test -race ./...` is a good habit.
4. Pick an item from `ROADMAP.md`, make a branch, open a pull request. CI runs the checks, `go mod tidy -diff` and `govulncheck`.
5. Rules: user-facing text lives in `internal/ui/strings.go`; the look follows `docs/design.md`; protocol changes follow the versioning rule in `docs/protocol.md`; never log message text; never store anything on the server without a good reason.

## Known limits

- An attacker with many addresses can fill the login pool for 5 s at a time. The 503 answer and the proxy's own protection limit the damage; a tighter per-network count is a possible next step.
- Desktop notifications use `notify-send` (Linux). macOS support is a roadmap item.
