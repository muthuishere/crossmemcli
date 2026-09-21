package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/crossmemcli/internal/version"
	"github.com/muthuishere/crossmemcli/pkg/skillinstall"
	"github.com/muthuishere/crossmemcli/skills"
)

// Skills are global, so installing them is a side effect on the user's home
// directory rather than on this repository. crossmem does it on the first run
// of any command — the skill is how an agent discovers the CLI at all, so a
// CLI installed without it is only half installed. A stamp file records what
// was installed; once it matches, the check is one small read.
//
// $CROSSMEM_NO_SKILL_INSTALL=1 turns the whole thing off, and
// `crossmem uninstall --skills` writes "disabled" into the stamp so a removed
// skill stays removed.
const skillStampDisabled = "disabled"

// ensureSkillsInstalled installs the bundled skills into every global skills
// directory unless that has already been done for this build. It never fails a
// command: an unwritable home directory is reported on stderr and ignored.
func ensureSkillsInstalled(stderr io.Writer) {
	if os.Getenv("CROSSMEM_NO_SKILL_INSTALL") == "1" {
		return
	}
	stamp, err := skillStampPath()
	if err != nil {
		return
	}
	want, err := bundledSkillFingerprint()
	if err != nil {
		return
	}
	if got, err := os.ReadFile(stamp); err == nil {
		if current := strings.TrimSpace(string(got)); current == want || current == skillStampDisabled {
			return
		}
	}
	targets, err := skillinstall.DefaultTargets(true)
	if err != nil {
		return
	}
	results, err := skillinstall.Install(skills.FS, skills.Loader, targets)
	if err != nil {
		fmt.Fprintf(stderr, "crossmem: could not install the %s skill: %v\n", skills.Loader, err)
		return
	}
	writeSkillStamp(want)
	for _, result := range results {
		fmt.Fprintf(stderr, "crossmem: %s skill %s at %s\n", result.Host, result.Action, result.Path)
	}
}

// writeSkillStamp records what is installed. A failure here only costs a
// repeated install next run, so it is not reported.
func writeSkillStamp(content string) {
	stamp, err := skillStampPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(stamp), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(stamp, []byte(content+"\n"), 0o644)
}

func skillStampPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "crossmemcli", "skills.stamp"), nil
}

// bundledSkillFingerprint hashes the embedded skill tree with the build
// version, so a rebuilt or edited skill reinstalls itself on the next run.
func bundledSkillFingerprint() (string, error) {
	sum := sha256.New()
	fmt.Fprintf(sum, "version\x00%s\x00", version.Version)
	err := fs.WalkDir(skills.FS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(skills.FS, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(sum, "%s\x00%d\x00", name, len(data))
		sum.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
