# crossmem-loader

Agent skill for loading portable context from local agent histories.

Install globally through the CLI:

```sh
crossmem install --skills
```

Typical usage inside an agent session:

```sh
crossmem load . --limit 5
crossmem load /path/to/repo --limit 5
```

The skill decides whether to summarize the loaded context or request a fuller bundle. The CLI remains the deterministic local context source.
