package crossmem

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"
)

func withRetry[T any](log *logger, label string, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	delays := []time.Duration{0, 25 * time.Millisecond, 75 * time.Millisecond}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		value, err := fn()
		if err == nil {
			if attempt > 0 {
				log.debugf("retry label=%q attempts=%d result=ok", label, attempt+1)
			}
			return value, nil
		}
		last = err
		if !isTransient(err) {
			return zero, err
		}
		log.debugf("retry label=%q attempt=%d err=%q", label, attempt+1, err)
	}
	return zero, last
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return false
	}
	text := err.Error()
	return containsAny(text, []string{
		"database is locked",
		"database table is locked",
		"busy",
		"resource temporarily unavailable",
		"interrupted system call",
		"too many open files",
	})
}

func containsAny(text string, needles []string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// listQuery finishes a newest-first session listing query. With no folder
// filter the database applies the limit. With one it cannot: the folder is
// matched in Go (case-folded on Windows, child directories included — ADR 1),
// so limiting in SQL first would return an empty page for a folder whose
// sessions are older than the newest `limit` rows elsewhere. Those rows stream
// instead and the caller stops once it has `limit` matches.
func listQuery(query string, limit int, cwdFilter string) (string, []any) {
	if cwdFilter != "" {
		return query, nil
	}
	return query + " limit ?", []any{limit}
}

// hasColumn reports whether table has column, so a query can use a column that
// only newer builds of a tool write.
func (c *Client) hasColumn(db *sql.DB, table string, column string) bool {
	rows, err := db.QueryContext(c.context(), "select name from pragma_table_info(?)", table)
	if err != nil {
		c.log.debugf("table_info table=%q err=%q", table, err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name == column {
			return true
		}
	}
	return false
}
