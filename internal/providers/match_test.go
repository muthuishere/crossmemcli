package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCopilotFolder(t *testing.T) {
	dir := t.TempDir()
	wsFile := filepath.Join(dir, "workspace.json")
	// VS Code stores the real project folder as a file:// URI, with spaces
	// percent-encoded. crossmem must decode it back to a real path so the
	// session matches the folder like the other providers.
	if err := os.WriteFile(wsFile, []byte(`{"folder":"file:///Users/m/My%20Repo/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readCopilotFolder(wsFile); got != "/Users/m/My Repo/app" {
		t.Fatalf("readCopilotFolder = %q, want %q", got, "/Users/m/My Repo/app")
	}
	if got := readCopilotFolder(filepath.Join(dir, "missing.json")); got != "" {
		t.Fatalf("missing file should yield empty string, got %q", got)
	}
}

func TestSameOrChild(t *testing.T) {
	cases := []struct {
		name  string
		value string
		root  string
		want  bool
	}{
		{"identical", "/a/b/c", "/a/b/c", true},
		{"child", "/a/b/c/d", "/a/b/c", true},
		{"parent is not child", "/a/b", "/a/b/c", false},
		{"sibling", "/a/b/x", "/a/b/c", false},
		// A real folder name containing a dash must match itself. This is the
		// regression: the encoded Claude store dir decodes "-" back to "/",
		// so matching has to use the real cwd, not the decoded path.
		{"dash in folder name", "/u/m/crossmem-workspace/cli", "/u/m/crossmem-workspace/cli", true},
		// A title sentence is not a path and must never match a folder. Before
		// the fix, a relative string was resolved against the process cwd and
		// matched almost anything.
		{"sentence never matches", "Load Claude sessions that modified this repo", "/a/b/c", false},
		{"empty value", "", "/a/b/c", false},
		{"empty root", "/a/b/c", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameOrChild(tc.value, tc.root); got != tc.want {
				t.Fatalf("sameOrChild(%q, %q) = %v, want %v", tc.value, tc.root, got, tc.want)
			}
		})
	}
}

func TestFilterByCWDMatchesOnRealWorkspaceOnly(t *testing.T) {
	cwd := "/u/m/crossmem-workspace/cli"
	sessions := []Session{
		{Provider: "claude", Workspace: cwd, Title: "this folder's session"},
		{Provider: "codex", Workspace: "/u/m/other-repo", Title: cwd}, // title looks like the cwd, but workspace is elsewhere
		{Provider: "claude", Workspace: "/u/m/unrelated", Title: "some sentence"},
	}
	got := filterByCWD(sessions, cwd)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 match, got %d: %+v", len(got), got)
	}
	if got[0].Workspace != cwd {
		t.Fatalf("matched the wrong session: %q", got[0].Workspace)
	}
}
