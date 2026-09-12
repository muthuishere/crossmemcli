package providers

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// storeDefinition declares one logical store once, with every OS-specific
// location it is known to live in. Candidates are tried in order and every one
// that exists on disk is used; a candidate referencing an environment variable
// that is not set on this machine (%APPDATA% on macOS, $XDG_DATA_HOME on
// Windows) drops out silently, so a single table covers macOS, Linux, and
// Windows without runtime.GOOS branching.
//
// Primary marks the store a bare provider key in the user config refers to
// (see config.go); providers with several kinds keep exactly one primary.
type storeDefinition struct {
	Provider string
	Kind     string
	Paths    []string
	Primary  bool
	Note     string
}

// appDataRoots is the per-platform data directory an app named `app` uses:
// XDG on Linux, Application Support on macOS, roaming and local AppData on
// Windows. The ~/AppData forms are there for shells that do not export the
// AppData variables.
func appDataRoots(app string) []string {
	return []string{
		"~/.local/share/" + app,
		"$XDG_DATA_HOME/" + app,
		"~/Library/Application Support/" + app,
		"%APPDATA%/" + app,
		"%LOCALAPPDATA%/" + app,
		"~/AppData/Roaming/" + app,
		"~/AppData/Local/" + app,
	}
}

// vscodeUserRoots is the "User" directory of a VS Code build (or fork) whose
// Electron data folder is named `app` — where workspaceStorage lives. Every
// fork keeps this layout, which is why the Devin desktop app is read the same
// way as VS Code itself.
func vscodeUserRoots(app string) []string {
	return []string{
		"~/Library/Application Support/" + app + "/User",
		"%APPDATA%/" + app + "/User",
		"~/AppData/Roaming/" + app + "/User",
		"~/.config/" + app + "/User",
	}
}

// devinCLIRoots is the Devin CLI data directory. Devin keeps every session in
// one SQLite database at <data>/sessions.db, and both the CLI and the desktop
// app write to it (the desktop drives the CLI through an ACP connector, so
// there is no separate desktop chat store). $DEVIN_HOME relocates the whole
// data directory; the new Windows default is %APPDATA%\devin\cli (the older
// %APPDATA%\Cognition\cli install path is retired).
var devinCLIRoots = append(
	[]string{"$DEVIN_HOME/cli"},
	appDataRoots("devin/cli")...,
)

// devinSessionDBCandidates is every location the one Devin sessions.db lives:
// the $DEVIN_DB_PATH env override first (a direct path to the file), then
// $DEVIN_HOME/cli/sessions.db and the standard data dirs.
var devinSessionDBCandidates = append([]string{"$DEVIN_DB_PATH"}, under(devinCLIRoots, "sessions.db")...)

var vscodeCopilotRoots = append(vscodeUserRoots("Code"), vscodeUserRoots("Code - Insiders")...)

var storeDefinitions = []storeDefinition{
	// $CLAUDE_CONFIG_DIR / $CODEX_HOME come first: when a user has moved the
	// config dir, that is where the sessions really are.
	{"claude", "jsonl-projects", []string{"$CLAUDE_CONFIG_DIR/projects", "~/.claude/projects"}, true, "Claude Code transcript JSONL files and per-project memory directories. Same path on every OS ($CLAUDE_CONFIG_DIR wins when set)."},
	{"codex", "jsonl-sessions", []string{"$CODEX_HOME/sessions", "~/.codex/sessions"}, true, "Codex CLI session JSONL files grouped by date. Same path on every OS ($CODEX_HOME wins when set)."},
	{"codex", "sqlite-logs", []string{"$CODEX_HOME/logs_2.sqlite", "~/.codex/logs_2.sqlite"}, false, "Codex structured log database."},
	{"codex", "jsonl-history", []string{"$CODEX_HOME/history.jsonl", "~/.codex/history.jsonl"}, false, "Codex prompt history."},
	{"copilot", "vscode-workspace-storage", under(vscodeCopilotRoots, "workspaceStorage"), true, "VS Code chatSessions and GitHub.copilot-chat transcript JSONL files (stable and Insiders)."},
	{"copilot", "zed-copilot", under(appDataRoots("Zed"), "copilot"), false, "Zed Copilot language-server cache; not a chat transcript store by itself."},
	{"copilot-cli", "sqlite-sessions", []string{"~/.copilot/session-store.db"}, true, "GitHub Copilot CLI SQLite session store. Home-relative on every OS."},
	{"devin", "sqlite-sessions", devinSessionDBCandidates, true, "Devin session database. One sessions.db holds every session, whether driven from the CLI or the desktop app's ACP connector. $DEVIN_DB_PATH / $DEVIN_HOME win when set; the Windows default is %APPDATA%\\devin\\cli."},
	{"devin", "cli-logs", under(devinCLIRoots, "logs"), false, "Devin CLI logs. Session content primarily lives in sessions.db."},
	{"opencode", "sqlite-sessions", under(appDataRoots("opencode"), "opencode*.db"), true, "OpenCode SQLite session DB (also opencode-dev.db / opencode-local.db)."},
}

func under(roots []string, leaf string) []string {
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, root+"/"+leaf)
	}
	return paths
}

// Providers lists every provider crossmem knows, for --provider help and for
// naming the stores a bundle searched.
func Providers() []string {
	var names []string
	seen := map[string]bool{}
	for _, def := range storeDefinitions {
		if !seen[def.Provider] {
			seen[def.Provider] = true
			names = append(names, def.Provider)
		}
	}
	return names
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return os.Getenv("HOME")
}

// expandHome resolves a leading ~ only. It is used for user-supplied folder
// arguments and for workspace paths read out of another tool's store, which
// must never be reinterpreted as environment references.
func expandHome(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(homeDir(), path[2:])
	}
	return path
}

var winEnvPattern = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)
var shEnvPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// expandPath resolves ~, %VAR%, and $VAR / ${VAR} in a store path candidate.
// It returns "" when the candidate names an environment variable that is unset
// here — that is how a Windows-only candidate disappears on macOS instead of
// collapsing into a bogus path rooted at "/".
func expandPath(path string) string {
	if path == "" {
		return ""
	}
	resolved := true
	substitute := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			resolved = false
		}
		return value
	}
	path = winEnvPattern.ReplaceAllStringFunc(path, func(match string) string {
		return substitute(match[1 : len(match)-1])
	})
	path = shEnvPattern.ReplaceAllStringFunc(path, func(match string) string {
		return substitute(strings.Trim(match, "${}"))
	})
	if !resolved {
		return ""
	}
	return filepath.Clean(expandHome(path))
}

// storeCandidates returns the raw (unexpanded) path candidates for a store,
// with any user config override applied.
func storeCandidates(provider string, kind string) []string {
	def, ok := storeDefinitionFor(provider, kind)
	if !ok {
		return nil
	}
	return userConfig().candidatesFor(def)
}

func storeDefinitionFor(provider string, kind string) (storeDefinition, bool) {
	for _, def := range storeDefinitions {
		if def.Provider == provider && def.Kind == kind {
			return def, true
		}
	}
	return storeDefinition{}, false
}

// storePaths returns every location of a store that actually exists on this
// machine, in candidate order and deduplicated. Candidates containing glob
// metacharacters are expanded (OpenCode ships opencode.db / opencode-dev.db /
// opencode-local.db side by side).
func storePaths(provider string, kind string) []string {
	var found []string
	seen := map[string]bool{}
	for _, candidate := range storeCandidates(provider, kind) {
		expanded := expandPath(candidate)
		if expanded == "" {
			continue
		}
		var matches []string
		if strings.ContainsAny(expanded, "*?[") {
			matches, _ = filepath.Glob(expanded)
		} else if _, err := os.Stat(expanded); err == nil {
			matches = []string{expanded}
		}
		for _, match := range matches {
			if seen[match] {
				continue
			}
			seen[match] = true
			found = append(found, match)
		}
	}
	return found
}

// storePath returns the single best existing location for a store, or "" when
// the tool is not installed here.
func storePath(provider string, kind string) string {
	paths := storePaths(provider, kind)
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

// displayCandidate is the path shown by scan for a store with nothing on disk:
// the first candidate that is meaningful on this platform.
func displayCandidate(provider string, kind string) string {
	candidates := storeCandidates(provider, kind)
	for _, candidate := range candidates {
		if expanded := expandPath(candidate); expanded != "" {
			return expanded
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// providerRoot is one walkable transcript directory together with the provider
// that owns it. The provider travels with the root because two providers can
// share a directory shape — Copilot in VS Code and the Devin desktop app both
// use workspaceStorage, and only the root they came from tells them apart.
type providerRoot struct {
	Provider string
	Path     string
}

func providerRoots(provider string) []providerRoot {
	roots := []providerRoot{}
	for _, def := range storeDefinitions {
		if provider != "all" && def.Provider != provider {
			continue
		}
		switch def.Kind {
		case "jsonl-projects", "jsonl-sessions", "vscode-workspace-storage":
			for _, path := range storePaths(def.Provider, def.Kind) {
				roots = append(roots, providerRoot{Provider: def.Provider, Path: path})
			}
		}
	}
	return roots
}

// isWorkspaceStoragePath reports whether a transcript lives in the VS Code
// workspaceStorage layout, shared by VS Code and every fork of it.
func isWorkspaceStoragePath(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/workspaceStorage/")
}

// inferProvider names the tool that owns a transcript path. The fallback is
// the provider of the root it was found under; it is only guessed from the
// path shape when a bare path arrives from `load --session`.
func inferProvider(path string, fallback string) string {
	if fallback != "" && fallback != "all" {
		return fallback
	}
	slashed := filepath.ToSlash(path)
	switch {
	case strings.Contains(slashed, "/.claude/"):
		return "claude"
	case strings.Contains(slashed, "/.codex/"):
		return "codex"
	case strings.Contains(slashed, "/workspaceStorage/"):
		return "copilot"
	default:
		return "unknown"
	}
}

// isVSCodeChat reports whether a provider stores its chat in the VS Code
// journal format, which decides both how a transcript is parsed and how its
// workspace folder is resolved.
func isVSCodeChat(provider string) bool {
	return provider == "copilot"
}

// normalizeCase folds a path's case on Windows, whose filesystem is
// case-insensitive and whose tools disagree on it (VS Code writes
// file:///c%3A/…, the shell reports C:\…). It is a no-op elsewhere.
func normalizeCase(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func pathsEqual(a string, b string) bool {
	return normalizeCase(a) == normalizeCase(b)
}
