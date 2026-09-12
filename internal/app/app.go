package app

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/muthuishere/crossmemcli/internal/providers"
	"github.com/muthuishere/crossmemcli/internal/skills"
	"github.com/muthuishere/crossmemcli/internal/version"
)

const helpText = `Usage: crossmem [options] [command]

Portable context memory across local agent tools.

Options:
  -V, --version                           output the version number
  -h, --help                              display help for command

Commands:
  scan [options]                          discover local agent session stores on this machine
  list [options]                          list available sessions across stores
  sessions [options]                      alias for list
  load [options] [folder]                 print a portable context bundle for a repo or folder
  context [options] [folder]              alias for load
  update [options] [folder]               write .crossmem/context.md and source manifests
  guardrails [folder]                     print active repo instruction file references
  export [options]                        copy all stores + instructions into one portable dump
  import [options]                        restore a dump back into this machine's stores
  sync [options]                          push (or pull) the dump dir with rclone
  config [options]                        show where each store is looked for, and override it
  install --skills [options]              install the global crossmem-loader skill
  uninstall --skills [options]            remove the global crossmem-loader skill
  help [command]                          display help for command

Examples:
  crossmem scan
  crossmem list --provider claude --limit 20
  crossmem list --provider devin --limit 10
  crossmem load . --provider codex --limit 5
  crossmem load /path/to/repo --out /tmp/context.md
  crossmem update .
  crossmem export --out ~/.assets/convdump
  crossmem import --in ~/.assets/convdump --dry-run
  crossmem sync --remote hetzbox:companydata/convdump
  crossmem help load
`

const scanHelpText = `Usage: crossmem scan [options]

Discover known local context stores without reading transcript contents.

Options:
  --json                                  print stores as JSON
  -h, --help                              display help for command

Examples:
  crossmem scan
  crossmem scan --json
`

const listHelpText = `Usage: crossmem list [options] [folder]

List available local sessions, most recent first. Pass a folder (positional or
--folder) to show only sessions whose real working directory is that folder,
across all tools — useful for picking which recent session to load.

Each row shows the session's title plus the FIRST and LAST thing the user asked
in it. A title says what a session was called; the first and last question say
what it became, which is what lets you tell two sessions in one folder apart.

The session this command is running inside is excluded by default — resuming
your own live session just hands you back the context you already have. Pass
--include-current to see it.

Options:
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, opencode, or all (default: all)
  --folder <path>                         only show sessions whose working directory is this folder
  --limit <number>                        maximum sessions to print (default: 50)
  --include-current                       also show the session this process is running inside
  --no-questions                          skip the first/last question lookup (faster)
  --json                                  print sessions as JSON
  -h, --help                              display help for command

Examples:
  crossmem list --limit 5                   # 5 most recent sessions across all tools
  crossmem list . --limit 5                 # 5 most recent sessions for THIS folder
  crossmem list --provider claude --limit 20
  crossmem list /path/to/repo --json
`

const loadHelpText = `Usage: crossmem load [options] [folder]

Print a portable context bundle for a repo or folder. The CLI extracts readable recent
history and references active repo instruction files; the consuming agent decides whether
to summarize or request more context.

Options:
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, opencode, or all (default: all)
  --limit <number>                        maximum sessions to include (default: 10)
  --mode <summary|full>                   excerpt size (default: defaults.mode from config, else summary)
  --full                                  shorthand for --mode full
  --include-current                       include the session this process is running inside
  --session <ref>                         load one specific session by its handle from list
                                          (a transcript path, or devin:<id>)
  --out <file>                            write bundle to file instead of stdout
  -h, --help                              display help for command

Examples:
  crossmem load .
  crossmem load . --limit 1                 # latest session for this folder (summary)
  crossmem load . --limit 1 --full          # latest session, fuller excerpt
  crossmem load --session /path/to/session.jsonl --full   # one chosen session
  crossmem load --session devin:1a2b3c --full             # a chosen Devin session
  crossmem load /path/to/repo --provider codex --limit 5
  crossmem load . --out .crossmem/context.md

Set a standing preference instead of passing --full every time:
  crossmem config --init, then "defaults": { "mode": "full" }
`

const updateHelpText = `Usage: crossmem update [options] [folder]

Write durable local context files under <folder>/.crossmem.

Files:
  context.md                              portable context bundle
  guardrails.md                           active repo instruction file references
  sessions.json                           selected session metadata
  sources.json                            discovered store and instruction metadata

Options:
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, opencode, or all (default: all)
  --limit <number>                        maximum sessions to include (default: 10)
  --mode <summary|full>                   excerpt size (default: defaults.mode from config, else summary)
  --full                                  shorthand for --mode full
  --include-current                       include the session this process is running inside
  -h, --help                              display help for command

The output is idempotent: the files carry no generated-at timestamp, and a file
whose content has not changed is left untouched, so re-running writes nothing
and produces no diff.

Examples:
  crossmem update .
  crossmem update /path/to/repo --provider claude --limit 5
`

const exportHelpText = `Usage: crossmem export [options]

Copy every discoverable store (Claude, Codex, Copilot, Devin, OpenCode
sessions) plus the well-known global instruction and memory files into a single
portable dump directory with a manifest.json. The dump is the one format
` + "`import`" + ` reads back and ` + "`sync`" + ` pushes to a remote.

Credential files, auth databases, env files, and vault/cache/node_modules
directories are never exported.

Options:
  --out <dir>                             dump directory (default: dumpDir from config, else ~/.assets/convdump)
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, opencode, or all (default: all)
  --json                                  print the manifest as JSON
  -h, --help                              display help for command

Examples:
  crossmem export
  crossmem export --out ~/.assets/convdump
  crossmem export --provider claude --json
`

const importHelpText = `Usage: crossmem import [options]

Restore a dump directory back into this machine's stores. Each store resolves
to the local location (honouring the user config), so a dump made on another
machine restores into the right place here. Files whose bytes already match are
skipped, making re-imports idempotent.

Options:
  --in <dir>                              dump directory to restore (default: dumpDir from config, else ~/.assets/convdump)
  --dry-run                               report what would be restored without writing anything
  --force                                 overwrite local files even when identical
  -h, --help                              display help for command

Examples:
  crossmem import --in ~/.assets/convdump --dry-run
  crossmem import --in ~/.assets/convdump
`

const syncHelpText = `Usage: crossmem sync [options]

Push the local dump directory to an rclone remote (or pull it back with
--pull) using the rclone binary on PATH. The dump is a directory, so what is
synced is exactly what ` + "`import`" + ` reads — no archive step.

Options:
  --remote <name:path>                    rclone destination/source, e.g. hetzbox:companydata/convdump
                                          (default: sync.remote from the config)
  --out <dir>                             local dump directory (default: dumpDir from config, else ~/.assets/convdump)
  --pull                                  pull from the remote into the local dump dir instead of pushing
  --prune                                 delete files at the destination not present in the source (rclone sync)
  -h, --help                              display help for command

Examples:
  crossmem sync
  crossmem sync --remote hetzbox:companydata/convdump
  crossmem sync --pull --remote hetzbox:companydata/convdump
`

const guardrailsHelpText = `Usage: crossmem guardrails [folder]

Print the repo instruction files an agent should read before acting.

Looks for:
  AGENTS.md
  CLAUDE.md
  .agents/AGENTS.md
  .claude/CLAUDE.md

Examples:
  crossmem guardrails
  crossmem guardrails /path/to/repo
`

const configHelpText = `Usage: crossmem config [options]

Show the config file location and, for every store, the paths crossmem looks in
on this machine and which of them exist. Use it to check a store was found, or
to see the exact key to override when a tool keeps its sessions somewhere else.

crossmem already knows the macOS, Linux, and Windows locations of each store
(Devin, for example, is ~/.local/share/devin/cli/sessions.db on macOS and Linux
and %APPDATA%\devin\cli\sessions.db on Windows). The config file is for the
cases it cannot know: a portable install, a second drive, a custom data dir.

File: ~/.config/crossmemcli/config.json    (override with $CROSSMEM_CONFIG)

  {
    "defaults": { "mode": "full", "limit": 5 },
    "stores": {
      "devin:sqlite-sessions": "D:/agents/devin/cli/sessions.db",
      "claude": ["~/work/.claude/projects"]
    },
    "extraStores": {
      "opencode": "~/other/opencode/opencode*.db"
    }
  }

  defaults      what happens with no flags: mode is "summary" or "full",
                limit is a session count. A flag on the command line wins.
  stores        replaces the built-in locations for that store
  extraStores   keeps the built-in locations and adds more
  keys          "provider:kind", or a bare "provider" for its primary store
  values        a path string or a list of them; ~, %VAR%, $VAR and * globs
                are expanded, and a path whose variable is unset is skipped

Options:
  --init                                  write a starter config file if none exists
  --json                                  print resolved stores as JSON
  -h, --help                              display help for command

Examples:
  crossmem config
  crossmem config --init
  crossmem config --json
`

const installHelpText = `Usage: crossmem install --skills [options]

Install the global crossmem-loader skill. This does not create repo-local skill folders.

Options:
  --skills                                required; installs the bundled skill
  --agents                                also target ~/.agents/skills when codex is not on PATH
  -h, --help                              display help for command

Targets:
  ~/.claude/skills/crossmem-loader
  ~/.agents/skills/crossmem-loader        when codex is on PATH or --agents is passed

Examples:
  crossmem install --skills
  crossmem install --skills --agents
`

const uninstallHelpText = `Usage: crossmem uninstall --skills [options]

Remove the global crossmem-loader skill.

Options:
  --skills                                required; removes the bundled skill
  --agents                                also target ~/.agents/skills when codex is not on PATH
  -h, --help                              display help for command

Examples:
  crossmem uninstall --skills
  crossmem uninstall --skills --agents
`

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprint(stdout, helpText)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-V" {
		fmt.Fprintln(stdout, version.String())
		return nil
	}
	if args[0] == "help" {
		return runHelp(args[1:], stdout)
	}

	switch args[0] {
	case "scan":
		return runScan(args[1:], stdout)
	case "list", "sessions":
		return runList(args[1:], stdout)
	case "load", "context":
		return runLoad(args[1:], stdout)
	case "guardrails":
		return runGuardrails(args[1:], stdout)
	case "update":
		return runUpdate(args[1:], stdout)
	case "export":
		return runExport(args[1:], stdout)
	case "import":
		return runImport(args[1:], stdout)
	case "sync":
		return runSync(args[1:], stdout)
	case "config":
		return runConfig(args[1:], stdout)
	case "install":
		return runTopLevelSkillAction("install", args[1:], stdout, stderr)
	case "uninstall":
		return runTopLevelSkillAction("uninstall", args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], helpText)
	}
}

func runHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, helpText)
		return nil
	}
	text, ok := commandHelp(args[0])
	if !ok {
		return fmt.Errorf("unknown command %q\n\n%s", args[0], helpText)
	}
	_, _ = fmt.Fprint(stdout, text)
	return nil
}

func commandHelp(command string) (string, bool) {
	switch command {
	case "scan":
		return scanHelpText, true
	case "list", "sessions":
		return listHelpText, true
	case "load", "context":
		return loadHelpText, true
	case "update":
		return updateHelpText, true
	case "export":
		return exportHelpText, true
	case "import":
		return importHelpText, true
	case "sync":
		return syncHelpText, true
	case "guardrails":
		return guardrailsHelpText, true
	case "config":
		return configHelpText, true
	case "install":
		return installHelpText, true
	case "uninstall":
		return uninstallHelpText, true
	default:
		return "", false
	}
}

func isHelpRequest(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func runGuardrails(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, guardrailsHelpText)
		return nil
	}
	folder := "."
	if len(args) > 0 {
		folder = args[0]
	}
	text, err := providers.BuildGuardrails(folder)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(stdout, text)
	return err
}

func runUpdate(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, updateHelpText)
		return nil
	}
	args, cwd := extractPositionalFolder(args)
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "all", "provider")
	limit := fs.Int("limit", 10, "limit")
	full := fs.Bool("full", false, "write fuller per-session excerpts")
	mode := fs.String("mode", providers.ModeSummary, "summary or full")
	includeCurrent := fs.Bool("include-current", false, "include the session this process is running inside")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cwd == "" {
		cwd = "."
	}
	result, err := providers.UpdateContext(providers.ListOptions{
		Provider:       *provider,
		CWD:            cwd,
		Limit:          resolveLimit(fs, limit, 10),
		Full:           resolveMode(fs, mode, full),
		IncludeCurrent: *includeCurrent,
	})
	if err != nil {
		return err
	}
	for _, path := range result.Written {
		fmt.Fprintf(stdout, "Wrote %s\n", path)
	}
	for _, path := range result.Unchanged {
		fmt.Fprintf(stdout, "Unchanged %s\n", path)
	}
	return nil
}

func runExport(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, exportHelpText)
		return nil
	}
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "dump directory")
	provider := fs.String("provider", "all", "provider")
	jsonOut := fs.Bool("json", false, "print the manifest as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sp := startSpinner(os.Stderr, "Exporting stores…")
	manifest, err := providers.ExportDump(providers.ExportOptions{Out: *out, Provider: *provider})
	sp.Stop()
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, manifest)
	}
	totalFiles, totalBytes := 0, int64(0)
	for _, store := range manifest.Stores {
		totalFiles += store.Files
		totalBytes += store.Bytes
	}
	fmt.Fprintf(stdout, "Exported %d stores (%d files, %s) to %s\n",
		len(manifest.Stores), totalFiles, humanBytes(totalBytes), manifest.Out)
	for _, store := range manifest.Stores {
		fmt.Fprintf(stdout, "  %-10s %-24s %6d files %10s\n", store.Provider, store.Kind, store.Files, humanBytes(store.Bytes))
	}
	fmt.Fprintf(stdout, "  instructions: %d, memory: %d\n", len(manifest.Instructions), len(manifest.Memory))
	return nil
}

func runImport(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, importHelpText)
		return nil
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	in := fs.String("in", "", "dump directory")
	dryRun := fs.Bool("dry-run", false, "report what would be restored without writing")
	force := fs.Bool("force", false, "overwrite local files even when identical")
	if err := fs.Parse(args); err != nil {
		return err
	}
	verb := "Restored"
	if *dryRun {
		verb = "Would restore"
	}
	res, err := providers.ImportDump(providers.ImportOptions{In: *in, DryRun: *dryRun, Force: *force})
	if err != nil {
		return err
	}
	for _, path := range res.Restored {
		fmt.Fprintf(stdout, "%s %s\n", verb, path)
	}
	for _, path := range res.Skipped {
		fmt.Fprintf(stdout, "Skipped (identical) %s\n", path)
	}
	for _, path := range res.Missing {
		fmt.Fprintf(stdout, "Missing target %s\n", path)
	}
	fmt.Fprintf(stdout, "%s %d files, skipped %d, missing %d\n", verb, len(res.Restored), len(res.Skipped), len(res.Missing))
	return nil
}

func runSync(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, syncHelpText)
		return nil
	}
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	remote := fs.String("remote", "", "rclone remote path")
	out := fs.String("out", "", "local dump directory")
	pull := fs.Bool("pull", false, "pull from the remote into the local dump dir")
	prune := fs.Bool("prune", false, "delete destination files not present in the source")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sp := startSpinner(os.Stderr, "Syncing with rclone…")
	res, err := providers.SyncDump(providers.SyncOptions{Remote: *remote, Pull: *pull, Prune: *prune, Out: *out})
	sp.Stop()
	if err != nil {
		return err
	}
	direction := "->"
	if res.Pull {
		direction = "<-"
	}
	fmt.Fprintf(stdout, "Synced %s %s %s\n", res.DumpDir, direction, res.Remote)
	if res.Output != "" {
		fmt.Fprintln(stdout, res.Output)
	}
	return nil
}

func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func runConfig(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, configHelpText)
		return nil
	}
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	initFlag := fs.Bool("init", false, "write a starter config file")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *initFlag {
		path, created, err := providers.InitConfig()
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(stdout, "Wrote %s\n", path)
		} else {
			fmt.Fprintf(stdout, "Config already exists at %s\n", path)
		}
		return nil
	}

	// A malformed config is reported here rather than swallowed, because this is
	// the command someone runs when their override is not taking effect.
	config, loadErr := providers.LoadConfig()
	stores := providers.EffectiveStores()
	if *jsonOut {
		payload := struct {
			Config providers.Config           `json:"config"`
			Error  string                     `json:"error,omitempty"`
			Stores []providers.StoreCandidate `json:"stores"`
		}{Config: config, Stores: stores}
		if loadErr != nil {
			payload.Error = loadErr.Error()
		}
		return writeJSON(stdout, payload)
	}

	fmt.Fprintf(stdout, "config: %s\n", config.Path)
	fmt.Fprintf(stdout, "exists: %t\n", config.Exists)
	if loadErr != nil {
		fmt.Fprintf(stdout, "error:  %v\n", loadErr)
		fmt.Fprintf(stdout, "        the file is ignored in full; built-in defaults and locations are in use\n")
	}
	// Report what is actually in effect, not what the file asked for: a config
	// that failed to load is discarded, and printing its values would describe
	// behaviour the user is not getting.
	defaults := providers.UserDefaults()
	mode := defaults.Mode
	if mode == "" {
		mode = providers.ModeSummary + " (built-in)"
	}
	fmt.Fprintf(stdout, "default mode:  %s\n", mode)
	if defaults.Limit > 0 {
		fmt.Fprintf(stdout, "default limit: %d\n", defaults.Limit)
	}
	fmt.Fprintln(stdout)
	for _, store := range stores {
		found := "not found"
		if len(store.Resolved) > 0 {
			found = strings.Join(store.Resolved, ", ")
		}
		fmt.Fprintf(stdout, "%s\n", store.Key)
		fmt.Fprintf(stdout, "  found: %s\n", found)
		for _, candidate := range store.Sources {
			fmt.Fprintf(stdout, "  looks in: %s\n", candidate)
		}
	}
	return nil
}

func runScan(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, scanHelpText)
		return nil
	}
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	stores, err := providers.DiscoverStores()
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, stores)
	}
	for _, store := range stores {
		fmt.Fprintf(stdout, "%s: %s\n", store.Provider, store.Path)
		fmt.Fprintf(stdout, "  kind: %s\n", store.Kind)
		fmt.Fprintf(stdout, "  exists: %t\n", store.Exists)
		if store.Files != nil {
			fmt.Fprintf(stdout, "  files: %d\n", *store.Files)
		}
		if store.Bytes != nil {
			fmt.Fprintf(stdout, "  bytes: %d\n", *store.Bytes)
		}
		if store.Note != "" {
			fmt.Fprintf(stdout, "  note: %s\n", store.Note)
		}
	}
	return nil
}

func runList(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, listHelpText)
		return nil
	}
	args, positional := extractPositionalFolder(args)
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "all", "provider")
	folder := fs.String("folder", "", "folder")
	limit := fs.Int("limit", 50, "limit")
	jsonOut := fs.Bool("json", false, "print JSON")
	includeCurrent := fs.Bool("include-current", false, "include the session this process is running inside")
	noQuestions := fs.Bool("no-questions", false, "skip the first/last question lookup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// A folder (positional or --folder) scopes the listing to sessions whose
	// real working directory is that folder, across all tools.
	cwd := positional
	if cwd == "" {
		cwd = *folder
	}
	sp := startSpinner(os.Stderr, "Scanning sessions…")
	sessions, err := providers.ListSessions(providers.ListOptions{
		Provider:       *provider,
		CWD:            cwd,
		Limit:          resolveLimit(fs, limit, 50),
		IncludeCurrent: *includeCurrent,
		Questions:      !*noQuestions,
	})
	sp.Stop()
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, sessions)
	}
	for _, session := range sessions {
		marker := ""
		if session.Current {
			marker = "  [current session]"
		}
		fmt.Fprintf(stdout, "%s %-11s %9d %s%s\n", session.Modified.Format("2006-01-02T15:04:05Z07:00"), session.Provider, session.Bytes, session.Ref, marker)
		if session.Workspace != "" {
			fmt.Fprintf(stdout, "  workspace: %s\n", session.Workspace)
		}
		if session.Title != "" {
			fmt.Fprintf(stdout, "  title:     %s\n", oneLine(session.Title, 140))
		}
		// The opening and closing question are what distinguish two sessions in
		// the same folder; print both when they differ.
		if session.FirstQuestion != "" {
			fmt.Fprintf(stdout, "  first:     %s\n", oneLine(session.FirstQuestion, 140))
		}
		if session.LastQuestion != "" && session.LastQuestion != session.FirstQuestion {
			fmt.Fprintf(stdout, "  last:      %s\n", oneLine(session.LastQuestion, 140))
		}
	}
	return nil
}

// oneLine collapses a value to a single truncated line. Titles and questions
// are arbitrary user prose — a Devin title can be an entire multi-line prompt —
// and a listing is only scannable if every row is one line.
func oneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max]) + "…"
}

func runLoad(args []string, stdout io.Writer) error {
	if isHelpRequest(args) {
		_, _ = fmt.Fprint(stdout, loadHelpText)
		return nil
	}
	args, cwd := extractPositionalFolder(args)
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "all", "provider")
	limit := fs.Int("limit", 10, "limit")
	out := fs.String("out", "", "output file")
	full := fs.Bool("full", false, "emit fuller per-session excerpts instead of the compact summary")
	mode := fs.String("mode", providers.ModeSummary, "summary or full")
	includeCurrent := fs.Bool("include-current", false, "include the session this process is running inside")
	session := fs.String("session", "", "load one specific session transcript by path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	wantFull := resolveMode(fs, mode, full)
	wantLimit := resolveLimit(fs, limit, 10)
	folder := ""
	if fs.NArg() > 0 {
		folder = fs.Arg(0)
	}
	if cwd == "" {
		cwd = folder
	}
	sp := startSpinner(os.Stderr, "Loading context…")
	var bundle string
	var err error
	if *session != "" {
		bundle, err = providers.BuildSessionContext(*session, cwd, wantFull)
	} else {
		bundle, err = providers.BuildContext(providers.ListOptions{
			Provider:       *provider,
			CWD:            cwd,
			Limit:          wantLimit,
			Full:           wantFull,
			IncludeCurrent: *includeCurrent,
		})
	}
	sp.Stop()
	if err != nil {
		return err
	}
	if *out != "" {
		path := expandHome(*out)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(bundle), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Wrote %s\n", path)
		return nil
	}
	_, err = fmt.Fprint(stdout, bundle)
	return err
}

// resolveMode decides summary vs full. Precedence: an explicit flag on this
// command line, then the user config's defaults.mode, then summary. `--full`
// stays supported as the older spelling of `--mode full`.
func resolveMode(fs *flag.FlagSet, mode *string, full *bool) bool {
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	switch {
	case explicit["mode"]:
		return *mode == providers.ModeFull
	case explicit["full"]:
		return *full
	default:
		return providers.UserDefaults().Full()
	}
}

// resolveLimit prefers an explicit --limit, then defaults.limit, then the
// command's own default.
func resolveLimit(fs *flag.FlagSet, limit *int, builtin int) int {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "limit" {
			explicit = true
		}
	})
	if explicit {
		return *limit
	}
	if configured := providers.UserDefaults().Limit; configured > 0 {
		return configured
	}
	return builtin
}

func extractPositionalFolder(args []string) ([]string, string) {
	filtered := make([]string, 0, len(args))
	folder := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			filtered = append(filtered, arg)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				filtered = append(filtered, args[i+1])
				i++
			}
			continue
		}
		if folder == "" {
			folder = arg
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, folder
}

func runTopLevelSkillAction(verb string, args []string, stdout io.Writer, stderr io.Writer) error {
	if isHelpRequest(args) {
		if verb == "uninstall" {
			_, _ = fmt.Fprint(stdout, uninstallHelpText)
		} else {
			_, _ = fmt.Fprint(stdout, installHelpText)
		}
		return nil
	}
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	skillsFlag := fs.Bool("skills", false, "install or remove the bundled crossmem-loader skill")
	agents := fs.Bool("agents", false, "also target ~/.agents/skills even when codex is not on PATH")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("crossmem %s: %w", verb, err)
	}
	if !*skillsFlag {
		return fmt.Errorf("crossmem %s: --skills is required", verb)
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("crossmem %s: unexpected arguments: %s", verb, strings.Join(fs.Args(), " "))
	}
	return executeSkillAction(verb, *agents, stdout, stderr)
}

func executeSkillAction(label string, agents bool, stdout io.Writer, stderr io.Writer) error {
	includeAgents := resolveIncludeAgents(agents)
	var (
		results []skills.InstallResult
		err     error
	)
	if strings.HasSuffix(label, "uninstall") {
		results, err = skills.UninstallBundledSkill(skills.InstallOptions{IncludeAgents: includeAgents})
	} else {
		results, err = skills.InstallBundledSkill(skills.InstallOptions{IncludeAgents: includeAgents})
	}
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "%s: %s at %s\n", result.Host, result.Action, result.Path)
	}
	if !includeAgents {
		fmt.Fprintln(stderr, "crossmem: skipped agents skill target because codex was not found on PATH; pass --agents to force it")
	}
	return nil
}

func resolveIncludeAgents(force bool) bool {
	if force {
		return true
	}
	_, err := exec.LookPath("codex")
	return err == nil
}
