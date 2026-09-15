package crossmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ocSession is one row of an OpenCode fixture database.
type ocSession struct {
	id, parent, directory, title string
	updated                      int64
	question, answer             string
}

// mustWriteOpenCodeDB writes an OpenCode store with the real session /
// message / part shape. withParent=false builds the schema older OpenCode
// releases shipped, before subagent sessions existed.
func mustWriteOpenCodeDB(t *testing.T, path string, withParent bool, sessions []ocSession) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	parentCol := ""
	if withParent {
		parentCol = "parent_id text,"
	}
	for _, stmt := range []string{
		`create table session (id text primary key, ` + parentCol + ` directory text not null, title text not null, time_updated integer not null)`,
		`create table message (id text primary key, session_id text not null, time_created integer not null, data text not null)`,
		`create table part (id text primary key, message_id text not null, session_id text not null, time_created integer not null, data text not null)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range sessions {
		if withParent {
			var parent any
			if s.parent != "" {
				parent = s.parent
			}
			_, err = db.Exec(`insert into session (id, parent_id, directory, title, time_updated) values (?,?,?,?,?)`, s.id, parent, s.directory, s.title, s.updated)
		} else {
			_, err = db.Exec(`insert into session (id, directory, title, time_updated) values (?,?,?,?)`, s.id, s.directory, s.title, s.updated)
		}
		if err != nil {
			t.Fatal(err)
		}
		for i, turn := range []struct{ role, text string }{{"user", s.question}, {"assistant", s.answer}} {
			if turn.text == "" {
				continue
			}
			mid := fmt.Sprintf("%s-m%d", s.id, i)
			at := s.updated + int64(i)
			if _, err := db.Exec(`insert into message values (?,?,?,?)`, mid, s.id, at, `{"role":"`+turn.role+`"}`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`insert into part values (?,?,?,?,?)`, mid+"-p", mid, s.id, at, `{"type":"text","text":"`+turn.text+`"}`); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// storesOnly returns a Config whose every store points nowhere except the given
// fixtures, so a test reads nothing from the machine it runs on.
func storesOnly(t *testing.T, fixtures map[string]string) *Config {
	t.Helper()
	nowhere := filepath.Join(t.TempDir(), "nowhere")
	stores := map[string][]string{}
	for _, key := range StoreKeys() {
		stores[key] = []string{nowhere}
	}
	for key, path := range fixtures {
		stores[key] = []string{path}
	}
	return &Config{Stores: stores}
}

func newTestClient(t *testing.T, fixtures map[string]string) *Client {
	t.Helper()
	c, err := New(Options{Config: storesOnly(t, fixtures), CurrentSessionIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientOpenCodeSubagentsHangOffTheirParent(t *testing.T) {
	work := filepath.Join(t.TempDir(), "repo")
	dbPath := filepath.Join(t.TempDir(), "opencode", "opencode.db")
	mustWriteOpenCodeDB(t, dbPath, true, []ocSession{
		{id: "ses_parent", directory: work, title: "Build the exporter", updated: 3000, question: "build the exporter", answer: "built it"},
		{id: "ses_child", parent: "ses_parent", directory: work, title: "Explore dump.go (@explore subagent)", updated: 2000, question: "scan dump.go", answer: "scanned"},
	})
	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath})
	ctx := context.Background()

	sessions, err := c.List(ctx, ListOptions{CWD: work, Provider: "opencode", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Ref != "opencode:ses_parent" {
		t.Fatalf("want only the parent session, got %#v", sessions)
	}
	if got := sessions[0].Children; len(got) != 1 || got[0] != "opencode:ses_child" {
		t.Fatalf("parent Children = %v, want [opencode:ses_child]", got)
	}

	all, err := c.List(ctx, ListOptions{CWD: work, Provider: "opencode", Limit: 5, IncludeSubagents: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].Parent != "opencode:ses_parent" {
		t.Fatalf("IncludeSubagents: want parent then child with Parent set, got %#v", all)
	}

	tr, err := c.Transcript(ctx, "opencode:ses_parent")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Events) != 2 || tr.Events[0].Role != "user" || tr.Events[0].Content != "build the exporter" || tr.Events[1].Role != "assistant" {
		t.Fatalf("transcript events = %#v", tr.Events)
	}
}

func TestClientOpenCodeSchemaWithoutParentColumn(t *testing.T) {
	work := filepath.Join(t.TempDir(), "repo")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	mustWriteOpenCodeDB(t, dbPath, false, []ocSession{{id: "ses_old", directory: work, title: "old build", updated: 10, question: "q", answer: "a"}})
	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath})
	sessions, err := c.List(context.Background(), ListOptions{CWD: work, Provider: "opencode", Limit: 5})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("pre-subagent schema must still list: %v %#v", err, sessions)
	}
}

// A folder whose sessions are older than the newest `limit` rows elsewhere must
// still list them. SQL LIMIT used to run before the folder filter and return an
// empty page.
func TestClientSQLiteFolderOlderThanLimit(t *testing.T) {
	target := filepath.Join(t.TempDir(), "old-repo")
	elsewhere := filepath.Join(t.TempDir(), "busy-repo")

	ocPath := filepath.Join(t.TempDir(), "opencode.db")
	rows := []ocSession{{id: "ses_target", directory: target, title: "old", updated: 1}}
	for i := 0; i < 20; i++ {
		rows = append(rows, ocSession{id: fmt.Sprintf("ses_busy%02d", i), directory: elsewhere, title: "busy", updated: int64(100 + i)})
	}
	mustWriteOpenCodeDB(t, ocPath, true, rows)

	devinPath := filepath.Join(t.TempDir(), "devin", "sessions.db")
	mustWriteDevinDB(t, devinPath, target, "devin-target", "old q", "old a")
	db, err := sql.Open("sqlite", devinPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`update sessions set last_activity_at = 1`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := db.Exec(`insert into sessions (id, title, working_directory, backend_type, model, agent_mode, last_activity_at, hidden) values (?, 'busy', ?, '', '', '', ?, 0)`, fmt.Sprintf("devin-busy%02d", i), elsewhere, 1000+i); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": ocPath, "devin:sqlite-sessions": devinPath})
	for _, provider := range []string{"opencode", "devin"} {
		sessions, err := c.List(context.Background(), ListOptions{CWD: target, Provider: provider, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 {
			t.Errorf("%s: folder with one old session listed %d sessions: %#v", provider, len(sessions), sessions)
		}
	}
}

// Two clients with different configs in one process must not see each other's
// stores. The package used to cache one config process-wide.
func TestClientsWithDifferentConfigsAreIsolated(t *testing.T) {
	work := filepath.Join(t.TempDir(), "repo")
	a := filepath.Join(t.TempDir(), "a.db")
	b := filepath.Join(t.TempDir(), "b.db")
	mustWriteOpenCodeDB(t, a, true, []ocSession{{id: "ses_a", directory: work, title: "A", updated: 1}})
	mustWriteOpenCodeDB(t, b, true, []ocSession{{id: "ses_b", directory: work, title: "B", updated: 1}})
	ca := newTestClient(t, map[string]string{"opencode:sqlite-sessions": a})
	cb := newTestClient(t, map[string]string{"opencode:sqlite-sessions": b})

	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 100; i++ {
		for _, pair := range []struct {
			c    *Client
			want string
		}{{ca, "opencode:ses_a"}, {cb, "opencode:ses_b"}} {
			wg.Add(1)
			go func(c *Client, want string) {
				defer wg.Done()
				s, err := c.List(context.Background(), ListOptions{CWD: work, Provider: "opencode", Limit: 5})
				if err != nil {
					errs <- err
					return
				}
				if len(s) != 1 || s[0].Ref != want {
					errs <- fmt.Errorf("want %s, got %#v", want, s)
				}
			}(pair.c, pair.want)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestClientListHonoursCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude")
	for i := 0; i < 50; i++ {
		mustWrite(t, filepath.Join(root, "-work-repo", fmt.Sprintf("s%02d.jsonl", i)), `{"type":"user","message":{"content":"hi"}}`+"\n")
	}
	c := newTestClient(t, map[string]string{"claude:jsonl-projects": root})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.List(ctx, ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("List on a canceled context = %v, want context.Canceled", err)
	}
	if _, err := c.Transcript(ctx, filepath.Join(root, "-work-repo", "s00.jsonl")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Transcript on a canceled context = %v, want context.Canceled", err)
	}
}

func TestNewRejectsUnknownStoreKey(t *testing.T) {
	_, err := New(Options{Config: &Config{Stores: map[string][]string{"notatool": {"/x"}}}})
	if err == nil || !strings.Contains(err.Error(), "unknown store key") {
		t.Fatalf("New with an unknown store key = %v", err)
	}
}

func TestClientDebugWriterIsPerClient(t *testing.T) {
	var a, b strings.Builder
	ca, _ := New(Options{Config: storesOnly(t, nil), Debug: &lockedWriter{w: &a}, CurrentSessionIDs: []string{}})
	cb, _ := New(Options{Config: storesOnly(t, nil), CurrentSessionIDs: []string{}})
	ca.log.debugf("only-a")
	cb.log.debugf("only-b")
	if !strings.Contains(a.String(), "only-a") || strings.Contains(a.String(), "only-b") || b.Len() != 0 {
		t.Fatalf("debug output leaked between clients: a=%q b=%q", a.String(), b.String())
	}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Every Ref List returns must load. A JSONL store outside ~/.claude or
// ~/.codex (a config override, $CLAUDE_CONFIG_DIR) used to list fine and then
// fail to load with "unrecognized session path".
func TestClientRefFromListLoadsFromARelocatedStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "relocated-claude-config", "projects")
	mustWrite(t, filepath.Join(root, "-work-repo", "s1.jsonl"), `{"type":"user","message":{"content":"where did I stop"}}`+"\n")
	c := newTestClient(t, map[string]string{"claude:jsonl-projects": root})
	ctx := context.Background()
	sessions, err := c.List(ctx, ListOptions{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list: %v %#v", err, sessions)
	}
	if _, err := c.Load(ctx, sessions[0].Ref, LoadFull); err != nil {
		t.Fatalf("Load(ref from List) = %v", err)
	}
	tr, err := c.Transcript(ctx, sessions[0].Ref)
	if err != nil || tr.Session.Provider != "claude" || len(tr.Events) == 0 {
		t.Fatalf("Transcript(ref from List) = %v, %#v", err, tr)
	}
}

// OpenCode and Devin record no per-turn time. A zero Time must be left out of
// the JSON rather than serialised as year 1.
func TestEventJSONOmitsUnknownTime(t *testing.T) {
	body, err := json.Marshal(Event{Role: "user", Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "time") {
		t.Fatalf("zero time serialised: %s", body)
	}
}

// A dangling transcript symlink (its target deleted) must not be listed, and a
// live one lists with its target's time and size, not the link's own.
func TestClientListFollowsTranscriptSymlinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude")
	dir := filepath.Join(root, "-work-repo")
	target := filepath.Join(dir, "real.jsonl")
	body := `{"type":"user","message":{"content":"hello"}}` + "\n"
	mustWrite(t, target, body)
	old := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.jsonl", filepath.Join(dir, "alias.jsonl")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink("deleted.jsonl", filepath.Join(dir, "dangling.jsonl")); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, map[string]string{"claude:jsonl-projects": root})
	sessions, err := c.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Session{}
	for _, s := range sessions {
		byName[filepath.Base(s.Path)] = s
	}
	if _, ok := byName["dangling.jsonl"]; ok {
		t.Error("dangling symlink listed")
	}
	alias, ok := byName["alias.jsonl"]
	if !ok {
		t.Fatalf("live symlink not listed: %#v", sessions)
	}
	if !alias.Modified.Equal(old) || alias.Bytes != int64(len(body)) {
		t.Errorf("symlink listed with its own stat: modified=%s bytes=%d", alias.Modified, alias.Bytes)
	}
	for _, s := range sessions {
		if _, err := c.Load(context.Background(), s.Ref, LoadSummary); err != nil {
			t.Errorf("listed ref does not load: %s: %v", s.Ref, err)
		}
	}
}

// Agents work in git worktrees, one per task. A worktree is a different path
// but the same repository, so a session recorded in the main checkout holds the
// context for work continuing in the worktree and must be offered there.
func TestClientListSpansGitWorktrees(t *testing.T) {
	repo := t.TempDir()
	main := filepath.Join(repo, "app")             // main checkout
	linked := filepath.Join(repo, "wt", "feature") // linked worktree
	gitDir := filepath.Join(main, ".git")
	entry := filepath.Join(gitDir, "worktrees", "feature")
	for _, dir := range []string{main, linked, entry} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// git's own layout: the worktree's .git file points at the entry, and the
	// entry points back at that .git file.
	mustWrite(t, filepath.Join(linked, ".git"), "gitdir: "+entry+"\n")
	mustWrite(t, filepath.Join(entry, "gitdir"), filepath.Join(linked, ".git")+"\n")
	mustWrite(t, filepath.Join(entry, "commondir"), "../..\n")

	claude := filepath.Join(t.TempDir(), "claude")
	mustWrite(t, filepath.Join(claude, "p1", "main.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(main)+`","message":{"content":"work in the main checkout"}}`+"\n")
	mustWrite(t, filepath.Join(claude, "p2", "wt.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(linked)+`","message":{"content":"work in the worktree"}}`+"\n")
	mustWrite(t, filepath.Join(claude, "p3", "other.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(t.TempDir())+`","message":{"content":"unrelated repo"}}`+"\n")

	c := newTestClient(t, map[string]string{"claude:jsonl-projects": claude})
	ctx := context.Background()

	for _, from := range []string{linked, main} {
		sessions, err := c.List(ctx, ListOptions{CWD: from})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 2 {
			t.Fatalf("from %s: want both checkouts' sessions, got %d: %#v", filepath.Base(from), len(sessions), refsIn(sessions))
		}
	}

	only, err := c.List(ctx, ListOptions{CWD: linked, SkipLinkedWorktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || !strings.Contains(only[0].Path, "wt.jsonl") {
		t.Fatalf("--no-worktrees should confine to the worktree, got %#v", refsIn(only))
	}

	// A plain directory that is not a git repository keeps the old behaviour.
	plain := t.TempDir()
	mustWrite(t, filepath.Join(claude, "p4", "plain.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(plain)+`","message":{"content":"no repo here"}}`+"\n")
	got, err := c.List(ctx, ListOptions{CWD: plain})
	if err != nil || len(got) != 1 {
		t.Fatalf("non-repo folder: %v %#v", err, refsIn(got))
	}
}

func refsIn(sessions []Session) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, filepath.Base(s.Path)+" @ "+s.Workspace)
	}
	return out
}
