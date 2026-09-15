package crossmem

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// OpenCode (sst/opencode) keeps sessions in SQLite under its data directory —
// ~/.local/share/opencode on Linux/macOS, %APPDATA%\opencode on Windows. A
// stable build writes opencode.db; dev/local builds use opencode-dev.db /
// opencode-local.db. We read whichever exist (read-only) and never touch the
// sibling auth.json / credential / account tables.
func (c *Client) openCodeDBs() []string {
	return c.storePaths("opencode", "sqlite-sessions")
}

func (c *Client) listOpenCode(limit int, folders []string, includeSubagents bool) ([]Session, error) {
	var sessions []Session
	seen := map[string]bool{}
	for _, dbPath := range c.openCodeDBs() {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
		if err != nil {
			c.log.debugf("opencode open db=%q err=%q", dbPath, err)
			continue
		}
		// OpenCode runs subagents (@explore, @general) as child sessions. They
		// are part of their parent's work, not sessions a user would resume,
		// so they are hidden unless asked for and listed on the parent instead.
		hasParent := c.hasColumn(db, "session", "parent_id")
		base := `select id, title, directory, time_updated, '' from session`
		if hasParent {
			base = `select id, title, directory, time_updated, coalesce(parent_id, '') from session`
			if !includeSubagents {
				base += ` where parent_id is null`
			}
		}
		query, args := listQuery(base+` order by time_updated desc`, limit, len(folders) > 0)
		rows, err := withRetry(c.log, "query opencode sessions", func() (*sql.Rows, error) {
			return db.QueryContext(c.context(), query, args...)
		})
		if err != nil {
			db.Close()
			c.log.debugf("opencode query db=%q err=%q", dbPath, err)
			continue
		}
		var found []Session
		for rows.Next() {
			var id, title, directory, parent string
			var updated int64
			if err := rows.Scan(&id, &title, &directory, &updated, &parent); err != nil {
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
			if parent != "" {
				session.Parent = "opencode:" + parent
			}
			if len(folders) > 0 && !matchesAnyFolder(session.Workspace, folders) {
				continue
			}
			found = append(found, session)
			if len(found) >= limit {
				break
			}
		}
		rows.Close()
		if hasParent {
			c.attachOpenCodeChildren(db, found)
		}
		sessions = append(sessions, found...)
		db.Close()
		if err := c.canceled(); err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

// attachOpenCodeChildren fills Children on each listed session with the refs
// of its subagent sessions, newest first.
func (c *Client) attachOpenCodeChildren(db *sql.DB, sessions []Session) {
	if len(sessions) == 0 {
		return
	}
	index := make(map[string]int, len(sessions))
	args := make([]any, 0, len(sessions))
	marks := make([]string, 0, len(sessions))
	for i, s := range sessions {
		index[s.ID] = i
		args = append(args, s.ID)
		marks = append(marks, "?")
	}
	rows, err := db.QueryContext(c.context(), `select parent_id, id from session where parent_id in (`+strings.Join(marks, ",")+`) order by time_updated desc`, args...)
	if err != nil {
		c.log.debugf("opencode children err=%q", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var parent, id string
		if rows.Scan(&parent, &id) != nil {
			continue
		}
		if i, ok := index[parent]; ok {
			sessions[i].Children = append(sessions[i].Children, "opencode:"+id)
		}
	}
}

// loadOpenCodeSession fetches one session by id for load --session opencode:<id>.
func (c *Client) loadOpenCodeSession(id string) (Session, error) {
	for _, dbPath := range c.openCodeDBs() {
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

func (c *Client) openCodePreview(sessionID string, maxChars int) string {
	if sessionID == "" {
		return ""
	}
	for _, dbPath := range c.openCodeDBs() {
		if text := c.openCodePreviewFromDB(dbPath, sessionID, maxChars); text != "" {
			return text
		}
	}
	return ""
}

func (c *Client) openCodePreviewFromDB(dbPath string, sessionID string, maxChars int) string {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		c.log.debugf("opencode preview open db=%q err=%q", dbPath, err)
		return ""
	}
	defer db.Close()

	rows, err := withRetry(c.log, "query opencode preview", func() (*sql.Rows, error) {
		return db.Query(`select m.data, p.data from part p join message m on m.id = p.message_id where p.session_id = ? order by m.time_created, p.time_created limit 4000`, sessionID)
	})
	if err != nil {
		c.log.debugf("opencode preview query session=%q err=%q", sessionID, err)
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
	}
	return fitPreview(chunks, "\n", maxChars)
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
