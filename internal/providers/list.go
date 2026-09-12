package providers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

	// The SQLite providers limit in the query, but sessions are dropped after
	// that — the caller's own live session, and anything outside the folder. Ask
	// for enough extra rows that filtering cannot starve the requested page.
	queryLimit := opts.Limit + len(currentSessionIDs())

	var sessions []Session
	if opts.Provider == "all" || opts.Provider == "devin" {
		devin, err := listDevin(queryLimit, opts.CWD)
		if err == nil {
			sessions = append(sessions, devin...)
		} else {
			diag.Debugf("list devin err=%q", err)
		}
	}
	if opts.Provider == "all" || opts.Provider == "opencode" {
		opencode, err := listOpenCode(queryLimit, opts.CWD)
		if err == nil {
			sessions = append(sessions, opencode...)
		} else {
			diag.Debugf("list opencode err=%q", err)
		}
	}
	if opts.Provider == "all" || opts.Provider == "copilot-cli" {
		copilotCLI, err := listCopilotCLI(queryLimit, opts.CWD)
		if err == nil {
			sessions = append(sessions, copilotCLI...)
		} else {
			diag.Debugf("list copilot-cli err=%q", err)
		}
	}

	for _, root := range providerRoots(opts.Provider) {
		jsonl, err := listJSONL(root.Path, root.Provider)
		if err != nil {
			diag.Debugf("list jsonl root=%q provider=%s err=%q", root.Path, root.Provider, err)
			continue
		}
		if opts.CWD != "" {
			jsonl = filterByCWD(jsonl, opts.CWD)
		}
		sessions = append(sessions, jsonl...)
	}

	// Newest first, with the ref as a tiebreaker so equal timestamps cannot
	// reorder between runs — callers write these results to disk.
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Modified.Equal(sessions[j].Modified) {
			return sessions[i].Ref < sessions[j].Ref
		}
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	sessions = markCurrent(sessions, opts.IncludeCurrent)
	if len(sessions) > opts.Limit {
		sessions = sessions[:opts.Limit]
	}
	if opts.Questions {
		sessions = withQuestions(sessions)
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
		// A VS Code workspaceStorage tree holds far more JSONL than chat; keep
		// only the chat transcripts. This applies to every fork of it, not just
		// Copilot in VS Code.
		if isWorkspaceStoragePath(path) && !isCopilotSessionPath(path) {
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
			base := filepath.Base(e.path)
			sessions[i] = Session{
				Provider: inferred,
				// JSONL stores name the transcript <session-id>.jsonl, which is
				// the id the owning agent exports for its live session.
				ID:        strings.TrimSuffix(base, filepath.Ext(base)),
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

// devinDB is the one Devin session database on this machine: XDG
// ~/.local/share/devin/cli on Linux/macOS, %APPDATA%\devin\cli on Windows, or
// wherever $DEVIN_DB_PATH / $DEVIN_HOME / the user config point it. Empty when
// Devin is not installed here.
func devinDB() string {
	return storePath("devin", "sqlite-sessions")
}

func listDevin(limit int, cwdFilter string) ([]Session, error) {
	dbPath := devinDB()
	if dbPath == "" {
		return nil, nil
	}
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
	dbPath := devinDB()
	if dbPath == "" {
		return Session{}, fmt.Errorf("devin session %q: no Devin session store found on this machine", id)
	}
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
	// Case is folded on Windows before comparing: the two sides come from
	// different tools, and they disagree on drive-letter and folder case.
	valueAbs := normalizeCase(filepath.Clean(value))
	rootAbs := normalizeCase(filepath.Clean(root))
	if pathsEqual(valueAbs, rootAbs) {
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
	slashed := filepath.ToSlash(path)
	return strings.Contains(slashed, "/chatSessions/") || strings.Contains(slashed, "/GitHub.copilot-chat/transcripts/")
}

func inferWorkspace(path string, provider string) string {
	// Keyed on the store's shape first: every VS Code fork records the real
	// folder in workspaceStorage/<id>/workspace.json, whoever ships it.
	if isWorkspaceStoragePath(path) {
		parts := strings.Split(filepath.ToSlash(path), "/workspaceStorage/")
		if len(parts) == 2 {
			id := strings.Split(parts[1], "/")[0]
			wsFile := filepath.Join(filepath.FromSlash(parts[0]), "workspaceStorage", id, "workspace.json")
			return readCopilotFolder(wsFile)
		}
		return ""
	}
	switch provider {
	case "claude":
		return decodeClaudeDir(filepath.Base(filepath.Dir(path)))
	case "codex":
		root := storePath("codex", "jsonl-sessions")
		if root == "" {
			root = expandPath("~/.codex/sessions")
		}
		if rel, err := filepath.Rel(root, filepath.Dir(path)); err == nil {
			return rel
		}
	}
	return ""
}

// claudeWindowsDir matches the Windows form of a Claude Code project directory,
// where the drive letter survives as "C--" ahead of the dash-joined path.
var claudeWindowsDir = regexp.MustCompile(`^([A-Za-z])--(.*)$`)

// decodeClaudeDir turns Claude Code's encoded project directory back into a
// working directory: every separator was replaced by "-", so /Users/x/repo is
// stored as -Users-x-repo and C:\Users\x\repo as C--Users-x-repo. Decoding is
// lossy when a real folder name contains a dash, which is why this is only the
// fallback for transcripts that carry no cwd line.
func decodeClaudeDir(dir string) string {
	if match := claudeWindowsDir.FindStringSubmatch(dir); match != nil {
		return match[1] + `:\` + strings.ReplaceAll(match[2], "-", `\`)
	}
	if strings.HasPrefix(dir, "-") {
		return strings.ReplaceAll(dir, "-", "/")
	}
	return dir
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
		uri = decoded
	}
	// A Windows folder arrives as file:///c%3A/Users/... which unescapes to
	// "/c:/Users/..."; drop the leading slash so it is a real path again.
	if windowsFileURIPath.MatchString(uri) {
		uri = strings.TrimPrefix(uri, "/")
	}
	return uri
}

var windowsFileURIPath = regexp.MustCompile(`^/[A-Za-z]:[/\\]`)
