package crossmem

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// event is one conversational turn. No provider, model, agent, or token fields.
type event struct {
	kind string // user, assistant, tool, thinking
	name string
	text string
	at   time.Time
}

func (c *Client) sessionQA(session Session) []QARecord {
	return qaPairs(c.sessionEvents(session), session)
}

func (c *Client) sessionEvents(session Session) []event {
	switch session.Provider {
	case "devin":
		return c.devinEvents(session.ID)
	case "opencode":
		return c.openCodeEvents(session.ID)
	case "copilot-cli":
		return c.copilotCLIEvents(session.ID)
	default:
		return c.jsonlEvents(session.Path, session.Provider)
	}
}

func (c *Client) jsonlEvents(path string, provider string) []event {
	data, err := withRetry(c.log, "read jsonl events "+path, func() ([]byte, error) {
		return os.ReadFile(path)
	})
	if err != nil {
		c.log.debugf("events read path=%q err=%q", path, err)
		return nil
	}
	var events []event
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		data = rest
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		events = append(events, eventsFromLine(line, provider)...)
	}
	return events
}

func eventsFromLine(line []byte, provider string) []event {
	switch {
	case provider == "codex":
		return codexLineEvents(line)
	case provider == "claude":
		return claudeLineEvents(line)
	case isVSCodeChat(provider):
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return nil
		}
		return copilotEvents(obj)
	default:
		return nil
	}
}

type claudeLine struct {
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	IsMeta        bool            `json:"isMeta"`
	Message       struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
}

func claudeLineEvents(line []byte) []event {
	var obj claudeLine
	if json.Unmarshal(line, &obj) != nil {
		return nil
	}
	if obj.IsMeta || (obj.Type != "user" && obj.Type != "assistant") {
		return nil
	}
	at := parseStamp(obj.Timestamp)
	events := contentEvents(obj.Type, obj.Message.Content, at)
	if obj.Type == "user" && len(obj.ToolUseResult) > 0 && !hasKind(events, "tool") {
		if text := rawString(obj.ToolUseResult); text != "" {
			events = append(events, event{kind: "tool", name: "result", text: text, at: at})
		}
	}
	return events
}

func contentEvents(role string, raw json.RawMessage, at time.Time) []event {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		s = strings.TrimSpace(s)
		if s == "" || (role == "user" && isSyntheticText(s)) {
			return nil
		}
		return []event{{kind: role, text: s, at: at}}
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		if s := strings.TrimSpace(string(raw)); s != "" {
			return []event{{kind: role, text: s, at: at}}
		}
		return nil
	}
	var events []event
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text", "":
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			if role == "user" && isSyntheticText(text) {
				continue
			}
			events = append(events, event{kind: role, text: text, at: at})
		case "thinking":
			if text := strings.TrimSpace(block.Thinking); text != "" {
				events = append(events, event{kind: "thinking", text: text, at: at})
			} else if text := strings.TrimSpace(block.Text); text != "" {
				events = append(events, event{kind: "thinking", text: text, at: at})
			}
		case "tool_use", "function_call", "custom_tool_use":
			text := rawString(block.Input)
			events = append(events, event{kind: "tool", name: block.Name, text: text, at: at})
		case "tool_result":
			text := rawString(block.Content)
			if text == "" {
				text = strings.TrimSpace(block.Text)
			}
			if text != "" {
				events = append(events, event{kind: "tool", name: "result", text: text, at: at})
			}
		}
	}
	return events
}

type codexLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Name      string          `json:"name"`
		Message   string          `json:"message"`
		Content   json.RawMessage `json:"content"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
		Output    json.RawMessage `json:"output"`
	} `json:"payload"`
}

func codexLineEvents(line []byte) []event {
	var obj codexLine
	if json.Unmarshal(line, &obj) != nil {
		return nil
	}
	at := parseStamp(obj.Timestamp)
	p := obj.Payload
	if obj.Type == "event_msg" && p.Type == "user_message" {
		text := strings.TrimSpace(p.Message)
		if text == "" || isSyntheticText(text) {
			return nil
		}
		return []event{{kind: "user", text: text, at: at}}
	}
	if obj.Type != "response_item" {
		return nil
	}
	switch p.Type {
	case "message":
		role := p.Role
		if role != "user" && role != "assistant" {
			return nil
		}
		return contentEvents(role, p.Content, at)
	case "function_call", "custom_tool_call", "tool_use", "custom_tool_call_input":
		text := rawString(p.Arguments)
		if text == "" {
			text = rawString(p.Input)
		}
		return []event{{kind: "tool", name: p.Name, text: text, at: at}}
	case "function_call_output", "custom_tool_call_output", "tool_result":
		text := rawString(p.Output)
		if text == "" {
			text = rawString(p.Content)
		}
		if text == "" {
			return nil
		}
		return []event{{kind: "tool", name: "result", text: text, at: at}}
	default:
		return nil
	}
}

func copilotEvents(obj map[string]any) []event {
	typ := stringValue(obj["type"])
	data := asMap(obj["data"])
	if typ == "user.message" {
		text := strings.TrimSpace(stringValue(data["content"]))
		if text == "" || isSyntheticText(text) {
			return nil
		}
		return []event{{kind: "user", text: text}}
	}
	if typ == "assistant.message" {
		text := strings.TrimSpace(stringValue(data["content"]))
		if text == "" {
			return nil
		}
		return []event{{kind: "assistant", text: text}}
	}

	var requests []any
	if kind, ok := obj["kind"].(float64); ok {
		switch kind {
		case 0:
			if v := asMap(obj["v"]); v != nil {
				requests, _ = v["requests"].([]any)
			}
		case 2:
			if keyPath, ok := obj["k"].([]any); ok && len(keyPath) >= 1 && stringValue(keyPath[0]) == "requests" {
				requests = asList(obj["v"])
			}
		}
	}
	var events []event
	for _, item := range requests {
		req := asMap(item)
		if req == nil {
			continue
		}
		if msg := asMap(req["message"]); msg != nil {
			text := strings.TrimSpace(stringValue(msg["text"]))
			if text != "" && !isSyntheticText(text) {
				events = append(events, event{kind: "user", text: text})
			}
		}
		if text := strings.TrimSpace(copilotResponseText(req)); text != "" {
			events = append(events, event{kind: "assistant", text: text})
		}
	}
	return events
}

func qaPairs(events []event, session Session) []QARecord {
	fallback := session.Modified.UTC()
	var pairs []QARecord
	var current *QARecord
	flush := func() {
		if current == nil {
			return
		}
		if current.Q == "" && len(current.Messages) == 0 {
			current = nil
			return
		}
		pairs = append(pairs, *current)
		current = nil
	}
	start := func(q string, at time.Time) {
		flush()
		if at.IsZero() {
			at = fallback
		}
		current = &QARecord{
			SessionID: session.ID,
			Folder:    session.Workspace,
			Q:         q,
			Time:      at.UTC().Format(time.RFC3339),
		}
		if q != "" {
			current.Messages = append(current.Messages, Message{Role: "user", Content: q})
		}
	}
	ensure := func(at time.Time) {
		if current == nil {
			start("", at)
		}
	}
	for _, ev := range events {
		switch ev.kind {
		case "user":
			if ev.text == "" {
				continue
			}
			start(ev.text, ev.at)
		case "assistant":
			if ev.text == "" {
				continue
			}
			ensure(ev.at)
			if current.A == "" {
				current.A = ev.text
			} else {
				current.A += "\n\n" + ev.text
			}
			current.Messages = append(current.Messages, Message{Role: "assistant", Content: ev.text})
		case "thinking":
			if ev.text == "" {
				continue
			}
			ensure(ev.at)
			current.Messages = append(current.Messages, Message{Role: "thinking", Content: ev.text})
		case "tool":
			if ev.text == "" && ev.name == "" {
				continue
			}
			ensure(ev.at)
			current.Messages = append(current.Messages, Message{Role: "tool", Name: ev.name, Content: ev.text})
		}
	}
	flush()
	return pairs
}

func parseStamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func rawString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	if raw[0] == '[' {
		var blocks []claudeBlock
		if json.Unmarshal(raw, &blocks) == nil {
			var b strings.Builder
			for _, block := range blocks {
				text := strings.TrimSpace(block.Text)
				if text == "" {
					text = strings.TrimSpace(block.Thinking)
				}
				if text == "" {
					text = rawString(block.Content)
				}
				if text == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(text)
			}
			if b.Len() > 0 {
				return b.String()
			}
		}
	}
	return string(raw)
}

func hasKind(events []event, kind string) bool {
	for _, ev := range events {
		if ev.kind == kind {
			return true
		}
	}
	return false
}

func asMap(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func asList(value any) []any {
	if value == nil {
		return nil
	}
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{value}
}

func (c *Client) devinEvents(sessionID string) []event {
	if sessionID == "" {
		return nil
	}
	dbPath := c.devinDB()
	if dbPath == "" {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		c.log.debugf("devin events open err=%q", err)
		return nil
	}
	defer db.Close()
	rows, err := withRetry(c.log, "query devin events", func() (*sql.Rows, error) {
		return db.Query(`select chat_message from message_nodes where session_id = ? order by node_id`, sessionID)
	})
	if err != nil {
		c.log.debugf("devin events query session=%q err=%q", sessionID, err)
		return nil
	}
	defer rows.Close()
	var events []event
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(raw), &obj) != nil {
			continue
		}
		role := stringValue(obj["role"])
		if role != "user" && role != "assistant" {
			if name := stringValue(obj["tool"]); name != "" {
				events = append(events, event{kind: "tool", name: name, text: rawString(jsonRaw(obj["content"]))})
			}
			continue
		}
		events = append(events, contentEvents(role, jsonRaw(obj["content"]), time.Time{})...)
	}
	return events
}

func (c *Client) copilotCLIEvents(sessionID string) []event {
	if sessionID == "" {
		return nil
	}
	db, _, _, err := c.openCopilotCLIDB()
	if err != nil {
		c.log.debugf("copilot-cli events open err=%q", err)
		return nil
	}
	defer db.Close()
	rows, err := withRetry(c.log, "query copilot-cli events", func() (*sql.Rows, error) {
		return db.Query(`select user_message, assistant_response from turns where session_id = ? order by turn_index`, sessionID)
	})
	if err != nil {
		c.log.debugf("copilot-cli events query session=%q err=%q", sessionID, err)
		return nil
	}
	defer rows.Close()
	var events []event
	for rows.Next() {
		var user, assistant sql.NullString
		if err := rows.Scan(&user, &assistant); err != nil {
			continue
		}
		if text := strings.TrimSpace(user.String); text != "" && !isSyntheticText(text) {
			events = append(events, event{kind: "user", text: text})
		}
		if text := strings.TrimSpace(assistant.String); text != "" {
			events = append(events, event{kind: "assistant", text: text})
		}
	}
	return events
}

func (c *Client) openCodeEvents(sessionID string) []event {
	if sessionID == "" {
		return nil
	}
	for _, dbPath := range c.openCodeDBs() {
		if events := c.openCodeEventsFromDB(dbPath, sessionID); len(events) > 0 {
			return events
		}
	}
	return nil
}

func (c *Client) openCodeEventsFromDB(dbPath string, sessionID string) []event {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(250)")
	if err != nil {
		c.log.debugf("opencode events open db=%q err=%q", dbPath, err)
		return nil
	}
	defer db.Close()
	rows, err := withRetry(c.log, "query opencode events", func() (*sql.Rows, error) {
		return db.Query(`select m.data, p.data from part p join message m on m.id = p.message_id where p.session_id = ? order by m.time_created, p.time_created`, sessionID)
	})
	if err != nil {
		c.log.debugf("opencode events query session=%q err=%q", sessionID, err)
		return nil
	}
	defer rows.Close()
	var events []event
	for rows.Next() {
		var messageData, partData string
		if err := rows.Scan(&messageData, &partData); err != nil {
			continue
		}
		var part map[string]any
		if json.Unmarshal([]byte(partData), &part) != nil {
			continue
		}
		role := "assistant"
		var msg map[string]any
		if json.Unmarshal([]byte(messageData), &msg) == nil {
			if r := stringValue(msg["role"]); r != "" {
				role = r
			}
		}
		switch stringValue(part["type"]) {
		case "text":
			text := strings.TrimSpace(stringValue(part["text"]))
			if text == "" || (role == "user" && isSyntheticText(text)) {
				continue
			}
			if role != "user" && role != "assistant" {
				role = "assistant"
			}
			events = append(events, event{kind: role, text: text})
		case "tool":
			name := stringValue(part["tool"])
			if name == "" {
				name = stringValue(part["name"])
			}
			state := asMap(part["state"])
			input := part["input"]
			if input == nil && state != nil {
				input = state["input"]
			}
			output := part["output"]
			if output == nil && state != nil {
				output = state["output"]
			}
			if text := rawString(jsonRaw(input)); text != "" || name != "" {
				events = append(events, event{kind: "tool", name: name, text: text})
			}
			if text := rawString(jsonRaw(output)); text != "" {
				events = append(events, event{kind: "tool", name: "result", text: text})
			}
		case "reasoning", "thinking":
			if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
				events = append(events, event{kind: "thinking", text: text})
			}
		}
	}
	return events
}

func jsonRaw(value any) json.RawMessage {
	switch v := value.(type) {
	case json.RawMessage:
		return v
	case string:
		b, _ := json.Marshal(v)
		return b
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return b
	}
}
