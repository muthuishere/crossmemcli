# crossmem

**Portable context memory across local agent tools.**

`crossmem` discovers your local Claude Code, Codex, Devin, and Copilot session stores, lists available sessions, and emits a clean context bundle that can be loaded into another agent session — so context follows you across tools and repos.

It is primarily a fast, local-first context CLI. It sends no telemetry; everything is local reads.

## Install

```sh
npm install -g @muthuishere/crossmem
```

This package is a thin launcher that resolves a prebuilt native binary for your platform (`darwin-arm64`, `darwin-x64`, `linux-arm64`, `linux-x64`, `windows-x64`) via optional dependencies — no compiler or Go toolchain required.

Also available via:

```sh
go install github.com/muthuishere/crossmemcli/cmd/crossmem@latest   # Go
brew install muthuishere/tap/crossmem                               # Homebrew
```

## Usage

```sh
crossmem scan                                  # discover known local context stores
crossmem list --provider claude --limit 20     # list recent sessions
crossmem list --provider devin --limit 10
crossmem load .                                # print a portable context bundle for this repo
crossmem load . --provider codex --out .crossmem/context.md
crossmem update .                              # write durable .crossmem/ context files
```

Every command has built-in help:

```sh
crossmem --version
crossmem help load
crossmem help scan
```

### Commands

| Command | What it does |
| --- | --- |
| `scan` | Discover known local stores without reading transcript contents. |
| `list` / `sessions` | List available sessions across stores; filter with `--provider` / `--folder`. |
| `load` / `context` | Print a portable context bundle for a repo or folder. |
| `update` | Write durable `<folder>/.crossmem/` files (`context.md`, `guardrails.md`, `sessions.json`, `sources.json`). |
| `guardrails` | Print the repo instruction files an agent should read first. |
| `install --skills` | Install the optional global `crossmem-loader` skill. |

## Local stores

`crossmem` reads these on-disk stores (read-only):

| Tool | Store |
| --- | --- |
| Claude Code | `~/.claude/projects/<encoded-workspace>/*.jsonl` (+ per-project `memory/`) |
| Codex | `~/.codex/sessions/YYYY/MM/DD/*.jsonl`, `~/.codex/history.jsonl` |
| Copilot (VS Code) | `~/Library/Application Support/Code/User/workspaceStorage/<id>/.../*.jsonl` |
| Devin CLI | `~/.local/share/devin/cli/sessions.db` (SQLite) |

## Context bundles & guardrails

`crossmem load .` prints a bundle; `crossmem update .` writes it durably under `.crossmem/`. A bundle separates two things on purpose:

- **Guardrails** — repo instruction files (`AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md`) treated as *authoritative instructions*.
- **History** — recent session previews, treated as *context only*.

Summarization is intentionally left to the consuming agent (or the `crossmem-loader` skill), since different tasks need different amounts of history.

## Safety

`crossmem` reads other tools' private stores, so it is deliberately conservative:

- It never reads `*.env`, credential files, auth databases, or `vault/` directories (e.g. Devin's `credentials.toml` is skipped).
- Secret values are never written into generated context.

## Debugging

Observability is local and opt-in:

```sh
CROSSMEM_DEBUG=1 crossmem scan
CROSSMEM_LOG=/tmp/crossmem.log crossmem load . --limit 5
```

Debug logs include command flow and local read/query failures — never transcript contents.

## Links

- Source & full docs: https://github.com/muthuishere/crossmemcli
- Issues: https://github.com/muthuishere/crossmemcli/issues

MIT © Muthukumaran Navaneethakrishnan
