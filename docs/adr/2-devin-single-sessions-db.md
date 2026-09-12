# ADR: Devin stores every session in one `sessions.db` — no `devin-gui` provider

- Status: accepted
- Date: 2026-09-12
- Supersedes: the Devin desktop / `devin-gui` parts of ADR 1 ("The Devin desktop app: declared, not read")

## Decision

1. **Devin is one provider.** Both the Devin CLI and the Devin desktop app
   (GUI) write every session to a single SQLite database, `sessions.db`. The
   `devin-gui` provider — added in 0.1.8 to model the desktop as a VS Code fork
   with a `workspaceStorage` chat store and an encrypted Cascade `.pb` store —
   is removed. There is no separate Devin desktop chat store, so there is
   nothing for a second provider to read.
2. **The default location changed.** Devin now ships to
   `~/.local/share/devin/cli/sessions.db` on macOS/Linux and
   `%APPDATA%\devin\cli\sessions.db` on Windows. The older Windows install
   path, `%APPDATA%\Cognition\cli\sessions.db`, is retired and no longer
   declared. The `devin/cli` data directory holds `sessions.db`, `logs/`, and
   `session_locks/`.
3. **Environment overrides are honored.** `$DEVIN_DB_PATH` (a direct path to
   the `sessions.db` file) and `$DEVIN_HOME` (the data directory, with the DB
   at `<home>/cli/sessions.db`) are listed before the defaults, in that order,
   exactly like `$CLAUDE_CONFIG_DIR` / `$CODEX_HOME`. `$XDG_DATA_HOME` is still
   honored through the shared candidate table.
4. **Read-safety is unchanged.** `sessions.db` is read `mode=ro`; the sibling
   `credentials.toml`, auth databases, and `*.env` files are never read or
   exported.

## Context

0.1.8 treated the Devin desktop app as a VS Code fork whose `workspaceStorage`
carried chat, and ADR 1 verified that claim false: the fork layout holds UI
state only, and the app's Cascade conversations lived encrypted in
`~/.codeium/windsurf/cascade/*.pb`. The honest fallback shipped in 0.1.8 was a
`devin-gui` provider that declared those stores but could not read them, with
the desktop's real, readable sessions surfacing through the `devin` provider
via the ACP connector.

Two facts changed the model:

- **The desktop drives the Devin CLI through an ACP connector.** Its
  global-storage keys (`windsurf.acp.connectorRegistryCache`,
  `windsurf.acp.metadataCache`, `windsurf.devin.settingsMigrationComplete`)
  confirm the desktop is a thin shell over the same CLI engine.
- **Every session, desktop-driven or not, lands in the one `sessions.db`.**
  Verified 2026-09-12 against a real install in active use: all 68 rows in
  `~/.local/share/devin/cli/sessions.db` carried `backend_type` = `Windsurf`
  (66) or `windsurf` (2) — i.e. the desktop's own sessions — with a single
  `sessions` table and no separate database anywhere on the machine
  (`find` for `sessions.db` returned exactly one hit).

So a "Devin GUI" provider described a store that does not exist, and the
Cascade `.pb` files it pointed at are an encrypted legacy that never produced a
readable session. Keeping the provider around only made `scan`, `config`, and
the help text lie about what is readable.

## Details

### Store declarations (`internal/providers/paths.go`)

```go
var devinCLIRoots = append(
    []string{"$DEVIN_HOME/cli"},
    appDataRoots("devin/cli")...,
)

var devinSessionDBCandidates = append([]string{"$DEVIN_DB_PATH"}, under(devinCLIRoots, "sessions.db")...)
```

`appDataRoots("devin/cli")` already yields the new Windows default
(`%APPDATA%\devin\cli`, `%LOCALAPPDATA%\devin\cli`, `~/AppData/{Roaming,Local}/devin/cli`)
plus the XDG and Application Support forms, so the "path changed" fix is a data
change in one table, consistent with ADR 1. `devin:sqlite-sessions` uses
`devinSessionDBCandidates`; `devin:cli-logs` uses `under(devinCLIRoots, "logs")`.

### Removed surface

- Provider `devin-gui` and both of its store kinds
  (`vscode-workspace-storage`, `cascade-conversations`).
- `devinDesktopRoots`, the `/Devin/User/` and `/Windsurf/User/` branches in
  `inferProvider`, and `devin-gui` from `isVSCodeChat`.
- README/CLAUDE.md claims about a desktop chat store and encrypted Cascade
  conversations. `scripts/devin-gui-decrypt` (a standalone recovery spike, not
  wired into the CLI) is untouched.

## Verification (2026-09-12, real install)

| Check | Method | Result |
| --- | --- | --- |
| One sessions.db | `find ~ -name sessions.db` | Exactly `~/.local/share/devin/cli/sessions.db` (28 MB, 68 rows). |
| Desktop sessions in it | SQL `backend_type` counts | 66 `Windsurf` + 2 `windsurf` — the desktop's sessions are CLI sessions. |
| Desktop is an ACP shell | `state.vscdb` global-storage keys | `windsurf.acp.*`, `windsurf.devin.settingsMigrationComplete` present. |
| New Windows default | `crossmem config` + docs.devin.ai troubleshooting | `%APPDATA%\devin\cli\logs\...` per official docs; config at `%APPDATA%\devin\config.json`. |
| Env overrides | `DEVIN_HOME` / `DEVIN_DB_PATH` test | `devinDB()` honors both, `DEVIN_DB_PATH` first. |
| `devin-gui` gone | `crossmem scan`, `StoreKeys()`, `Providers()` | No devin-gui anywhere. |

## Consequences

- `scan`, `config`, and `--provider` help are honest again: there is exactly
  one Devin store, and it is readable.
- A Devin installation relocated via `$DEVIN_HOME` or `$DEVIN_DB_PATH` is found
  without a config file, the same first-class treatment Claude (`$CLAUDE_CONFIG_DIR`)
  and Codex (`$CODEX_HOME`) already get.
- Windows users on the old `Cognition\cli` path who have not re-run the
  installer are the one case that no longer resolves; the official installer
  path is `devin\cli`, and the user can repoint via config if needed.
- ADR 1's "declared, not read" principle still stands for the stores that
  genuinely are unreadable (none currently); Devin is no longer one of them.

## Alternatives rejected

- **Keep `devin-gui` as a thin alias over `sessions.db`.** Every session would
  be listed twice (once per provider) with no way to tell a "desktop" session
  from a "CLI" session in the shared table — worse than useless.
- **Keep the Cascade `.pb` store as discovery-only.** The files are an
  encrypted legacy the desktop no longer uses as its session store; reporting
  them as a Devin store would mislead `scan` users into thinking those
  conversations matter. The standalone `devin-gui-decrypt` script remains for
  anyone who needs the legacy artifacts.
- **Keep `%APPDATA%\Cognition\cli` as a legacy fallback.** The vendor-folder
  install path is retired upstream and this project now ships the correct
  `%APPDATA%\devin\cli`; a legacy candidate would just be a stale branch that
  never matches on any current machine.

## Source files

- `internal/providers/paths.go` — `devinCLIRoots`, `devinSessionDBCandidates`, `storeDefinitions`, `inferProvider`, `isVSCodeChat`
- `internal/providers/list.go` — `devinDB`
- `internal/providers/context.go` — `extractObject`
- `internal/app/app.go` — `--provider` help, `configHelpText`
- Tests: `internal/providers/paths_test.go` (`TestDevinCandidatesCoverWindows`, `TestDevinDBHonorsEnvOverrides`, `TestDevinIsTheOnlySessionSource`, `TestDevinHasNoDesktopCascadeStore`)