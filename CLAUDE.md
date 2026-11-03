# Notes for coding agents and contributors

pling is a terminal chat: a TUI client (`cmd/pling`) and a relay server (`cmd/plingd`), written in Go. Start with README.md, then docs/protocol.md and docs/design.md.

## Build and test
- Go 1.26 or newer (go.mod pins toolchain go1.27.3, which `go` downloads if needed).
- `make check`: gofmt, `go vet` and all tests. It must be green before every commit.
- `make race`: the tests with the race detector.
- `go mod tidy -diff`: must print nothing (CI checks it).
- `make build` builds `bin/pling` and `bin/plingd`. `make run-server` and `make run-client` run them locally against `./users.yaml` (git ignores it).
- `make snapshot`: release archives in `dist/`, via GoReleaser.
- The end-to-end tests (`internal/server/e2e_test.go`, `cmd/plingd/main_test.go`) start a real server on a random port and connect real clients. `goleak` checks the server package for leaked goroutines.

## Layout
| Package | Responsibility |
|---|---|
| `internal/protocol` | Frame types, encode/decode, limits, name rules |
| `internal/auth` | Users file, challenge nonces, signature checks |
| `internal/hub` | Online users, room and DM routing, rate limit |
| `internal/server` | HTTP/WebSocket server, login, connection limits, ping/pong |
| `internal/client` | Config, keys, local history, reconnecting session |
| `internal/ui` | Bubble Tea interface; all user-facing text is in `strings.go` |

## Conventions
- Everything in the repo is English: code, comments, commits, docs and UI text.
- Keep packages small. Pass `context.Context` for cancellation, and leave no goroutine running after a connection or a test ends.
- Protocol: new kinds and new optional fields keep `"v": 1`. A breaking change bumps `v` and updates docs/protocol.md in the same change.
- The server stores nothing on disk and never logs message text. Keep it that way.
- The TUI look follows docs/design.md: no boxes, one rule under the header, colours that work in 16 colours, nick colour = FNV-1a % 8.
- Text from the network is untrusted. Sanitize it before drawing, and validate names with `protocol.ValidName`.

## No secrets
- Never commit keys, tokens, real users files or `.env` files. `.gitignore` covers `users.yaml`, `deploy/config/`, `.env` and `*.env`.
- Tests generate their own keys. Examples use obvious placeholders (`AAAA…=`).
