package providers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

const (
	// summaryPreviewChars is the default compact excerpt per session — enough
	// for an agent to summarize. fullPreviewChars is the larger excerpt emitted
	// with --full when the user wants fuller context loaded verbatim.
	summaryPreviewChars = 1800
	fullPreviewChars    = 24000
	summaryPreviewLines = 180
	fullPreviewLines    = 2000
)

func BuildContext(opts ListOptions) (string, error) {
	sessions, err := ListSessions(opts)
	if err != nil {
		return "", err
	}
	return renderBundle(sessions, opts), nil
}

// BuildSessionContext renders a bundle for one specific session the user picked
// (e.g. from a "choose from the last 5" list), instead of every session matching
// the folder. ref is the uniform handle from list: a transcript file path for
// the JSONL tools, or "devin:<id>" for the SQLite-backed Devin store. cwd is
// used only to attach the repo's active instructions to the bundle.
func BuildSessionContext(ref string, cwd string, full bool) (string, error) {
	if id, ok := strings.CutPrefix(ref, "devin:"); ok {
		session, err := loadDevinSession(id)
		if err != nil {
			return "", err
		}
		return renderBundle([]Session{session}, ListOptions{Provider: "devin", CWD: cwd, Full: full}), nil
	}

	abs, err := filepath.Abs(expandHome(ref))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	provider := inferProvider(abs, "")
	if provider == "unknown" {
		return "", fmt.Errorf("unrecognized session path: %s", abs)
	}
	title, scwd := readJSONLMeta(abs, provider)
	workspace := scwd
	if workspace == "" {
		workspace = inferWorkspace(abs, provider)
	}
	session := Session{
		Provider:  provider,
		Path:      abs,
		Bytes:     info.Size(),
		Modified:  info.ModTime(),
		Workspace: workspace,
		Title:     title,
	}
	return renderBundle([]Session{session}, ListOptions{Provider: provider, CWD: cwd, Full: full}), nil
}

func renderBundle(sessions []Session, opts ListOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CrossMem Context Bundle\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Provider: %s\n", providerOrAll(opts.Provider))
	if opts.CWD != "" {
		fmt.Fprintf(&b, "Folder: %s\n", expandHome(opts.CWD))
	} else {
		fmt.Fprintf(&b, "Folder: auto-discovered\n")
	}
	mode := "summary"
	if opts.Full {
		mode = "full"
	}
	fmt.Fprintf(&b, "Mode: %s\n", mode)
	fmt.Fprintf(&b, "Sessions: %d\n\n", len(sessions))

	previewChars := summaryPreviewChars
	previewLines := summaryPreviewLines
	if opts.Full {
		previewChars = fullPreviewChars
		previewLines = fullPreviewLines
	}

	if opts.CWD != "" {
		if guardrails, err := BuildGuardrails(opts.CWD); err == nil && !strings.Contains(guardrails, "No repo instruction files found") {
			fmt.Fprintln(&b, strings.TrimSpace(guardrails))
			fmt.Fprintln(&b)
		}
	}

	for _, session := range sessions {
		preview := ""
		if session.Provider == "devin" {
			preview = devinPreview(session.ID, previewChars)
		} else {
			preview = jsonlPreview(session.Path, session.Provider, previewChars, previewLines)
		}
		fmt.Fprintf(&b, "## %s: %s\n\n", session.Provider, titleOrBase(session))
		fmt.Fprintf(&b, "- Path: `%s`\n", session.Path)
		fmt.Fprintf(&b, "- Modified: %s\n", session.Modified.Format(time.RFC3339))
		fmt.Fprintf(&b, "- Bytes: %d\n", session.Bytes)
		if session.Workspace != "" {
			fmt.Fprintf(&b, "- Workspace: `%s`\n", session.Workspace)
		}
		fmt.Fprintln(&b)
		if preview == "" {
			fmt.Fprintln(&b, "_No readable text extracted._")
		} else {
			fmt.Fprintln(&b, preview)
		}
		fmt.Fprintln(&b)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func providerOrAll(provider string) string {
	if provider == "" {
		return "all"
	}
	return provider
}

func titleOrBase(session Session) string {
	if session.Title != "" {
		return session.Title
	}
	if session.ID != "" {
		return session.ID
	}
	return session.Path
}

func jsonlPreview(path string, provider string, maxChars int, maxLines int) string {
	file, err := withRetry("open jsonl preview "+path, func() (*os.File, error) {
		return os.Open(path)
	})
	if err != nil {
		diag.Debugf("jsonl preview path=%q provider=%s err=%q", path, provider, err)
		return ""
	}
	defer file.Close()

	ring := make([]string, 0, maxLines)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(ring) == maxLines {
			copy(ring, ring[1:])
			ring[len(ring)-1] = line
		} else {
			ring = append(ring, line)
		}
	}

	var chunks []string
	for _, line := range ring {
		text := extractJSONLText([]byte(line), provider)
		if text == "" {
			continue
		}
		chunks = append(chunks, text)
		if len(strings.Join(chunks, "\n\n")) > maxChars {
			break
		}
	}
	return truncate(strings.Join(chunks, "\n\n"), maxChars)
}

func extractJSONLText(raw []byte, provider string) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	switch provider {
	case "codex":
		return extractCodex(obj)
	case "claude":
		return extractClaude(obj)
	case "copilot":
		return extractCopilot(obj)
	default:
		return ""
	}
}

func extractCodex(obj map[string]any) string {
	payload, _ := obj["payload"].(map[string]any)
	if obj["type"] == "event_msg" && payload["type"] == "user_message" {
		return roleText("user", stringValue(payload["message"]))
	}
	if obj["type"] != "response_item" || payload["type"] != "message" {
		return ""
	}
	role := stringValue(payload["role"])
	if role != "user" && role != "assistant" {
		return ""
	}
	return roleText(role, contentText(payload["content"]))
}

func extractClaude(obj map[string]any) string {
	if obj["type"] == "ai-title" {
		return roleText("title", stringValue(obj["aiTitle"]))
	}
	typ := stringValue(obj["type"])
	if typ != "user" && typ != "assistant" {
		return ""
	}
	msg, _ := obj["message"].(map[string]any)
	return roleText(typ, contentText(msg["content"]))
}

func extractCopilot(obj map[string]any) string {
	typ := stringValue(obj["type"])
	data, _ := obj["data"].(map[string]any)
	if typ == "user.message" {
		return roleText("user", stringValue(data["content"]))
	}
	if typ == "assistant.message" {
		return roleText("assistant", stringValue(data["content"]))
	}

	if kind, ok := obj["kind"].(float64); !ok || kind != 0 {
		return ""
	}
	v, _ := obj["v"].(map[string]any)
	requests, _ := v["requests"].([]any)
	var chunks []string
	for _, item := range requests {
		req, _ := item.(map[string]any)
		msg, _ := req["message"].(map[string]any)
		if text := roleText("user", stringValue(msg["text"])); text != "" {
			chunks = append(chunks, text)
		}
		info, _ := req["responseMarkdownInfo"].(map[string]any)
		if text := roleText("assistant", stringValue(info["markdown"])); text != "" {
			chunks = append(chunks, text)
		}
	}
	return strings.Join(chunks, "\n")
}

func devinPreview(sessionID string, maxChars int) string {
	if sessionID == "" {
		return ""
	}
	dbPath := expandHome("~/.local/share/devin/cli/sessions.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		diag.Debugf("devin preview open db=%q err=%q", dbPath, err)
		return ""
	}
	defer db.Close()

	rows, err := withRetry("query devin preview", func() (*sql.Rows, error) {
		return db.Query(`select chat_message from message_nodes where session_id = ? order by node_id desc limit 24`, sessionID)
	})
	if err != nil {
		diag.Debugf("devin preview query session=%q err=%q", sessionID, err)
		return ""
	}
	defer rows.Close()

	var raws []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err == nil {
			raws = append(raws, raw)
		}
	}
	var chunks []string
	seen := map[string]bool{}
	for i := len(raws) - 1; i >= 0; i-- {
		text := extractDevin(raws[i])
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		chunks = append(chunks, text)
	}
	return truncate(strings.Join(chunks, "\n\n"), maxChars)
}

func extractDevin(raw string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}
	role := stringValue(obj["role"])
	if role != "user" && role != "assistant" {
		return ""
	}
	return roleText(role, stringValue(obj["content"]))
}

func contentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			switch part := item.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				if part["type"] == "text" || part["type"] == "input_text" || part["type"] == "output_text" || part["type"] == "tool_result" {
					parts = append(parts, stringValue(part["text"]))
					parts = append(parts, stringValue(part["content"]))
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func roleText(role string, text string) string {
	text = normalize(text)
	if text == "" {
		return ""
	}
	return role + ": " + text
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "..."
}
