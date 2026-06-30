package providers

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

// The GitHub Copilot CLI (distinct from the VS Code Copilot chat store) keeps its
// own SQLite session store at ~/.copilot/session-store.db: a `sessions` table
// (cwd = working dir, summary = title) and a denormalized `turns` table with one
// user_message / assistant_response pair per row. The sibling auth.db and the
// config/* state files are never read.
const copilotCLIDBPath = "~/.copilot/session-store.db"

func openCopilotCLIDB() (*sql.DB, os.FileInfo, error) {
	dbPath := expandHome(copilotCLIDBPath)
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		return nil, nil, err
	}
	return db, info, nil
}

func listCopilotCLI(limit int, cwdFilter string) ([]Session, error) {
	db, info, err := openCopilotCLIDB()
	if err != nil {
		return nil, nil
	}
	defer db.Close()

	rows, err := withRetry("query copilot-cli sessions", func() (*sql.Rows, error) {
		return db.Query(`select id, cwd, summary, updated_at from sessions order by updated_at desc limit ?`, limit)
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var id string
		var cwd, summary sql.NullString
		var updated sql.NullString
		if err := rows.Scan(&id, &cwd, &summary, &updated); err != nil {
			continue
		}
		session := Session{
			Provider:  "copilot-cli",
			ID:        id,
			Ref:       "copilot-cli:" + id,
			Path:      info.Name(),
			Bytes:     info.Size(),
			Modified:  parseTimeFlexible(updated.String),
			Workspace: cwd.String,
			Title:     summary.String,
		}
		if cwdFilter != "" && !sameOrChild(session.Workspace, cwdFilter) {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func loadCopilotCLISession(id string) (Session, error) {
	db, info, err := openCopilotCLIDB()
	if err != nil {
		return Session{}, err
	}
	defer db.Close()

	row := db.QueryRow(`select cwd, summary, updated_at from sessions where id = ?`, id)
	var cwd, summary, updated sql.NullString
	if err := row.Scan(&cwd, &summary, &updated); err != nil {
		return Session{}, fmt.Errorf("copilot-cli session %q: %w", id, err)
	}
	return Session{
		Provider:  "copilot-cli",
		ID:        id,
		Ref:       "copilot-cli:" + id,
		Path:      info.Name(),
		Bytes:     info.Size(),
		Modified:  parseTimeFlexible(updated.String),
		Workspace: cwd.String,
		Title:     summary.String,
	}, nil
}

func copilotCLIPreview(sessionID string, maxChars int) string {
	if sessionID == "" {
		return ""
	}
	db, _, err := openCopilotCLIDB()
	if err != nil {
		diag.Debugf("copilot-cli preview open err=%q", err)
		return ""
	}
	defer db.Close()

	rows, err := withRetry("query copilot-cli preview", func() (*sql.Rows, error) {
		return db.Query(`select user_message, assistant_response from turns where session_id = ? order by turn_index limit 400`, sessionID)
	})
	if err != nil {
		diag.Debugf("copilot-cli preview query session=%q err=%q", sessionID, err)
		return ""
	}
	defer rows.Close()

	var chunks []string
	for rows.Next() {
		var user, assistant sql.NullString
		if err := rows.Scan(&user, &assistant); err != nil {
			continue
		}
		if text := roleText("user", user.String); text != "" {
			chunks = append(chunks, text)
		}
		if text := roleText("assistant", assistant.String); text != "" {
			chunks = append(chunks, text)
		}
		if len(strings.Join(chunks, "\n")) > maxChars {
			break
		}
	}
	return truncate(strings.Join(chunks, "\n"), maxChars)
}

// parseTimeFlexible accepts the RFC3339 timestamps the Copilot CLI writes
// (2026-06-29T16:03:02.557Z) and the plain SQLite datetime() form, returning a
// zero time on anything unparseable.
func parseTimeFlexible(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
