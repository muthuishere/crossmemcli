package providers

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

const (
	// briefPreviewChars is the default per-session budget of cleaned conversation
	// fed to the agent to synthesize a persona + decisions brief. fullPreviewChars
	// is the larger, more verbatim budget used with --full.
	briefPreviewChars = 9000
	fullPreviewChars  = 24000
	previewLines      = 4000
	// previewWorkers bounds concurrent transcript reads so we go fast without
	// tripping "too many open files".
	previewWorkers = 24
)

// BuildContext renders a bundle of the most recent sessions matching the folder.
// crossmem is deterministic plumbing: it finds the sessions and emits their
// conversation. Deciding what to keep, skip, or treat as noise is the consuming
// agent's job (see the crossmem-loader skill).
func BuildContext(opts ListOptions) (string, error) {
	sessions, err := ListSessions(opts)
	if err != nil {
		return "", err
	}
	maxChars := briefPreviewChars
	if opts.Full {
		maxChars = fullPreviewChars
	}
	bodies := computePreviews(sessions, maxChars)
	return renderBundle(sessions, bodies, opts), nil
}

// BuildSessionContext renders a bundle for one specific session the user picked
// from a list. ref is the uniform handle: a transcript file path for the JSONL
// tools, or "devin:<id>" for the SQLite-backed Devin store.
func BuildSessionContext(ref string, cwd string, full bool) (string, error) {
	var session Session
	if id, ok := strings.CutPrefix(ref, "devin:"); ok {
		s, err := loadDevinSession(id)
		if err != nil {
			return "", err
		}
		session = s
	} else {
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
		session = Session{Provider: provider, Path: abs, Bytes: info.Size(), Modified: info.ModTime(), Workspace: workspace, Title: title}
	}
	maxChars := briefPreviewChars
	if full {
		maxChars = fullPreviewChars
	}
	body := computePreviews([]Session{session}, maxChars)[0]
	return renderBundle([]Session{session}, []string{body}, ListOptions{Provider: session.Provider, CWD: cwd, Full: full}), nil
}

// computePreviews extracts each session's cleaned conversation concurrently.
func computePreviews(sessions []Session, maxChars int) []string {
	out := make([]string, len(sessions))
	sem := make(chan struct{}, previewWorkers)
	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s := sessions[i]
			if s.Provider == "devin" {
				out[i] = devinPreview(s.ID, maxChars)
			} else {
				out[i] = jsonlPreview(s.Path, s.Provider, maxChars, previewLines)
			}
		}(i)
	}
	wg.Wait()
	return out
}

func renderBundle(sessions []Session, bodies []string, opts ListOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CrossMem Context Bundle\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Searched: %s\n", searchedProviders(opts.Provider))
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

	for i, session := range sessions {
		fmt.Fprintf(&b, "## %s: %s\n\n", session.Provider, titleOrBase(session))
		fmt.Fprintf(&b, "- Modified: %s\n", session.Modified.Format(time.RFC3339))
		if session.Workspace != "" {
			fmt.Fprintf(&b, "- Workspace: `%s`\n", session.Workspace)
		}
		fmt.Fprintln(&b)
		if strings.TrimSpace(bodies[i]) == "" {
			fmt.Fprintln(&b, "_No readable text extracted._")
		} else {
			fmt.Fprintln(&b, bodies[i])
		}
		fmt.Fprintln(&b)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// searchedProviders names the stores that were searched, so it's clear every
// tool (including Copilot) was covered.
func searchedProviders(provider string) string {
	if provider == "" || provider == "all" {
		return "claude, codex, copilot, devin"
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
