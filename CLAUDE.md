# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

do-ai is a transparent TUI supervisor wrapper for CLI AI agents (Claude, Codex, Gemini). It launches the target command inside a PTY, monitors output, and auto-injects configurable prompts when idle for a threshold period (default: 3 minutes). The tool is designed for unattended execution — keeping AI agents progressing without human intervention.

## Build & Test

```bash
# Build (Linux)
go build -trimpath -ldflags "-s -w" -o do-ai ./src

# Cross-compile for Windows
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o do-ai.exe ./src

# Run tests
go test ./src

# Run a single test
go test ./src -run TestIsMeaningfulOutput

# Debug mode
DO_AI_DEBUG=1 ./do-ai codex
```

## Architecture

Single-binary Go tool with platform-specific PTY implementations selected via build tags.

**Core event loop** (`src/main.go`):
- Output goroutine reads PTY, filters ANSI-only output via `isMeaningfulOutput()`, updates atomic `lastOutput` timestamp
- Input goroutine passes stdin to PTY transparently
- 1-second ticker checks idle condition: `sinceOutput >= idle AND sinceKick >= idle` → triggers injection
- Injection sequence: optional pre-input cleanup → message write → submit keys → timestamp update

**PTY abstraction** (`src/pty.go` interface, `src/pty_unix.go` / `src/pty_windows.go`):
- Unix: `github.com/creack/pty` with SIGWINCH handling
- Windows: `github.com/UserExistsError/conpty` with console size polling

**Terminal control** (`src/term_unix.go` / `src/term_windows.go`):
- Raw mode setup/teardown, platform-specific signal handling

**Key mechanisms**:
- `.do-ai.lock` sentinel file — lifecycle control; deleting it stops auto-injection
- `{LOCK_FILE}` placeholder in messages gets replaced with the actual sentinel path
- DSR (Device Status Report) auto-reply for Codex TUI compatibility
- 32-byte tail buffer for detecting ANSI sequences split across read boundaries
- Calibration counter: every Nth injection (default 5) uses `message_calib` instead of `message_main`

## Configuration

Priority (highest to lowest): environment variables → CLI args → YAML config → hardcoded defaults.

**YAML** (`do-ai.yaml` / `~/.config/do-ai/config.yaml` / `~/.do-ai.yaml`):
- `idle`: duration string (e.g. `3m`, `5min 10s`, `120`)
- `message_main`: primary injection text
- `message_calib`: periodic calibration text

**Key environment variables**: `DO_AI_IDLE`, `DO_AI_DEBUG`, `DO_AI_CALIB_EVERY`, `DO_AI_SUBMIT`, `DO_AI_SUBMIT_MODE`, `DO_AI_SUBMIT_DELAY`, `DO_AI_PRE_INPUT`

## Conventions

- All user-facing text, comments, and documentation are in Chinese
- No external config parsing library — YAML is parsed via `gopkg.in/yaml.v3`
- Thread-safe timestamps use `sync/atomic` (int64 Unix nanoseconds)
- Platform code separated by build tags (`//go:build !windows` / `//go:build windows`)
- Commit messages use `feat:` / `fix:` / `chore:` prefixes
