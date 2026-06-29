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
	Provider  string    `json:"provider"`
	ID        string    `json:"id,omitempty"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	Modified  time.Time `json:"modified"`
	Workspace string    `json:"workspace,omitempty"`
	Title     string    `json:"title,omitempty"`
}

type ListOptions struct {
	Provider string
	Folder   string
	CWD      string
	Limit    int
}
