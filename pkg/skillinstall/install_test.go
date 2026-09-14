package skillinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func skillFS() fstest.MapFS {
	return fstest.MapFS{
		"demo/SKILL.md":           {Data: []byte("---\nname: demo\n---\nv2")},
		"demo/references/deep.md": {Data: []byte("deep")},
		"nodoc/README.md":         {Data: []byte("no skill file")},
	}
}

func TestInstallReplacesThePreviousCopyWhole(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "demo", "references", "removed-in-v2.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(skillFS(), "demo", []Target{{Host: "claude", Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Action != "updated" {
		t.Errorf("action = %q, want updated", res[0].Action)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a file dropped from the skill survived the reinstall")
	}
	got, err := os.ReadFile(filepath.Join(dir, "demo", "references", "deep.md"))
	if err != nil || string(got) != "deep" {
		t.Fatalf("references not installed: %v %q", err, got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "demo.tmp-*"))
	if len(leftovers) != 0 {
		t.Errorf("temp install dirs left behind: %v", leftovers)
	}
}

// Install and Uninstall RemoveAll <Dir>/<name>. A name that is not a single
// path segment must never reach that call.
func TestNamesThatEscapeTheTargetAreRejected(t *testing.T) {
	outer := t.TempDir()
	dir := filepath.Join(outer, "skills")
	victim := filepath.Join(outer, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "../victim", "a/b", `a\b`, "/abs"} {
		if _, err := Uninstall(name, []Target{{Host: "x", Dir: dir}}); err == nil {
			t.Errorf("Uninstall(%q) accepted", name)
		}
		if _, err := Install(skillFS(), name, []Target{{Host: "x", Dir: dir}}); err == nil {
			t.Errorf("Install(%q) accepted", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a directory outside the target was removed: %v", err)
	}
}

func TestInstallRequiresSkillMD(t *testing.T) {
	_, err := Install(skillFS(), "nodoc", []Target{{Host: "x", Dir: t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("Install without SKILL.md = %v", err)
	}
}

func TestUninstallReportsMissing(t *testing.T) {
	res, err := Uninstall("demo", []Target{{Host: "x", Dir: t.TempDir()}})
	if err != nil || res[0].Action != "not-installed" {
		t.Fatalf("Uninstall of a missing skill = %v %#v", err, res)
	}
}
