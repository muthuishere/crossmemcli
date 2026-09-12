package providers

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

// A title says what a session was called; the first and last thing actually
// asked says what it became. Together they are what lets a caller — human or
// agent — tell two sessions in the same folder apart and pick the right one,
// which is the decision `list` exists to support.
const (
	questionChars   = 220
	questionHeadMax = 400
	questionTailMax = 512 * 1024
)

// withQuestions fills FirstQuestion/LastQuestion for the sessions that will
// actually be shown. It runs after limiting, so the cost is bounded by the
// page size rather than by how many sessions exist.
func withQuestions(sessions []Session) []Session {
	sem := make(chan struct{}, previewWorkers)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			first, last := sessionQuestions(sessions[i])
			sessions[i].FirstQuestion = truncate(first, questionChars)
			sessions[i].LastQuestion = truncate(last, questionChars)
		}(i)
	}
	wg.Wait()
	return sessions
}

func sessionQuestions(session Session) (string, string) {
	switch session.Provider {
	case "devin":
		return devinQuestions(session.ID)
	case "opencode":
		return openCodeQuestions(session.ID)
	case "copilot-cli":
		return copilotCLIQuestions(session.ID)
	default:
		return jsonlQuestions(session.Path, session.Provider)
	}
}

// jsonlQuestions reads the head of a transcript for the opening question and
// only the tail for the closing one. Transcripts run to tens of megabytes, so
// reading the whole file to find its last user line would make listing far
// slower than the decision it informs is worth.
func jsonlQuestions(path string, provider string) (string, string) {
	file, err := withRetry("open jsonl questions "+path, func() (*os.File, error) {
		return os.Open(path)
	})
	if err != nil {
		diag.Debugf("questions open path=%q err=%q", path, err)
		return "", ""
	}
	defer file.Close()

	first := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for i := 0; i < questionHeadMax && scanner.Scan(); i++ {
		if texts := userTexts(scanner.Bytes(), provider); len(texts) > 0 {
			first = texts[0]
			break
		}
	}

	info, err := file.Stat()
	if err != nil {
		return first, ""
	}
	offset := int64(0)
	if info.Size() > questionTailMax {
		offset = info.Size() - questionTailMax
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return first, ""
	}
	tail, err := readTail(file)
	if err != nil {
		return first, ""
	}
	lines := bytes.Split(tail, []byte("\n"))
	if offset > 0 && len(lines) > 0 {
		// The seek lands mid-line; that fragment is not valid JSON.
		lines = lines[1:]
	}
	last := ""
	for _, line := range lines {
		if texts := userTexts(line, provider); len(texts) > 0 {
			last = texts[len(texts)-1]
		}
	}
	if last == "" {
		last = first
	}
	return first, last
}

func readTail(file *os.File) ([]byte, error) {
	var buf bytes.Buffer
	reader := bufio.NewReaderSize(file, 64*1024)
	if _, err := buf.ReadFrom(reader); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// userTexts returns the user's words in one transcript line, in order, and
// nothing for assistant turns, tool traffic, and metadata. One line can carry
// several turns: a VS Code chat snapshot holds the whole conversation so far,
// and the extractor renders it as one "role: text" per line.
func userTexts(line []byte, provider string) []string {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(line, &obj) != nil || isSyntheticUserTurn(obj, provider) {
		return nil
	}
	var texts []string
	for _, chunk := range strings.Split(extractObject(obj, provider), "\n") {
		if rest, ok := strings.CutPrefix(chunk, "user: "); ok {
			if rest = strings.TrimSpace(rest); rest != "" && !isSyntheticText(rest) {
				texts = append(texts, rest)
			}
		}
	}
	return texts
}

func devinQuestions(sessionID string) (string, string) {
	return sqliteQuestions(devinDB(), sessionID,
		`select json_extract(chat_message,'$.content') from message_nodes
		   where session_id = ? and json_extract(chat_message,'$.role') = 'user'
		   order by node_id`)
}

func copilotCLIQuestions(sessionID string) (string, string) {
	return sqliteQuestions(storePath("copilot-cli", "sqlite-sessions"), sessionID,
		`select user_message from turns where session_id = ? order by turn_index`)
}

func openCodeQuestions(sessionID string) (string, string) {
	for _, dbPath := range openCodeDBs() {
		first, last := sqliteQuestions(dbPath, sessionID,
			`select json_extract(p.data,'$.text') from part p join message m on m.id = p.message_id
			   where p.session_id = ? and json_extract(m.data,'$.role') = 'user'
			     and json_extract(p.data,'$.type') = 'text'
			   order by m.time_created, p.time_created`)
		if first != "" {
			return first, last
		}
	}
	return "", ""
}

// sqliteQuestions runs one ordered query and keeps its first and last non-empty
// row. The row counts here are small (turns in a session), so a single ordered
// pass beats two queries with opposite sorts.
func sqliteQuestions(dbPath string, sessionID string, query string) (string, string) {
	if dbPath == "" || sessionID == "" {
		return "", ""
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		diag.Debugf("questions open db=%q err=%q", dbPath, err)
		return "", ""
	}
	defer db.Close()

	rows, err := withRetry("query questions", func() (*sql.Rows, error) {
		return db.Query(query, sessionID)
	})
	if err != nil {
		diag.Debugf("questions query session=%q err=%q", sessionID, err)
		return "", ""
	}
	defer rows.Close()

	first, last := "", ""
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			continue
		}
		text := strings.TrimSpace(value.String)
		if text == "" {
			continue
		}
		if first == "" {
			first = text
		}
		last = text
	}
	return first, last
}

// Agents record tool output as a user turn: a Claude tool_result carries
// type:"user" with a toolUseResult field. Those are the agent talking to
// itself, and taking the newest one as "the last question" surfaces a shell
// transcript instead of what the person actually asked. Only genuine human
// turns count here — the preview path still shows everything.
func isSyntheticUserTurn(obj map[string]any, provider string) bool {
	if _, ok := obj["toolUseResult"]; ok {
		return true
	}
	if meta, ok := obj["isMeta"].(bool); ok && meta {
		return true
	}
	message, _ := obj["message"].(map[string]any)
	if message == nil {
		return false
	}
	blocks, ok := message["content"].([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		item, _ := block.(map[string]any)
		if item != nil && stringValue(item["type"]) == "tool_result" {
			return true
		}
	}
	return false
}

// syntheticPrefixes mark text the harness injected into the conversation as if
// the user had typed it: slash-command plumbing, captured command output, the
// preamble of a resumed session, and the repo instruction files that Codex and
// others prepend to the first turn. These are the same blocks the
// crossmem-loader skill tells an agent to ignore when writing a brief.
var syntheticPrefixes = []string{
	"<command-name>",
	"<command-message>",
	"<local-command-stdout>",
	"<system-reminder>",
	"<task-notification>",
	"<user-prompt-submit-hook>",
	"<bash-input>",
	"<bash-stdout>",
	"[Request interrupted",
	"# AGENTS.md",
	"# CLAUDE.md",
	"<INSTRUCTIONS>",
	"# CrossMem Context Bundle",
	"Caveat: The messages below were generated",
	"This session is being continued from a previous conversation",
}

func isSyntheticText(text string) bool {
	for _, prefix := range syntheticPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
