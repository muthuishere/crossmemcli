package providers

import (
	"os"
	"path/filepath"
	"strings"
)

type storeDefinition struct {
	Provider string
	Kind     string
	Path     string
	Note     string
}

var storeDefinitions = []storeDefinition{
	{"claude", "jsonl-projects", "~/.claude/projects", "Claude Code transcript JSONL files and per-project memory directories."},
	{"codex", "jsonl-sessions", "~/.codex/sessions", "Codex CLI session JSONL files grouped by date."},
	{"codex", "sqlite-logs", "~/.codex/logs_2.sqlite", "Codex structured log database."},
	{"codex", "jsonl-history", "~/.codex/history.jsonl", "Codex prompt history."},
	{"copilot", "vscode-workspace-storage", "~/Library/Application Support/Code/User/workspaceStorage", "VS Code chatSessions and GitHub.copilot-chat transcript JSONL files."},
	{"copilot", "zed-copilot", "~/Library/Application Support/Zed/copilot", "Zed Copilot language-server cache; not a chat transcript store by itself."},
	{"devin", "sqlite-sessions", "~/.local/share/devin/cli/sessions.db", "Devin CLI SQLite session DB."},
	{"devin", "cli-logs", "~/.local/share/devin/cli/logs", "Devin CLI logs. Session content primarily lives in sessions.db."},
}

func expandHome(path string) string {
	if path == "~" {
		return os.Getenv("HOME")
	}
	if strings.HasPrefix(path, "~/") {
		return os.Getenv("HOME") + path[1:]
	}
	return path
}

func providerRoots(provider string) []string {
	roots := []string{}
	for _, def := range storeDefinitions {
		if provider != "all" && def.Provider != provider {
			continue
		}
		switch def.Kind {
		case "jsonl-projects", "jsonl-sessions", "vscode-workspace-storage":
			roots = append(roots, expandHome(def.Path))
		}
	}
	return roots
}

func inferProvider(path string, fallback string) string {
	if fallback != "" && fallback != "all" {
		return fallback
	}
	switch {
	case strings.Contains(path, string(filepath.Separator)+".claude"+string(filepath.Separator)):
		return "claude"
	case strings.Contains(path, string(filepath.Separator)+".codex"+string(filepath.Separator)):
		return "codex"
	case strings.Contains(path, string(filepath.Separator)+"workspaceStorage"+string(filepath.Separator)):
		return "copilot"
	default:
		return "unknown"
	}
}
