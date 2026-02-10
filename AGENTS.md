# Repository Guidelines

## Project Structure & Module Organization
- `src/` contains all Go source code for the `do-ai` binary.
  - `main.go`: PTY wrapper, idle detection, auto-injection loop.
  - `pty_*.go`, `term_*.go`: platform-specific PTY/terminal handling via build tags.
  - `relay.go`: relay server (`do-ai relay`) + client heartbeat reporting.
  - `*_test.go`: unit tests for core behavior.
- `docs/` stores user-facing documentation.
- `dist/` contains release artifacts and checksums.
- `scripts/` holds operational scripts (for example, ECS relay deployment).

## Build, Test, and Development Commands
- Build local binary:
  - `go build -trimpath -ldflags "-s -w" -o do-ai ./src`
- Run tests:
  - `go test ./...`
- Run one test case:
  - `go test ./src -run TestIsMeaningfulOutput`
- Run relay server locally:
  - `./do-ai relay --listen 0.0.0.0:8787 --token <token>`
- Deploy relay to ECS with Supervisor:
  - `scripts/deploy_relay_ecs.sh root@<ip> 18787`

## Coding Style & Naming Conventions
- Follow idiomatic Go; always run `gofmt` on changed files.
- Keep code and comments concise; user-facing messages are primarily Chinese in this repository.
- Use clear names (`relayReporter`, `buildAlerts`) and avoid one-letter identifiers.
- Keep platform differences isolated in `*_unix.go` / `*_windows.go` files.

## Testing Guidelines
- Add or update unit tests for new logic in `src/*_test.go`.
- Prefer deterministic tests for parsing, alert rules, and state transitions.
- For relay changes, verify both:
  - API behavior (`/healthz`, `/api/v1/heartbeat`, `/api/v1/sessions`)
  - End-to-end client reporting with `DO_AI_RELAY_URL` + `DO_AI_RELAY_TOKEN`.

## Commit & Pull Request Guidelines
- Use Conventional-style prefixes seen in history: `feat:`, `fix:`, `chore:`, `docs:`.
- Keep each commit focused on one change set (feature, fix, or docs).
- PRs should include:
  - What changed and why
  - Commands run for validation (for example `go test ./...`)
  - Screenshots or curl output for relay/dashboard changes when relevant.

## Security & Configuration Tips
- Never hardcode relay tokens; use env vars (`DO_AI_RELAY_TOKEN`, `DO_AI_RELAY_URL`).
- Prefer outbound heartbeat reporting over exposing PTY ports directly.
- For production relay, run behind TLS/reverse proxy and keep Supervisor logs enabled.
