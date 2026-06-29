package providers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/muthuishere/crossmemcli/internal/diag"

	_ "modernc.org/sqlite"
)

func ListSessions(opts ListOptions) ([]Session, error) {
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.CWD != "" {
		if abs, err := filepath.Abs(expandHome(opts.CWD)); err == nil {
			opts.CWD = abs
		}
	}

	var sessions []Session
	if opts.Provider == "all" || opts.Provider == "devin" {
		devin, err := listDevin(opts.Limit, opts.CWD)
		if err == nil {
			sessions = append(sessions, devin...)
		} else {
			diag.Debugf("list devin err=%q", err)
		}
	}

	roots := []string{}
	if opts.Folder != "" {
		roots = []string{expandHome(opts.Folder)}
	} else {
		roots = providerRoots(opts.Provider)
	}
	for _, root := range roots {
		jsonl, err := listJSONL(root, opts.Provider)
		if err != nil {
			diag.Debugf("list jsonl root=%q provider=%s err=%q", root, opts.Provider, err)
			continue
		}
		if opts.CWD != "" {
			jsonl = filterByCWD(jsonl, opts.CWD)
		}
		sessions = append(sessions, jsonl...)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	if len(sessions) > opts.Limit {
		sessions = sessions[:opts.Limit]
	}
	return sessions, nil
}

func filterByCWD(sessions []Session, cwd string) []Session {
	filtered := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if sameOrChild(session.Workspace, cwd) || sameOrChild(session.Title, cwd) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func listJSONL(root string, provider string) ([]Session, error) {
	var sessions []Session
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			if err != nil {
				diag.Debugf("list walk path=%q err=%q", path, err)
			}
			return nil
		}
		inferred := inferProvider(path, provider)
		if inferred == "copilot" && !isCopilotSessionPath(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		title := readJSONLTitle(path, inferred)
		sessions = append(sessions, Session{
			Provider:  inferred,
			Path:      path,
			Bytes:     info.Size(),
			Modified:  info.ModTime(),
			Workspace: inferWorkspace(path, inferred),
			Title:     title,
		})
		return nil
	})
	return sessions, err
}

func readJSONLTitle(path string, provider string) string {
	file, err := withRetry("open jsonl title "+path, func() (*os.File, error) {
		return os.Open(path)
	})
	if err != nil {
		diag.Debugf("read title path=%q provider=%s err=%q", path, provider, err)
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for i := 0; i < 12 && scanner.Scan(); i++ {
		var obj map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			continue
		}
		switch provider {
		case "claude":
			if value, ok := obj["aiTitle"].(string); ok {
				return value
			}
			if value, ok := obj["summary"].(string); ok {
				return value
			}
		case "codex":
			if payload, ok := obj["payload"].(map[string]any); ok {
				if value, ok := payload["cwd"].(string); ok {
					return value
				}
			}
		}
	}
	return ""
}

func listDevin(limit int, cwdFilter string) ([]Session, error) {
	dbPath := expandHome("~/.local/share/devin/cli/sessions.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := withRetry("query devin sessions", func() (*sql.Rows, error) {
		return db.Query(`select id, title, working_directory, backend_type, model, agent_mode, last_activity_at from sessions where hidden = 0 order by last_activity_at desc limit ?`, limit)
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var id, title, workingDirectory, backend, model, mode string
		var last int64
		if err := rows.Scan(&id, &title, &workingDirectory, &backend, &model, &mode, &last); err != nil {
			continue
		}
		if title == "" {
			title = strings.Trim(strings.Join([]string{backend, model, mode}, "/"), "/")
		}
		session := Session{
			Provider:  "devin",
			ID:        id,
			Path:      dbPath,
			Bytes:     info.Size(),
			Modified:  unixFlexible(last),
			Workspace: workingDirectory,
			Title:     title,
		}
		if cwdFilter != "" && !sameOrChild(session.Workspace, cwdFilter) {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func sameOrChild(value string, root string) bool {
	if value == "" || root == "" {
		return false
	}
	valueAbs, err := filepath.Abs(expandHome(value))
	if err != nil {
		valueAbs = value
	}
	rootAbs, err := filepath.Abs(expandHome(root))
	if err != nil {
		rootAbs = root
	}
	if valueAbs == rootAbs {
		return true
	}
	rel, err := filepath.Rel(rootAbs, valueAbs)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func unixFlexible(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0).UTC()
	}
	return time.UnixMilli(value).UTC()
}

func isCopilotSessionPath(path string) bool {
	return strings.Contains(path, "/chatSessions/") || strings.Contains(path, "/GitHub.copilot-chat/transcripts/")
}

func inferWorkspace(path string, provider string) string {
	switch provider {
	case "claude":
		dir := filepath.Base(filepath.Dir(path))
		if strings.HasPrefix(dir, "-") {
			return strings.ReplaceAll(dir, "-", "/")
		}
		return dir
	case "codex":
		root := expandHome("~/.codex/sessions")
		if rel, err := filepath.Rel(root, filepath.Dir(path)); err == nil {
			return rel
		}
	case "copilot":
		parts := strings.Split(path, "/workspaceStorage/")
		if len(parts) == 2 {
			id := strings.Split(parts[1], "/")[0]
			return filepath.Join(parts[0], "workspaceStorage", id, "workspace.json")
		}
	}
	return ""
}
