---
name: crossmem-loader
description: Restore prior work context for the current folder using the crossmem CLI — it pulls the last real session from local Claude Code, Codex, Devin, Copilot, and OpenCode histories. Use this FIRST, before listing or reading files, whenever the user says any of: pick up where I left off, resume, continue where I was, what was I doing here, where did I leave off, catch me up on this folder, load context, load my last session, resume from another agent/tool, or import Claude/Codex/Devin/Copilot/OpenCode memory.
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

   This searches all tools (Claude, Codex, Copilot, Devin, OpenCode), newest first. Each row's
   last column is a handle: a transcript path, or `devin:<id>`. If nothing matches,
   name the folder: `crossmem list /path/to/repo --limit 5`.

2. **Pick the session to resume — skip the live one.** The newest row is almost
   always THE SESSION YOU ARE IN RIGHT NOW (same tool, timestamp ≈ now, its content
   is this very conversation). That is not what to resume — skip it. Resume the most
   recent *prior* session (often the previous tool). If unsure which is live, show
   the list and ask.

3. **Load the full session** — always pass `--full` so the brief is built from the
   complete session, not a truncated slice (this is what produces a good summary):

   ```sh
   crossmem load --session <handle> --full
   ```

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
