package crossmem

import (
	"os"
	"path/filepath"
	"strings"
)

// relatedFolders returns folder plus every other checkout of the same git
// repository: the main worktree and each linked worktree.
//
// Agents work in worktrees (one per task, review, or experiment), and each
// checkout is a different path — so the sessions that hold the context for this
// work were recorded against the main repo or a sibling worktree and would be
// invisible from here. They are the same repository, so they are offered
// together, with the folder asked for first.
//
// git's own files are read rather than shelling out to git: a linked worktree's
// .git is a file saying "gitdir: <common>/worktrees/<name>", and each entry
// there has a "gitdir" file pointing back at that worktree's .git. This needs no
// git on PATH and behaves the same on every OS.
func relatedFolders(folder string) []string {
	folders := []string{folder}
	if folder == "" {
		return folders
	}
	common := commonGitDir(folder)
	if common == "" {
		return folders // not a git repository, or unreadable
	}
	add := func(dir string) {
		if dir == "" {
			return
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return
		}
		for _, existing := range folders {
			if pathsEqual(normalizeCase(filepath.Clean(existing)), normalizeCase(filepath.Clean(dir))) {
				return
			}
		}
		folders = append(folders, dir)
	}

	// The main worktree is the directory holding the common .git directory.
	// A bare repository (common dir not named ".git") has no main worktree.
	if filepath.Base(common) == ".git" {
		add(filepath.Dir(common))
	}
	// Linked worktrees, including the one we may be standing in.
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return folders
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pointer, err := os.ReadFile(filepath.Join(common, "worktrees", entry.Name(), "gitdir"))
		if err != nil {
			continue
		}
		// The file holds the path of that worktree's .git file; its directory
		// is the checkout. A pruned worktree leaves a stale entry, which the
		// existence check in add drops.
		if dotGit := strings.TrimSpace(string(pointer)); dotGit != "" {
			add(filepath.Dir(dotGit))
		}
	}
	return folders
}

// commonGitDir resolves the git directory shared by every worktree of the
// repository containing folder, or "" when folder is not in a git repository.
func commonGitDir(folder string) string {
	dir := filepath.Clean(folder)
	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Lstat(candidate)
		switch {
		case err == nil && info.IsDir():
			return candidate
		case err == nil:
			// A .git file points elsewhere: a linked worktree, or a submodule.
			own := readGitdirPointer(candidate, dir)
			if own == "" {
				return ""
			}
			return commonDirOf(own)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// readGitdirPointer reads "gitdir: <path>" from a .git file, resolving a
// relative path against the checkout that contains it.
func readGitdirPointer(path string, base string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value)
}

// commonDirOf maps a worktree's own git directory to the one shared by every
// worktree of the repository. git records it in a "commondir" file; without one
// (a submodule, say) the directory is its own common directory.
func commonDirOf(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return gitDir
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(gitDir, value)
	}
	return filepath.Clean(value)
}

// matchesAnyFolder reports whether workspace is one of the folders or sits
// under one of them.
func matchesAnyFolder(workspace string, folders []string) bool {
	for _, folder := range folders {
		if sameOrChild(workspace, folder) {
			return true
		}
	}
	return false
}
