# Moving agent memory between machines

One dump format. `export` writes it, `import` restores it, `sync` moves it over
rclone. The dump is a plain directory — no archive step — so what syncs is exactly
what imports.

## Export (this machine → a dump)

```sh
crossmem export                                # default dir
crossmem export --out ~/.assets/convdump       # explicit
crossmem export --provider claude --json       # one provider, manifest as JSON
```

Copies every discoverable store (Claude, Codex, Copilot, Copilot CLI, Devin,
OpenCode) plus the well-known global instruction and memory files, into one
directory with a `manifest.json`.

Default dir is `dumpDir` from config, else `~/.assets/convdump`.

**Credential files, auth databases, `*.env`, and vault/cache/node_modules dirs are
never exported.** This is enforced in the CLI, not left to you — but still never
add a path by hand that reaches into one.

## Conversation export (training / RAG)

A different file from the store dump. `--qa` writes one `qa.jsonl` of every
question, the full answer, and the tools/results/thinking in between. No agent
name, no model name, no tokens. Use this when the user wants a corpus, not a
restoreable dump.

```sh
crossmem export --qa --out ~/conversations     # whole machine
crossmem export --qa .                         # this folder only
crossmem export --qa --dump --out ~/.assets/convdump
```

Each line is:

```json
{"sessionId":"...","folder":"/path","q":"...","a":"...","time":"...","messages":[{"role":"user","content":"..."},{"role":"assistant","content":"..."},{"role":"tool","name":"Write","content":"..."}]}
```

`--qa` does not copy original stores. Add `--dump` when they also want the
importable dump. Reads run in parallel; the file is renamed into place.

## Sync (dump ↔ remote)

```sh
crossmem sync --remote hetzbox:companydata/convdump          # push
crossmem sync --pull --remote hetzbox:companydata/convdump   # pull
crossmem sync --prune ...                                    # mirror: delete extras at the destination
```

Needs the `rclone` binary on PATH. With no `--remote`, it uses `sync.remote` from
the config. `--prune` deletes destination files absent from the source — confirm
with the user before using it against a remote that holds their only copy.

## Import (dump → this machine)

```sh
crossmem import --in ~/.assets/convdump --dry-run   # always do this first
crossmem import --in ~/.assets/convdump
```

Each store resolves to **this** machine's location, honouring the local config —
so a dump made on a Mac restores into the right Windows or Linux paths. Files whose
bytes already match are skipped, so re-imports are idempotent. `--force` overwrites
even identical files; it is rarely what you want.

**Always `--dry-run` first** and show the user what it reports. Import writes into
other tools' live session stores; that is worth one confirmation.

## The whole trip

```sh
# on the old machine
crossmem export && crossmem sync --remote hetzbox:companydata/convdump

# on the new machine
crossmem sync --pull --remote hetzbox:companydata/convdump
crossmem import --dry-run && crossmem import
crossmem list . --limit 5      # the other machine's sessions are now local
```
