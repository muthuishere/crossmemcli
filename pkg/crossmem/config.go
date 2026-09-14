package crossmem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Config is the optional user override file. crossmem ships a candidate list
// per store for macOS, Linux, and Windows, but agent tools move their stores
// and people relocate them (a portable install, a synced drive, WSL). The
// config lets any store be repointed without a new release.
//
// Location: ~/.config/crossmemcli/config.json, or $CROSSMEM_CONFIG.
//
//	{
//	  "defaults": { "mode": "full", "limit": 5 },
//	  "stores": {
//	    "devin:sqlite-sessions": "%APPDATA%/devin/cli/sessions.db",
//	    "claude": ["D:/agents/.claude/projects"]
//	  },
//	  "extraStores": {
//	    "opencode": "~/work/opencode/opencode*.db"
//	  },
//	  "dumpDir": "~/.assets/convdump",
//	  "sync": { "remote": "hetzbox:companydata/convdump" }
//	}
//
// Keys are "provider:kind" (see `crossmem scan` for every kind), or a bare
// "provider" which addresses that provider's primary store. Values are a
// string or a list of strings; ~, %VAR%, $VAR, and globs are all supported.
// "stores" replaces the built-in candidates for a store; "extraStores" keeps
// them and appends. Nothing here is read as a secret — paths only.
type Config struct {
	// Path is where the config was loaded from, or would be loaded from.
	Path string `json:"path"`
	// Defaults set what the CLI does when a flag is not given. A flag on the
	// command line always wins.
	Defaults Defaults `json:"defaults"`
	// Exists reports whether that file is present.
	Exists bool `json:"exists"`
	// Stores replaces the built-in candidates for a store, keyed by
	// "provider:kind" or a bare "provider".
	Stores map[string][]string `json:"stores,omitempty"`
	// ExtraStores appends to the built-in candidates, same key form.
	ExtraStores map[string][]string `json:"extraStores,omitempty"`
	// DumpDir is where `crossmem export` writes the portable dump and where
	// `crossmem sync` pushes from (or pulls into). Defaults to
	// ~/.assets/convdump.
	DumpDir string `json:"dumpDir,omitempty"`
	// Sync holds the rclone remote used by `crossmem sync`.
	Sync SyncConfig `json:"sync,omitempty"`
}

// SyncConfig configures `crossmem sync`, which pushes the dump directory to an
// rclone remote with `rclone copy` (or `rclone sync` with --prune).
type SyncConfig struct {
	// Remote is the rclone destination, e.g. "hetzbox:companydata/convdump".
	Remote string `json:"remote,omitempty"`
}

// Defaults is the "how should crossmem behave for me" half of the config.
// Excerpt size is the setting people actually have a standing preference
// about: some always want the compact summary, others always want the full
// transcript, and re-typing --full on every call is friction.
type Defaults struct {
	// Mode is "summary" or "full". Empty means summary.
	Mode string `json:"mode,omitempty"`
	// Limit is the default session count for list/load/update. Zero means the
	// per-command built-in default.
	Limit int `json:"limit,omitempty"`
}

// Bundle excerpt sizes, as named in the config file's defaults.mode and by
// `crossmem load --mode`.
const (
	ModeSummary = "summary"
	ModeFull    = "full"
)

// Full reports whether the configured default asks for full excerpts.
func (d Defaults) Full() bool { return d.Mode == ModeFull }

// configFile is the on-disk shape; pathList accepts a string or a list.
type configFile struct {
	Defaults    Defaults            `json:"defaults"`
	Stores      map[string]pathList `json:"stores"`
	ExtraStores map[string]pathList `json:"extraStores"`
	DumpDir     string              `json:"dumpDir"`
	Sync        SyncConfig          `json:"sync"`
}

type pathList []string

func (p *pathList) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*p = pathList{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("expected a path string or a list of path strings")
	}
	*p = many
	return nil
}

// ConfigPath returns the config file location for this machine.
func ConfigPath() string {
	if override := os.Getenv("CROSSMEM_CONFIG"); override != "" {
		return expandPath(override)
	}
	return filepath.Join(homeDir(), ".config", "crossmemcli", "config.json")
}

// LoadConfig reads the user config. A missing file is not an error — it yields
// an empty config, which is the default state on every machine.
func LoadConfig() (Config, error) {
	path := ConfigPath()
	config := Config{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, fmt.Errorf("read %s: %w", path, err)
	}
	config.Exists = true

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		return config, fmt.Errorf("parse %s: %w", path, err)
	}
	config.Defaults = file.Defaults
	config.Stores = toStringMap(file.Stores)
	config.ExtraStores = toStringMap(file.ExtraStores)
	config.DumpDir = file.DumpDir
	config.Sync = file.Sync
	if err := config.validate(); err != nil {
		return config, fmt.Errorf("%s: %w", path, err)
	}
	return config, nil
}

func toStringMap(in map[string]pathList) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, value := range in {
		out[key] = []string(value)
	}
	return out
}

// validate rejects keys that address no known store, so a typo surfaces as an
// error instead of silently doing nothing.
func (c Config) validate() error {
	switch c.Defaults.Mode {
	case "", ModeSummary, ModeFull:
	default:
		return fmt.Errorf("defaults.mode is %q, want %q or %q", c.Defaults.Mode, ModeSummary, ModeFull)
	}
	if c.Defaults.Limit < 0 {
		return fmt.Errorf("defaults.limit is %d, want a positive number", c.Defaults.Limit)
	}
	for _, section := range []map[string][]string{c.Stores, c.ExtraStores} {
		for key := range section {
			if !knownStoreKey(key) {
				return fmt.Errorf("unknown store key %q (valid keys: %v)", key, StoreKeys())
			}
		}
	}
	return nil
}

func knownStoreKey(key string) bool {
	for _, def := range storeDefinitions {
		if key == def.Provider+":"+def.Kind {
			return true
		}
		if def.Primary && key == def.Provider {
			return true
		}
	}
	return false
}

// StoreKeys lists every key accepted in the config, for help and error text.
func StoreKeys() []string {
	var keys []string
	for _, def := range storeDefinitions {
		keys = append(keys, def.Provider+":"+def.Kind)
		if def.Primary {
			keys = append(keys, def.Provider)
		}
	}
	sort.Strings(keys)
	return keys
}

// candidatesFor applies this config to one store definition.
func (c Config) candidatesFor(def storeDefinition) []string {
	candidates := def.Paths
	if override, ok := lookupStore(c.Stores, def); ok {
		candidates = override
	}
	if extra, ok := lookupStore(c.ExtraStores, def); ok {
		candidates = append(append([]string{}, candidates...), extra...)
	}
	return candidates
}

// lookupStore prefers the exact "provider:kind" key; a bare "provider" key
// applies only to that provider's primary store, so overriding "devin" cannot
// accidentally repoint the Devin log directory at a database file.
func lookupStore(section map[string][]string, def storeDefinition) ([]string, bool) {
	if len(section) == 0 {
		return nil, false
	}
	if value, ok := section[def.Provider+":"+def.Kind]; ok {
		return value, true
	}
	if def.Primary {
		if value, ok := section[def.Provider]; ok {
			return value, true
		}
	}
	return nil, false
}

// StoreCandidate reports the effective candidate paths for one store and which
// of them exist, for `crossmem config`.
type StoreCandidate struct {
	Provider string   `json:"provider"`
	Kind     string   `json:"kind"`
	Key      string   `json:"key"`
	Sources  []string `json:"candidates"`
	Resolved []string `json:"resolved"`
}

// effectiveStores describes how every store resolves on this machine, after
// the user config is applied.
func (c *Client) effectiveStores() []StoreCandidate {
	out := make([]StoreCandidate, 0, len(storeDefinitions))
	for _, def := range storeDefinitions {
		out = append(out, StoreCandidate{
			Provider: def.Provider,
			Kind:     def.Kind,
			Key:      def.Provider + ":" + def.Kind,
			Sources:  c.config.candidatesFor(def),
			Resolved: c.storePaths(def.Provider, def.Kind),
		})
	}
	return out
}

// configTemplate keeps its guidance in a top-level "readme" key, which the
// parser ignores, so the file stays valid JSON and passes validation as-is.
// userDefaults exposes the configured defaults to the CLI layer.
func (c *Client) userDefaults() Defaults { return c.config.Defaults }

const configTemplate = `{
  "readme": [
    "crossmem store overrides. Keys are provider:kind (run 'crossmem config' for the full list) or a bare provider for its primary store.",
    "Values are a path string or a list of them; ~, %VAR%, $VAR and * globs are all expanded. A path whose env var is unset on this machine is skipped.",
    "'stores' replaces the built-in locations for a store; 'extraStores' keeps them and adds more.",
    "'defaults' sets what happens with no flags: mode is 'summary' or 'full', limit is a session count.",
    "'dumpDir' sets where 'crossmem export' writes and 'crossmem sync' pushes from (default ~/.assets/convdump).",
    "'sync.remote' is the rclone destination for 'crossmem sync', e.g. 'hetzbox:companydata/convdump'."
  ],
  "defaults": {
    "mode": "summary"
  },
  "stores": {
  },
  "extraStores": {
  },
  "dumpDir": "",
  "sync": {
    "remote": ""
  }
}
`

// InitConfig writes a commented template config if none exists yet.
func InitConfig() (string, bool, error) {
	path := ConfigPath()
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	if err := os.WriteFile(path, []byte(configTemplate), 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}
