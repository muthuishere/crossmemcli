# ADR: crossmem Is Also a Go Library — `pkg/crossmem` with an Explicit Client

- Status: accepted — implemented 2026-09-14, released in v0.2.0
- Date: 2026-09-14
- Driven by: Huddle Go CLI (`github.com/muthuishere/huddle`, its ADR 8 — cross-agent context via crossmem)

## Decision

1. **The providers are a public package.** `internal/providers` → `pkg/crossmem` (`package crossmem`). `internal/app` and `internal/version` stay internal; the CLI is one consumer of the library. `internal/diag` stays, but only `cmd/crossmem/main.go` uses it — the library no longer imports it (see correction 1).
2. **No process-global state in the library.** Callers construct a client:

   ```go
   c, err := crossmem.New(crossmem.Options{
       Config:            nil, // nil = LoadConfig() from $CROSSMEM_CONFIG / default path
       CurrentSessionIDs: nil, // nil = detect from env; []string{} = exclude nothing
       Debug:             nil, // io.Writer; nil discards
   })
   sessions, _ := c.List(ctx, crossmem.ListOptions{CWD: root, Limit: 5})
   tr, _       := c.Transcript(ctx, sessions[0].Ref)  // typed events, not markdown
   md, _       := c.Load(ctx, ref, crossmem.LoadFull) // today's rendered bundle
   rules, _    := c.Guardrails(root)
   stores, _   := c.Scan()
   cands       := c.Stores()                          // candidate paths per store
   ```

   Config, current-session ids, and the debug writer live on the `Client`. Every function that read them — 62, found by walking the call graph from `userConfig`, `currentSessionIDs`, and `diag.Debugf` — is now a method on `*Client`. The package-level functions (`ListSessions`, `BuildSessionContext`, …) are one-line wrappers in `default.go` over a lazily-built default client, so the CLI changed only its import path. That default client is the one process-wide value left, and nothing but the wrappers touches it. `ResetConfig` survives only for tests.

   Public methods take a `context.Context`. It is bound to a short-lived copy of the client for that call (`c.with(ctx)`), and the JSONL walk, the metadata fan-out, and every SQLite listing query honour it.
3. **Typed transcript access is exported.** `Transcript{Session, Events []Event}` with `Event{Role, Name, Content, Time}` — the data `sessionEvents` already computed. `Role` is `user` / `assistant` / `tool` / `thinking`; `Name` is the tool name on tool events. `Time` is `omitzero`: OpenCode and Devin record no per-turn time, and a zero `time.Time` under `omitempty` serialises as year 1. crossmem still never summarises (repo rule).
4. **`storeDefinitions` stays the single place a path is written** (ADR 1). A client adds or replaces stores through `Options.Config.Stores` / `ExtraStores` without touching env vars. `New` validates that config: an unknown store key is an error, not silently ignored.
5. **The skill installer is generalised:** `internal/skills` → `pkg/skillinstall` with `Install(fsys fs.FS, name string, targets []Target) ([]Result, error)`, `Uninstall(name, targets)`, and `DefaultTargets(includeAgents)`, keeping the atomic temp-dir → rename behaviour. crossmem passes `skills.FS`; Huddle passes its own skill.
6. **One skill source.** The embedded copy moved to top-level `skills/crossmem-loader`, embedded by `skills/skills.go` (`skills.FS`, `skills.Loader`). The stale top-level copy (last touched in 0.1.6) is gone; registries that read a repo's `skills/` and `crossmem install --skills` now see the same files.
7. **Compatibility promise:** `pkg/crossmem` follows semver from `v0.2.0`. JSON field names of `Session`, `Store`, `QARecord`, `Transcript`, and `Event` are frozen; additions only. Stated in the package doc (`doc.go`).

## Context

- Huddle is being rewritten as a Go binary and needs "what did Claude / Codex / Devin / Copilot / OpenCode do in this folder" without re-implementing six parsers. Everything useful was already exported *within* `internal/providers`, which Go forbids other modules from importing.
- Shelling out works (`crossmem list --json`) but couples Huddle to whatever crossmem version is on PATH — this bit us on 2026-09-14: the global npm install was 0.1.4, before the OpenCode provider (0.1.5), so an OpenCode huddle session was invisible.
- Config was cached process-wide from `$CROSSMEM_CONFIG`, `diag` read env in `init()`, and current-session detection read env directly — all fine for a CLI, all hostile to an embedding caller.
- Both modules are CGO-free (`modernc.org/sqlite`), so linking adds no build complexity to Huddle's goreleaser pipeline.

## Corrections to the proposal

Found while implementing; the decision above already reflects them.

1. **"A public package cannot import an internal one" is wrong.** Go's `internal/` rule restricts the *importer's* path, not visibility through a chain: `pkg/crossmem` may import `internal/diag` (same module root), and Huddle may import `pkg/crossmem` regardless. The real reason to cut the import stands — `diag` configures itself from env in `init()`, which an embedding caller cannot control — so the library logs through `Options.Debug` instead.
2. **SQL `LIKE` for the folder filter was the wrong fix.** `directory LIKE ? || '/%'` treats `_` and `%` in real paths (`my_repo`) as wildcards unless every call escapes them, and still needs a separate case-folding rule on Windows. Instead, when a folder filter is set the query drops `LIMIT`, rows stream newest-first, the folder is matched in Go by the same `sameOrChild` everything else uses, and iteration stops at `limit` matches (`listQuery`). Session tables hold hundreds to low thousands of rows; streaming them is cheap and exactly as correct as the JSONL path.
3. **`Event.Tool` was dropped.** It duplicated `Role == "tool"`.
4. **Env reads that remain are per call, not cached.** `expandPath` still reads `HOME`, `%APPDATA%`, `$CLAUDE_CONFIG_DIR`, etc. at resolution time — those *are* the machine's store locations. `$CROSSMEM_CONFIG` and the session-id variables are read only when `Options.Config` / `Options.CurrentSessionIDs` are nil.

## Details — provider fixes shipped in the same release

1. **Limit applied before folder filter** (OpenCode, Devin, Copilot CLI). A folder with older sessions returned nothing once more than `limit` newer sessions existed elsewhere. Reproduced on this machine before the fix: an OpenCode scratch folder with one session, `crossmem list <folder> --provider opencode --limit 3` → empty. Fixed as in correction 2.
2. **Subagent sessions listed as top-level.** OpenCode child sessions (`parent_id IS NOT NULL`, titles like "(@general subagent)") appeared as peers — 6 of 24 sessions on this machine. Now excluded by default and exposed as `Session.Children` (refs, newest first) on the parent; `ListOptions.IncludeSubagents` / `crossmem list --subagents` lists them with `Session.Parent` set. OpenCode builds predating `parent_id` still list (`hasColumn` guards the query).
3. **OpenCode fixtures added** in `client_test.go`: a parent + child session with real message/part rows, the pre-`parent_id` schema, and the older-than-limit folder for OpenCode and Devin.
4. **Stale skill copy** removed — decision 6.
5. **A `Ref` from `List` could fail to `Load`** (found by the new fixtures, not in the original survey). For a file ref, `resolveRef` inferred the provider from the substrings `/.claude/` and `/.codex/`, so any relocated store — a config override, or `$CLAUDE_CONFIG_DIR=~/.claude-cys` — listed fine and then failed with `unrecognized session path`. `providerForPath` now asks the client's own store roots first.
6. **Symlinked transcripts were listed by the link's own `lstat`** (found by the real-store round-trip below). A dangling link — `~/.claude/projects/…/review-app.jsonl` pointing at a deleted transcript — was offered by `List` and failed in `Load`; a live one sorted by the link's creation time and reported the link's size. `listJSONL` now follows the link, uses the target's stat, and skips dangling links.
7. **`List` allocated a 64 KB scanner buffer per transcript per call** (found by the scale run's allocation numbers). `readJSONLMeta` now draws it from a `sync.Pool`: 352 MB → 25.7 MB of garbage per `List` over 5,000 transcripts, 78 ms → 60 ms. The pool is scratch space, not state.

## Consequences

- Huddle pins `github.com/muthuishere/crossmemcli v0.2.x`; provider fixes reach it via `go get -u` + a Huddle release, independent of what's on PATH.
- The move was mechanical (`git mv` + import rewrite) and behaviour-neutral: the suite produced the same result before and after it. The client refactor was verified the same way before any behaviour change.
- `go doc` is part of the product: every exported identifier in `pkg/crossmem` and `pkg/skillinstall` has a doc comment (checked by an AST scan, 0 missing).
- New code inside `pkg/crossmem` must not add package-level mutable state. Anything that depends on config, current sessions, or logging is a `*Client` method.

## Verification

- `go build ./...`, `go vet ./...` (with and without `-tags stress`) clean; `go test ./...` passes in every package. `TestImportDryRunWritesNothing`, which failed before this work, is fixed in the same release: `ExportDump` recorded a symlinked instruction file (`~/.claude-cys/CLAUDE.md`) in the manifest without copying it, so `ImportDump` hard-failed. Those allowlisted files now follow the link through the export filters, the manifest lists only copied files, and import reports a listed-but-absent file as Missing — which also restores dumps written by 0.1.9–0.1.10.
- `examples/list/main.go` lists this machine's OpenCode sessions through `crossmem.New`: the ctx-optimize worktree folder shows one parent with two subagent children, and `Transcript` returns its 847 typed events.
- Fixes 1–2 on real data: the previously empty folder lists its session; `list <folder> --provider opencode` returns one parent instead of three peers.
- Stress results: see below. Fixes 6 and 7 came out of that run.

## Stress test

`pkg/crossmem/stress_test.go`, behind the `stress` build tag: `go test -tags stress -race -run Stress -v ./pkg/crossmem`. Numbers are from an M-series Mac, 2026-09-14.

| Scenario | Result |
|---|---|
| Whole suite under `-race` | No data races. |
| **Oracle** — 300 random `List` queries (provider × folder × child folder × limit 1–40 × subagents) over 3,000 OpenCode + 1,500 Claude sessions, on folders named `my_repo`, `100%done`, `it's-quoted`, `with space`, `ünïcödé` | Every result equals a brute-force oracle. |
| **Mutation check** — reintroduce `LIMIT` before the folder filter | Killed: the oracle fails at query 5, `TestClientSQLiteFolderOlderThanLimit` fails for OpenCode and Devin. Restored code passes. |
| **Isolation** — 16 clients with different configs, 64 goroutines, `-race` | 11,520 List/Transcript/Load calls in 8.0 s; 0 foreign sessions seen, 0 errors. |
| **Live writers** — an agent appends to a JSONL transcript (with periodic torn lines) and inserts OpenCode rows in WAL mode while 8 readers List + Transcript | 10,772 writes, 332 read rounds, 0 errors; torn lines skipped. |
| **Exclusive lock** — rollback-journal DB held under `BEGIN EXCLUSIVE` | `List` still succeeds in 1.1 s with the other stores' sessions; the locked provider is skipped and logged (`SQLITE_BUSY` after retries). Every locked SQLite store adds about that much. |
| **Scale** — 20,000 OpenCode sessions (80,000 parts) + 5,000 Claude transcripts | `List` all/limit 50: 104 ms · folder/limit 5: 88 ms · with questions: 91 ms · OpenCode folder with no match (full row stream): 26 ms. |
| **Huge session** — 60 MB, 60,000 turns | `Transcript`: 392 ms, 60,000 events · `Load` full: 68 ms, bundle 23,966 bytes (within budget). |
| **Cancellation** — cancel 10 ms into a List over that store | `context.Canceled` in 11 ms. |
| **Leaks** — 1,000 calls, a quarter pre-canceled, including early `break` out of SQL rows | Goroutines 2 → 2, open FDs 9 → 9 (`lsof`). |
| **Fuzz** — `fitPreview` budget, `resolveRef` on arbitrary refs | 8.6 M and 1.3 M executions (45 s each); no panics, budget never exceeded. |
| **External module** — a separate `go.mod` importing `pkg/crossmem`, `pkg/skillinstall`, `skills` via `replace` | Builds, vets, lists sessions, installs the skill. Importing `internal/diag` from it is refused by the toolchain. |
| **Cross-compile** (`CGO_ENABLED=0`) | windows/amd64, linux/amd64, linux/arm64, darwin/amd64 build and vet. |
| **Real stores, round-trip** — `List` every session on this machine, then `Load` + `Transcript` each, 16 at a time | 2,982 sessions (claude 2,664 · codex 129 · devin 68 · opencode 54 · copilot 37 · copilot-cli 30) listed in 462 ms, all loaded in 2.9 s, **0 failures** (1 before fix 6). Same run built with `-race`: 0 races. |
| **Real stores, old vs new binary** — `list --limit 100000 --json` from `fb782e7` and from this change | Identical ref sets except the one dangling symlink. |
| **Parallel CLI** — 12 `crossmem list . --json` processes at once | All exit 0, byte-identical output. |

CI (`.github/workflows/ci.yml`) now runs the suite on ubuntu, windows, and macos, so the Windows path handling a library consumer depends on is executed, not just cross-compiled. The stress tests themselves ran on macOS only; CI vets them on every OS.
