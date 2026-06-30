package providers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

	for _, root := range providerRoots(opts.Provider) {
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
		// Match on the real working directory only. The session belongs to the
		// target folder when its cwd is that folder or sits under it. Titles are
		// human sentences, not paths, so they are never used for matching.
		if sameOrChild(session.Workspace, cwd) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func listJSONL(root string, provider string) ([]Session, error) {
	// First walk the tree (cheap) to collect candidate transcript files, then read
	// each file's metadata (cwd + title) concurrently — that per-file read is the
	// bottleneck when there are hundreds of transcripts.
	type entry struct {
		path string
		info os.FileInfo
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			if err != nil {
				diag.Debugf("list walk path=%q err=%q", path, err)
			}
			return nil
		}
		if inferProvider(path, provider) == "copilot" && !isCopilotSessionPath(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, entry{path: path, info: info})
		return nil
	})

	sessions := make([]Session, len(entries))
	sem := make(chan struct{}, previewWorkers)
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e := entries[i]
			inferred := inferProvider(e.path, provider)
			title, cwd := readJSONLMeta(e.path, inferred)
			workspace := cwd
			if workspace == "" {
				workspace = inferWorkspace(e.path, inferred)
			}
			sessions[i] = Session{
				Provider:  inferred,
				Ref:       e.path,
				Path:      e.path,
				Bytes:     e.info.Size(),
				Modified:  e.info.ModTime(),
				Workspace: workspace,
				Title:     title,
			}
		}(i)
	}
	wg.Wait()
	return sessions, err
}

// readJSONLMeta scans the head of a transcript for a human title and the real
// working directory the session ran in. The cwd is the reliable key for
// matching a session to a folder; the encoded store path is lossy when a real
// folder name contains a dash (e.g. "crossmem-workspace").
func readJSONLMeta(path string, provider string) (title string, cwd string) {
	file, err := withRetry("open jsonl meta "+path, func() (*os.File, error) {
		return os.Open(path)
	})
	if err != nil {
		diag.Debugf("read meta path=%q provider=%s err=%q", path, provider, err)
		return "", ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for i := 0; i < 60 && scanner.Scan(); i++ {
		var obj map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			continue
		}
		switch provider {
		case "claude":
			if cwd == "" {
				if value, ok := obj["cwd"].(string); ok && value != "" {
					cwd = value
				}
			}
			if title == "" {
				if value, ok := obj["aiTitle"].(string); ok && value != "" {
					title = value
				} else if value, ok := obj["summary"].(string); ok && value != "" {
					title = value
				}
			}
		case "codex":
			if payload, ok := obj["payload"].(map[string]any); ok {
				if cwd == "" {
					if value, ok := payload["cwd"].(string); ok && value != "" {
						cwd = value
					}
				}
			}
		}
		if cwd != "" && title != "" {
			break
		}
	}
	return title, cwd
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
			Ref:       "devin:" + id,
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

// loadDevinSession fetches one Devin session by id for load --session devin:<id>.
func loadDevinSession(id string) (Session, error) {
	dbPath := expandHome("~/.local/share/devin/cli/sessions.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		return Session{}, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		return Session{}, err
	}
	defer db.Close()

	row := db.QueryRow(`select title, working_directory, backend_type, model, agent_mode, last_activity_at from sessions where id = ?`, id)
	var title, workingDirectory, backend, model, mode string
	var last int64
	if err := row.Scan(&title, &workingDirectory, &backend, &model, &mode, &last); err != nil {
		return Session{}, fmt.Errorf("devin session %q: %w", id, err)
	}
	if title == "" {
		title = strings.Trim(strings.Join([]string{backend, model, mode}, "/"), "/")
	}
	return Session{
		Provider:  "devin",
		ID:        id,
		Ref:       "devin:" + id,
		Path:      dbPath,
		Bytes:     info.Size(),
		Modified:  unixFlexible(last),
		Workspace: workingDirectory,
		Title:     title,
	}, nil
}

// sameOrChild reports whether value is the same path as root or nested under
// it. Both must be absolute (after ~ expansion); relative inputs return false
// rather than being resolved against the process working directory, which would
// make any non-path string spuriously match.
func sameOrChild(value string, root string) bool {
	value = expandHome(value)
	root = expandHome(root)
	if !filepath.IsAbs(value) || !filepath.IsAbs(root) {
		return false
	}
	valueAbs := filepath.Clean(value)
	rootAbs := filepath.Clean(root)
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
			wsFile := filepath.Join(parts[0], "workspaceStorage", id, "workspace.json")
			return readCopilotFolder(wsFile)
		}
	}
	return ""
}

// readCopilotFolder resolves the real project folder for a VS Code Copilot
// session from its workspace.json ({"folder": "file:///abs/path"}), so sessions
// match a folder the same way Claude/Codex/Devin do. Returns "" if unavailable.
func readCopilotFolder(wsFile string) string {
	data, err := os.ReadFile(wsFile)
	if err != nil {
		return ""
	}
	var ws struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(data, &ws); err != nil || ws.Folder == "" {
		return ""
	}
	uri := strings.TrimPrefix(ws.Folder, "file://")
	if decoded, err := url.PathUnescape(uri); err == nil {
		return decoded
	}
	return uri
}
