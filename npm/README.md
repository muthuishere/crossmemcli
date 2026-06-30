# crossmem

**Portable context memory across local agent tools.**

`crossmem` discovers your local Claude Code, Codex, Devin, Copilot (VS Code and CLI), and OpenCode session stores, lists available sessions, and emits a clean context bundle that can be loaded into another agent session — so context follows you across tools and repos.

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

## Resume across tools

The core flow: Codex hits its usage limit, you reopen the **same folder** in Claude
Code, and pick up where you left off.

```sh
# 1. From the folder, load the latest session for it (summary by default)
crossmem load . --limit 1

# 2. Prefer to choose? List the recent sessions for THIS folder, newest first
crossmem list . --limit 5
#   2026-06-29T14:27  codex   /Users/you/.codex/sessions/.../rollout-….jsonl
#   2026-06-29T04:10  devin   devin:narrow-action
#   2026-06-28T21:02  claude  /Users/you/.claude/projects/.../<id>.jsonl

# 3. Load the one you picked, by the handle in the last column
crossmem load --session <handle>            # e.g. a .jsonl path, or devin:<id>
crossmem load --session <handle> --full     # fuller excerpt instead of the summary
```

`crossmem` matches a session to a folder by the **real working directory** recorded
in each transcript — so it works even when the folder name contains a dash, and
across every tool. The handle from `list` is uniform (`--session` takes a transcript
path or `devin:<id>`), so loading is the same whether the session lived in a JSONL
file or a SQLite database.

**summary vs full** — by default `load` emits a compact, summary-friendly excerpt per
session; add `--full` for a larger, more verbatim excerpt.

## Usage

```sh
crossmem scan                                  # discover known local context stores
crossmem list . --limit 5                      # recent sessions for THIS folder
crossmem list --provider claude --limit 20     # recent Claude sessions everywhere
crossmem load .                                # portable context bundle for this repo
crossmem load --session <handle> --full        # one chosen session, fuller excerpt
crossmem update .                              # write durable .crossmem/ context files
```

Every command has built-in help: `crossmem help load`, `crossmem --version`.

### Commands

| Command | What it does |
| --- | --- |
| `scan` | Discover known local stores without reading transcript contents. |
| `list` / `sessions` | List recent sessions; pass a folder to scope to it, or filter with `--provider`. |
| `load` / `context` | Print a context bundle for a folder, or one session via `--session`; `--full` for more. |
| `update` | Write durable `<folder>/.crossmem/` files (`context.md`, `guardrails.md`, `sessions.json`, `sources.json`). |
| `guardrails` | Print the repo instruction files an agent should read first. |
| `install --skills` | Install the optional global `crossmem-loader` skill that drives this flow. |

## Local stores

`crossmem` reads these on-disk stores (read-only):

| Tool | Store |
| --- | --- |
| Claude Code | `~/.claude/projects/<encoded-workspace>/*.jsonl` (+ per-project `memory/`) |
| Codex | `~/.codex/sessions/YYYY/MM/DD/*.jsonl`, `~/.codex/history.jsonl` |
| Copilot (VS Code) | `~/Library/Application Support/Code/User/workspaceStorage/<id>/.../*.jsonl` |
| Devin CLI | `~/.local/share/devin/cli/sessions.db` (SQLite) |

## Bundles & guardrails

`crossmem load` prints a bundle; `crossmem update .` writes it durably under `.crossmem/`. A bundle separates two things on purpose:

- **Guardrails** — repo instruction files (`AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md`) treated as *authoritative instructions*.
- **History** — recent session excerpts, treated as *context only*.

How much to summarize is left to the consuming agent (or the `crossmem-loader` skill), since different tasks need different amounts of history.

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
