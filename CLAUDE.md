# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`crossmem` is a local-first CLI that makes agent context portable. It discovers the on-disk session stores of local agent tools (Claude Code, Codex, Devin, Copilot, OpenCode), lists sessions, and emits a clean Markdown **context bundle** that a different agent session can load. It sends no telemetry; everything is local reads.

## Commands

Tasks live in `Taskfile.yml` (Taskfile v3):

- `task build` — `go build -o bin/crossmem ./cmd/crossmem`
- `task test` — `go test ./...`
- `task lint` — `go vet ./...`
- `task ci` — test + vet + build + `--version`/`--help` smoke check (mirrors `.github/workflows/ci.yml`)
- `task snapshot` / `task release:dry-run` — local multi-platform build via GoReleaser

Run a single test: `go test ./internal/providers -run TestName`. Tests live in `internal/app/app_test.go` and `internal/providers/retry_test.go`.

Debug a run locally (no transcript contents are logged):
```sh
CROSSMEM_DEBUG=1 crossmem scan
CROSSMEM_LOG=/tmp/crossmem.log crossmem load . --limit 5
```

## Architecture

Thin entrypoint → command dispatch → providers core.

- `cmd/crossmem/main.go` — calls `app.Run(args, stdout, stderr)`. All logic is in packages so it's testable; `main` only wires stdio and exit codes.
- `internal/app` — CLI surface. `app.go` holds the hand-rolled command dispatch (no cobra), the embedded help text constants, and flag parsing per subcommand via `flag.FlagSet`. `extractPositionalFolder` lets a folder arg appear anywhere among flags (e.g. `load --limit 5 .`). Commands: `scan`, `list`/`sessions`, `load`/`context`, `update`, `guardrails`, `install`/`uninstall --skills`.
- `internal/providers` — the engine. This is where almost all real work happens.
- `internal/skills` — installs the bundled `crossmem-loader` skill (embedded via `//go:embed bundled`) into `~/.claude/skills` and optionally `~/.agents/skills`.
- `internal/diag` — env-gated debug logging (`CROSSMEM_DEBUG`, `CROSSMEM_LOG`). Never logs transcript contents.
- `internal/version` — `Version`/`Commit`/`Date` set at build time via GoReleaser `-ldflags -X`.

### How providers work (the core model)

Every supported tool's stores are declared once in `paths.go` `storeDefinitions` (provider, kind, path, note). Two fundamentally different store shapes are unified into `Session`/`Store`:

1. **JSONL-on-disk** (Claude, Codex, Copilot) — `listJSONL` walks the provider root for `*.jsonl`. Provider is inferred from the path (`inferProvider`), workspace from path structure (`inferWorkspace` — e.g. Claude encodes the cwd as a `-`-delimited dir name), and a title is sniffed from the first ~12 lines (`readJSONLTitle`, provider-specific keys). Copilot's VS Code store is a journal (`kind:0` snapshot + `kind:2` append lines); `extractCopilot` reconstructs turns from `requests[].message.text` + `response[].value`.
2. **SQLite** (Devin, OpenCode) — `listDevin`/`listOpenCode` open the DB read-only and query directly. Devin uses `sessions.db`; OpenCode uses `~/.local/share/opencode/opencode*.db` (`session`/`message`/`part` tables, `session.directory` = cwd). Both expose sessions via a `Ref` (`devin:<id>` / `opencode:<id>`) that `BuildSessionContext` routes on. The sibling `auth.json` / credential tables are never read.

`ListSessions` merges both, sorts by mtime desc, applies `--limit`. CWD/folder filtering (`filterByCWD` / `sameOrChild`) matches a session's workspace or title against the target repo path.

`BuildContext` (in `context.go`) is what `load`/`update` produce: a header, the **guardrails block**, then a bounded preview (`maxPreviewChars`) per session.

### Two concepts that drive the design

- **Guardrails vs. history.** `guardrails.go` finds repo instruction files (`AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md`) and marks them as *authoritative instructions*, while session transcripts are *context only*. This distinction is intentional and stated in the bundle output and in `AGENTS.md` — preserve it.
- **Read-safety.** This tool reads other tools' private stores, so it must never touch secrets. Per `AGENTS.md`: do not read/export credential files, auth DBs, `*.env`, or `vault/` directories (e.g. Devin's `credentials.toml` is deliberately not read). Don't add repo-local skill install behavior — skill activation is global-only by design.

### Transient-failure handling

Because active agents may be writing these files concurrently, reads use `withRetry` (`retry.go`): up to 3 attempts with small backoff, retrying only on transient errors ("database is locked", "busy", "too many open files", etc.) and never on `ErrNotExist`/`ErrPermission`. SQLite opens use `mode=ro` + `busy_timeout`.

## `update` output

`crossmem update <folder>` writes `<folder>/.crossmem/`: `context.md`, `guardrails.md`, `sessions.json`, `sources.json`. This is the durable, committable form of a bundle.

## Distribution

Three channels, all built by GoReleaser from one Go binary:

- **Go**: `go install github.com/muthuishere/crossmemcli/cmd/crossmem@latest`
- **npm**: `@muthuishere/crossmem` is a thin JS launcher (`npm/`) that resolves a prebuilt platform package (`@muthuishere/crossmem-<os>-<arch>`), like `windowctl`. Publishing is via GitHub Actions OIDC trusted publishing (`.github/workflows/npm-publish.yml`): **platform packages publish first, the root `@muthuishere/crossmem` last.** When adding npm packages, configure the same trusted publisher for each.
- **Homebrew**: tap cask generated under `dist/homebrew/`.
