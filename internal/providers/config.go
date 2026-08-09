package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

// Config is the optional user override file. crossmem ships a candidate list
// per store for macOS, Linux, and Windows, but agent tools move their stores
// and people relocate them (a portable install, a synced drive, WSL). The
// config lets any store be repointed without a new release.
//
// Location: ~/.config/crossmemcli/config.json, or $CROSSMEM_CONFIG.
//
//	{
//	  "stores": {
//	    "devin:sqlite-sessions": "%APPDATA%/Cognition/cli/sessions.db",
//	    "claude": ["D:/agents/.claude/projects"]
//	  },
//	  "extraStores": {
//	    "opencode": "~/work/opencode/opencode*.db"
//	  }
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
	// Exists reports whether that file is present.
	Exists bool `json:"exists"`
	// Stores replaces the built-in candidates for a store, keyed by
	// "provider:kind" or a bare "provider".
	Stores map[string][]string `json:"stores,omitempty"`
	// ExtraStores appends to the built-in candidates, same key form.
	ExtraStores map[string][]string `json:"extraStores,omitempty"`
}

// configFile is the on-disk shape; pathList accepts a string or a list.
type configFile struct {
	Stores      map[string]pathList `json:"stores"`
	ExtraStores map[string]pathList `json:"extraStores"`
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
	config.Stores = toStringMap(file.Stores)
	config.ExtraStores = toStringMap(file.ExtraStores)
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

var (
	configMu     sync.Mutex
	configLoaded bool
	configCached Config
)

// userConfig returns the process-wide config, read once. A broken config must
// not take the whole CLI down — it is reported to the debug log and ignored,
// leaving the built-in candidates in force. `crossmem config` surfaces the
// error properly.
func userConfig() Config {
	configMu.Lock()
	defer configMu.Unlock()
	if !configLoaded {
		config, err := LoadConfig()
		if err != nil {
			diag.Debugf("config load err=%q", err)
			config = Config{Path: config.Path, Exists: config.Exists}
		}
		configCached = config
		configLoaded = true
	}
	return configCached
}

// resetConfig drops the cached config so the next read picks the file up again.
func resetConfig() {
	configMu.Lock()
	configLoaded = false
	configCached = Config{}
	configMu.Unlock()
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

// EffectiveStores describes how every store resolves on this machine, after
// the user config is applied.
func EffectiveStores() []StoreCandidate {
	out := make([]StoreCandidate, 0, len(storeDefinitions))
	for _, def := range storeDefinitions {
		out = append(out, StoreCandidate{
			Provider: def.Provider,
			Kind:     def.Kind,
			Key:      def.Provider + ":" + def.Kind,
			Sources:  userConfig().candidatesFor(def),
			Resolved: storePaths(def.Provider, def.Kind),
		})
	}
	return out
}

// configTemplate keeps its guidance in a top-level "readme" key, which the
// parser ignores, so the file stays valid JSON and passes validation as-is.
const configTemplate = `{
  "readme": [
    "crossmem store overrides. Keys are provider:kind (run 'crossmem config' for the full list) or a bare provider for its primary store.",
    "Values are a path string or a list of them; ~, %VAR%, $VAR and * globs are all expanded. A path whose env var is unset on this machine is skipped.",
    "'stores' replaces the built-in locations for a store; 'extraStores' keeps them and adds more."
  ],
  "stores": {
  },
  "extraStores": {
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
