# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`crossmem` is a local-first CLI that makes agent context portable. It discovers the on-disk session stores of local agent tools (Claude Code, Codex, Devin (CLI and desktop), Copilot (VS Code and CLI), OpenCode), lists sessions, and emits a clean Markdown **context bundle** that a different agent session can load. It sends no telemetry; everything is local reads.

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
- `internal/app` — CLI surface. `app.go` holds the hand-rolled command dispatch (no cobra), the embedded help text constants, and flag parsing per subcommand via `flag.FlagSet`. `extractPositionalFolder` lets a folder arg appear anywhere among flags (e.g. `load --limit 5 .`). Commands: `scan`, `list`/`sessions`, `load`/`context`, `update`, `guardrails`, `export`, `import`, `sync`, `config`, `install`/`uninstall --skills`.
- `internal/providers` — the engine. This is where almost all real work happens. `convexport.go` / `transcript.go` are `export`/`import`: one `qa.jsonl` of full Q&A plus tools/results (`sessionId`, `folder`, `q`, `a`, `time`, `messages`) with no agent, model, or token fields. `dump.go` (`ExportDump`/`ImportDump`/`SyncDump`) is only used by `sync` to rclone original stores.
- `internal/skills` — installs the bundled `crossmem-loader` skill (embedded via `//go:embed bundled`) into `~/.claude/skills` and optionally `~/.agents/skills`.
- `internal/diag` — env-gated debug logging (`CROSSMEM_DEBUG`, `CROSSMEM_LOG`). Never logs transcript contents.
- `internal/version` — `Version`/`Commit`/`Date` set at build time via GoReleaser `-ldflags -X`.

### How providers work (the core model)

Every supported tool's stores are declared once in `paths.go` `storeDefinitions` (provider, kind, **path candidates**, primary, note). Two fundamentally different store shapes are unified into `Session`/`Store`:

1. **JSONL-on-disk** (Claude, Codex, Copilot in VS Code) — `listJSONL` walks each root from `providerRoots` for `*.jsonl`. Roots are `providerRoot{Provider, Path}` values: the provider **travels with the root**, because two providers can share a directory shape. Workspace comes from path structure (`inferWorkspace` — Claude encodes the cwd as a `-`-delimited dir name; every VS Code fork records it in `workspaceStorage/<id>/workspace.json`), and a title is sniffed from the head of the file (`readJSONLMeta`, provider-specific keys). The VS Code chat store is a journal (`kind:0` snapshot + `kind:2` append lines); `extractCopilot` reconstructs turns from `requests[].message.text` + `response[].value`.
2. **SQLite** (Devin, OpenCode, Copilot CLI) — `listDevin`/`listOpenCode`/`listCopilotCLI` open the DB read-only and query directly. Devin uses one `sessions.db` for every session (CLI and desktop alike — the desktop drives the CLI through an ACP connector); OpenCode uses `~/.local/share/opencode/opencode*.db` (`session`/`message`/`part` tables, `session.directory` = cwd); the GitHub Copilot **CLI** (distinct from the VS Code Copilot JSONL store) uses `~/.copilot/session-store.db` (`sessions.cwd` + a denormalized `turns` table). Each exposes sessions via a `Ref` (`devin:<id>` / `opencode:<id>` / `copilot-cli:<id>`) that `BuildSessionContext` routes on. The sibling `auth.json` / `auth.db` / credential tables are never read.

### Cross-platform path resolution (paths.go + config.go)

The reasoning, the verification evidence, and the rules for adding a provider live in [`docs/adr/1-cross-platform-store-resolution.md`](docs/adr/1-cross-platform-store-resolution.md). Read it before adding a store or claiming support for a tool.

Never hardcode a store path at a call site — go through `storePath(provider, kind)` (best single existing location), `storePaths(...)` (all existing locations; globs expanded), or `providerRoots(...)`. Each definition lists every OS-specific candidate in one slice, built from the `appDataRoots(app)` / `vscodeUserRoots(app)` helpers rather than written out per platform. `expandPath` resolves `~`, `%VAR%`, `$VAR`/`${VAR}` and returns `""` when a referenced env var is unset, which is how a Windows-only candidate drops out on macOS **without any `runtime.GOOS` branching**. Two facts worth keeping: Devin's Windows store is under `%APPDATA%\devin\cli` (changed from the retired `%APPDATA%\Cognition\cli`), `$DEVIN_HOME` / `$DEVIN_DB_PATH` relocate it, and `$CLAUDE_CONFIG_DIR` / `$CODEX_HOME` are listed *first* for their providers so a relocated config dir wins over the default. See [ADR 2](docs/adr/2-devin-single-sessions-db.md).

`config.go` layers user overrides from `~/.config/crossmemcli/config.json` (or `$CROSSMEM_CONFIG`) on top: `stores` replaces a store's candidates, `extraStores` appends. Keys are `provider:kind`, or a bare `provider` which resolves only to the definition marked `Primary` — so overriding `devin` cannot repoint `devin:cli-logs` at a database file. Unknown keys are a load error, surfaced by `crossmem config` and ignored (with the built-ins still in force) everywhere else. The config is cached process-wide behind a mutex; tests call `resetConfig`.

### VS Code chat (`copilot`)

Copilot in VS Code is the only remaining consumer of the VS Code chat store.
It stores chat in `workspaceStorage/<id>/chatSessions/*.jsonl` (and
`GitHub.copilot-chat/transcripts/`), the VS Code journal format. A `devin-gui`
provider existed for the Devin desktop app when it was believed to be a VS Code
fork with its own chat store; verified against a real install, the desktop
drives the Devin CLI through an ACP connector and every session lands in the
CLI's single `sessions.db`, so `devin-gui` and the encrypted Cascade store were
removed ([ADR 2](docs/adr/2-devin-single-sessions-db.md)). Devin is now one
provider.

Anything shared by forks keys off the **path shape**, not the provider name: `isWorkspaceStoragePath` gates the chat-only filter and workspace resolution. Anything about the transcript *format* keys off `isVSCodeChat(provider)`. Adding another fork (Cursor, VSCodium) is one `storeDefinition` using `vscodeUserRoots("<DataFolder>")` plus a case in `inferProvider`; don't special-case `"copilot"` by name in new code.

Windows path shapes also matter inside the JSONL providers: compare paths through `pathsEqual`/`normalizeCase` (case-folded on Windows), match store-relative segments on `filepath.ToSlash(path)`, and decode Claude's encoded project dir via `decodeClaudeDir` (`C--Users-m-repo` → `C:\Users\m\repo`). VS Code writes Windows folders as `file:///c%3A/...`, whose leading slash `readCopilotFolder` strips.

`ListSessions` merges both, sorts by mtime desc, applies `--limit`. CWD/folder filtering (`filterByCWD` / `sameOrChild`) matches a session's workspace or title against the target repo path.

`BuildContext` (in `context.go`) is what `load`/`update` produce: a header, the **guardrails block**, then a bounded preview (`maxPreviewChars`) per session.

### Two concepts that drive the design

- **Guardrails vs. history.** `guardrails.go` finds repo instruction files (`AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md`) and marks them as *authoritative instructions*, while session transcripts are *context only*. This distinction is intentional and stated in the bundle output and in `AGENTS.md` — preserve it.
- **Read-safety.** This tool reads other tools' private stores, so it must never touch secrets. Per `AGENTS.md`: do not read/export credential files, auth DBs, `*.env`, or `vault/` directories (e.g. Devin's `credentials.toml` is deliberately not read). Don't add repo-local skill install behavior — skill activation is global-only by design. The dump shares this contract: `skipExportFile`/`skipExportDir` in `dump.go` filter auth/credential/env/key material and vault/cache/node_modules dirs out of `export`, so a dump pushed to a sync remote never carries a secret value.

### Transient-failure handling

Because active agents may be writing these files concurrently, reads use `withRetry` (`retry.go`): up to 3 attempts with small backoff, retrying only on transient errors ("database is locked", "busy", "too many open files", etc.) and never on `ErrNotExist`/`ErrPermission`. SQLite opens use `mode=ro` + `busy_timeout`.

## `update` output

`crossmem update <folder>` writes `<folder>/.crossmem/`: `context.md`, `guardrails.md`, `sessions.json`, `sources.json`. This is the durable, committable form of a bundle.

## Distribution

Three channels, all built by GoReleaser from one Go binary:

- **Go**: `go install github.com/muthuishere/crossmemcli/cmd/crossmem@latest`
- **npm**: `@muthuishere/crossmem` is a thin JS launcher (`npm/`) that resolves a prebuilt platform package (`@muthuishere/crossmem-<os>-<arch>`), like `windowctl`. Publishing is via GitHub Actions OIDC trusted publishing (`.github/workflows/npm-publish.yml`): **platform packages publish first, the root `@muthuishere/crossmem` last.** When adding npm packages, configure the same trusted publisher for each.
- **Homebrew**: tap cask generated under `dist/homebrew/`.

<!-- ctx-optimize:begin -->
<ctx-optimize>
  <precondition>Run `command -v ctx-optimize` first. If it is NOT installed, IGNORE this entire
  block and answer by reading the code normally — the store is an optimization, not a requirement
  (install later with `npm install -g @muthuishere/ctx-optimize`, or download the binary). Everything
  below applies ONLY when the command exists.</precondition>
  <store>MULTI-MODULE repo, pre-built knowledge store at `~/ctxoptimize/crossmemcli/` — one graph per module + a navigator, 7 modules declared in `.ctxoptimize/config.json`.</store>
  <use>Use it INSTEAD of grep-and-read chains — PICK BY INTENT: find → `ctx-optimize query "<terms>"` ·
  inspect a symbol → `card <symbol>` · about to EDIT → `change-plan <symbol>` (callers+impact+tests, one
  call) · blast radius → `affected <symbol>` · connection → `path <a> <b>` ·
  list/filter (no jq): `nodes --kind K` / `edges --relation R` / `deps --scope dev`.
  Scope follows your cwd: a module dir answers from that module (zero hits escalate repo-wide); the root
  federates via the navigator (`~/ctxoptimize/crossmemcli/navigator.md`; `--modules all|a,b` widens).
  Output is parsed fact with exact file:line — cite it directly, do NOT re-verify in source.
  Exhaustive literal-string sweeps stay grep's job.</use>
  <deep-doc>The FULL usage card — verify discipline, store-vs-grep ladder, sources (databases/
  buckets/queues/APIs by env-var name), remote push/pull, `up` — is committed at
  `.ctxoptimize/instructions.md`. Read it before deeper store work.</deep-doc>
  <no-local-store>Fresh clone with nothing at `~/ctxoptimize/crossmemcli/`? Run `ctx-optimize up` —
  it pulls the team's prebuilt store when the config declares one, otherwise rebuilds every module store in seconds.</no-local-store>
</ctx-optimize>
<!-- ctx-optimize:end -->
