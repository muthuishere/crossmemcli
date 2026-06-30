package providers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

// OpenCode (sst/opencode) keeps sessions in SQLite under ~/.local/share/opencode.
// A stable build writes opencode.db; dev/local builds use opencode-dev.db /
// opencode-local.db. We read whichever exist (read-only) and never touch the
// sibling auth.json / credential / account tables.
func openCodeDBs() []string {
	matches, err := filepath.Glob(expandHome("~/.local/share/opencode/opencode*.db"))
	if err != nil {
		return nil
	}
	return matches
}

func listOpenCode(limit int, cwdFilter string) ([]Session, error) {
	var sessions []Session
	seen := map[string]bool{}
	for _, dbPath := range openCodeDBs() {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
		if err != nil {
			diag.Debugf("opencode open db=%q err=%q", dbPath, err)
			continue
		}
		rows, err := withRetry("query opencode sessions", func() (*sql.Rows, error) {
			return db.Query(`select id, title, directory, time_updated from session order by time_updated desc limit ?`, limit)
		})
		if err != nil {
			db.Close()
			diag.Debugf("opencode query db=%q err=%q", dbPath, err)
			continue
		}
		for rows.Next() {
			var id, title, directory string
			var updated int64
			if err := rows.Scan(&id, &title, &directory, &updated); err != nil {
				continue
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			session := Session{
				Provider:  "opencode",
				ID:        id,
				Ref:       "opencode:" + id,
				Path:      dbPath,
				Bytes:     info.Size(),
				Modified:  unixFlexible(updated),
				Workspace: directory,
				Title:     title,
			}
			if cwdFilter != "" && !sameOrChild(session.Workspace, cwdFilter) {
				continue
			}
			sessions = append(sessions, session)
		}
		rows.Close()
		db.Close()
	}
	return sessions, nil
}

// loadOpenCodeSession fetches one session by id for load --session opencode:<id>.
func loadOpenCodeSession(id string) (Session, error) {
	for _, dbPath := range openCodeDBs() {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
		if err != nil {
			continue
		}
		row := db.QueryRow(`select title, directory, time_updated from session where id = ?`, id)
		var title, directory string
		var updated int64
		err = row.Scan(&title, &directory, &updated)
		db.Close()
		if err == nil {
			return Session{
				Provider:  "opencode",
				ID:        id,
				Ref:       "opencode:" + id,
				Path:      dbPath,
				Bytes:     info.Size(),
				Modified:  unixFlexible(updated),
				Workspace: directory,
				Title:     title,
			}, nil
		}
	}
	return Session{}, fmt.Errorf("opencode session %q not found", id)
}

func openCodePreview(sessionID string, maxChars int) string {
	if sessionID == "" {
		return ""
	}
	for _, dbPath := range openCodeDBs() {
		if text := openCodePreviewFromDB(dbPath, sessionID, maxChars); text != "" {
			return text
		}
	}
	return ""
}

func openCodePreviewFromDB(dbPath string, sessionID string, maxChars int) string {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		diag.Debugf("opencode preview open db=%q err=%q", dbPath, err)
		return ""
	}
	defer db.Close()

	rows, err := withRetry("query opencode preview", func() (*sql.Rows, error) {
		return db.Query(`select m.data, p.data from part p join message m on m.id = p.message_id where p.session_id = ? order by m.time_created, p.time_created limit 4000`, sessionID)
	})
	if err != nil {
		diag.Debugf("opencode preview query session=%q err=%q", sessionID, err)
		return ""
	}
	defer rows.Close()

	var chunks []string
	for rows.Next() {
		var messageData, partData string
		if err := rows.Scan(&messageData, &partData); err != nil {
			continue
		}
		text := extractOpenCode(messageData, partData)
		if text == "" {
			continue
		}
		chunks = append(chunks, text)
		if len(strings.Join(chunks, "\n")) > maxChars {
			break
		}
	}
	return truncate(strings.Join(chunks, "\n"), maxChars)
}

// extractOpenCode turns one (message, part) pair into "role: text" for text
// parts only. Tool calls, reasoning, and step markers carry no conversational
// text and are skipped.
func extractOpenCode(messageData string, partData string) string {
	var part map[string]any
	if err := json.Unmarshal([]byte(partData), &part); err != nil {
		return ""
	}
	if stringValue(part["type"]) != "text" {
		return ""
	}
	text := stringValue(part["text"])
	if text == "" {
		return ""
	}
	role := "assistant"
	var msg map[string]any
	if err := json.Unmarshal([]byte(messageData), &msg); err == nil {
		if r := stringValue(msg["role"]); r != "" {
			role = r
		}
	}
	return roleText(role, text)
}
