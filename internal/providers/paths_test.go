package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store candidate that names an environment variable which is not set on this
// machine must drop out entirely, not collapse into a path rooted at "/". This
// is how the Windows-only Devin location stays inert on macOS and Linux.
func TestExpandPathDropsUnsetEnvCandidates(t *testing.T) {
	t.Setenv("CROSSMEM_TEST_ROOT", "/data/roaming")
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"windows form", "%CROSSMEM_TEST_ROOT%/Cognition/cli/sessions.db", "/data/roaming/Cognition/cli/sessions.db"},
		{"shell form", "$CROSSMEM_TEST_ROOT/Cognition", "/data/roaming/Cognition"},
		{"braced form", "${CROSSMEM_TEST_ROOT}/Cognition", "/data/roaming/Cognition"},
		{"unset windows var", "%CROSSMEM_TEST_MISSING%/Cognition/cli/sessions.db", ""},
		{"unset shell var", "$CROSSMEM_TEST_MISSING/opencode", ""},
		{"plain path", "/tmp/x", "/tmp/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandPath(tc.in); got != tc.want {
				t.Fatalf("expandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExpandPathResolvesHome(t *testing.T) {
	want := filepath.Join(homeDir(), ".claude", "projects")
	if got := expandPath("~/.claude/projects"); got != want {
		t.Fatalf("expandPath = %q, want %q", got, want)
	}
}

// Every Devin candidate must be a real per-platform location, and the Windows
// one the CLI actually ships to must be present.
func TestDevinCandidatesCoverWindows(t *testing.T) {
	candidates := storeCandidates("devin", "sqlite-sessions")
	wantAny := []string{
		"~/.local/share/devin/cli/sessions.db",
		"%APPDATA%/Cognition/cli/sessions.db",
	}
	for _, want := range wantAny {
		found := false
		for _, candidate := range candidates {
			if candidate == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("devin candidates %v missing %q", candidates, want)
		}
	}
}

func TestStorePathsFindsExistingAndGlobs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opencode.db", "opencode-dev.db", "auth.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"stores":{"opencode":"`+filepath.ToSlash(dir)+`/opencode*.db"}}`))
	resetConfigForTest(t)

	got := storePaths("opencode", "sqlite-sessions")
	if len(got) != 2 {
		t.Fatalf("expected the two opencode*.db files, got %v", got)
	}
	for _, path := range got {
		if filepath.Base(path) == "auth.json" {
			t.Fatal("glob must not pick up the credential file")
		}
	}
}

func TestDecodeClaudeDir(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"posix", "-Users-m-repo", "/Users/m/repo"},
		{"windows drive", "C--Users-m-repo", `C:\Users\m\repo`},
		{"lowercase drive", "d--work-repo", `d:\work\repo`},
		{"unencoded", "repo", "repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeClaudeDir(tc.in); got != tc.want {
				t.Fatalf("decodeClaudeDir(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// VS Code writes a Windows folder as file:///c%3A/… which unescapes to a path
// with a spurious leading slash; that has to come off or nothing matches.
func TestReadCopilotFolderWindowsURI(t *testing.T) {
	dir := t.TempDir()
	wsFile := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(wsFile, []byte(`{"folder":"file:///c%3A/Users/m/My%20Repo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readCopilotFolder(wsFile); got != "c:/Users/m/My Repo" {
		t.Fatalf("readCopilotFolder = %q, want %q", got, "c:/Users/m/My Repo")
	}
}

// The Devin desktop app is a VS Code fork (product.json: nameLong "Devin",
// formerly Windsurf), so a transcript under its data folder must be labelled
// devin-gui, parsed with the VS Code chat reader, and resolve its folder from
// workspaceStorage/<id>/workspace.json like any other fork.
func TestDevinDesktopIsReadAsAVSCodeFork(t *testing.T) {
	transcript := "/Users/m/Library/Application Support/Devin/User/workspaceStorage/abc123/chatSessions/s.jsonl"

	if got := inferProvider(transcript, ""); got != "devin-gui" {
		t.Fatalf("inferProvider = %q, want devin-gui", got)
	}
	// Copilot in VS Code must still win for the VS Code data folder.
	if got := inferProvider("/Users/m/Library/Application Support/Code/User/workspaceStorage/x/chatSessions/s.jsonl", ""); got != "copilot" {
		t.Fatalf("inferProvider for VS Code = %q, want copilot", got)
	}

	line := `{"kind":0,"v":{"requests":[{"message":{"text":"hello"},"responseMarkdownInfo":{"markdown":"hi there"}}]}}`
	if got := extractJSONLText([]byte(line), "devin-gui"); !strings.Contains(got, "user: hello") {
		t.Fatalf("devin-gui transcript should parse as VS Code chat, got %q", got)
	}

	// Only chat transcripts, not the rest of the workspaceStorage tree.
	if isCopilotSessionPath("/x/workspaceStorage/abc/state/other.jsonl") {
		t.Fatal("non-chat workspaceStorage files must be skipped")
	}

	dir := t.TempDir()
	wsDir := filepath.Join(dir, "workspaceStorage", "abc123")
	if err := os.MkdirAll(filepath.Join(wsDir, "chatSessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(`{"folder":"file:///Users/m/repo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := inferWorkspace(filepath.Join(wsDir, "chatSessions", "s.jsonl"), "devin-gui"); got != "/Users/m/repo" {
		t.Fatalf("inferWorkspace = %q, want /Users/m/repo", got)
	}
}

// Each walkable root carries the provider that owns it, so two forks sharing
// the workspaceStorage shape stay distinguishable.
func TestProviderRootsAreLabelled(t *testing.T) {
	for _, root := range providerRoots("all") {
		if root.Provider == "" || root.Path == "" {
			t.Fatalf("unlabelled root: %+v", root)
		}
	}
	for _, root := range providerRoots("claude") {
		if root.Provider != "claude" {
			t.Fatalf("--provider claude returned a %s root", root.Provider)
		}
	}
}

// A relocated config dir is where the sessions actually are, so it must be
// preferred over the default location.
func TestConfigDirEnvVarsWin(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)
	if got := storePath("codex", "jsonl-sessions"); got != filepath.Join(dir, "sessions") {
		t.Fatalf("storePath = %q, want the $CODEX_HOME sessions dir", got)
	}
}
