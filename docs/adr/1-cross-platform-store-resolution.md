# ADR: Cross-Platform Store Resolution, User Overrides, and the Limits of Provider Support

- Status: accepted
- Date: 2026-08-29
- Shipped in: 0.1.8 (resolution + config), follow-up correction to the Devin desktop claim

## Decision

1. **A store is declared once, as an ordered list of per-OS path candidates** — never as a single hardcoded path, and never behind a `runtime.GOOS` branch. `storeDefinitions` in `pkg/crossmem/paths.go` is the only place a store location may be written.
2. **Platform selection happens by environment-variable resolution, not by OS detection.** `expandPath` resolves `~`, `%VAR%`, `$VAR`/`${VAR}` and returns `""` when a referenced variable is unset on this machine. A `%APPDATA%/...` candidate therefore evaporates on macOS and a `$XDG_DATA_HOME/...` candidate evaporates on Windows, with no conditional code.
3. **Call sites never build paths.** They go through `storePath(provider, kind)` (best existing location), `storePaths(...)` (all existing locations, globs expanded), or `providerRoots(...)` (walkable roots, each labelled with its owning provider).
4. **The user can repoint any store** from `~/.config/crossmemcli/config.json` (or `$CROSSMEM_CONFIG`). `stores` replaces a store's candidates; `extraStores` appends. Keys are `provider:kind`, or a bare `provider` resolving only to the definition marked `Primary`. Unknown keys are a load error.
5. **A store may be declared for discovery without being readable.** When a tool's conversations exist but cannot be parsed — encrypted, or a private binary format — the store is still declared so `scan` can report it, is marked non-`Primary`, and its `Note` states plainly that crossmem does not read it. Discovery and extraction are separate promises.
6. **Provider support is claimed only after verification against a real install.** A documented on-disk layout is a hypothesis, not evidence.

## Context

Every store path was a hardcoded POSIX string (`~/.local/share/devin/cli/sessions.db`, `~/Library/Application Support/Code/...`). On Windows nothing was found for any tool, and the failure was silent: no store, no sessions, no error. `expandHome` also read `$HOME`, which is unset on Windows, so even the `~`-relative paths that *are* correct there resolved to garbage.

Three further problems sat behind the first:

- Path handling assumed `/` separators, case-sensitive comparison, and POSIX shapes throughout the JSONL readers.
- Even with correct built-ins, users relocate stores (portable installs, second drives, `$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`), and a new release is a poor way to fix one person's path.
- A store's *location* and a store's *readability* were conflated, which let a plausible-looking layout be shipped as working support without anyone opening the files.

## Details

### Resolution order

`storeCandidates` → user config overlay → `expandPath` per candidate (drop unset-variable candidates) → glob if the candidate contains `*?[` → `os.Stat` → deduplicate. `storePaths` returns every real hit; `storePath` returns the first. `displayCandidate` supplies the platform-appropriate path to show when nothing exists, so `scan` prints something meaningful on a machine where the tool is not installed.

Candidate lists are generated, not transcribed: `appDataRoots(app)` yields the XDG / Application Support / `%APPDATA%` / `%LOCALAPPDATA%` / `~/AppData/{Roaming,Local}` family, and `vscodeUserRoots(app)` yields the `User` directory of a VS Code build or fork.

`$CLAUDE_CONFIG_DIR/projects` and `$CODEX_HOME/...` are listed **first** for their providers: when a config dir has been relocated, that is where the sessions actually are.

### Config schema

```json
{
  "stores":      { "devin:sqlite-sessions": "D:/agents/Cognition/cli/sessions.db" },
  "extraStores": { "opencode": "~/other/opencode/opencode*.db" }
}
```

Values accept a string or a list, with `~`, `%VAR%`, `$VAR`, and globs. Because an unset variable drops a path, one config file can be committed and shared across a Mac and a Windows box. The bare-`provider` key resolves only to the `Primary` definition, so overriding `devin` cannot repoint `devin:cli-logs` at a database file. Config is cached process-wide behind a mutex; tests call `resetConfig`.

### Windows path shapes inside the readers

- `expandHome` uses `os.UserHomeDir()`, not `$HOME`.
- Store-relative segment matching runs on `filepath.ToSlash(path)`.
- Folder comparison folds case (`normalizeCase` / `pathsEqual`) — VS Code writes `c:\`, the shell reports `C:\`.
- `decodeClaudeDir` decodes Claude's encoded project directory in both forms: `-Users-m-repo` → `/Users/m/repo`, `C--Users-m-repo` → `C:\Users\m\repo`.
- `readCopilotFolder` strips the leading slash from `file:///c%3A/...`, which unescapes to `/c:/Users/...`.

### VS Code forks

Anything shared between forks keys off the **path shape** (`isWorkspaceStoragePath`), not the provider name; anything about transcript *format* keys off `isVSCodeChat(provider)`. `providerRoots` returns `providerRoot{Provider, Path}` pairs, because Copilot-in-VS-Code and the Devin desktop app share the `workspaceStorage` shape and only the root they came from distinguishes them. Adding another fork (Cursor, VSCodium) is one `storeDefinition` using `vscodeUserRoots("<DataFolder>")` plus a case in `inferProvider`.

## Verification (2026-08-29, real install)

Checked against the raw stores rather than crossmem's own output:

| Provider | Method | Result |
| --- | --- | --- |
| Claude Code | `find` on the project dir vs `crossmem list .` | Same transcript, correct workspace, real text extracted. Both `$CLAUDE_CONFIG_DIR` and `~/.claude` roots discovered and walked. |
| Devin CLI | SQL against `sessions.db` vs `crossmem list --provider devin` | Identical ids, order, working directories, titles. `load --session devin:<id> --full` returns the full conversation from `message_nodes`. |
| Devin desktop | Filesystem + entropy analysis | **Not readable.** See below. |

A session rendering as a single line was investigated and is correct behavior, not a bug: that session holds 30 `system` rows and 4 duplicate `user` rows, and no `assistant` rows at all.

## The Devin desktop app: declared, not read

> **Superseded by [ADR 2](2-devin-single-sessions-db.md).** The desktop drives
> the Devin CLI through an ACP connector and every session lands in the one
> `sessions.db`, so `devin-gui` and the encrypted Cascade store were removed.
> This section is kept as the record of what was verified and then retired.

The desktop app is a VS Code fork — `product.json` gives `nameLong: Devin`, `dataFolderName: .devin`, `oldDataFolderName: .windsurf`, bundle id `com.exafunction.windsurf`. The fork layout was therefore assumed to carry chat in `workspaceStorage/<id>/chatSessions/*.jsonl`, and 0.1.8 shipped that assumption as working support. **It is wrong.** Measured on an install in active use:

- `workspaceStorage/<id>/` contains `workspace.json` and `state.vscdb` UI state only. No `chatSessions`. Listing the folder its `workspace.json` points at returns nothing.
- The app's own Cascade conversations live in `~/.codeium/windsurf/cascade/*.pb`, one file per conversation. That file is **encrypted at rest**: 7.998 bits/byte of entropy against a maximum of 8.0, no compression magic, and zero extractable strings at `strings -n 6`. It is not a format that can be parsed; it is a file that cannot be read.
- `~/.devin` — documented in 0.1.8 as holding "Cascade conversation blobs" — holds `argv.json` and an extensions list, 12 KB. That claim was false and has been removed.

What *is* readable: the desktop drives the Devin CLI through an ACP connector. Its global-storage key `windsurfSpace.resourceToSpace` maps spaces to `vscode-cascade-editor:///cascade-acp/devin-cli/<session-id>`, and those ids are exactly the rows in the CLI's `sessions.db`. **Desktop work driven through the devin-cli connector is already covered by the `devin` provider; chats with the built-in Cascade agent are not covered and cannot be.**

Consequently `devin-gui` declares two stores: `vscode-workspace-storage` (kept, harmless, and correct if a future build writes `chatSessions`) and `cascade-conversations` pointing at the real encrypted location, non-`Primary`, with `ENCRYPTED AT REST` in its note. `scan` reports it exists and how large it is; nothing reads it.

## Consequences

- Adding a provider or a platform is a data change in one table, not new branching logic.
- `crossmem scan` and `crossmem config` are the diagnostic surface: `config` prints, per store, every path consulted and which ones were found.
- A user whose tool lives somewhere unusual is unblocked without a release.
- `scan` reports one row per real location, so a tool with several databases (OpenCode's stable/dev/local) shows all of them.
- The cost of rule 6 is honesty about gaps: the provider list includes `devin-gui`, and the docs state that its conversations cannot be read. That is preferable to a provider that silently returns nothing.

## Alternatives rejected

- **`runtime.GOOS` switches per store.** Triples the branches, and cannot express "`$CODEX_HOME` if set, else `~/.codex`". Env-var dropout covers both cases with one mechanism.
- **Auto-discovery by scanning the filesystem for known filenames.** Slow, and it would wander into directories crossmem has no business reading.
- **Decrypting the Cascade store.** The key is not ours; the store belongs to another vendor's app. Reading it would violate the read-safety posture in `AGENTS.md` even if it were technically possible.
- **Dropping `devin-gui` entirely.** Discovery has value — `scan` telling you the conversations exist and where, is a true and useful answer.

## Source files

- `pkg/crossmem/paths.go` — `storeDefinitions`, `appDataRoots`, `vscodeUserRoots`, `expandPath`, `storePath(s)`, `providerRoots`, `isWorkspaceStoragePath`, `isVSCodeChat`
- `pkg/crossmem/config.go` — `Config`, `LoadConfig`, `candidatesFor`, `EffectiveStores`, `InitConfig`
- `pkg/crossmem/list.go` — `decodeClaudeDir`, `readCopilotFolder`, `sameOrChild`, `inferWorkspace`
- `pkg/crossmem/scan.go` — `DiscoverStores`
- `internal/app/app.go` — `runConfig` and `configHelpText`
- Tests: `pkg/crossmem/paths_test.go`, `pkg/crossmem/config_test.go`
