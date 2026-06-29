---
name: crossmem-loader
description: Load portable project context from local Claude Code, Codex, Devin, and Copilot histories using the crossmem CLI. Use when the user says load context, resume from another agent, import Claude/Codex/Devin/Copilot memory, or asks what context is available for the current repo.
---

# CrossMem Loader

Use `crossmem` to find and load local context for the current repository or another folder the user names. This also applies when the active agent has spawned helper agents that need context for a specific repo.

## Commands

```sh
crossmem scan
crossmem list --provider all --limit 20
crossmem load . --limit 5
crossmem load /path/to/repo --limit 5
crossmem update .
```

When loading context, start with the smallest useful bundle:

```sh
crossmem load . --limit 3
```

Decide whether to summarize or load fuller excerpts in the agent session. The CLI should be treated as the deterministic context source, not as the summarizer.

Do not read credential files, env files, auth databases, or `vault/` directories.

## Active Repo Instructions

Before acting on loaded context, check whether the bundle contains an Active Repo Instructions section. Read the referenced `AGENTS.md` / `CLAUDE.md` files before proceeding. Treat those files as authoritative instructions; session history is context only. If multiple continuations are possible, ask the user which one to resume.
