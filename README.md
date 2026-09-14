# crossmem

[![Go Reference](https://pkg.go.dev/badge/github.com/muthuishere/crossmemcli.svg)](https://pkg.go.dev/github.com/muthuishere/crossmemcli)

Portable context memory across local agent tools.

`crossmem` discovers local Claude Code, Codex, Devin, Copilot (VS Code and CLI), and OpenCode session stores, lists available sessions, and emits a clean context bundle that can be loaded into another agent session. Go docs: [pkg.go.dev/github.com/muthuishere/crossmemcli](https://pkg.go.dev/github.com/muthuishere/crossmemcli).

It is primarily a fast local context CLI. Skills are optional global integration points for agents that support `SKILL.md`.

## Install

### Go

```sh
go install github.com/muthuishere/crossmemcli/cmd/crossmem@latest
```

### npm

```sh
npm install -g @muthuishere/crossmem
```

The npm package is a thin launcher that resolves a prebuilt platform package, following the same pattern as `windowctl`.

### Homebrew

```sh
brew install muthuishere/tap/crossmem
```

## Resume across tools

The core flow: one tool (say Codex) hits its usage limit, you reopen the **same
folder** in another (say Claude Code), and pick up where you left off.

```sh
# 1. From the folder, load its latest session (summary by default)
crossmem load . --limit 1

# 2. Prefer to choose? List recent sessions for THIS folder, newest first
crossmem list . --limit 5
#   2026-06-29T14:27  codex   /Users/you/.codex/sessions/.../rollout-….jsonl
#   2026-06-29T04:10  devin   devin:narrow-action
#   2026-06-28T21:02  claude  /Users/you/.claude/projects/.../<id>.jsonl

# 3. Load the one you picked, by the handle in the last column
crossmem load --session <handle>            # a .jsonl path, or devin:<id>
crossmem load --session <handle> --full     # fuller excerpt instead of the summary
```

### Choosing a session

`crossmem list` shows, for every session, its title plus the **first and last thing the user asked** in it. A title says what a session was called; the first and last question say what it became, which is what distinguishes two sessions in the same folder:

```
2026-08-29T12:07  claude   279691  /Users/…/5a982600-….jsonl
  workspace: /Users/…/apl
  title:     In-memory work with claude.md and ctx-optimize
  first:     do it in memory for this folder claude.md use ctx-optimize for…
  last:      now wire the oauth consent screen and verify the callback
```

Tool output is skipped when picking those questions — agents record their own tool results as user turns, so the naive "last user message" is usually a shell transcript, not a question.

**The session you are running inside is excluded by default.** crossmem reads the agent's own session id from the environment, so `load .` means the *previous* session rather than handing your live conversation back to you. `--include-current` overrides it.

| Tool | Live session detected via | Automatic |
| --- | --- | --- |
| Claude Code | `CLAUDE_CODE_SESSION_ID` | yes |
| Devin CLI | `DEVIN_SESSION_ID` (set by Devin's shell integration) | yes |
| Codex | `CODEX_THREAD_ID` (a suffix of the rollout filename) | yes |
| Copilot (VS Code and CLI), OpenCode | neither binary exports a session id | no — set `CROSSMEM_CURRENT_SESSION` |

`CROSSMEM_CURRENT_SESSION` accepts a comma-separated list and works for every provider, so a tool without an exported id can still be excluded.

`crossmem` matches a session to a folder by the **real working directory** recorded
in each transcript — so it works even when the folder name contains a dash, and
across every tool. The handle from `list` is uniform (`--session` accepts a
transcript path or `devin:<id>`), so loading is identical whether the session lived
in a JSONL file or a SQLite database. By default `load` emits a compact,
summary-friendly excerpt; add `--full` for a larger, more verbatim one.

## Usage

```sh
crossmem scan                                  # discover known local context stores
crossmem list . --limit 5                      # recent sessions for THIS folder
crossmem list --provider claude --limit 20     # recent Claude sessions everywhere
crossmem load .                                # context bundle for this repo
crossmem load --session <handle> --full        # one chosen session, fuller excerpt
crossmem load . --provider codex --out .crossmem/context.md
crossmem update .                              # write durable .crossmem/ files (idempotent)
crossmem export .                              # write .crossmem/qa.jsonl for this folder
crossmem export --out ~/conversations          # whole-machine qa.jsonl
crossmem import . --in ~/conversations         # import qa.jsonl into this folder
crossmem sync --remote hetzbox:companydata/convdump   # push a store dump with rclone
```

`crossmem update` is idempotent: the files it writes carry no generated-at timestamp and are left untouched when their content has not changed, so re-running produces no diff and no mtime churn.

Use command help for the full option set:

```sh
crossmem --version
crossmem help load
crossmem help list
```

## Use as a Go library

`pkg/crossmem` is the same engine the CLI runs, with no process-global state, so an embedding program (a Go agent orchestrator, for example) can link it instead of shelling out to whatever `crossmem` is on `PATH`:

```sh
go get github.com/muthuishere/crossmemcli@latest
```

```go
c, err := crossmem.New(crossmem.Options{}) // config, current session, debug: all per client
sessions, err := c.List(ctx, crossmem.ListOptions{CWD: root, Limit: 5})
tr, err := c.Transcript(ctx, sessions[0].Ref)          // typed events: role, name, content, time
md, err := c.Load(ctx, sessions[0].Ref, crossmem.LoadFull) // the Markdown bundle `load` prints
rules, err := c.Guardrails(root)                       // authoritative repo instructions
```

`Options.Config` points a client at its own stores without environment variables; several clients with different configs can run side by side. OpenCode subagent sessions are folded into their parent's `Children` unless `ListOptions.IncludeSubagents` is set. `pkg/skillinstall` installs any skill directory from an `fs.FS` the way `crossmem install --skills` does. The package follows semver from v0.2.0 and the JSON field names of `Session`, `Store`, `QARecord`, `Transcript`, and `Event` are frozen. Design and verification: [ADR 3](docs/adr/3-public-library-api.md); runnable example: `go run ./examples/list <folder>`.

## Export / import (qa.jsonl)

`crossmem export` writes one `qa.jsonl` of every question, the full answer, and
the tools/results/thinking in between — **no agent name, no model name, no
tokens**. `import` reads that same file back. That is the only export/import
format.

Each line:

```json
{"sessionId":"...","folder":"/path","q":"...","a":"...","time":"...","messages":[{"role":"user","content":"..."},{"role":"assistant","content":"..."},{"role":"tool","name":"Write","content":"..."}]}
```

```sh
crossmem export .                              # this folder → .crossmem/qa.jsonl
crossmem export --out ~/conversations          # whole machine
crossmem import . --in ~/conversations         # copy qa.jsonl into this folder
crossmem import . --in ~/conversations --merge --dry-run
```

```text
.crossmem/qa.jsonl
```

`sync` is separate: it rclone-copies a **store dump** (original transcripts) for
moving machines. It does not go through `export`/`import`.

Set a standing remote and dump dir in the config instead of typing them every
time:

```json
{
  "dumpDir": "~/.assets/convdump",
  "sync": { "remote": "hetzbox:companydata/convdump" }
}
```

## Local Stores

| Tool | Store | Notes |
| --- | --- | --- |
| Claude Code | `~/.claude/projects/<encoded-workspace>/*.jsonl` | Main transcript JSONL files. Subagents can appear under `<session-id>/subagents/*.jsonl`. Project memory is under `~/.claude/projects/<encoded-workspace>/memory/`. |
| Codex | `~/.codex/sessions/YYYY/MM/DD/*.jsonl` | Session JSONL files. |
| Codex | `~/.codex/logs_2.sqlite` | Structured log database. |
| Codex | `~/.codex/history.jsonl` | Prompt history. |
| Copilot in VS Code | `<code-user>/workspaceStorage/<id>/chatSessions/*.jsonl` | VS Code chat session JSONL files. |
| Copilot in VS Code | `<code-user>/workspaceStorage/<id>/GitHub.copilot-chat/transcripts/*.jsonl` | Copilot transcript JSONL files where available. |
| Copilot CLI | `~/.copilot/session-store.db` | SQLite DB with `sessions` and a denormalized `turns` table. |
| Devin CLI | `~/.local/share/devin/cli/sessions.db` | One SQLite DB holds every session, whether driven from the CLI or the desktop app's ACP connector. Windows default is `%APPDATA%\devin\cli\sessions.db`; `$DEVIN_HOME` relocates the data dir, `$DEVIN_DB_PATH` points straight at the file. |
| Devin CLI | `<devin-cli>/logs/*.log` | CLI logs. Primary resumable conversation content is in `sessions.db`. |
| Devin CLI | `~/.local/share/devin/credentials.toml` | Credentials file. Deliberately not read by this tool. |
| OpenCode | `<opencode-data>/opencode*.db` | SQLite DB with `session`, `message`, and `part` tables. |

Store locations differ per platform, and `crossmem` knows all of them:

| Placeholder | macOS | Linux | Windows |
| --- | --- | --- | --- |
| `<devin-cli>` | `~/.local/share/devin/cli` | `~/.local/share/devin/cli` | `%APPDATA%\devin\cli` |
| `<opencode-data>` | `~/.local/share/opencode` | `~/.local/share/opencode` | `%APPDATA%\opencode` |
| `<code-user>` | `~/Library/Application Support/Code/User` | `~/.config/Code/User` | `%APPDATA%\Code\User` |

**What this means for the Devin desktop app:** there is no separate desktop
chat store. The desktop drives the Devin CLI through an ACP connector, so every
session — desktop or terminal-driven — lands in the one `sessions.db` and shows
up under the `devin` provider. A relocated Devin install is found via
`$DEVIN_HOME` (data dir) or `$DEVIN_DB_PATH` (the database file), both honored
before the defaults. See [ADR 2](docs/adr/2-devin-single-sessions-db.md).

Claude Code, Codex, and the Copilot CLI use the same `~`-relative paths on every platform; `$CLAUDE_CONFIG_DIR` and `$CODEX_HOME` are honored and win when set. `%LOCALAPPDATA%` and `$XDG_DATA_HOME` are checked too. Run `crossmem config` for the exact list on your machine.

## Store Overrides

Run `crossmem config` to see, per store, the paths `crossmem` looks in and which ones it found. When a tool keeps its sessions somewhere else — a portable install, a second drive, a custom data directory — point `crossmem` at it in `~/.config/crossmemcli/config.json` (override the file location with `$CROSSMEM_CONFIG`):

```json
{
  "stores": {
    "devin:sqlite-sessions": "D:/agents/devin/cli/sessions.db",
    "claude": ["~/work/.claude/projects"]
  },
  "extraStores": {
    "opencode": "~/other/opencode/opencode*.db"
  }
}
```

- `defaults` sets what happens when no flag is given: `mode` is `summary` or `full`, `limit` is a session count. A flag on the command line always wins, so `"mode": "full"` means you never type `--full` again.
- `stores` replaces a store's built-in locations; `extraStores` keeps them and adds more.
- Keys are `provider:kind` (as printed by `crossmem scan`), or a bare `provider` for that provider's primary store.
- Values are a path string or a list of them. `~`, `%VAR%`, `$VAR`, and `*` globs are expanded, and a path whose variable is unset on the current machine is skipped — so one config file can be shared across machines.

`crossmem config --init` writes a starter file.

## Safety

- Do not read `*.env`, credential files, auth DBs, or `vault/` directories.
- Treat env vars with `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, or `_PW` as use-only secrets.
- Prefer JSONL transcript files and known safe SQLite metadata over auth/config stores.

## Production Notes

`crossmem` does not send telemetry anywhere. Observability is local and opt-in:

```sh
CROSSMEM_DEBUG=1 crossmem scan
CROSSMEM_LOG=/tmp/crossmem.log crossmem load . --limit 5
```

Debug logs include command flow and local read/query failures, but not transcript contents. Local file and SQLite reads use small retries for transient races with active agent writers.

Release builds include version metadata:

```sh
crossmem --version
```

## npm Trusted Publishing

npm releases are published by `.github/workflows/npm-publish.yml` using GitHub Actions OIDC trusted publishing. The workflow builds all native packages with GoReleaser, publishes the platform packages first, and publishes `@muthuishere/crossmem` last.

Configure the same trusted publisher for every npm package:

```sh
for package in \
  @muthuishere/crossmem-darwin-arm64 \
  @muthuishere/crossmem-darwin-x64 \
  @muthuishere/crossmem-linux-arm64 \
  @muthuishere/crossmem-linux-x64 \
  @muthuishere/crossmem-windows-x64 \
  @muthuishere/crossmem
do
  npm trust github "$package" \
    --repo muthuishere/crossmemcli \
    --file npm-publish.yml \
    --allow-publish \
    --yes
  sleep 2
done
```

The packages must already exist on npm before running the trust commands. The workflow does not use `NODE_AUTH_TOKEN`; npm exchanges the GitHub OIDC token during `npm publish --provenance`.

## Skill Install

Optional global skill activation follows the same shape as `windowctl`:

```sh
crossmem install --skills
crossmem uninstall --skills
```

This installs the bundled `crossmem-loader` skill globally into:

- `~/.claude/skills/crossmem-loader`
- `~/.agents/skills/crossmem-loader` when `codex` is on `PATH`

Pass `--agents` to force the agents target even when `codex` is not on `PATH`:

```sh
crossmem install --skills --agents
```

`crossmem` does not install repo-local skills by default. The product is a global, cross-repo context layer for Claude Code, Codex, Devin, Copilot (VS Code and CLI), OpenCode, and spawned agent processes that need to ask "what context exists for this folder?".

## Context Update

`crossmem load .` prints a context bundle. `crossmem update .` writes the durable local context files:

```text
.crossmem/
  context.md
  guardrails.md
  sources.json
  sessions.json
```

`context.md` is the portable context bundle. Summarization policy is intentionally left to the consuming agent or the `crossmem-loader` skill, because different agents and tasks need different amounts of history.

Active repo instructions are gathered from repo-local instruction files first:

```text
AGENTS.md
CLAUDE.md
.agents/AGENTS.md
.claude/CLAUDE.md
```

The loader skill should tell the agent to read those files as authoritative instructions before using session history.
