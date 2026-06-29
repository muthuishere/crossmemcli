package providers

import (
	"os"
	"path/filepath"
)

type UpdateResult struct {
	Paths []string
}

func UpdateContext(opts ListOptions) (UpdateResult, error) {
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
	context, err := BuildContext(opts)
	if err != nil {
		return UpdateResult{}, err
	}
	sessions, err := ListSessions(opts)
	if err != nil {
		return UpdateResult{}, err
	}
	stores, err := DiscoverStores()
	if err != nil {
		return UpdateResult{}, err
	}

	writes := map[string][]byte{
		filepath.Join(outDir, "guardrails.md"): []byte(guardrails),
		filepath.Join(outDir, "context.md"):    []byte(context),
		filepath.Join(outDir, "sessions.json"): marshalIndent(sessions),
		filepath.Join(outDir, "sources.json"): marshalIndent(map[string]any{
			"stores":     stores,
			"guardrails": guardrailManifest(files),
		}),
	}

	result := UpdateResult{Paths: make([]string, 0, len(writes))}
	for path, data := range writes {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return UpdateResult{}, err
		}
		result.Paths = append(result.Paths, path)
	}
	return result, nil
}
