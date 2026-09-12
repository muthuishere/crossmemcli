# Export / import conversations

`export` writes one `qa.jsonl`. `import` reads it back. That is the only
export/import format — not a dump of original stores, not a raw jsonl of tokens
or model names.

Each line is a full exchange: sessionId, folder, question, answer, time, and
`messages` (user / assistant / tool / thinking). No agent name, no model name,
no tokens.

## Export

```sh
crossmem export .                              # this folder → .crossmem/qa.jsonl
crossmem export --out ~/conversations          # whole machine
crossmem export --provider claude --json
```

Pass a folder to keep only sessions whose working directory is that folder.
Omit it to export every session on the machine.

`--out` is the destination **directory**. Default is `<folder>/.crossmem` when a
folder is passed, else `dumpDir` from config / `~/.assets/convdump`.

Use this when the user wants a training corpus, a RAG corpus, or to carry
conversations to another machine.

## Import

```sh
crossmem import --in ~/conversations           # into dumpDir
crossmem import . --in ~/conversations         # into ./.crossmem/qa.jsonl
crossmem import . --in ~/conversations/qa.jsonl --merge --dry-run
```

`--in` may be the `qa.jsonl` file or a directory that contains one. `--merge`
appends pairs that are not already in the destination. `--dry-run` reports
counts without writing.

Always `--dry-run` first if the destination already has a corpus, and show the
user what it reports.

## The whole trip

```sh
# on the old machine
crossmem export --out ~/conversations

# copy ~/conversations/qa.jsonl however they like

# on the new machine
crossmem import . --in ~/conversations --dry-run
crossmem import . --in ~/conversations
```

## Store dump (`sync` only)

Moving **original** session stores between machines is `sync`, not export/import.
`sync` rclone-copies a store dump. Do not offer `export`/`import` for that.
