---
name: crossmem-loader
description: Restore prior work context for the current folder using the crossmem CLI — it pulls the last real session from local Claude Code, Codex, Devin (CLI and desktop), Copilot (VS Code and CLI), and OpenCode histories. Use this FIRST, before listing or reading files, whenever the user says any of: pick up where I left off, resume, continue where I was, what was I doing here, where did I leave off, catch me up on this folder, load context, load my last session, resume from another agent/tool, or import Claude/Codex/Devin/Copilot/OpenCode memory.
---

# CrossMem Loader

Restore context for the current folder when work moves between agents — e.g. Codex
hits a usage limit and the user reopens the same folder in Claude Code.

**Invoke this skill immediately** on any resume / continue / "where did I leave off"
request for a folder. Do **not** `ls` or read files first to guess — crossmem is the
way to recover prior context across tools; run it before manual inspection.

`crossmem` is deterministic plumbing: it finds the folder's sessions across every
tool and emits their raw conversation. **You** decide which session to resume and
what in it is signal vs. noise.

## The load flow

When the user asks to load context / resume / pick up where they left off:

1. **List the recent sessions for this folder:**

   ```sh
   crossmem list . --limit 5
   ```

   Searches every tool (Claude, Codex, Copilot, Copilot CLI, Devin CLI, Devin
   desktop, OpenCode), newest first. Each row carries what you need to choose:

   ```
   2026-08-29T12:07  claude   279691  /Users/…/5a982600-….jsonl
     workspace: /Users/…/apl
     title:     In-memory work with claude.md and ctx-optimize
     first:     do it in memory for this folder claude.md use ctx-optimize for…
     last:      now wire the oauth consent screen and verify the callback
   ```

   The last column is the handle: a transcript path, or `devin:<id>`. If nothing
   matches, name the folder: `crossmem list /path/to/repo --limit 5`.

   **The session you are in right now is already excluded** — crossmem detects it
   from the agent's own session id, automatically for Claude Code, the Devin CLI,
   and Codex. (`--include-current` brings it back if you ever need it.)

   Copilot and OpenCode export no session id, so if you are running inside one of
   those, the newest row may still be this conversation — its `first`/`last` will
   read as the request you are handling right now. Skip it, and export
   `CROSSMEM_CURRENT_SESSION=<id>` to have crossmem filter it for you.

2. **Choose by content, not by recency.** `title` is what the session was called;
   `first` and `last` are the opening and closing question, and they are what tell
   two sessions in one folder apart. Rank them:

   - **`last` describes unfinished work** that matches what the user now wants —
     strongest signal, pick it. This is usually where they stopped.
   - **`first` states the goal** the user is now referring to — next strongest.
   - **Same workspace** as the folder in play — required, already filtered for.
   - **Recency** — a tiebreak between otherwise equal candidates, never the reason
     on its own.

   **If two or more sessions are plausibly the right one, ask.** Show them as a
   short numbered list with title / first / last, and let the user pick — guessing
   wrong costs them a whole reconstructed context. Ask only when genuinely torn;
   one clear winner means just load it.

3. **Load the chosen session in full:**

   ```sh
   crossmem load --session <handle> --full
   ```

   Always `--full` here: the brief must be built from the complete session, not a
   truncated slice. (A user who always wants full can set it as a standing default
   with `crossmem config --init` and `"defaults": { "mode": "full" }` — but pass
   `--full` explicitly anyway, so the skill does not depend on their config.)

4. **Write the brief — this is where you filter noise.** The raw transcript contains
   boilerplate you must IGNORE:

   - harness-injected instruction blocks: `# AGENTS.md`, `# CLAUDE.md`,
     `<INSTRUCTIONS>…</INSTRUCTIONS>`, `<system-reminder>`, command hooks;
   - pasted `# CrossMem Context Bundle` blocks from earlier loads;
   - crossmem-loading narration ("I'll list the sessions", "skip the current
     session", "load --session", "loaded the prior context", etc.).

   From the *real* work that remains, synthesize:

   - **Persona / role** the work was operating as.
   - **Decisions** — capture *all* of them (what was built, chosen, rejected, why).
   - **Current state** — what exists now / what's next.

5. **Resume** the work that brief describes.

## Safety

Do not read credential files, `*.env`, auth databases, or `vault/` directories.
crossmem already avoids these; never paste secret values into loaded context.
