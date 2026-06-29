# crossmem

Portable context memory across local agent tools.

`crossmem` discovers local Claude Code, Codex, Devin, and Copilot session stores, lists available sessions, and emits a clean context bundle that can be loaded into another agent session.

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
crossmem update .                              # write durable .crossmem/ files
```

Use command help for the full option set:

```sh
crossmem --version
crossmem help load
crossmem help list
```

## Local Stores

| Tool | Store | Notes |
| --- | --- | --- |
| Claude Code | `~/.claude/projects/<encoded-workspace>/*.jsonl` | Main transcript JSONL files. Subagents can appear under `<session-id>/subagents/*.jsonl`. Project memory is under `~/.claude/projects/<encoded-workspace>/memory/`. |
| Codex | `~/.codex/sessions/YYYY/MM/DD/*.jsonl` | Session JSONL files. |
| Codex | `~/.codex/logs_2.sqlite` | Structured log database. |
| Codex | `~/.codex/history.jsonl` | Prompt history. |
| Copilot in VS Code | `~/Library/Application Support/Code/User/workspaceStorage/<id>/chatSessions/*.jsonl` | VS Code chat session JSONL files. |
| Copilot in VS Code | `~/Library/Application Support/Code/User/workspaceStorage/<id>/GitHub.copilot-chat/transcripts/*.jsonl` | Copilot transcript JSONL files where available. |
| Devin CLI | `~/.local/share/devin/cli/sessions.db` | SQLite DB with `sessions`, `prompt_history`, `message_nodes`, `rendered_commits`, and `tool_call_state`. |
| Devin CLI | `~/.local/share/devin/cli/logs/*.log` | CLI logs. Primary resumable conversation content is in `sessions.db`. |
| Devin CLI | `~/.local/share/devin/credentials.toml` | Credentials file. Deliberately not read by this tool. |

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

`crossmem` does not install repo-local skills by default. The product is a global, cross-repo context layer for Claude Code, Codex, Devin, Copilot, and spawned agent processes that need to ask "what context exists for this folder?".

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
