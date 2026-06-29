---
name: crossmem-loader
description: Load portable project context from local Claude Code, Codex, Devin, and Copilot histories using the crossmem CLI. Use when the user says load context, resume from another agent, import Claude/Codex/Devin/Copilot memory, or asks what context is available for the current repo.
---

# CrossMem Loader

Use `crossmem` to find and load local context for the current repository.

## Commands

```sh
crossmem scan
crossmem list --provider all --limit 20
crossmem load . --limit 5
```

When loading context, prefer the smallest useful bundle:

```sh
crossmem load . --limit 3
```

Do not read credential files, env files, auth databases, or `vault/` directories.
