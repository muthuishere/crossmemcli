package crossmem

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2EClaudeCodexDevinAndGrokGap is the provider contract: Claude, Codex,
// and Devin can be listed, loaded, and exported as nameless Q&A from isolated
// fixtures. Grok is not a provider — ~/.grok/sessions exists on this machine
// but crossmem does not read it yet.
func TestE2EClaudeCodexDevinAndGrokGap(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work", "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(t.TempDir(), "claude-projects")
	codexDir := filepath.Join(t.TempDir(), "codex-sessions")
	devinDBPath := filepath.Join(t.TempDir(), "devin", "sessions.db")

	mustWrite(t, filepath.Join(claudeDir, "-work-repo", "sess-claude.jsonl"), strings.Join([]string{
		`{"type":"user","cwd":"` + jsonEscape(workspace) + `","message":{"content":[{"type":"text","text":"claude question about export"}]}}`,
		`{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"claude answer about export"}]}}`,
	}, "\n")+"\n")

	mustWrite(t, filepath.Join(codexDir, "2026", "09", "13", "rollout-codex.jsonl"), strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"user_message","message":"codex question about list","cwd":"` + jsonEscape(workspace) + `"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"codex answer about list"}]}}`,
	}, "\n")+"\n")

	mustWriteDevinDB(t, devinDBPath, workspace, "sess-devin", "devin question about load", "devin answer about load")

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": claudeDir,
		"codex:jsonl-sessions":  codexDir,
		"devin:sqlite-sessions": devinDBPath,
	})
	t.Setenv("CROSSMEM_CONFIG", configPath)
	resetConfigForTest(t)

	names := Providers()
	for _, want := range []string{"claude", "codex", "devin"} {
		if !containsString(names, want) {
			t.Fatalf("Providers() missing %q: %v", want, names)
		}
	}
	if containsString(names, "grok") {
		t.Fatal("Providers() unexpectedly includes grok; update this test if support shipped")
	}

	sessions, err := ListSessions(ListOptions{Provider: "all", CWD: workspace, Limit: 20, Questions: true, IncludeCurrent: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]Session{}
	for _, s := range sessions {
		got[s.Provider] = s
	}
	for _, provider := range []string{"claude", "codex", "devin"} {
		s, ok := got[provider]
		if !ok {
			t.Fatalf("list missing provider %q; got %#v", provider, sessions)
		}
		if !sameOrChild(s.Workspace, workspace) {
			t.Fatalf("%s workspace = %q, want under %q", provider, s.Workspace, workspace)
		}
		if s.Ago == "" {
			t.Fatalf("%s missing relative ago", provider)
		}
	}
	if got["claude"].FirstQuestion == "" || !strings.Contains(got["claude"].FirstQuestion, "claude question") {
		t.Fatalf("claude first question = %q", got["claude"].FirstQuestion)
	}
	if got["codex"].FirstQuestion == "" || !strings.Contains(got["codex"].FirstQuestion, "codex question") {
		t.Fatalf("codex first question = %q", got["codex"].FirstQuestion)
	}
	if got["devin"].FirstQuestion == "" || !strings.Contains(got["devin"].FirstQuestion, "devin question") {
		t.Fatalf("devin first question = %q", got["devin"].FirstQuestion)
	}

	bundle, err := BuildContext(ListOptions{Provider: "all", CWD: workspace, Limit: 20, Full: true, IncludeCurrent: true})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, want := range []string{
		"claude question about export", "claude answer about export",
		"codex question about list", "codex answer about list",
		"devin question about load", "devin answer about load",
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("bundle missing %q:\n%s", want, bundle)
		}
	}
	if strings.Contains(bundle, "claude-opus") {
		t.Fatalf("bundle leaked a model name")
	}

	outDir := filepath.Join(t.TempDir(), "qa")
	result, err := ExportConversations(ConvExportOptions{Out: outDir, CWD: workspace})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.QAPairs < 3 {
		t.Fatalf("qa pairs = %d, want at least 3", result.QAPairs)
	}
	qa := readFile(t, result.QAFile)
	for _, want := range []string{
		`"q":"claude question about export"`,
		`"a":"claude answer about export"`,
		`"q":"codex question about list"`,
		`"a":"codex answer about list"`,
		`"q":"devin question about load"`,
		`"a":"devin answer about load"`,
	} {
		if !strings.Contains(qa, want) {
			t.Fatalf("qa.jsonl missing %s:\n%s", want, qa)
		}
	}
	if strings.Contains(qa, "claude-opus") || strings.Contains(qa, `"model"`) {
		t.Fatalf("qa.jsonl leaked model metadata:\n%s", qa)
	}

	grok, err := ListSessions(ListOptions{Provider: "grok", CWD: workspace, Limit: 20, IncludeCurrent: true})
	if err != nil {
		t.Fatalf("list grok: %v", err)
	}
	if len(grok) != 0 {
		t.Fatalf("grok is not a provider; list returned %d sessions", len(grok))
	}
}

func mustWriteDevinDB(t *testing.T, path, workspace, id, question, answer string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`create table sessions (
			id text primary key,
			title text,
			working_directory text,
			backend_type text,
			model text,
			agent_mode text,
			last_activity_at integer,
			hidden integer
		)`,
		`create table message_nodes (
			session_id text,
			node_id integer,
			chat_message text
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	userJSON, _ := json.Marshal(map[string]any{"role": "user", "content": question})
	asstJSON, _ := json.Marshal(map[string]any{"role": "assistant", "content": answer})
	if _, err := db.Exec(
		`insert into sessions(id,title,working_directory,backend_type,model,agent_mode,last_activity_at,hidden)
		 values(?,?,?,?,?,?,?,0)`,
		id, "devin session", workspace, "cli", "some-model", "default", time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into message_nodes(session_id,node_id,chat_message) values(?,?,?)`, id, 1, string(userJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into message_nodes(session_id,node_id,chat_message) values(?,?,?)`, id, 2, string(asstJSON)); err != nil {
		t.Fatal(err)
	}
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
