# crossmem

[![Go Reference](https://pkg.go.dev/badge/github.com/muthuishere/crossmemcli.svg)](https://pkg.go.dev/github.com/muthuishere/crossmemcli)

Portable context memory across local agent tools. Hit a limit in one tool, resume in another.

`crossmem` reads the session stores Claude Code, Codex, Devin, Copilot (VS Code and CLI) and OpenCode already keep on your disk, and hands the next agent a clean brief. Local reads only, no telemetry.

**Docs: [muthuishere.github.io/crossmemcli](https://muthuishere.github.io/crossmemcli/)** — [CLI reference](https://muthuishere.github.io/crossmemcli/cli) · [the agent skill](https://muthuishere.github.io/crossmemcli/skill) · [stores & config](https://muthuishere.github.io/crossmemcli/stores)

## Install

```sh
brew install muthuishere/tap/crossmem
npm install -g @muthuishere/crossmem
go install github.com/muthuishere/crossmemcli/cmd/crossmem@latest
```

Installing the CLI installs the bundled `crossmem-loader` skill into `~/.claude/skills` and `~/.agents/skills` — that skill is how an agent knows crossmem exists. npm does it in `postinstall`, every other channel on the first command after an install or upgrade. Escape hatches: `crossmem install --skills --agents`, `crossmem uninstall --skills --agents`, `CROSSMEM_NO_SKILL_INSTALL=1`.

## Use

```sh
crossmem list . --limit 5            # this folder's recent sessions, all tools
crossmem load --session <ref> --full # load the one you picked
crossmem update .                    # write a committable .crossmem/
crossmem scan                        # which stores were found
crossmem help <command>
```

`list` shows each session's first and last question — what it was about, and where it stopped. Sessions match a folder by the real working directory in the transcript, including the repo's other git worktrees (`--no-worktrees` opts out). Your own live session is excluded unless you pass `--include-current`.

`update` writes `.crossmem/{context.md,guardrails.md,sessions.json,sources.json}` and is idempotent — no timestamps, unchanged files untouched, so re-running makes no diff.

Repo instruction files (`AGENTS.md`, `CLAUDE.md`, `.agents/AGENTS.md`, `.claude/CLAUDE.md`) are **authoritative**; transcripts are **context only**. Credential files, auth DBs, `*.env` and `vault/` are never read — including in `sync` dumps.

`export` writes one `qa.jsonl` of every question, answer and the tools in between, with no agent name, no model name and no tokens; `import` reads it back.

## As a Go library

```go
c, _ := crossmem.New(crossmem.Options{})          // no process-global state
sessions, _ := c.List(ctx, crossmem.ListOptions{CWD: root, Limit: 5})
md, _ := c.Load(ctx, sessions[0].Ref, crossmem.LoadFull)
```

Semver from v0.2.0; `Session`, `Store`, `QARecord`, `Transcript` and `Event` JSON names are frozen. [ADR 3](docs/adr/3-public-library-api.md) · `go run ./examples/list <folder>`.

## Config

`crossmem config` prints, per store, the paths searched on this machine and which exist. Override them in `~/.config/crossmemcli/config.json` (or `$CROSSMEM_CONFIG`; `--init` writes a starter):

```json
{
  "defaults": { "mode": "full", "limit": 10 },
  "stores": { "devin:sqlite-sessions": "D:/agents/devin/cli/sessions.db" },
  "extraStores": { "opencode": "~/other/opencode/opencode*.db" }
}
```

`stores` replaces a store's locations, `extraStores` adds to them. Keys are `provider:kind` or a bare `provider`. `~`, `%VAR%`, `$VAR` and globs expand; a path whose variable is unset is skipped, so one config works on several machines. `$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`, `$DEVIN_HOME` and `$DEVIN_DB_PATH` win over the defaults. Full store table: [stores & config](https://muthuishere.github.io/crossmemcli/stores).

## Develop

```sh
task ci                  # test + vet + build + smoke
task release -- 0.2.2    # version, tag, push — GitHub release, Homebrew and npm follow
```

Debugging: `CROSSMEM_DEBUG=1`, `CROSSMEM_LOG=/tmp/crossmem.log` (command flow only, never transcript contents). Release details: [docs/releasing.md](docs/releasing.md). Docs site: `site/` (Astro, `npm run dev`).
