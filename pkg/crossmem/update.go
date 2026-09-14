package crossmem

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
)

// storeManifest keeps what is durable about a store — where it is and whether
// it exists — and drops its byte and file counts. Those change every time any
// agent writes a transcript, which would make this committed file churn on
// every run for reasons that say nothing about the folder's context.
func storeManifest(stores []Store) []Store {
	out := make([]Store, 0, len(stores))
	for _, store := range stores {
		store.Bytes = nil
		store.Files = nil
		out = append(out, store)
	}
	return out
}

// UpdateResult reports what UpdateContext wrote under .crossmem/.
type UpdateResult struct {
	// Paths is every file the bundle covers, sorted.
	Paths []string
	// Written is the subset actually rewritten this run; Unchanged is the rest.
	// A second run with no new sessions writes nothing.
	Written   []string
	Unchanged []string
}

// updateContext writes the durable bundle under <folder>/.crossmem. It is
// idempotent: the rendered content carries no timestamps, and a file whose
// bytes are unchanged is left alone rather than rewritten, so re-running it
// produces no diff and no mtime churn.
func (c *Client) updateContext(opts ListOptions) (UpdateResult, error) {
	opts.Deterministic = true
	root, err := filepath.Abs(expandHome(opts.CWD))
	if err != nil {
		return UpdateResult{}, err
	}
	outDir := filepath.Join(root, ".crossmem")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return UpdateResult{}, err
	}

	guardrails, err := BuildGuardrails(root)
	if err != nil {
		return UpdateResult{}, err
	}
	files, err := ReadGuardrails(root)
	if err != nil {
		return UpdateResult{}, err
	}
	context, err := c.buildContext(opts)
	if err != nil {
		return UpdateResult{}, err
	}
	sessions, err := c.listSessions(opts)
	if err != nil {
		return UpdateResult{}, err
	}
	stores, err := c.discoverStores()
	if err != nil {
		return UpdateResult{}, err
	}

	writes := map[string][]byte{
		filepath.Join(outDir, "guardrails.md"): []byte(guardrails),
		filepath.Join(outDir, "context.md"):    []byte(context),
		filepath.Join(outDir, "sessions.json"): marshalIndent(sessions),
		filepath.Join(outDir, "sources.json"): marshalIndent(map[string]any{
			"stores":     storeManifest(stores),
			"guardrails": guardrailManifest(files),
		}),
	}

	result := UpdateResult{Paths: make([]string, 0, len(writes))}
	for path := range writes {
		result.Paths = append(result.Paths, path)
	}
	// Map iteration is randomized; the reported order must not be.
	sort.Strings(result.Paths)
	for _, path := range result.Paths {
		data := writes[path]
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
			result.Unchanged = append(result.Unchanged, path)
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return UpdateResult{}, err
		}
		result.Written = append(result.Written, path)
	}
	return result, nil
}
