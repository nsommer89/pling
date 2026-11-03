# Roadmap

pling v0.1 covers one room, private messages, an online list, typing indicators, local history, notifications and a bit of fun. Below are ideas anyone can pick up. Each has a rough size: **S** is an evening, **M** is a weekend, **L** is bigger and worth discussing in an issue first. The hint says where in the code the change belongs.

Before you start, read [docs/design.md](docs/design.md) (the look and the rules) and [docs/protocol.md](docs/protocol.md) (the frames). See "Contributing" in [README.md](README.md) for the workflow.

## Planned versions

### v0.2: end-to-end encryption (L)
Private messages (and later the room) encrypted on the clients, so the server can only relay ciphertext.
- Each user already has an Ed25519 key. Derive an X25519 key for key agreement (or let clients publish a separate X25519 key, signed with their Ed25519 key). Then encrypt DMs with an AEAD (XChaCha20-Poly1305 via `golang.org/x/crypto`).
- The server needs a way to hand out public keys: a new frame, e.g. `keys{name, pubkey}`, sent with `welcome`/`presence`. The server already knows every key from `users.yaml`.
- The encrypted payload goes in the reserved `cipher` kind: `{"v":1,"kind":"cipher","to":"anders","nonce":"…","box":"…"}`. It's a new kind, so `v` stays at 1, and old clients ignore it.
- Any signature v0.2 adds must use its own context prefix (e.g. `"pling-e2e-v1\x00" || …`). That keeps it from ever being confused with the login signature `nonce || host || name`.
- Hints: `internal/protocol` (new kinds), a new package `internal/e2e`, `internal/ui/model.go` (`handleFrame` and `sendChat`), `internal/hub` (relay `cipher` like `msg`).

### v0.3: files and images (M–L)
Send a file of a few MB to a person or the room. The server relays it and never stores it.
- Reserved kind `blob`: `blob{id, to, name, size, mime, chunk, data}`, sent in chunks of e.g. 32 KB so it doesn't block the chat. It needs its own size and rate limits in `internal/server/conn.go` and a higher read limit for blob frames only.
- The client saves to `~/Downloads/pling/` after asking (`/accept`?). It could show images inline in terminals that support kitty/sixel graphics.
- With v0.2 in place, encrypt the chunks too.
- Hints: `internal/protocol`, `internal/hub` (routing like DMs), `internal/ui` (progress line in the log).

## Ideas

| Idea | Size | Description | Where |
|---|---|---|---|
| More rooms (`/join #room`) | M | Named rooms with their own online list, plus `/part` and `/rooms`. Add a `room` field to `msg`/`typing`/`presence`; no field means the default room. | `internal/hub` (rooms map), `internal/protocol`, `internal/ui` (current room in the status bar) |
| Reactions | S | `/react 👍` on the last message, shown after it. Needs a `react{id, emoji}` frame that references the message `id` the server already assigns. | `internal/protocol`, `internal/ui/render.go` |
| Emoji picker | S | `:smi` + Tab completes to 😄. Make it a small built-in table, no dependency. | `internal/ui/model.go` (`complete`) |
| Themes and colour choice | S | `theme = "amber" \| "green" \| "mono"` in config, plus a choice of your own nick colour. | `internal/ui/styles.go`, `internal/client/config.go` |
| Translations (`lang` setting) | S | `lang = "da"` in config. All text is already in `internal/ui/strings.go`; turn the constants into a table per language. | `internal/ui/strings.go`, `internal/client/config.go`, `cmd/pling` (init prompts) |
| History search | S–M | `/search word` searches the local history file and shows the hits. Ctrl-R could work like in a shell. | `internal/client/store.go`, `internal/ui` |
| Bots and webhooks | M | A small `cmd/plingbot` that logs in with its own key and answers commands, e.g. a weather bot (`!weather Aalborg`). A server-side webhook that posts into the room is another option. | new `cmd/plingbot` using `internal/client` |
| Read receipts | S–M | Show ✓ when a DM has been shown on the recipient's screen. Add a `seen{id}` frame and route it like a DM. | `internal/protocol`, `internal/hub`, `internal/ui/render.go` |
| Mobile or web client | L | A client in another language or for the browser, speaking the same protocol. A browser needs the server to allow its Origin; today `websocket.Accept` skips the origin check because there are no browsers. | new repo or `web/`, plus `internal/server/conn.go` (origin policy) |
| mDNS/LAN mode without a server | L | `pling --lan` finds others on the local network via mDNS and chats peer to peer. The keys can still prove who is who. | new `internal/lan`, `cmd/pling` |
| Server admin commands | M | Admins (marked in `users.yaml`) can `/kick`, `/motd`, and list connections. This needs a richer users file format; keep it backwards compatible. | `internal/auth` (users file), `internal/server`, `internal/hub` |
| Status messages (away) | S | `/away lunch` shows a status next to the name in the online list, and `/back` clears it. Add a `status{text}` frame that is broadcast like `typing`. | `internal/protocol`, `internal/hub`, `internal/ui` (list view) |
| Sounds | S | A short sound per notification instead of the bell (`notify = "sound"`), e.g. via `paplay`/`afplay`, off at night. | `internal/ui/notify.go` |
| Input history | S | Up/Down arrows recall your own previous lines, like a shell. | `internal/ui/model.go` (`handleKey`) |
| History rotation | S | The history file grows forever. Rotate it to `history-YYYY.jsonl` once a year or above a size limit. | `internal/client/store.go` |
| Desktop notifications on macOS | S | Use `osascript -e 'display notification …'` when `notify-send` is missing on macOS. | `internal/ui/notify.go` |

## Protocol versioning rule

Every frame carries `"v": 1`. New kinds and new optional fields do **not** change `v`, because receivers ignore what they don't know. Bump `v` only for breaking changes, and update [docs/protocol.md](docs/protocol.md) in the same pull request.
