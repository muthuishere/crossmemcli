// Package crossmem reads the local session stores of agent tools — Claude
// Code, Codex, Devin, Copilot (VS Code and CLI), and OpenCode — so one agent
// can see what another did in a folder.
//
// Construct a Client and ask it for sessions:
//
//	c, err := crossmem.New(crossmem.Options{})
//	sessions, err := c.List(ctx, crossmem.ListOptions{CWD: root, Limit: 5})
//	tr, err := c.Transcript(ctx, sessions[0].Ref) // typed events
//	md, err := c.Load(ctx, sessions[0].Ref, crossmem.LoadFull)
//	rules, err := c.Guardrails(root)
//
// A Client holds its own config, current-session ids, and debug writer, so
// several can run in one process. Every read is read-only; credential files,
// auth databases, and env files are never opened.
//
// crossmem does not summarise or judge what it returns. Repo instruction files
// (Guardrails) are authoritative; session transcripts are context only.
//
// # Compatibility
//
// The package follows semantic versioning from v0.2.0. The JSON field names of
// Session, Store, QARecord, Transcript, and Event are frozen: fields may be
// added, never renamed or removed.
package crossmem
