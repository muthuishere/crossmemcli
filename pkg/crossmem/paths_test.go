package crossmem

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
		{"windows form", "%CROSSMEM_TEST_ROOT%/devin/cli/sessions.db", "/data/roaming/devin/cli/sessions.db"},
		{"shell form", "$CROSSMEM_TEST_ROOT/devin", "/data/roaming/devin"},
		{"braced form", "${CROSSMEM_TEST_ROOT}/devin", "/data/roaming/devin"},
		{"unset windows var", "%CROSSMEM_TEST_MISSING%/devin/cli/sessions.db", ""},
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
// one the CLI ships to (the path changed from %APPDATA%\devin\cli) must be
// present.
func TestDevinCandidatesCoverWindows(t *testing.T) {
	candidates := storeCandidates("devin", "sqlite-sessions")
	wantAny := []string{
		"~/.local/share/devin/cli/sessions.db",
		"%APPDATA%/devin/cli/sessions.db",
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

// $DEVIN_HOME relocates the whole Devin data directory and $DEVIN_DB_PATH
// points straight at the sessions.db file; both must win over the defaults.
func TestDevinDBHonorsEnvOverrides(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "custom.db")
	if err := os.MkdirAll(filepath.Join(home, "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(home, "cli", "sessions.db"), dbPath} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resetConfigForTest(t)

	t.Setenv("DEVIN_HOME", home)
	if got := devinDB(); got != filepath.Join(home, "cli", "sessions.db") {
		t.Fatalf("DEVIN_HOME not honored: devinDB() = %q, want %q", got, filepath.Join(home, "cli", "sessions.db"))
	}
	// A direct DB path wins over the home-derived default; DEVIN_DB_PATH is
	// listed before the DEVIN_HOME-derived candidates.
	t.Setenv("DEVIN_DB_PATH", dbPath)
	if got := devinDB(); got != dbPath {
		t.Fatalf("DEVIN_DB_PATH not honored: devinDB() = %q, want %q", got, dbPath)
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

// The Devin desktop app is not a separate chat store: it drives the Devin CLI
// through an ACP connector, so every desktop session lands in the same
// sessions.db and is read by the single `devin` provider. There is no
// `devin-gui` provider, and the retired Windsurf/Cascade stores are gone.
func TestDevinIsTheOnlySessionSource(t *testing.T) {
	for _, name := range Providers() {
		if name == "devin-gui" {
			t.Fatal("devin-gui must no longer be a provider")
		}
	}
	if _, ok := storeDefinitionFor("devin-gui", "vscode-workspace-storage"); ok {
		t.Fatal("the retired devin-gui workspaceStorage store must be gone")
	}
	if _, ok := storeDefinitionFor("devin-gui", "cascade-conversations"); ok {
		t.Fatal("the retired cascade store must be gone")
	}
	db, ok := storeDefinitionFor("devin", "sqlite-sessions")
	if !ok || !db.Primary {
		t.Fatal("devin:sqlite-sessions must be the provider's primary store")
	}
	if !strings.Contains(db.Note, "sessions.db") {
		t.Fatalf("the Devin note must name the single sessions.db, got %q", db.Note)
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

// Verified against a real Devin desktop install in 2026-08-29: the desktop
// keeps no separate chat store — it drives the Devin CLI through an ACP
// connector and every session lands in the one sessions.db (all 68 rows in the
// verified DB carried backend_type Windsurf). That replaced the earlier claim
// of encrypted ~/.codeium/windsurf/cascade conversations, which is retired
// along with the devin-gui provider. See docs/adr/2-devin-single-sessions-db.md.
func TestDevinHasNoDesktopCascadeStore(t *testing.T) {
	for _, key := range StoreKeys() {
		if strings.Contains(key, "devin-gui") {
			t.Fatalf("devin-gui must not appear in config keys, got %q", key)
		}
	}
}
