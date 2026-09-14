# When crossmem can't find a store

Symptom: `list` returns nothing, or a tool the user definitely uses is missing.
Work the ladder — do not guess at paths, and never hardcode one.

## 1. What does it actually see?

```sh
crossmem scan          # every store, whether it exists, file count, bytes
crossmem scan --json
```

Each row gives the provider, the path, its `kind`, `exists`, `files`, `bytes`, and
a note on what the store is. A store with `exists: true` but `files: 0` is a real
directory the tool has not written to yet — different problem from a missing path.

## 2. Where is it looking, and what is the override key?

```sh
crossmem config          # every candidate path per store, and which exist
crossmem config --json
```

crossmem already knows the macOS, Linux, and Windows locations of each store. The
config file is only for what it cannot know: a portable install, a second drive, a
custom data dir.

## 3. Point it at the real location

```sh
crossmem config --init   # writes a starter config if none exists
```

`~/.config/crossmemcli/config.json` (override the whole path with `$CROSSMEM_CONFIG`):

```json
{
  "defaults": { "mode": "full", "limit": 5 },
  "stores":      { "devin:sqlite-sessions": "D:/agents/devin/cli/sessions.db",
                   "claude": ["~/work/.claude/projects"] },
  "extraStores": { "opencode": "~/other/opencode/opencode*.db" }
}
```

- `stores` **replaces** the built-in locations for that store. `extraStores`
  **keeps** them and adds more — prefer `extraStores` unless the built-ins are
  actively wrong.
- Keys are `provider:kind`, or a bare `provider` for its **primary** store only.
- Values take `~`, `%VAR%`, `$VAR`, and `*` globs. A path whose variable is unset
  is silently skipped — that is how a Windows-only candidate drops out on macOS.
- An unknown key is a load error. `crossmem config` surfaces it; every other
  command ignores the bad config and runs on the built-ins, so if an override
  "does nothing", run `crossmem config` to see the error.

`defaults` also sets standing behaviour — `mode` (`summary`/`full`) and `limit` —
with any command-line flag winning over it.

## 4. Relevant environment variables

`$CLAUDE_CONFIG_DIR`, `$CODEX_HOME`, `$DEVIN_HOME`, `$DEVIN_DB_PATH` relocate their
tool's store and are honoured ahead of the defaults. If the user relocated a config
dir, that is usually the whole answer.

## 5. Debugging a run

```sh
CROSSMEM_DEBUG=1 crossmem scan
CROSSMEM_LOG=/tmp/crossmem.log crossmem load . --limit 5
```

No transcript contents are ever logged.

## Transient failures

Active agents write these files while crossmem reads them. Reads already retry
(3 attempts, small backoff) on transient errors — "database is locked", "busy",
"too many open files" — and never on not-exist/permission. A single "database is
locked" that survives retry usually means a tool is mid-write: wait a beat and
re-run rather than reaching for `--force` anything.
