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
)

const helpText = `crossmem

Portable context memory across local agent tools.

Usage:
  crossmem scan [--json]
  crossmem list [--provider claude|codex|copilot|devin|all] [--folder PATH] [--limit N] [--json]
  crossmem load [FOLDER] [--provider claude|codex|copilot|devin|all] [--limit N] [--out FILE]
  crossmem context [same flags as load]
  crossmem guardrails [FOLDER]
  crossmem update [FOLDER] [--provider claude|codex|copilot|devin|all] [--limit N]
  crossmem install --skills [--agents]
  crossmem uninstall --skills [--agents]

Examples:
  crossmem scan
  crossmem list --provider devin --limit 10
  crossmem load . --limit 5 --out .crossmem/context.md
`

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprint(stdout, helpText)
		return nil
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
	case "install":
		return runTopLevelSkillAction("install", args[1:], stdout, stderr)
	case "uninstall":
		return runTopLevelSkillAction("uninstall", args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], helpText)
	}
}

func runGuardrails(args []string, stdout io.Writer) error {
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

func runScan(args []string, stdout io.Writer) error {
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
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "all", "provider")
	folder := fs.String("folder", "", "folder")
	limit := fs.Int("limit", 50, "limit")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sessions, err := providers.ListSessions(providers.ListOptions{
		Provider: *provider,
		Folder:   *folder,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSON(stdout, sessions)
	}
	for _, session := range sessions {
		fmt.Fprintf(stdout, "%s %-7s %9d %s\n", session.Modified.Format("2006-01-02T15:04:05Z07:00"), session.Provider, session.Bytes, session.Path)
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
	args, cwd := extractPositionalFolder(args)
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	provider := fs.String("provider", "all", "provider")
	limit := fs.Int("limit", 10, "limit")
	out := fs.String("out", "", "output file")
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
	bundle, err := providers.BuildContext(providers.ListOptions{
		Provider: *provider,
		CWD:      cwd,
		Limit:    *limit,
	})
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
