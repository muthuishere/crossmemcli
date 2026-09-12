package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-running update must produce no diff and no mtime churn: the files are
// meant to be committed, and a bundle that rewrites itself on every run is
// noise in every review.
func TestUpdateContextIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := ListOptions{Provider: "all", CWD: dir, Limit: 3}

	first, err := UpdateContext(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Written) == 0 {
		t.Fatal("the first run must write the bundle")
	}

	before := map[string]os.FileInfo{}
	for _, path := range first.Paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = info
	}

	second, err := UpdateContext(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Written) != 0 {
		t.Fatalf("a repeat run rewrote %v", second.Written)
	}
	if len(second.Unchanged) != len(first.Paths) {
		t.Fatalf("expected every file unchanged, got %v", second.Unchanged)
	}
	for _, path := range second.Paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(before[path].ModTime()) {
			t.Fatalf("%s was touched despite being unchanged", filepath.Base(path))
		}
	}

	// The persisted bundle carries no generated-at stamp; that is what makes
	// the bytes stable across runs.
	body, err := os.ReadFile(filepath.Join(dir, ".crossmem", "context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Generated:") {
		t.Fatal("the written bundle must not carry a generated-at timestamp")
	}
}

// Paths are reported in a stable order; they come out of a map internally.
func TestUpdateContextReportsSortedPaths(t *testing.T) {
	dir := t.TempDir()
	result, err := UpdateContext(ListOptions{Provider: "all", CWD: dir, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(result.Paths); i++ {
		if result.Paths[i-1] > result.Paths[i] {
			t.Fatalf("paths are not sorted: %v", result.Paths)
		}
	}
}
