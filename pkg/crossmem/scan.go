package crossmem

import (
	"os"
	"path/filepath"
	"strings"
)

func (c *Client) discoverStores() ([]Store, error) {
	stores := make([]Store, 0, len(storeDefinitions))
	for _, def := range storeDefinitions {
		// A store can resolve to several real paths (OpenCode's stable/dev/local
		// databases), to exactly one, or to none when the tool is not installed.
		// Report one line per real path, or a single line naming the location
		// this platform would use.
		paths := c.storePaths(def.Provider, def.Kind)
		if len(paths) == 0 {
			stores = append(stores, Store{Provider: def.Provider, Kind: def.Kind, Path: c.displayCandidate(def.Provider, def.Kind), Exists: false, Note: def.Note})
			continue
		}
		for _, path := range paths {
			store := Store{Provider: def.Provider, Kind: def.Kind, Path: path, Exists: true, Note: def.Note}
			info, err := os.Stat(path)
			if err != nil {
				c.log.debugf("scan stat provider=%s kind=%s path=%q err=%q", def.Provider, def.Kind, path, err)
				store.Exists = false
				stores = append(stores, store)
				continue
			}
			size := info.Size()
			store.Bytes = &size
			if info.IsDir() {
				files, bytes := c.countInteresting(path)
				store.Files = &files
				store.Bytes = &bytes
			}
			stores = append(stores, store)
		}
	}
	return stores, nil
}

func (c *Client) countInteresting(root string) (int, int64) {
	var files int
	var bytes int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			c.log.debugf("scan walk path=%q err=%q", path, err)
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
		strings.HasSuffix(lower, ".pb") ||
		lower == "state.vscdb"
}
