// Package skillinstall installs an agent skill — a directory holding SKILL.md
// and its references — into the global skill directories agent tools read,
// replacing any previous copy atomically.
package skillinstall

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Target is one skills root, such as ~/.claude/skills. A skill named n installs
// to <Dir>/<n>.
type Target struct {
	Host string
	Dir  string
}

// Result reports what happened at one target: Action is "installed",
// "updated", "removed", or "not-installed".
type Result struct {
	Host   string
	Path   string
	Action string
}

// DefaultTargets returns ~/.claude/skills and, when includeAgents is set,
// ~/.agents/skills. $CLAUDE_SKILLS_DIR and $AGENTS_SKILLS_DIR override them.
func DefaultTargets(includeAgents bool) ([]Target, error) {
	claude, err := rootDir("CLAUDE_SKILLS_DIR", ".claude", "skills")
	if err != nil {
		return nil, err
	}
	targets := []Target{{Host: "claude", Dir: claude}}
	if includeAgents {
		agents, err := rootDir("AGENTS_SKILLS_DIR", ".agents", "skills")
		if err != nil {
			return nil, err
		}
		targets = append(targets, Target{Host: "agents", Dir: agents})
	}
	return targets, nil
}

// Install copies the directory name from fsys into every target. Each target
// is written to a temporary sibling and renamed into place, so a reader never
// sees a half-written skill and a failed install leaves the old copy intact.
func Install(fsys fs.FS, name string, targets []Target) ([]Result, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if _, err := fs.Stat(fsys, path.Join(name, "SKILL.md")); err != nil {
		return nil, fmt.Errorf("skill %q has no SKILL.md: %w", name, err)
	}
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		dst := filepath.Join(target.Dir, name)
		action, err := installAt(fsys, name, dst)
		if err != nil {
			return results, fmt.Errorf("%s install failed: %w", target.Host, err)
		}
		results = append(results, Result{Host: target.Host, Path: dst, Action: action})
	}
	return results, nil
}

// Uninstall removes the skill directory name from every target.
func Uninstall(name string, targets []Target) ([]Result, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		dst := filepath.Join(target.Dir, name)
		action := "removed"
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			action = "not-installed"
		} else if err != nil {
			return results, fmt.Errorf("%s uninstall failed: stat %s: %w", target.Host, dst, err)
		} else if err := os.RemoveAll(dst); err != nil {
			return results, fmt.Errorf("%s uninstall failed: remove %s: %w", target.Host, dst, err)
		}
		results = append(results, Result{Host: target.Host, Path: dst, Action: action})
	}
	return results, nil
}

// validName rejects anything but a single plain path segment. Install and
// Uninstall delete <Dir>/<name>; a name like "../x" would reach outside Dir.
func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || filepath.Base(name) != name {
		return fmt.Errorf("invalid skill name %q: must be a single directory name", name)
	}
	return nil
}

func rootDir(envKey string, fallback ...string) (string, error) {
	if dir := strings.TrimSpace(os.Getenv(envKey)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for %s: %w", envKey, err)
	}
	return filepath.Join(append([]string{home}, fallback...)...), nil
}

func installAt(fsys fs.FS, name string, dst string) (string, error) {
	action := "installed"
	if _, err := os.Lstat(dst); err == nil {
		action = "updated"
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", dst, err)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("create parent %s: %w", parent, err)
	}
	tmp, err := os.MkdirTemp(parent, name+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp install dir in %s: %w", parent, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := copyTree(fsys, name, tmp); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dst); err != nil {
		return "", fmt.Errorf("remove existing install %s: %w", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", fmt.Errorf("activate install at %s: %w", dst, err)
	}
	cleanup = false
	return action, nil
}

func copyTree(fsys fs.FS, root string, dst string) error {
	return fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		out := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(p, root+"/")))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read skill file %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return fmt.Errorf("create parent for %s: %w", out, err)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", out, err)
		}
		return nil
	})
}
