# crossmem

Portable context memory across local agent tools.

`crossmem` discovers local Claude Code, Codex, Devin, and Copilot session stores, lists available sessions, and emits a clean context bundle that can be loaded into another agent session.

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

## Usage

```sh
crossmem scan
crossmem list --provider claude --limit 20
crossmem list --provider devin --limit 10
crossmem load . --limit 5
crossmem load . --provider codex --out .crossmem/context.md
crossmem update .
crossmem install --skills
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

## Skill Install

Global activation follows the same shape as `windowctl`:

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
