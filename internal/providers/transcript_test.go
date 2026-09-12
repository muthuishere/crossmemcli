package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeLineEventsKeepToolsAndResults(t *testing.T) {
	events := claudeLineEvents([]byte(`{
		"type":"assistant",
		"timestamp":"2026-09-12T10:00:00Z",
		"message":{
			"role":"assistant",
			"model":"claude-opus-4-6",
			"content":[{"type":"text","text":"here is the fix"},{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/hello.go","content":"package main"}}],
			"usage":{"input_tokens":12,"output_tokens":8}
		}
	}`))
	if len(events) != 2 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].kind != "assistant" || events[0].text != "here is the fix" {
		t.Fatalf("text = %#v", events[0])
	}
	if events[1].kind != "tool" || events[1].name != "Write" || !strings.Contains(events[1].text, "package main") {
		t.Fatalf("tool = %#v", events[1])
	}

	got := claudeLineEvents([]byte(`{
		"type":"user",
		"message":{"content":[{"type":"tool_result","content":"wrote /tmp/hello.go"}]}
	}`))
	if len(got) != 1 || got[0].kind != "tool" || got[0].text != "wrote /tmp/hello.go" {
		t.Fatalf("tool result = %#v", got)
	}
}

func TestCodexLineEventsKeepToolsSkipTokens(t *testing.T) {
	user := codexLineEvents([]byte(`{"type":"event_msg","timestamp":"2026-09-12T11:00:00Z","payload":{"type":"user_message","message":"open the file"}}`))
	tool := codexLineEvents([]byte(`{"type":"response_item","payload":{"type":"function_call","name":"apply_patch","arguments":"{\"patch\":\"*** Add File: src/main.go\"}"}}`))
	usage := codexLineEvents([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":40}}}}`))
	reply := codexLineEvents([]byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`))
	if len(user) != 1 || user[0].text != "open the file" {
		t.Fatalf("user = %#v", user)
	}
	if len(tool) != 1 || tool[0].name != "apply_patch" {
		t.Fatalf("tool=%#v", tool)
	}
	if len(usage) != 0 {
		t.Fatalf("usage=%#v", usage)
	}
	if len(reply) != 1 || reply[0].text != "done" {
		t.Fatalf("reply = %#v", reply)
	}
}

func TestQAPairsCarrySessionFolderAndTime(t *testing.T) {
	session := Session{
		ID:        "abc-123",
		Workspace: "/tmp/repo",
		Modified:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	pairs := qaPairs([]event{
		{kind: "user", text: "write hello.go", at: time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)},
		{kind: "assistant", text: "writing it"},
		{kind: "assistant", text: "done"},
		{kind: "user", text: "never answered"},
	}, session)
	if len(pairs) != 2 {
		t.Fatalf("pairs = %#v", pairs)
	}
	got := pairs[0]
	if got.SessionID != "abc-123" || got.Folder != "/tmp/repo" {
		t.Fatalf("ids = %#v", got)
	}
	if got.Q != "write hello.go" || got.A != "writing it\n\ndone" {
		t.Fatalf("qa = %#v", got)
	}
	if got.Time != "2026-09-12T08:00:00Z" {
		t.Fatalf("time = %q", got.Time)
	}
	if pairs[1].Q != "never answered" || pairs[1].A != "" {
		t.Fatalf("unanswered = %#v", pairs[1])
	}
}

func TestExportConversationsWritesFullQA(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), "claude-projects")
	workspace := filepath.Join(claudeDir, "-Users-me-repo")
	transcript := strings.Join([]string{
		`{"type":"user","timestamp":"2026-09-12T09:00:00Z","message":{"content":[{"type":"text","text":"add a flag"}]}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"adding --verbose"},{"type":"tool_use","name":"Edit","input":{"file_path":"cmd/app.go"}}],"usage":{"input_tokens":21}}}`,
		`{"type":"user","toolUseResult":{"ok":true},"message":{"content":[{"type":"tool_result","content":"patched"}]}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"the flag is in"}]}}`,
	}, "\n") + "\n"
	mustWrite(t, filepath.Join(workspace, "session-abc.jsonl"), transcript)

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": claudeDir,
	})
	t.Setenv("CROSSMEM_CONFIG", configPath)
	resetConfigForTest(t)

	outDir := filepath.Join(t.TempDir(), "convos")
	result, err := ExportConversations(ConvExportOptions{Out: outDir})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.QAPairs != 1 {
		t.Fatalf("qa pairs = %d, want 1 (result=%#v)", result.QAPairs, result)
	}
	qa := readFile(t, result.QAFile)
	if _, err := os.Stat(filepath.Join(outDir, "raw.jsonl")); err == nil {
		t.Fatal("raw.jsonl should not be written")
	}
	var rec QARecord
	if err := json.Unmarshal([]byte(qa), &rec); err != nil {
		t.Fatalf("qa.jsonl not json: %v\n%s", err, qa)
	}
	if rec.SessionID != "session-abc" {
		t.Fatalf("sessionId = %q", rec.SessionID)
	}
	if rec.Q != "add a flag" || !strings.Contains(rec.A, "adding --verbose") || !strings.Contains(rec.A, "the flag is in") {
		t.Fatalf("qa = %#v", rec)
	}
	if rec.Time != "2026-09-12T09:00:00Z" {
		t.Fatalf("time = %q", rec.Time)
	}
	if len(rec.Messages) < 3 {
		t.Fatalf("messages = %#v", rec.Messages)
	}
	foundTool := false
	for _, msg := range rec.Messages {
		if msg.Role == "tool" && msg.Name == "Edit" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("missing tool message: %#v", rec.Messages)
	}
	for _, leaked := range []string{"claude-opus", `"model"`, "input_tokens"} {
		if strings.Contains(qa, leaked) {
			t.Fatalf("qa.jsonl leaked %q:\n%s", leaked, qa)
		}
	}

	dest := filepath.Join(t.TempDir(), "imported")
	imp, err := ImportConversations(ConvImportOptions{In: outDir, Out: dest})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Added != 1 {
		t.Fatalf("imported %d, want 1", imp.Added)
	}
	got := readFile(t, imp.QAFile)
	if got != qa {
		t.Fatalf("imported file differs from export")
	}
	again, err := ImportConversations(ConvImportOptions{In: outDir, Out: dest, Merge: true})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if again.Added != 0 {
		t.Fatalf("merge added %d, want 0", again.Added)
	}
}
