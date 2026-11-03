# The pling protocol (v1)

pling speaks JSON frames over a WebSocket. The client connects to `wss://<host>/ws`. TLS is done by a reverse proxy in front of `plingd`, which itself listens on plain HTTP.

## Frames

Each WebSocket **text** message holds exactly one JSON object:

```json
{"v":1,"kind":"msg","text":"hi"}
```

- `v` is the protocol version. This document describes `1`.
- `kind` names the frame type. The other fields depend on the kind.
- Byte fields (`pubkey`, `nonce`, `sig`) are standard base64 with padding.
- Times (`ts`) are Unix milliseconds by the server's clock.

### Compatibility rules

- Receivers **ignore unknown kinds** and **unknown fields**. That's how the protocol grows without breaking old clients and servers.
- New kinds and new optional fields keep `v` the same. Bump `v` only for breaking changes: removing or renaming a field, changing what a field means, or changing the login.
- A frame with a different `v` is answered with `error{code:"unsupported_version"}`. During login, the connection is closed after that.

## Frame reference

| Kind | Direction | Fields | Meaning |
|---|---|---|---|
| `hello` | client → server | `name`, `pubkey` | Starts the login. `pubkey` is the raw 32-byte Ed25519 public key. |
| `challenge` | server → client | `nonce` | 32 random bytes to sign. Valid for 30 s, and only once. |
| `auth` | client → server | `sig` | Ed25519 signature over `nonce \|\| host \|\| name` (see below). |
| `welcome` | server → client | `name`, `motd`, `users[]`, `server` | Login accepted. `users` is the sorted online list including you; `server` is the server version (informational). |
| `error` | server → client | `code`, `detail` | Something went wrong (codes below). `detail` is optional; for `not_online` it's the name. |
| `msg` | both | `id`, `from`, `to`, `text`, `me`, `ts` | A chat message. The client sends `text`, plus `to` for a DM and `me: true` for an action. The server fills in `id`, `from` and `ts`, delivers it, and **echoes it back to the sender**. A missing or empty `to` means the room. |
| `typing` | both | `from`, `to` | "Is typing" hint. The client sends it at most every 3 s while typing; the server relays at most one per second per user and fills in `from`. Clients show it for about 5 s. An empty `to` means the room. |
| `presence` | server → client | `event` (`join`/`leave`), `name` | Someone came online or left. You don't get your own join. |
| `cipher` | (reserved) | | v0.2: end-to-end encrypted message (see ROADMAP.md). |
| `blob` | (reserved) | | v0.3: file transfer in chunks (see ROADMAP.md). |

### Error codes

| Code | When | Connection |
|---|---|---|
| `auth_failed` | Unknown name, wrong key, bad signature, or an expired/reused nonce. All of these look the same, so nobody can probe which names exist. | closed (1008) |
| `unsupported_version` | Wrong `v` (during login, a wrong `v` in `auth` is reported as `bad_frame`) | closed during login, open afterwards |
| `bad_frame` | Not JSON, invalid UTF-8 anywhere in the frame, missing `kind`, wrong type, wrong frame during login, empty text | closed during login, open afterwards |
| `server_full` | The login succeeded, but the server reached its user cap in the meantime (normally a full server answers HTTP 503 before the upgrade) | closed (1013) |
| `too_large` | `text` over 4096 bytes | open |
| `rate_limited` | More than 20 `msg` frames in 10 s; the frame is dropped | open |
| `not_online` | DM to someone who isn't online; `detail` is the name | open |
| `replaced` | The same name logged in from somewhere else; the newest login wins | closed (1000) |
| `timeout` | No pong in time, or the client can't keep up with its messages | closed (1000) |
| `shutdown` | The server is stopping | closed (1001) |

An `error` frame that ends the connection is sent just before the close frame. The close reason is the same code.

## Login

```
client                                      server
  | -- hello{name, pubkey} ------------------> |
  | <------------------------ challenge{nonce} |   32 random bytes, 30 s, single use
  | -- auth{sig} ----------------------------> |   sig = Ed25519(nonce || host || name)
  | <-------------- welcome{...} or error{...} |
```

- `host` is the `host[:port]` part of the URL the client connected to, e.g. `pling.viaxyz.net`. The server compares it against its configured `PLING_HOST`, so a signature made for one server is useless on another. plingd refuses to start without `PLING_HOST`. The only exception is `-insecure-any-host`, for local testing: then the server uses each request's `Host` header, and that promise no longer holds, because a server that shares your key could relay your login.
- `||` is plain byte concatenation, with no separators. The nonce has a fixed size and the host is fixed per server, so the result is unambiguous.
- The server re-reads `users.yaml` on **every** login attempt. It checks that `name` exists, that `pubkey` is the key on file for that name, and that `sig` verifies. Any failure gives the same `auth_failed`.
- The nonce is used up by the first `auth` attempt, whether it succeeds or not.
- The whole login must finish within 5 s of connecting (the server's handshake timeout). Otherwise the connection is closed.

Names are 2–16 characters of `a-z`, `0-9`, `_` and `-`.

## Limits and liveness

| What | Limit |
|---|---|
| Message text | 4096 bytes (UTF-8) |
| WebSocket frame | 32 KiB (bigger frames close the connection with 1009) |
| Messages | 20 per 10 s per user (sliding window) |
| Logged-in users | 100 per server; more get HTTP 503 before the WebSocket upgrade |
| Logins in progress | 64 per server and 2 per client address (an IPv4 address, or an IPv6 /64 prefix); more get HTTP 503. The client address is the TCP peer, or the last `X-Forwarded-For` entry when the peer is a loopback or private address (a local reverse proxy) |
| Logins per name | 1; a new login kicks the old one with `replaced` |
| Liveness | The server pings every 15 s and drops a connection without a pong within 30 s, so a dead peer is noticed within 45 s |

`GET /healthz` answers `200 ok`. A plain HTTP request to `/ws` gets `426 Upgrade Required` and is logged.

## Example session

`>` is client to server, `<` is server to client. Keys are shortened.

```
> {"v":1,"kind":"hello","name":"anders","pubkey":"AAAA…AAA="}
< {"v":1,"kind":"challenge","nonce":"q8e1…Zg=="}
> {"v":1,"kind":"auth","sig":"3n0V…AQ=="}
< {"v":1,"kind":"welcome","name":"anders","motd":"Welcome!","users":["anders","nikolaj"],"server":"v0.1.0"}

> {"v":1,"kind":"typing"}
> {"v":1,"kind":"msg","text":"hi everyone"}
< {"v":1,"kind":"msg","id":"9f2c1a7e5b3d4c60","from":"anders","text":"hi everyone","ts":1793708280000}

< {"v":1,"kind":"typing","from":"nikolaj","to":"anders"}
< {"v":1,"kind":"msg","id":"0b6e…","from":"nikolaj","to":"anders","text":"psst","ts":1793708285000}
> {"v":1,"kind":"msg","to":"nikolaj","text":"hi back","me":false}
< {"v":1,"kind":"msg","id":"77aa…","from":"anders","to":"nikolaj","text":"hi back","ts":1793708290000}

> {"v":1,"kind":"msg","to":"carl","text":"are you there?"}
< {"v":1,"kind":"error","code":"not_online","detail":"carl"}

< {"v":1,"kind":"presence","event":"join","name":"carl"}
< {"v":1,"kind":"presence","event":"leave","name":"nikolaj"}

< {"v":1,"kind":"error","code":"shutdown"}
  (close 1001 "shutdown")
```

## What the server keeps

Nothing on disk, and no message history in memory. A message is encoded once, put on each recipient's send queue (64 frames per connection) and forgotten. The log has one line per connect, disconnect and failed login, with the name and time, and never any message text.
