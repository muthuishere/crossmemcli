package providers

import "time"

type Store struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Files    *int   `json:"files,omitempty"`
	Bytes    *int64 `json:"bytes,omitempty"`
	Note     string `json:"note,omitempty"`
}

type Session struct {
	Provider string `json:"provider"`
	ID       string `json:"id,omitempty"`
	// Ref is the uniform handle for loading this session, regardless of how the
	// provider stores it: a transcript file path for the JSONL tools, or
	// "devin:<id>" for the SQLite-backed Devin store. Pass it to load --session.
	Ref       string    `json:"ref"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	Modified  time.Time `json:"modified"`
	Workspace string    `json:"workspace,omitempty"`
	Title     string    `json:"title,omitempty"`
	// FirstQuestion and LastQuestion are the session's opening and closing user
	// messages. A title alone rarely says what a session became; the first and
	// last thing asked is what lets a caller tell two sessions apart and pick
	// the right one to resume.
	FirstQuestion string `json:"firstQuestion,omitempty"`
	LastQuestion  string `json:"lastQuestion,omitempty"`
	// Current marks the session this process is running inside, which must
	// never be offered as context to resume from.
	Current bool `json:"current,omitempty"`
}

type ListOptions struct {
	Provider string
	CWD      string
	Limit    int
	Full     bool
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
}
