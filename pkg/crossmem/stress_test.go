//go:build stress

// Stress tests for the library surface (ADR 3). They build large synthetic
// stores, so they are behind a build tag:
//
//	go test -tags stress -race -run Stress -v ./pkg/crossmem
package crossmem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// folderNames deliberately include the characters that break naive SQL
// matching (LIKE wildcards _ and %, quotes) and path edge cases.
var folderNames = []string{
	"plain", "my_repo", "100%done", "it's-quoted", "with space", "ünïcödé", "a", "a-b", "a_b", "nested",
}

type fixtureSession struct {
	provider string
	ref      string
	folder   string
	updated  int64
	parent   string
}

// buildOpenCodeStore writes n OpenCode sessions spread over folders (and their
// child directories), with a share of subagent children. It returns every row.
func buildOpenCodeStore(t *testing.T, dbPath string, root string, n int, rng *rand.Rand) []fixtureSession {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`pragma journal_mode=wal`,
		`create table session (id text primary key, parent_id text, directory text not null, title text not null, time_updated integer not null)`,
		`create table message (id text primary key, session_id text not null, time_created integer not null, data text not null)`,
		`create table part (id text primary key, message_id text not null, session_id text not null, time_created integer not null, data text not null)`,
		`create index part_session_idx on part (session_id)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var out []fixtureSession
	var parents []fixtureSession
	for i := 0; i < n; i++ {
		folder := filepath.Join(root, folderNames[rng.Intn(len(folderNames))])
		if rng.Intn(4) == 0 {
			folder = filepath.Join(folder, "child", "deeper")
		}
		s := fixtureSession{provider: "opencode", ref: fmt.Sprintf("opencode:ses_%06d", i), folder: folder, updated: int64(rng.Intn(1_000_000_000))}
		var parent any
		if len(parents) > 0 && rng.Intn(5) == 0 {
			p := parents[rng.Intn(len(parents))]
			s.parent = p.ref
			s.folder = p.folder
			parent = strings.TrimPrefix(p.ref, "opencode:")
		} else {
			parents = append(parents, s)
		}
		id := strings.TrimPrefix(s.ref, "opencode:")
		if _, err := tx.Exec(`insert into session values (?,?,?,?,?)`, id, parent, s.folder, "title "+id, s.updated); err != nil {
			t.Fatal(err)
		}
		for m := 0; m < 4; m++ {
			mid := fmt.Sprintf("%s-m%d", id, m)
			role := []string{"user", "assistant"}[m%2]
			if _, err := tx.Exec(`insert into message values (?,?,?,?)`, mid, id, s.updated+int64(m), `{"role":"`+role+`"}`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`insert into part values (?,?,?,?,?)`, mid+"-p", mid, id, s.updated+int64(m), `{"type":"text","text":"turn `+fmt.Sprint(m)+` of `+id+`"}`); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, s)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

// buildClaudeStore writes n Claude transcripts, each recording its cwd.
func buildClaudeStore(t *testing.T, projects string, root string, n int, rng *rand.Rand) []fixtureSession {
	t.Helper()
	var out []fixtureSession
	for i := 0; i < n; i++ {
		folder := filepath.Join(root, folderNames[rng.Intn(len(folderNames))])
		if rng.Intn(4) == 0 {
			folder = filepath.Join(folder, "child")
		}
		path := filepath.Join(projects, fmt.Sprintf("proj%03d", i%97), fmt.Sprintf("sess-%06d.jsonl", i))
		body := `{"type":"user","cwd":"` + jsonEscape(folder) + `","message":{"content":[{"type":"text","text":"question ` + fmt.Sprint(i) + `"}]}}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"text","text":"answer ` + fmt.Sprint(i) + `"}]}}` + "\n"
		mustWrite(t, path, body)
		mod := time.Unix(int64(1_600_000_000+rng.Intn(100_000_000)), 0)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
		out = append(out, fixtureSession{provider: "claude", ref: path, folder: folder, updated: mod.Unix()})
	}
	return out
}

// oracle is the brute-force answer List must agree with.
func oracle(rows []fixtureSession, provider string, folder string, limit int, includeSubagents bool) []string {
	var hits []fixtureSession
	for _, r := range rows {
		if provider != "all" && r.provider != provider {
			continue
		}
		if r.parent != "" && !includeSubagents {
			continue
		}
		if folder != "" && !sameOrChild(r.folder, folder) {
			continue
		}
		hits = append(hits, r)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].updated > hits[j].updated })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	refs := make([]string, len(hits))
	for i, h := range hits {
		refs[i] = h.ref
	}
	sort.Strings(refs)
	return refs
}

func refsOf(sessions []Session) []string {
	refs := make([]string, len(sessions))
	for i, s := range sessions {
		refs[i] = s.Ref
	}
	sort.Strings(refs)
	return refs
}

// S1 — List agrees with a brute-force oracle over random folders, limits,
// providers, and subagent settings, on paths full of SQL-hostile characters.
func TestStressListMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20260914))
	root := filepath.Join(t.TempDir(), "work")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	projects := filepath.Join(t.TempDir(), "claude")
	oc := buildOpenCodeStore(t, dbPath, root, 3000, rng)
	cl := buildClaudeStore(t, projects, root, 1500, rng)
	rows := append(append([]fixtureSession{}, oc...), cl...)
	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects})

	queries := 0
	for i := 0; i < 300; i++ {
		provider := []string{"all", "opencode", "claude"}[rng.Intn(3)]
		folder := ""
		if rng.Intn(6) != 0 {
			folder = filepath.Join(root, folderNames[rng.Intn(len(folderNames))])
			if rng.Intn(3) == 0 {
				folder = filepath.Join(folder, "child")
			}
		}
		limit := 1 + rng.Intn(40)
		sub := rng.Intn(2) == 0
		got, err := c.List(context.Background(), ListOptions{Provider: provider, CWD: folder, Limit: limit, IncludeSubagents: sub})
		if err != nil {
			t.Fatal(err)
		}
		want := oracle(rows, provider, folder, limit, sub)
		if strings.Join(refsOf(got), "\n") != strings.Join(want, "\n") {
			t.Fatalf("query %d provider=%s folder=%q limit=%d subagents=%v:\n got %d refs\nwant %d refs", i, provider, folder, limit, sub, len(got), len(want))
		}
		queries++
	}
	t.Logf("%d random queries over %d OpenCode + %d Claude sessions matched the oracle", queries, len(oc), len(cl))
}

// S2 — many clients with different configs hammered concurrently never see
// each other's stores, under -race.
func TestStressConcurrentClientsStayIsolated(t *testing.T) {
	const clients = 16
	type fixture struct {
		c      *Client
		folder string
		refs   map[string]bool
	}
	var fixtures []fixture
	for i := 0; i < clients; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		root := filepath.Join(t.TempDir(), fmt.Sprintf("client%02d", i))
		dbPath := filepath.Join(t.TempDir(), "opencode.db")
		projects := filepath.Join(t.TempDir(), "claude")
		rows := append(buildOpenCodeStore(t, dbPath, root, 60, rng), buildClaudeStore(t, projects, root, 30, rng)...)
		refs := map[string]bool{}
		for _, r := range rows {
			refs[r.ref] = true
		}
		fixtures = append(fixtures, fixture{
			c:      newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects}),
			folder: root,
			refs:   refs,
		})
	}

	var calls, failures atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	start := time.Now()
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(1000 + g)))
			for i := 0; i < 60; i++ {
				f := fixtures[rng.Intn(clients)]
				ctx := context.Background()
				sessions, err := f.c.List(ctx, ListOptions{CWD: f.folder, Limit: 20, IncludeSubagents: true, Questions: i%3 == 0})
				calls.Add(1)
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err)
					continue
				}
				for _, s := range sessions {
					if !f.refs[s.Ref] {
						failures.Add(1)
						firstErr.CompareAndSwap(nil, fmt.Errorf("client leaked foreign session %s", s.Ref))
					}
				}
				if len(sessions) == 0 {
					continue
				}
				s := sessions[rng.Intn(len(sessions))]
				if _, err := f.c.Transcript(ctx, s.Ref); err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Errorf("transcript %s: %w", s.Ref, err))
				}
				if _, err := f.c.Load(ctx, s.Ref, LoadFull); err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Errorf("load %s: %w", s.Ref, err))
				}
				calls.Add(2)
			}
		}(g)
	}
	wg.Wait()
	if failures.Load() > 0 {
		t.Fatalf("%d/%d calls failed; first: %v", failures.Load(), calls.Load(), firstErr.Load())
	}
	t.Logf("%d concurrent calls across %d clients / 64 goroutines in %s, 0 leaks, 0 errors", calls.Load(), clients, time.Since(start).Round(time.Millisecond))
}

// S3 — readers keep working while agents write: a writer appends to a live
// JSONL transcript and inserts OpenCode rows in WAL mode the whole time.
func TestStressReadWhileAgentsWrite(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	root := filepath.Join(t.TempDir(), "work")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	projects := filepath.Join(t.TempDir(), "claude")
	buildOpenCodeStore(t, dbPath, root, 200, rng)
	buildClaudeStore(t, projects, root, 50, rng)
	live := filepath.Join(projects, "live", "live.jsonl")
	mustWrite(t, live, `{"type":"user","cwd":"`+jsonEscape(root)+`","message":{"content":"start"}}`+"\n")
	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects})

	ctx, stop := context.WithTimeout(context.Background(), 4*time.Second)
	defer stop()
	var writes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the agent
		defer wg.Done()
		db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Error(err)
			return
		}
		defer db.Close()
		f, err := os.OpenFile(live, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		for i := 0; ctx.Err() == nil; i++ {
			fmt.Fprintf(f, `{"type":"assistant","message":{"content":[{"type":"text","text":"streamed %d"}]}}`+"\n", i)
			// a half-written line, as a crash or a mid-flush read would see
			if i%50 == 0 {
				f.WriteString(`{"type":"assistant","message":{"con`)
				f.WriteString("\n")
			}
			if _, err := db.Exec(`insert into session values (?,?,?,?,?)`, fmt.Sprintf("ses_live%06d", i), nil, root, "live", 2_000_000_000+i); err != nil {
				t.Error(err)
				return
			}
			writes.Add(1)
		}
	}()

	var reads, errs atomic.Int64
	var firstErr atomic.Value
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				s, err := c.List(context.Background(), ListOptions{CWD: root, Limit: 10})
				if err == nil {
					_, err = c.Transcript(context.Background(), live)
				}
				reads.Add(1)
				if err != nil {
					errs.Add(1)
					firstErr.CompareAndSwap(nil, err)
				}
				_ = s
			}
		}()
	}
	wg.Wait()
	if errs.Load() > 0 {
		t.Fatalf("%d/%d reads failed while writing; first: %v", errs.Load(), reads.Load(), firstErr.Load())
	}
	tr, err := c.Transcript(context.Background(), live)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d writer iterations, %d concurrent read rounds, 0 errors; live transcript now %d events (torn lines skipped)", writes.Load(), reads.Load(), len(tr.Events))
}

// S4 — a store held under an exclusive write lock longer than busy_timeout plus
// every retry must degrade (that provider skipped), never hang or fail List.
func TestStressExclusiveLockDegradesGracefully(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	root := filepath.Join(t.TempDir(), "work")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	projects := filepath.Join(t.TempDir(), "claude")
	buildOpenCodeStore(t, dbPath, root, 50, rng)
	buildClaudeStore(t, projects, root, 20, rng)

	// Rollback journal + an exclusive lock is the worst case: readers are
	// blocked outright, unlike WAL.
	locker, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)
	if _, err := locker.Exec(`pragma journal_mode=delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := locker.Exec(`pragma locking_mode=exclusive`); err != nil {
		t.Fatal(err)
	}
	if _, err := locker.Exec(`begin exclusive`); err != nil {
		t.Fatal(err)
	}
	defer locker.Exec(`rollback`)

	var debug strings.Builder
	c, err := New(Options{Config: storesOnly(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects}), CurrentSessionIDs: []string{}, Debug: &lockedWriter{w: &debug}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sessions, err := c.List(context.Background(), ListOptions{CWD: root, Limit: 100})
	took := time.Since(start)
	if err != nil {
		t.Fatalf("List failed outright under a locked store: %v", err)
	}
	providers := map[string]int{}
	for _, s := range sessions {
		providers[s.Provider]++
	}
	if providers["claude"] == 0 {
		t.Fatal("the unlocked Claude store was not listed")
	}
	if took > 5*time.Second {
		t.Fatalf("List took %s under a locked store", took)
	}
	t.Logf("locked OpenCode store: List returned in %s with %v (debug log: %q)", took.Round(time.Millisecond), providers, firstLine(debug.String(), "opencode"))
}

func firstLine(text string, containing string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, containing) {
			if len(line) > 160 {
				return line[:160] + "…"
			}
			return line
		}
	}
	return ""
}

// S5 — scale: latency and memory at a size well past a heavy real user.
func TestStressScale(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	root := filepath.Join(t.TempDir(), "work")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	projects := filepath.Join(t.TempDir(), "claude")
	buildStart := time.Now()
	buildOpenCodeStore(t, dbPath, root, 20000, rng)
	buildClaudeStore(t, projects, root, 5000, rng)
	t.Logf("built 20,000 OpenCode sessions (80,000 parts) + 5,000 Claude transcripts in %s", time.Since(buildStart).Round(time.Millisecond))

	// one very large transcript: ~60 MB, 60,000 turns
	huge := filepath.Join(projects, "huge", "huge.jsonl")
	var b strings.Builder
	pad := strings.Repeat("x", 1000)
	for i := 0; i < 60000; i++ {
		fmt.Fprintf(&b, `{"type":"user","cwd":"`+jsonEscape(root)+`","message":{"content":[{"type":"text","text":"turn %d %s"}]}}`+"\n", i, pad)
	}
	mustWrite(t, huge, b.String())
	b.Reset()

	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects})
	ctx := context.Background()
	measure := func(label string, fn func() error) {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		took := time.Since(start)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		t.Logf("%-44s %9s  alloc %6.1f MB", label, took.Round(time.Millisecond), float64(after.TotalAlloc-before.TotalAlloc)/1e6)
	}
	measure("List all, limit 50", func() error { _, err := c.List(ctx, ListOptions{Limit: 50}); return err })
	measure("List folder, limit 5", func() error {
		_, err := c.List(ctx, ListOptions{CWD: filepath.Join(root, "my_repo"), Limit: 5})
		return err
	})
	measure("List folder, limit 5, with questions", func() error {
		_, err := c.List(ctx, ListOptions{CWD: filepath.Join(root, "my_repo"), Limit: 5, Questions: true})
		return err
	})
	measure("List opencode, rare folder (full stream)", func() error {
		_, err := c.List(ctx, ListOptions{Provider: "opencode", CWD: filepath.Join(root, "no-such-folder"), Limit: 5})
		return err
	})
	var events int
	measure("Transcript of a 60 MB / 60k-turn session", func() error {
		tr, err := c.Transcript(ctx, huge)
		events = len(tr.Events)
		return err
	})
	var bundle int
	measure("Load full of the same session", func() error {
		md, err := c.Load(ctx, huge, LoadFull)
		bundle = len(md)
		return err
	})
	t.Logf("huge session: %d events, full bundle %d bytes", events, bundle)
	if bundle > fullPreviewChars+4096 {
		t.Errorf("full bundle %d bytes exceeds the preview budget", bundle)
	}

	// S6 — cancellation latency on the same store
	ctx2, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := c.List(ctx2, ListOptions{Limit: 50, Questions: true})
	took := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("List canceled after 10ms returned %v", err)
	}
	t.Logf("List canceled after 10ms returned context.Canceled in %s", took.Round(time.Millisecond))
	if took > 2*time.Second {
		t.Errorf("cancellation took %s", took)
	}
}

// S7 — no goroutine or file-descriptor leak across many calls, including
// calls that break out of SQL rows early and calls that are canceled.
func TestStressNoLeaks(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	root := filepath.Join(t.TempDir(), "work")
	dbPath := filepath.Join(t.TempDir(), "opencode.db")
	projects := filepath.Join(t.TempDir(), "claude")
	buildOpenCodeStore(t, dbPath, root, 500, rng)
	buildClaudeStore(t, projects, root, 200, rng)
	c := newTestClient(t, map[string]string{"opencode:sqlite-sessions": dbPath, "claude:jsonl-projects": projects})

	warm := func() {
		_, _ = c.List(context.Background(), ListOptions{CWD: root, Limit: 3})
	}
	warm()
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	goroutines := runtime.NumGoroutine()
	fds := openFDs()

	for i := 0; i < 1000; i++ {
		ctx := context.Background()
		var cancel context.CancelFunc = func() {}
		if i%4 == 0 {
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		folder := filepath.Join(root, folderNames[i%len(folderNames)])
		sessions, _ := c.List(ctx, ListOptions{CWD: folder, Limit: 1 + i%3, Questions: i%5 == 0})
		if len(sessions) > 0 {
			_, _ = c.Transcript(ctx, sessions[0].Ref)
		}
		cancel()
	}
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	gAfter, fdAfter := runtime.NumGoroutine(), openFDs()
	t.Logf("after 1000 mixed/canceled calls: goroutines %d → %d, open fds %d → %d", goroutines, gAfter, fds, fdAfter)
	if gAfter > goroutines+2 {
		t.Errorf("goroutine leak: %d → %d", goroutines, gAfter)
	}
	if fds < 0 {
		t.Fatal("could not count open file descriptors on this OS")
	}
	if fdAfter > fds+4 {
		t.Errorf("file descriptor leak: %d → %d", fds, fdAfter)
	}
}

func openFDs() int {
	if entries, err := os.ReadDir("/proc/self/fd"); err == nil {
		return len(entries)
	}
	// macOS: /dev/fd cannot be listed reliably; ask lsof about this process.
	out, err := exec.Command("lsof", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return -1
	}
	return strings.Count(string(out), "\n") - 1
}

// S8 — fuzzed inputs must never panic and fitPreview must respect its budget.
func FuzzFitPreviewBudget(f *testing.F) {
	f.Add("user: hello", 3, 200)
	f.Add(strings.Repeat("é", 5000), 40, 1000)
	f.Fuzz(func(t *testing.T, chunk string, count int, max int) {
		if count < 0 || count > 500 || max < 0 || max > 50000 {
			return
		}
		chunks := make([]string, count)
		for i := range chunks {
			chunks[i] = chunk
		}
		out := fitPreview(chunks, "\n\n", max)
		// truncate appends "..." when it has to hard-cut
		if len(out) > max+3 {
			t.Fatalf("fitPreview(%d chunks of %d bytes, max %d) = %d bytes", count, len(chunk), max, len(out))
		}
	})
}

func FuzzResolveRef(f *testing.F) {
	for _, seed := range []string{"devin:", "opencode:x", "copilot-cli:../../etc", "", "~", "/", "\x00", "devin:' or 1=1 --"} {
		f.Add(seed)
	}
	c, err := New(Options{Config: &Config{Stores: map[string][]string{}}, CurrentSessionIDs: []string{}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		_, _ = c.Transcript(context.Background(), ref)
		_, _ = c.Load(context.Background(), ref, LoadSummary)
	})
}
