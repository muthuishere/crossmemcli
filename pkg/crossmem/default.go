package crossmem

// Package-level functions run on the default client (see defaultClient): config
// from $CROSSMEM_CONFIG or the default path, current sessions and debug output
// from the environment. Code embedding crossmem should prefer New.

// ListSessions lists sessions across every store on the default client.
func ListSessions(opts ListOptions) ([]Session, error) { return defaultClient().listSessions(opts) }

// BuildContext renders a bundle of the most recent sessions matching the folder.
func BuildContext(opts ListOptions) (string, error) { return defaultClient().buildContext(opts) }

// BuildSessionContext renders a bundle for one session identified by its Ref.
func BuildSessionContext(ref string, cwd string, full bool) (string, error) {
	return defaultClient().buildSessionContext(ref, cwd, full)
}

// UpdateContext writes <folder>/.crossmem/ for the sessions matching opts.
func UpdateContext(opts ListOptions) (UpdateResult, error) {
	return defaultClient().updateContext(opts)
}

// DiscoverStores reports every known store location and whether it exists.
func DiscoverStores() ([]Store, error) { return defaultClient().discoverStores() }

// EffectiveStores reports the candidate paths for every store.
func EffectiveStores() []StoreCandidate { return defaultClient().effectiveStores() }

// UserDefaults returns the defaults block of the user config.
func UserDefaults() Defaults { return defaultClient().userDefaults() }

// ExportConversations writes every session's Q&A to one qa.jsonl.
func ExportConversations(opts ConvExportOptions) (ConvExportResult, error) {
	return defaultClient().exportConversations(opts)
}

// ImportConversations merges a qa.jsonl into a folder's .crossmem/.
func ImportConversations(opts ConvImportOptions) (ConvImportResult, error) {
	return defaultClient().importConversations(opts)
}

// DefaultDumpDir is the dump directory from the config, else ~/.assets/convdump.
func DefaultDumpDir() string { return defaultClient().defaultDumpDir() }

// ExportDump mirrors every store into one dump directory.
func ExportDump(opts ExportOptions) (DumpManifest, error) { return defaultClient().exportDump(opts) }

// ImportDump restores a dump directory into this machine's stores.
func ImportDump(opts ImportOptions) (ImportResult, error) { return defaultClient().importDump(opts) }

// SyncDump pushes or pulls the dump directory with rclone.
func SyncDump(opts SyncOptions) (SyncResult, error) { return defaultClient().syncDump(opts) }
