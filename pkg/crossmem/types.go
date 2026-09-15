package crossmem

import "time"

// Store is one on-disk location a provider keeps sessions or logs in.
type Store struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Files    *int   `json:"files,omitempty"`
	Bytes    *int64 `json:"bytes,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Session is one conversation in one tool's store.
type Session struct {
	Provider string `json:"provider"`
	ID       string `json:"id,omitempty"`
	// Ref is the uniform handle for loading this session, regardless of how the
	// provider stores it: a transcript file path for the JSONL tools, or
	// "devin:<id>" for the SQLite-backed Devin store. Pass it to load --session.
	Ref      string    `json:"ref"`
	Path     string    `json:"path"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
	// Ago is Modified as a short relative string ("5 mins ago", "15 hours ago")
	// filled at list time so a caller can show recency without computing it.
	Ago       string `json:"ago,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Title     string `json:"title,omitempty"`
	// FirstQuestion and LastQuestion are the session's opening and closing user
	// messages. A title alone rarely says what a session became; the first and
	// last thing asked is what lets a caller tell two sessions apart and pick
	// the right one to resume.
	FirstQuestion string `json:"firstQuestion,omitempty"`
	LastQuestion  string `json:"lastQuestion,omitempty"`
	// Current marks the session this process is running inside, which must
	// never be offered as context to resume from.
	Current bool `json:"current,omitempty"`
	// Parent is the Ref of the session that spawned this one, for subagent
	// sessions (OpenCode @explore/@general). Subagents are listed only with
	// ListOptions.IncludeSubagents.
	Parent string `json:"parent,omitempty"`
	// Children are the Refs of subagent sessions this session spawned.
	Children []string `json:"children,omitempty"`
}

// ListOptions selects and shapes the sessions List returns.
type ListOptions struct {
	// Provider is one of Providers(), or "" / "all" for every tool.
	Provider string
	// CWD limits results to sessions whose working directory is this folder
	// or inside it. Empty lists every folder.
	CWD string
	// Limit caps the number of sessions returned; 0 means 50.
	Limit int
	// Full selects the larger per-session excerpt when rendering bundles.
	Full bool
	// IncludeCurrent keeps the session this process runs inside in the results.
	// Off by default: resuming your own live session returns your own context.
	IncludeCurrent bool
	// Questions populates FirstQuestion/LastQuestion. It costs one extra tail
	// read (or two indexed queries) per session, so listing asks for it and
	// bundle building does not.
	Questions bool
	// Deterministic drops generated-at timestamps so repeated runs produce
	// byte-identical output. Set by `update`, which writes files to disk.
	Deterministic bool
	// SkipLinkedWorktrees confines the folder filter to CWD itself. By default
	// a folder inside a git repository also matches the repository's other
	// checkouts — the main worktree and its linked worktrees — because an agent
	// working in a worktree still wants the context recorded in the main repo.
	SkipLinkedWorktrees bool
	// IncludeSubagents lists subagent sessions (Session.Parent set) as rows of
	// their own. Off by default: they are part of their parent's work.
	IncludeSubagents bool
}
