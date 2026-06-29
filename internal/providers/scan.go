package providers

import (
	"os"
	"path/filepath"
	"strings"
)

func DiscoverStores() ([]Store, error) {
	stores := make([]Store, 0, len(storeDefinitions))
	for _, def := range storeDefinitions {
		path := expandHome(def.Path)
		info, err := os.Stat(path)
		store := Store{Provider: def.Provider, Kind: def.Kind, Path: path, Exists: err == nil, Note: def.Note}
		if err == nil {
			size := info.Size()
			store.Bytes = &size
			if info.IsDir() {
				files, bytes := countInteresting(path)
				store.Files = &files
				store.Bytes = &bytes
			}
		}
		stores = append(stores, store)
	}
	return stores, nil
}

func countInteresting(root string) (int, int64) {
	var files int
	var bytes int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "vault" {
				return filepath.SkipDir
			}
			return nil
		}
		if interestingFile(d.Name()) {
			files++
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
			}
		}
		return nil
	})
	return files, bytes
}

func interestingFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".sqlite") ||
		strings.HasSuffix(lower, ".sqlite3") ||
		strings.HasSuffix(lower, ".db") ||
		strings.HasSuffix(lower, ".log") ||
		strings.HasSuffix(lower, ".json") ||
		lower == "state.vscdb"
}
