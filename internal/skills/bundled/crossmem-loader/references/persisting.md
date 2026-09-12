# Persisting context so nobody re-pays for it

`crossmem load` prints to stdout and dies with the session. `crossmem update`
writes it to disk, where the next session — yours, another tool's, or a
teammate's — can read it for free.

```sh
crossmem update .
```

Writes `<folder>/.crossmem/`:

| file | what it is |
|---|---|
| `context.md` | the portable context bundle |
| `guardrails.md` | the repo's active instruction file references |
| `sessions.json` | metadata for the sessions that were selected |
| `sources.json` | which stores and instruction files were discovered |

Useful flags: `--full` (fuller excerpts), `--limit N`, `--provider <name>`.

**It is idempotent by design.** The files carry no generated-at timestamp, and a
file whose content has not changed is left untouched — so re-running produces no
diff and no spurious commit. That is what makes `.crossmem/` safe to commit.

## When to reach for it

- The user says "save this", "write it down", "so I don't have to explain again".
- Long work is ending and will continue in another session or another tool.
- A repo more than one person or agent works in — a committed `.crossmem/` is a
  shared starting brief.

## Reading it back

If `.crossmem/context.md` already exists in a folder, read it **before** running
`list`/`load` — it is a curated brief that costs one file read, where a full load
costs a whole transcript. Fall through to the load flow when it is missing, stale
against the work the user is describing, or too thin to act on.
