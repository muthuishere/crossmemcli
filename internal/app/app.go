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

Options:
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, devin-gui,
                                          opencode, or all (default: all)
  --folder <path>                         only show sessions whose working directory is this folder
  --limit <number>                        maximum sessions to print (default: 50)
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
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, devin-gui,
                                          opencode, or all (default: all)
  --limit <number>                        maximum sessions to include (default: 10)
  --full                                  emit fuller per-session excerpts instead of the compact summary
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
`

const updateHelpText = `Usage: crossmem update [options] [folder]

Write durable local context files under <folder>/.crossmem.

Files:
  context.md                              portable context bundle
  guardrails.md                           active repo instruction file references
  sessions.json                           selected session metadata
  sources.json                            discovered store and instruction metadata

Options:
  --provider <name>                       claude, codex, copilot, copilot-cli, devin, devin-gui,
                                          opencode, or all (default: all)
  --limit <number>                        maximum sessions to include (default: 10)
  -h, --help                              display help for command

Examples:
  crossmem update .
  crossmem update /path/to/repo --provider claude --limit 5
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
and %APPDATA%\Cognition\cli\sessions.db on Windows). The config file is for the
cases it cannot know: a portable install, a second drive, a custom data dir.

File: ~/.config/crossmemcli/config.json    (override with $CROSSMEM_CONFIG)

  {
    "stores": {
      "devin:sqlite-sessions": "D:/agents/Cognition/cli/sessions.db",
      "claude": ["~/work/.claude/projects"]
    },
    "extraStores": {
      "opencode": "~/other/opencode/opencode*.db"
    }
  }

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cwd == "" {
		cwd = "."
	}
	result, err := providers.UpdateContext(providers.ListOptions{
		Provider: *provider,
		CWD:      cwd,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}
	for _, path := range result.Paths {
		fmt.Fprintf(stdout, "Wrote %s\n", path)
	}
	return nil
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
		fmt.Fprintf(stdout, "error:  %v (ignored; built-in locations in use)\n", loadErr)
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
		Provider: *provider,
		CWD:      cwd,
		Limit:    *limit,
	})
	sp.Stop()
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, sessions)
	}
	for _, session := range sessions {
		fmt.Fprintf(stdout, "%s %-7s %9d %s\n", session.Modified.Format("2006-01-02T15:04:05Z07:00"), session.Provider, session.Bytes, session.Ref)
		if session.Workspace != "" {
			fmt.Fprintf(stdout, "  workspace: %s\n", session.Workspace)
		}
		if session.Title != "" {
			fmt.Fprintf(stdout, "  title: %s\n", session.Title)
		}
	}
	return nil
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
	session := fs.String("session", "", "load one specific session transcript by path")
	if err := fs.Parse(args); err != nil {
		return err
	}
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
		bundle, err = providers.BuildSessionContext(*session, cwd, *full)
	} else {
		bundle, err = providers.BuildContext(providers.ListOptions{
			Provider: *provider,
			CWD:      cwd,
			Limit:    *limit,
			Full:     *full,
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
