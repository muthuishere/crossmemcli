package crossmem

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// resetConfigForTest clears the process-wide config cache before and after a
// test, so tests that point CROSSMEM_CONFIG at a fixture do not leak into each
// other or into the rest of the suite.
func resetConfigForTest(t *testing.T) {
	t.Helper()
	resetConfig()
	t.Cleanup(resetConfig)
}

func TestLoadConfigMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("CROSSMEM_CONFIG", filepath.Join(t.TempDir(), "absent.json"))
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if config.Exists {
		t.Fatal("Exists should be false for a missing file")
	}
}

// A value may be a single path string or a list of them — people write both.
func TestLoadConfigAcceptsStringOrList(t *testing.T) {
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{
	  "stores": {"devin": "D:/agents/devin/cli/sessions.db"},
	  "extraStores": {"claude:jsonl-projects": ["~/one", "~/two"]}
	}`))
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"D:/agents/devin/cli/sessions.db"}; !reflect.DeepEqual(config.Stores["devin"], want) {
		t.Fatalf("stores = %v, want %v", config.Stores["devin"], want)
	}
	if want := []string{"~/one", "~/two"}; !reflect.DeepEqual(config.ExtraStores["claude:jsonl-projects"], want) {
		t.Fatalf("extraStores = %v, want %v", config.ExtraStores["claude:jsonl-projects"], want)
	}
}

// A typo in a key must be reported, not silently ignored — a config that does
// nothing is worse than one that refuses to load.
func TestLoadConfigRejectsUnknownKey(t *testing.T) {
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"stores":{"devon":"/x"}}`))
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "unknown store key") {
		t.Fatalf("expected an unknown-key error, got %v", err)
	}
}

func TestCandidatesForOverrideAndAppend(t *testing.T) {
	devin, _ := storeDefinitionFor("devin", "sqlite-sessions")
	logs, _ := storeDefinitionFor("devin", "cli-logs")

	replace := Config{Stores: map[string][]string{"devin": {"/custom/sessions.db"}}}
	if got := replace.candidatesFor(devin); !reflect.DeepEqual(got, []string{"/custom/sessions.db"}) {
		t.Fatalf("override = %v, want the single custom path", got)
	}
	// A bare provider key addresses only the provider's primary store, so it
	// cannot accidentally repoint the Devin log directory at a database file.
	if got := replace.candidatesFor(logs); !reflect.DeepEqual(got, logs.Paths) {
		t.Fatalf("bare provider key leaked into cli-logs: %v", got)
	}

	appendCfg := Config{ExtraStores: map[string][]string{"devin:sqlite-sessions": {"/extra.db"}}}
	got := appendCfg.candidatesFor(devin)
	if len(got) != len(devin.Paths)+1 || got[len(got)-1] != "/extra.db" {
		t.Fatalf("extraStores should append to the built-ins, got %v", got)
	}
}

// The end-to-end path: a config file repoints the Devin store, and the
// resolver returns that file. This is the Windows fix in miniature.
func TestConfigOverrideRepointsDevinStore(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "sessions.db")
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"stores":{"devin:sqlite-sessions":"`+filepath.ToSlash(db)+`"}}`))
	resetConfigForTest(t)

	if got := devinDB(); got != db {
		t.Fatalf("devinDB() = %q, want %q", got, db)
	}
}

// A standing preference for full excerpts belongs in the config, not in every
// command line.
func TestDefaultsModeAndValidation(t *testing.T) {
	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"defaults":{"mode":"full","limit":3}}`))
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Defaults.Full() || config.Defaults.Limit != 3 {
		t.Fatalf("defaults = %+v", config.Defaults)
	}

	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{"defaults":{"mode":"verbose"}}`))
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "defaults.mode") {
		t.Fatalf("an unknown mode must be rejected, got %v", err)
	}

	t.Setenv("CROSSMEM_CONFIG", writeConfig(t, `{}`))
	config, err = LoadConfig()
	if err != nil || config.Defaults.Full() {
		t.Fatalf("summary must remain the built-in default: %+v %v", config.Defaults, err)
	}
}
