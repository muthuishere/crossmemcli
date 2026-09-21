package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every other test in this package calls Run, which would otherwise install
// the skill into the developer's real home directory. Off by default here;
// isolateSkillHome turns it back on against temp dirs.
func TestMain(m *testing.M) {
	os.Setenv("CROSSMEM_NO_SKILL_INSTALL", "1")
	os.Exit(m.Run())
}

// isolateSkillHome points the stamp file and both skill targets at temp dirs,
// so the test never writes to the real home directory.
func isolateSkillHome(t *testing.T) (claude string, agents string) {
	t.Helper()
	home := t.TempDir()
	claude = filepath.Join(home, "claude-skills")
	agents = filepath.Join(home, "agents-skills")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_SKILLS_DIR", claude)
	t.Setenv("AGENTS_SKILLS_DIR", agents)
	t.Setenv("CROSSMEM_NO_SKILL_INSTALL", "")
	return claude, agents
}

func TestAnyCommandInstallsTheSkillOnFirstRun(t *testing.T) {
	claude, agents := isolateSkillHome(t)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"scan"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{claude, agents} {
		if _, err := os.Stat(filepath.Join(dir, "crossmem-loader", "SKILL.md")); err != nil {
			t.Fatalf("skill not installed under %s: %v", dir, err)
		}
	}

	// Second run: the stamp matches, so nothing is reinstalled or reported.
	if err := os.RemoveAll(filepath.Join(claude, "crossmem-loader")); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if err := Run([]string{"scan"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "crossmem-loader")); !os.IsNotExist(err) {
		t.Error("the skill was reinstalled although the stamp was current")
	}
}

func TestUninstallKeepsTheSkillUninstalled(t *testing.T) {
	claude, _ := isolateSkillHome(t)
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"install", "--skills", "--agents"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"uninstall", "--skills", "--agents"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"scan"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "crossmem-loader")); !os.IsNotExist(err) {
		t.Error("a deliberately uninstalled skill came back on the next command")
	}
}

func TestSkillInstallCanBeTurnedOff(t *testing.T) {
	claude, _ := isolateSkillHome(t)
	t.Setenv("CROSSMEM_NO_SKILL_INSTALL", "1")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"scan"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "crossmem-loader")); !os.IsNotExist(err) {
		t.Error("CROSSMEM_NO_SKILL_INSTALL=1 still installed the skill")
	}
}
