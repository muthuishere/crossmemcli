package crossmem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DumpSchema identifies the portable dump format. A dump is a directory with a
// manifest.json plus the material it describes. Import refuses any directory
// whose manifest carries a different schema, so a future format change is a
// clean break instead of a silent misread.
const DumpSchema = "crossmem.dump.v1"

// defaultDumpDir is where whole-machine `crossmem export` writes qa.jsonl, and
// where `crossmem sync` pushes a store dump from, when neither a flag nor the
// config says otherwise.
func (c *Client) defaultDumpDir() string {
	if configured := c.config.DumpDir; configured != "" {
		return expandPath(configured)
	}
	return filepath.Join(homeDir(), ".assets", "convdump")
}

// DumpStore describes one exported store in the manifest: where it came from
// and where its files landed in the dump.
type DumpStore struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	// RootKind is "dir" when the store root is a directory of transcripts and
	// "file" when it is a single database/log file (SQLite-backed stores).
	RootKind    string `json:"rootKind"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Note        string `json:"note,omitempty"`
}

// DumpFile describes one well-known instruction or memory file in the dump.
type DumpFile struct {
	// Name is the stable logical name used to resolve where the file is
	// restored on import, so a dump made on one machine restores correctly on
	// another.
	Name        string `json:"name"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
}

// DumpManifest is the single source of truth for a dump directory.
type DumpManifest struct {
	Schema      string      `json:"schema"`
	GeneratedAt string      `json:"generatedAt"`
	Host        string      `json:"host"`
	OS          string      `json:"os"`
	Arch        string      `json:"arch"`
	Out         string      `json:"out"`
	Stores      []DumpStore `json:"stores"`
	// Instructions are the global instruction files an agent reads first
	// (Claude Code's CLAUDE.md, Codex's AGENTS.md, ~/.agents/AGENTS.md).
	Instructions []DumpFile `json:"instructions,omitempty"`
	// Memory is tool-specific memory that lives outside the session stores
	// (Codex's goals_1.sqlite, for example).
	Memory []DumpFile `json:"memory,omitempty"`
}

// ExportOptions configures ExportDump.
type ExportOptions struct {
	// Out is the dump directory. Empty means c.defaultDumpDir().
	Out string
	// Provider restricts the exported stores to one provider ("all" for every
	// provider). Instructions and memory are always included.
	Provider string
}

// exportDump copies every discoverable store plus the well-known instruction
// and memory files into a single portable dump directory and writes
// manifest.json. It never copies credential files, auth databases, env files,
// or anything under a vault/, cache/, or node_modules/ directory.
func (c *Client) exportDump(opts ExportOptions) (DumpManifest, error) {
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	out := opts.Out
	if out == "" {
		out = c.defaultDumpDir()
	}
	out = expandPath(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return DumpManifest{}, err
	}

	host, _ := os.Hostname()
	manifest := DumpManifest{
		Schema:      DumpSchema,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Host:        host,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Out:         out,
	}

	stores, err := c.discoverStores()
	if err != nil {
		return DumpManifest{}, err
	}
	for _, store := range stores {
		if opts.Provider != "all" && store.Provider != opts.Provider {
			continue
		}
		if !store.Exists {
			continue
		}
		if isWithin(out, store.Path) {
			c.log.debugf("export skip provider=%s kind=%s path=%q inside dump dir", store.Provider, store.Kind, store.Path)
			continue
		}
		info, err := os.Stat(store.Path)
		if err != nil {
			c.log.debugf("export stat provider=%s kind=%s path=%q err=%q", store.Provider, store.Kind, store.Path, err)
			continue
		}
		ds := DumpStore{
			Provider:    store.Provider,
			Kind:        store.Kind,
			Source:      store.Path,
			Destination: filepath.Join("stores", store.Provider, store.Kind),
			Note:        store.Note,
		}
		destAbs := filepath.Join(out, filepath.FromSlash(ds.Destination))
		if info.IsDir() {
			ds.RootKind = "dir"
			ds.Files, ds.Bytes, err = c.copyStoreDir(store.Path, destAbs)
		} else {
			ds.RootKind = "file"
			ds.Files, ds.Bytes, err = c.copyStoreFile(store.Path, destAbs)
		}
		if err != nil {
			return DumpManifest{}, fmt.Errorf("export %s:%s: %w", store.Provider, store.Kind, err)
		}
		manifest.Stores = append(manifest.Stores, ds)
	}

	for _, extra := range extraFiles {
		source := firstExisting(extra.Candidates)
		if source == "" {
			continue
		}
		destination := extra.Section + "/" + extra.Name
		if err := c.copyFile(source, filepath.Join(out, filepath.FromSlash(destination))); err != nil {
			return DumpManifest{}, fmt.Errorf("export %s: %w", extra.Name, err)
		}
		entry := DumpFile{Name: extra.Name, Source: source, Destination: destination}
		if extra.Section == "memory" {
			manifest.Memory = append(manifest.Memory, entry)
		} else {
			manifest.Instructions = append(manifest.Instructions, entry)
		}
	}

	if err := writeManifest(filepath.Join(out, "manifest.json"), manifest); err != nil {
		return DumpManifest{}, err
	}
	return manifest, nil
}

// ImportOptions configures ImportDump.
type ImportOptions struct {
	// In is the dump directory to restore from.
	In string
	// DryRun reports what would be restored without writing anything.
	DryRun bool
	// Force overwrites a local file even when its bytes already match the
	// dump. Without it, identical files are skipped (idempotent restore).
	Force bool
}

// ImportResult reports what a restore did.
type ImportResult struct {
	Restored []string
	Skipped  []string
	// Missing lists stores and files present in the dump whose restore target
	// does not exist on this machine and has no known default location.
	Missing []string
}

// importDump restores a dump directory back into this machine's stores. Each
// store resolves to the current machine's location (honouring the user
// config), so a dump made on another machine restores into the right place
// here. Identical files are skipped unless Force is set.
func (c *Client) importDump(opts ImportOptions) (ImportResult, error) {
	in := expandPath(opts.In)
	manifest, err := readManifest(filepath.Join(in, "manifest.json"))
	if err != nil {
		return ImportResult{}, err
	}

	var res ImportResult
	for _, store := range manifest.Stores {
		target := c.importTarget(store.Provider, store.Kind)
		if target == "" {
			res.Missing = append(res.Missing, fmt.Sprintf("%s:%s (from %s) — no location on this machine", store.Provider, store.Kind, store.Source))
			continue
		}
		srcDir := filepath.Join(in, filepath.FromSlash(store.Destination))
		if store.RootKind == "file" {
			// A file-rooted store (SQLite DB, log) maps each dumped file back to
			// its own sibling next to the resolved store path — OpenCode's
			// opencode.db / opencode-dev.db / opencode-local.db all share one
			// kind but must restore to three different files.
			baseDir := filepath.Dir(target)
			entries, err := os.ReadDir(srcDir)
			if err != nil {
				if !os.IsNotExist(err) {
					return res, err
				}
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				dest := filepath.Join(baseDir, entry.Name())
				if err := c.restoreOne(filepath.Join(srcDir, entry.Name()), dest, opts, &res); err != nil {
					return res, err
				}
			}
			continue
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return res, err
		}
		err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}
			return c.restoreOne(path, filepath.Join(target, rel), opts, &res)
		})
		if err != nil {
			return res, err
		}
	}

	// Instructions and memory restore by their stable name, resolving to the
	// current machine's location regardless of where the dump was made.
	for _, section := range []struct {
		entries []DumpFile
		label   string
	}{{manifest.Instructions, "instructions"}, {manifest.Memory, "memory"}} {
		for _, entry := range section.entries {
			dest := extraTarget(entry.Name)
			if dest == "" {
				res.Missing = append(res.Missing, fmt.Sprintf("%s/%s — no location on this machine", section.label, entry.Name))
				continue
			}
			src := filepath.Join(in, filepath.FromSlash(entry.Destination))
			if err := c.restoreOne(src, dest, opts, &res); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func (c *Client) restoreOne(src string, dest string, opts ImportOptions, res *ImportResult) error {
	restored, err := c.restoreFile(src, dest, opts.DryRun, opts.Force)
	if err != nil {
		return err
	}
	if restored {
		res.Restored = append(res.Restored, dest)
	} else {
		res.Skipped = append(res.Skipped, dest)
	}
	return nil
}

// SyncOptions configures SyncDump.
type SyncOptions struct {
	// Remote is the rclone destination/path. Empty means the config's
	// sync.remote.
	Remote string
	// Pull copies from the remote into the local dump dir instead of pushing.
	Pull bool
	// Prune deletes files at the destination that are not in the source
	// (rclone sync) instead of additive copy.
	Prune bool
	// Out is the local dump directory. Empty means c.defaultDumpDir().
	Out string
}

// SyncResult reports where a sync went.
type SyncResult struct {
	Remote  string
	DumpDir string
	Pull    bool
	Prune   bool
	// Output is rclone's combined stdout/stderr.
	Output string
}

// syncDump pushes the dump directory to (or pulls it from) an rclone remote.
// The dump is a directory, so the same layout that import reads is what gets
// synced; no archive step is needed.
func (c *Client) syncDump(opts SyncOptions) (SyncResult, error) {
	remote := opts.Remote
	if remote == "" {
		remote = c.config.Sync.Remote
	}
	if remote == "" {
		return SyncResult{}, fmt.Errorf("no sync remote: pass --remote or set sync.remote in the config")
	}
	dumpDir := opts.Out
	if dumpDir == "" {
		dumpDir = c.defaultDumpDir()
	}
	dumpDir = expandPath(dumpDir)
	if !opts.Pull {
		if _, err := os.Stat(filepath.Join(dumpDir, "manifest.json")); err != nil {
			return SyncResult{}, fmt.Errorf("no dump found at %s (run crossmem export first): %w", dumpDir, err)
		}
	}

	rclone, err := exec.LookPath("rclone")
	if err != nil {
		return SyncResult{}, fmt.Errorf("rclone not found on PATH (install it, or set sync.remote to a plain path): %w", err)
	}

	verb := "copy"
	if opts.Prune {
		verb = "sync"
	}
	source, destination := dumpDir, remote
	if opts.Pull {
		source, destination = remote, dumpDir
	}
	args := []string{verb, source, destination, "--stats-one-line", "-v"}
	c.log.debugf("sync rclone=%s args=%q", rclone, args)
	cmd := exec.Command(rclone, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return SyncResult{}, fmt.Errorf("rclone %s failed: %w\n%s", verb, err, output)
	}
	return SyncResult{Remote: remote, DumpDir: dumpDir, Pull: opts.Pull, Prune: opts.Prune, Output: string(output)}, nil
}

// extraFile declares one well-known instruction or memory file. Candidates are
// tried in order: export uses the first that exists here, import restores to
// the first that resolves here.
type extraFile struct {
	Name       string
	Section    string // "instructions" or "memory"
	Candidates []string
}

var extraFiles = []extraFile{
	{"claude/CLAUDE.md", "instructions", []string{"$CLAUDE_CONFIG_DIR/CLAUDE.md", "~/.claude/CLAUDE.md"}},
	{"codex/AGENTS.md", "instructions", []string{"$CODEX_HOME/AGENTS.md", "~/.codex/AGENTS.md"}},
	{"agents/AGENTS.md", "instructions", []string{"~/.agents/AGENTS.md"}},
	{"home/AGENTS.md", "instructions", []string{"~/AGENTS.md"}},
	{"home/CLAUDE.md", "instructions", []string{"~/CLAUDE.md"}},
	{"home/AGENT_INSTRUCTIONS.md", "instructions", []string{"~/AGENT_INSTRUCTIONS.md"}},
	{"codex/goals.sqlite", "memory", []string{"$CODEX_HOME/goals_1.sqlite", "~/.codex/goals_1.sqlite"}},
}

func firstExisting(candidates []string) string {
	for _, candidate := range candidates {
		path := expandPath(candidate)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func extraTarget(name string) string {
	for _, extra := range extraFiles {
		if extra.Name != name {
			continue
		}
		for _, candidate := range extra.Candidates {
			if path := expandPath(candidate); path != "" {
				return path
			}
		}
	}
	return ""
}

// importTarget is the machine-local path a dumped store restores into: the
// existing store location when the tool is installed, else the platform's
// default candidate (so a fresh machine still gets the files).
func (c *Client) importTarget(provider string, kind string) string {
	if path := c.storePath(provider, kind); path != "" {
		return path
	}
	return c.displayCandidate(provider, kind)
}

func (c *Client) copyStoreDir(root string, dest string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			c.log.debugf("export walk path=%q err=%q", path, err)
			return nil
		}
		if d.IsDir() {
			if path != root && skipExportDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !exportableFile(d.Name(), filepath.ToSlash(rel)) {
			return nil
		}
		if err := c.copyFile(path, filepath.Join(dest, rel)); err != nil {
			return err
		}
		if info, err := d.Info(); err == nil {
			bytes += info.Size()
		}
		files++
		return nil
	})
	return files, bytes, err
}

func (c *Client) copyStoreFile(src string, destDir string) (int, int64, error) {
	if skipExportFile(filepath.Base(src)) {
		return 0, 0, nil
	}
	if err := c.copyFile(src, filepath.Join(destDir, filepath.Base(src))); err != nil {
		return 0, 0, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return 1, 0, nil
	}
	return 1, info.Size(), nil
}

// skipExportDir prunes directories that hold no portable context: caches,
// backups, vendored code, encrypted secrets.
func skipExportDir(name string) bool {
	switch strings.ToLower(name) {
	case "node_modules", "vault", "cache", "backups", "telemetry", ".git",
		"_skill-archive", "paste-cache", "downloads", "debug", "shell-snapshots",
		".tmp", "file-history", "chrome", "daemon":
		return true
	}
	return false
}

// skipExportFile filters credential, auth, and transient files out of a dump.
// A dump is shared through a sync remote, so anything that could authorize a
// session or hold a secret value never leaves the machine.
func skipExportFile(name string) bool {
	lower := strings.ToLower(name)
	for _, base := range []string{
		"auth.json", "auth.db", "credentials.json", "credentials.toml",
		"credentials", ".netrc", "netrc", "settings.json", "config.toml",
	} {
		if lower == base {
			return true
		}
	}
	for _, suffix := range []string{
		".key", ".pem", ".p8", ".p12", ".pfx", ".crt", ".gpg", ".kdbx",
		".env", "-wal", "-shm", ".wal", ".shm",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	if strings.HasPrefix(lower, ".env") {
		return true
	}
	if strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "token") {
		return true
	}
	return false
}

// exportableFile decides what gets copied from a store directory: the same
// "interesting" transcripts scan counts, plus markdown project memory files.
func exportableFile(name string, rel string) bool {
	if skipExportFile(name) {
		return false
	}
	if interestingFile(name) {
		return true
	}
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		for _, segment := range strings.Split(rel, "/") {
			if segment == "memory" {
				return true
			}
		}
	}
	return false
}

// isWithin reports whether path lies inside root.
func isWithin(root string, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c *Client) copyFile(src string, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		c.log.debugf("export skip symlink %q", src)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// restoreFile copies src to dest unless the bytes already match. It reports
// whether it would/has restore the file (false = skipped as identical).
func (c *Client) restoreFile(src string, dest string, dryRun bool, force bool) (bool, error) {
	if !force {
		want, err := os.ReadFile(src)
		if err != nil {
			return false, err
		}
		if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, want) {
			return false, nil
		}
	}
	if dryRun {
		return true, nil
	}
	if err := c.copyFile(src, dest); err != nil {
		return false, err
	}
	return true, nil
}

func writeManifest(path string, manifest DumpManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readManifest(path string) (DumpManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DumpManifest{}, fmt.Errorf("read dump manifest: %w", err)
	}
	var manifest DumpManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return DumpManifest{}, fmt.Errorf("parse dump manifest: %w", err)
	}
	if manifest.Schema != DumpSchema {
		return DumpManifest{}, fmt.Errorf("unsupported dump schema %q (want %s)", manifest.Schema, DumpSchema)
	}
	return manifest, nil
}
