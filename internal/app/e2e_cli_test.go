package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/crossmemcli/pkg/crossmem"
)

// TestE2ECLIListLoadExportClaudeCodexDevin drives the public CLI against
// isolated Claude, Codex, and Devin fixtures. Grok is asserted absent.
func TestE2ECLIListLoadExportClaudeCodexDevin(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work", "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeDir := filepath.Join(t.TempDir(), "claude-projects")
	codexDir := filepath.Join(t.TempDir(), "codex-sessions")
	devinDBPath := filepath.Join(t.TempDir(), "devin", "sessions.db")

	mustWriteCLI(t, filepath.Join(claudeDir, "-work-repo", "sess-claude.jsonl"),
		`{"type":"user","cwd":"`+cliJSONEscape(workspace)+`","message":{"content":[{"type":"text","text":"claude e2e question"}]}}`+"\n"+
			`{"type":"assistant","message":{"content":[{"type":"text","text":"claude e2e answer"}]}}`+"\n")
	mustWriteCLI(t, filepath.Join(codexDir, "2026", "09", "13", "rollout-codex.jsonl"),
		`{"type":"event_msg","payload":{"type":"user_message","message":"codex e2e question","cwd":"`+cliJSONEscape(workspace)+`"}}`+"\n"+
			`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"codex e2e answer"}]}}`+"\n")
	mustWriteDevinDBCLI(t, devinDBPath, workspace)

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeCLIStoreConfig(t, configPath, map[string]string{
		"claude:jsonl-projects": claudeDir,
		"codex:jsonl-sessions":  codexDir,
		"devin:sqlite-sessions": devinDBPath,
	})
	t.Setenv("CROSSMEM_CONFIG", configPath)
	crossmem.ResetConfig()
	t.Cleanup(crossmem.ResetConfig)

	var listOut, listErr bytes.Buffer
	if err := Run([]string{"list", workspace, "--json", "--limit", "20", "--include-current"}, &listOut, &listErr); err != nil {
		t.Fatalf("list: %v\nstderr=%s", err, listErr.String())
	}
	var listed []crossmem.Session
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("list json: %v\n%s", err, listOut.String())
	}
	got := map[string]crossmem.Session{}
	for _, s := range listed {
		got[s.Provider] = s
	}
	for _, provider := range []string{"claude", "codex", "devin"} {
		if _, ok := got[provider]; !ok {
			t.Fatalf("cli list missing %q: %s", provider, listOut.String())
		}
	}
	if _, ok := got["grok"]; ok {
		t.Fatal("cli list returned grok; grok is not a supported provider")
	}

	var loadOut, loadErr bytes.Buffer
	if err := Run([]string{"load", workspace, "--limit", "20", "--full", "--include-current"}, &loadOut, &loadErr); err != nil {
		t.Fatalf("load: %v\nstderr=%s", err, loadErr.String())
	}
	bundle := loadOut.String()
	for _, want := range []string{"claude e2e question", "claude e2e answer", "codex e2e question", "codex e2e answer", "devin e2e question", "devin e2e answer"} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("load missing %q:\n%s", want, bundle)
		}
	}

	outDir := filepath.Join(t.TempDir(), "qa")
	var expOut, expErr bytes.Buffer
	if err := Run([]string{"export", workspace, "--out", outDir}, &expOut, &expErr); err != nil {
		t.Fatalf("export: %v\nstderr=%s", err, expErr.String())
	}
	qa, err := os.ReadFile(filepath.Join(outDir, "qa.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(qa)
	for _, want := range []string{"claude e2e question", "codex e2e question", "devin e2e question"} {
		if !strings.Contains(body, want) {
			t.Fatalf("export missing %q:\n%s", want, body)
		}
	}

	var grokOut, grokErr bytes.Buffer
	if err := Run([]string{"list", workspace, "--provider", "grok", "--json"}, &grokOut, &grokErr); err != nil {
		t.Fatalf("list grok: %v", err)
	}
	var grok []crossmem.Session
	if err := json.Unmarshal(grokOut.Bytes(), &grok); err != nil {
		t.Fatalf("grok json: %v\n%s", err, grokOut.String())
	}
	if len(grok) != 0 {
		t.Fatalf("grok is unsupported; list returned %d", len(grok))
	}
}

func writeCLIStoreConfig(t *testing.T, configPath string, fixtures map[string]string) {
	t.Helper()
	nowhere := filepath.Join(t.TempDir(), "nowhere")
	stores := map[string]string{}
	for _, key := range crossmem.StoreKeys() {
		stores[key] = nowhere
	}
	for key, path := range fixtures {
		stores[key] = path
	}
	body, err := json.Marshal(map[string]any{"stores": stores})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteCLI(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cliJSONEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func mustWriteDevinDBCLI(t *testing.T, path, workspace string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table sessions (id text, title text, working_directory text, backend_type text, model text, agent_mode text, last_activity_at integer, hidden integer)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table message_nodes (session_id text, node_id integer, chat_message text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into sessions values(?,?,?,?,?,?,?,0)`, "sess-devin", "devin", workspace, "cli", "x", "default", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	user, _ := json.Marshal(map[string]any{"role": "user", "content": "devin e2e question"})
	asst, _ := json.Marshal(map[string]any{"role": "assistant", "content": "devin e2e answer"})
	if _, err := db.Exec(`insert into message_nodes values(?,?,?)`, "sess-devin", 1, string(user)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into message_nodes values(?,?,?)`, "sess-devin", 2, string(asst)); err != nil {
		t.Fatal(err)
	}
}
