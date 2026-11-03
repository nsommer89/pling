# Design

pling should feel like a retro BBS or IRC client with a small twist: simple and readable, with a little fun. This page is the rulebook for the terminal UI, so it stays that way as people add features.

## Layout

```
 ┌─┐┬  ┬┌┐┌┌─┐                                         ← banner, 3 lines (1 line if the terminal is shorter than 18 rows)
 ├─┘│  │││││ ┬   tagline
 ┴  ┴─┘┴┘└┘└─┘   connection status
──────────────────────────────────────────────────────  ← the only rule
── Tuesday 3 November 2026 ────────                    online (2)    ← online list: right column, 18 cells,
15:58 <anders> message text that wraps and             anders           hidden below 80 columns
               continues under the text column         nikolaj…
15:58 -!- system line
 ● host · name · 2 online   anders is typing…       ↓ new messages   ← status bar, 1 line
anders› input                                                         ← input, nick as prompt
```

- **No boxes.** There is one thin rule under the header, and nothing else: no borders around panes, no vertical separators. Whitespace does the work.
- The **message log** takes all remaining space. Lines wrap at word boundaries, and continuation lines are indented to where the text starts.
- The **status bar** shows the connection dot (green ● connected, amber ○ connecting, red ● disconnected), the host, your name, the online count, who's typing, and on the right a scroll hint or `↓ new messages` when you're scrolled up and something new arrives.
- The **online list** shows names in their colours, with `…` after someone who is typing.

## Message lines

| Kind | Format | Style |
|---|---|---|
| Room | `HH:MM <nick> text` | time dim, brackets dim, nick in its colour |
| DM received | `HH:MM ← nick: text` | arrow and name in the DM colour (magenta), bold |
| DM sent | `HH:MM → nick: text` | same, the name is the recipient |
| Action (`/me`) | `HH:MM * nick text` | italic |
| System | `HH:MM -!- text` | dim italic |
| Error | `HH:MM -!- text` | soft red italic |
| MOTD | `HH:MM *** text` | amber |
| Day change | `── Tuesday 3 November 2026 ───` | cyan |

## Colours

- A dark background is assumed. The UI asks the terminal for its background colour and switches to a light palette if needed (`lipgloss.LightDark`, `tea.BackgroundColorMsg`).
- Accents: **amber** (banner, MOTD, unread marker), **cyan** (day lines), **green** (connected). Magenta is for DMs only. Nothing is neon-only: every colour still reads in a 16-colour terminal, because Bubble Tea downsamples hex colours to the terminal's profile.
- **Nick colours** are 8 of the 16 ANSI colours (bright yellow, cyan, green, magenta, blue, red, plus yellow and cyan), picked by FNV-1a hash of the name modulo 8. So a name has the same colour for everybody, in the log, the list and the prompt. Dark blue and black are left out because they're hard to read on dark backgrounds. Don't change the hash: it would recolour everyone.
- Text from the network is sanitised before it's drawn. Control characters, escape sequences and bidi overrides are removed, so nobody can repaint your terminal.

## The twist, and its limits

- **Confetti** when someone joins: 8 frames of 125 ms (1 second), ASCII pieces `*+.'o,~^` in the accent colours on both sides of the "joined" line, which then settles to a plain system line. `fun = false` in the config turns it off.
- `/roll [NdM]`, `/me`, `/shrug`.
- That's the list. New fun goes through an issue first. The chat must stay calm to read.

## Text

- All user-facing text is in `internal/ui/strings.go`, in English. Keep it short and friendly, and never blame the user.
- Error lines say what to do next ("run `pling pubkey` and send the line to…").

## Code map

- `internal/ui/model.go`: state, `Update` (keys, events, commands), `View` (layout).
- `internal/ui/render.go`: how each log entry becomes lines (wrapping, prefixes) and `sanitize`.
- `internal/ui/styles.go`: palette, nick colours.
- `internal/ui/commands.go`: input parsing, `/roll`, `/shrug`, typing targets.
- `internal/ui/confetti.go`, `internal/ui/notify.go`.

## Technical choices

- **Bubble Tea v2, Bubbles v2, Lip Gloss v2** (`charm.land/…`): the current major versions, with the new renderer, background colour detection and focus reporting.
- **WebSocket library: `github.com/coder/websocket`** (formerly `nhooyr.io/websocket`). Its API is built around `context.Context`, so cancellation and timeouts are the same everywhere. It's safe for concurrent writes, has a built-in `Ping` that waits for the pong, handles the close handshake properly, and is small and actively maintained by Coder. `gorilla/websocket` would also work, but it needs manual deadlines and a single writer goroutine for the same result.
- **TLS is not plingd's job.** A reverse proxy (Caddy) terminates TLS. plingd stays one small binary without certificates.
- **No database.** The server is a pure relay. History is a JSON Lines file on each client.
