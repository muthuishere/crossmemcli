package crossmem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigPointingEveryStoreAt redirects every known store to a
// nonexistent path so DiscoverStores sees a clean machine, then overrides the
// named fixtures. This keeps export tests from copying the real ~/.claude and
// ~/.codex stores. The config is written to configPath.
func writeConfigPointingEveryStoreAt(t *testing.T, configPath string, fixtures map[string]string) {
	t.Helper()
	nowhere := filepath.Join(t.TempDir(), "nowhere")
	stores := map[string]string{}
	for _, key := range StoreKeys() {
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

func TestExportImportRoundTrip(t *testing.T) {
	// A fake machine: a Claude project tree (transcripts + project memory +
	// a subagents dir) and a Codex session tree, plus fixture instruction and
	// memory files that stand in for the real home-dir extras.
	claudeDir := filepath.Join(t.TempDir(), "claude-projects")
	codexDir := filepath.Join(t.TempDir(), "codex-sessions")
	workspace := filepath.Join(claudeDir, "-Users-me-repo")
	mustWrite(t, filepath.Join(workspace, "session-abc.jsonl"), `{"type":"user","message":{"content":"hi"}}`+"\n")
	mustWrite(t, filepath.Join(workspace, "subagents", "sub.jsonl"), `{"type":"user","message":{"content":"sub"}}`+"\n")
	mustWrite(t, filepath.Join(workspace, "memory", "project-memory.md"), "# memory: the important stuff\n")
	mustWrite(t, filepath.Join(claudeDir, "auth.json"), `{"token":"should-not-leak"}`)
	mustWrite(t, filepath.Join(claudeDir, ".env"), "SECRET=x\n")
	mustWrite(t, filepath.Join(claudeDir, "vault", "creds.json"), `{"nope":true}`)
	mustWrite(t, filepath.Join(codexDir, "2026", "09", "12", "rollout-1.jsonl"), `{"type":"response_item"}`+"\n")

	// Swap the well-known instruction/memory files for fixtures so nothing in
	// the test touches the real home directory.
	mdFixture := filepath.Join(t.TempDir(), "CLAUDE.md")
	goalsFixture := filepath.Join(t.TempDir(), "goals_1.sqlite")
	mustWrite(t, mdFixture, "# global instructions\n")
	mustWrite(t, goalsFixture, "sqlite-bytes")
	oldExtras := extraFiles
	extraFiles = []extraFile{
		{"claude/CLAUDE.md", "instructions", []string{mdFixture}},
		{"codex/goals.sqlite", "memory", []string{goalsFixture}},
	}
	t.Cleanup(func() { extraFiles = oldExtras })

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": claudeDir,
		"codex:jsonl-sessions":  codexDir,
	})
	t.Setenv("CROSSMEM_CONFIG", configPath)
	resetConfigForTest(t)

	outDir := filepath.Join(t.TempDir(), "dump")
	manifest, err := ExportDump(ExportOptions{Out: outDir})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if manifest.Schema != DumpSchema {
		t.Fatalf("schema = %q, want %q", manifest.Schema, DumpSchema)
	}

	// Everything portable is copied...
	for _, want := range []string{
		"stores/claude/jsonl-projects/-Users-me-repo/session-abc.jsonl",
		"stores/claude/jsonl-projects/-Users-me-repo/subagents/sub.jsonl",
		"stores/claude/jsonl-projects/-Users-me-repo/memory/project-memory.md",
		"stores/codex/jsonl-sessions/2026/09/12/rollout-1.jsonl",
		"instructions/claude/CLAUDE.md",
		"memory/codex/goals.sqlite",
		"manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(want))); err != nil {
			t.Errorf("dump missing %s: %v", want, err)
		}
	}
	// ...and nothing secret is.
	for _, unwanted := range []string{"auth.json", ".env"} {
		if matches := walkNames(outDir, unwanted); len(matches) > 0 {
			t.Errorf("dump contains forbidden %s: %v", unwanted, matches)
		}
	}
	if matches := walkNames(outDir, "vault"); len(matches) > 0 {
		t.Errorf("dump contains forbidden vault dir: %v", matches)
	}
	if found := strings.Contains(readFile(t, filepath.Join(outDir, "manifest.json")), "should-not-leak"); found {
		t.Error("dump manifest mentions secret content")
	}

	// Re-point the machine at fresh restore targets and import.
	restoreClaude := filepath.Join(t.TempDir(), "restore-claude")
	restoreCodex := filepath.Join(t.TempDir(), "restore-codex")
	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": restoreClaude,
		"codex:jsonl-sessions":  restoreCodex,
	})
	resetConfig()

	res, err := ImportDump(ImportOptions{In: outDir})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("import reported missing targets: %v", res.Missing)
	}
	if got := readFile(t, filepath.Join(restoreClaude, "-Users-me-repo", "session-abc.jsonl")); !strings.Contains(got, "hi") {
		t.Errorf("claude session not restored: %q", got)
	}
	if got := readFile(t, filepath.Join(restoreClaude, "-Users-me-repo", "memory", "project-memory.md")); !strings.Contains(got, "important stuff") {
		t.Errorf("project memory not restored: %q", got)
	}
	if got := readFile(t, filepath.Join(restoreCodex, "2026", "09", "12", "rollout-1.jsonl")); got == "" {
		t.Error("codex session not restored")
	}
	if _, err := os.Stat(filepath.Join(restoreClaude, "auth.json")); err == nil {
		t.Error("auth.json was restored to the target machine")
	}
	// Instructions restore to the fixture location via its stable name.
	if got := readFile(t, mdFixture); !strings.Contains(got, "global instructions") {
		t.Errorf("instruction file not restored: %q", got)
	}

	// A second import is idempotent: everything is skipped as identical.
	res2, err := ImportDump(ImportOptions{In: outDir})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(res2.Restored) != 0 || len(res2.Skipped) == 0 {
		t.Errorf("second import should skip everything, got restored=%d skipped=%d", len(res2.Restored), len(res2.Skipped))
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	claudeDir := filepath.Join(t.TempDir(), "claude-projects")
	restoreClaude := filepath.Join(t.TempDir(), "restore-claude")
	mustWrite(t, filepath.Join(claudeDir, "s.jsonl"), "abc\n")

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": claudeDir,
	})
	t.Setenv("CROSSMEM_CONFIG", configPath)
	resetConfigForTest(t)

	outDir := filepath.Join(t.TempDir(), "dump")
	if _, err := ExportDump(ExportOptions{Out: outDir}); err != nil {
		t.Fatalf("export: %v", err)
	}

	writeConfigPointingEveryStoreAt(t, configPath, map[string]string{
		"claude:jsonl-projects": restoreClaude,
	})
	resetConfig()

	res, err := ImportDump(ImportOptions{In: outDir, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run import: %v", err)
	}
	if len(res.Restored) == 0 {
		t.Fatal("dry-run should report files it would restore")
	}
	if _, err := os.Stat(filepath.Join(restoreClaude, "s.jsonl")); err == nil {
		t.Error("dry-run wrote a file")
	}
}

func TestSyncRequiresRemote(t *testing.T) {
	resetConfigForTest(t)
	if _, err := SyncDump(SyncOptions{}); err == nil || !strings.Contains(err.Error(), "no sync remote") {
		t.Fatalf("expected a missing-remote error, got %v", err)
	}
}

func TestSyncRejectsMissingDump(t *testing.T) {
	resetConfigForTest(t)
	_, err := SyncDump(SyncOptions{Remote: "fake:remote", Out: filepath.Join(t.TempDir(), "absent")})
	if err == nil || !strings.Contains(err.Error(), "no dump found") {
		t.Fatalf("expected a missing-dump error, got %v", err)
	}
}

func TestDefaultDumpDirFromConfig(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "mydump")
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"dumpDir":"`+custom+`"}`))
	resetConfigForTest(t)
	if got := DefaultDumpDir(); got != custom {
		t.Fatalf("DefaultDumpDir = %q, want %q", got, custom)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// walkNames returns every path under root whose base name equals name.
func walkNames(root string, name string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == name {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// A symlinked global instruction file (~/.claude-cys/CLAUDE.md -> a shared copy)
// is exported with its target's bytes. A link that resolves into a vault is
// not followed and leaves no manifest entry. A manifest entry whose file is
// missing — what 0.1.9/0.1.10 wrote for symlinks — is reported, not fatal.
func TestExportFollowsInstructionSymlinksSafely(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	claudeConfig := filepath.Join(home, "claude-cys")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfigPointingEveryStoreAt(t, configPath, nil)
	t.Setenv("CROSSMEM_CONFIG", configPath)
	resetConfigForTest(t)

	shared := filepath.Join(home, "claudedefault", "CLAUDE.md")
	mustWrite(t, shared, "shared global instructions\n")
	if err := os.MkdirAll(claudeConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claudeConfig, "CLAUDE.md")
	if err := os.Symlink(shared, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}

	out := filepath.Join(t.TempDir(), "dump")
	manifest, err := ExportDump(ExportOptions{Out: out})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "instructions", "claude", "CLAUDE.md"))
	if err != nil || string(got) != "shared global instructions\n" {
		t.Fatalf("symlinked CLAUDE.md not exported with its target's bytes: %v %q", err, got)
	}
	if len(manifest.Instructions) != 1 {
		t.Fatalf("manifest instructions = %#v", manifest.Instructions)
	}

	// Repoint the link into a vault: it must not be followed.
	secret := filepath.Join(home, "vault", "CLAUDE.md")
	mustWrite(t, secret, "do not export\n")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(t.TempDir(), "dump2")
	manifest2, err := ExportDump(ExportOptions{Out: out2})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(manifest2.Instructions) != 0 {
		t.Fatalf("a link into a vault was exported: %#v", manifest2.Instructions)
	}
	if _, err := os.Stat(filepath.Join(out2, "instructions", "claude", "CLAUDE.md")); err == nil {
		t.Fatal("vault content landed in the dump")
	}

	// A manifest that lists a file the dump lacks imports with a Missing note.
	manifest2.Instructions = []DumpFile{{Name: "claude/CLAUDE.md", Destination: "instructions/claude/CLAUDE.md"}}
	body, _ := json.Marshal(manifest2)
	if err := os.WriteFile(filepath.Join(out2, "manifest.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ImportDump(ImportOptions{In: out2, DryRun: true})
	if err != nil {
		t.Fatalf("import of a dump with a phantom entry failed: %v", err)
	}
	if len(res.Missing) != 1 || !strings.Contains(res.Missing[0], "not in the dump") {
		t.Fatalf("missing = %#v", res.Missing)
	}
}
