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
}

type ListOptions struct {
	Provider string
	CWD      string
	Limit    int
	Full     bool
}
