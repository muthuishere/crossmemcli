# crossmem-loader

Agent skill for portable agent memory across local tools — resuming prior work,
persisting a durable brief, moving history between machines, and diagnosing a
store crossmem cannot find.

Install globally through the CLI:

```sh
crossmem install --skills
```

Layout:

- `SKILL.md` — routing table, the load hot path, guardrails vs. history, safety.
- `references/persisting.md` — `crossmem update` and the committable `.crossmem/`.
- `references/portability.md` — `export` / `sync` / `import` between machines, plus `export --qa` for a training/RAG jsonl.
- `references/troubleshooting.md` — `scan`, `config`, env overrides, debug logging.

The CLI stays the deterministic local context source; the skill decides which
session matters and what in it is signal.
