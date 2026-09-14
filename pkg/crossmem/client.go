package crossmem

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Options configures a Client. The zero value reproduces the CLI: config from
// $CROSSMEM_CONFIG or the default path, the caller's live session detected from
// the agent environment variables, and no debug output.
type Options struct {
	// Config is the store configuration the client resolves paths with. nil
	// loads it with LoadConfig. Either way an invalid config (an unknown store
	// key) is an error from New rather than silently ignored.
	Config *Config
	// CurrentSessionIDs are the sessions the caller is running inside, which are
	// excluded from List unless ListOptions.IncludeCurrent is set. nil detects
	// them from the environment (CLAUDE_CODE_SESSION_ID, DEVIN_SESSION_ID,
	// CODEX_THREAD_ID, CROSSMEM_CURRENT_SESSION); a non-nil empty slice disables
	// the exclusion.
	CurrentSessionIDs []string
	// Debug receives diagnostic lines. nil discards them. Transcript contents
	// are never written to it.
	Debug io.Writer
}

// Client reads local agent session stores. It holds no process-global state:
// two clients with different configs can run side by side in one process. A
// Client is safe for concurrent use.
type Client struct {
	config     Config
	currentIDs map[string]bool
	log        *logger
	// ctx is set only on the short-lived copy a public method makes for one
	// call (see with); it is never stored on a client a caller holds.
	ctx context.Context
}

// New builds a Client from opts.
func New(opts Options) (*Client, error) {
	c := &Client{log: newLogger(opts.Debug)}
	if opts.Config != nil {
		if err := opts.Config.validate(); err != nil {
			return nil, err
		}
		c.config = *opts.Config
	} else {
		config, err := LoadConfig()
		if err != nil {
			return nil, err
		}
		c.config = config
	}
	if opts.CurrentSessionIDs != nil {
		c.currentIDs = map[string]bool{}
		for _, id := range opts.CurrentSessionIDs {
			if id = strings.TrimSpace(id); id != "" {
				c.currentIDs[id] = true
			}
		}
	} else {
		c.currentIDs = envCurrentSessionIDs()
	}
	return c, nil
}

// LoadMode selects how much of a session Load renders.
type LoadMode int

const (
	// LoadSummary renders the default, smaller per-session excerpt.
	LoadSummary LoadMode = iota
	// LoadFull renders the larger excerpt used by `crossmem load --full`.
	LoadFull
)

// List returns sessions across every store, newest first. See ListOptions.
func (c *Client) List(ctx context.Context, opts ListOptions) ([]Session, error) {
	return c.with(ctx).listSessions(opts)
}

// Load renders one session, identified by the Ref from List, as the Markdown
// context bundle `crossmem load --session` prints.
func (c *Client) Load(ctx context.Context, ref string, mode LoadMode) (string, error) {
	cwd, _ := os.Getwd()
	return c.with(ctx).buildSessionContext(ref, cwd, mode == LoadFull)
}

// Transcript returns one session's conversation as typed events, in order.
// crossmem does not summarise or filter it: deciding what is signal is the
// caller's job.
func (c *Client) Transcript(ctx context.Context, ref string) (Transcript, error) {
	cc := c.with(ctx)
	if err := cc.canceled(); err != nil {
		return Transcript{}, err
	}
	session, err := cc.resolveRef(ref)
	if err != nil {
		return Transcript{}, err
	}
	raw := cc.sessionEvents(session)
	if err := cc.canceled(); err != nil {
		return Transcript{}, err
	}
	events := make([]Event, 0, len(raw))
	for _, e := range raw {
		events = append(events, Event{Role: e.kind, Name: e.name, Content: e.text, Time: e.at})
	}
	return Transcript{Session: session, Events: events}, nil
}

// Guardrails returns the repo instruction files an agent must follow for root.
// They are authoritative; session transcripts are context only.
func (c *Client) Guardrails(root string) ([]GuardrailFile, error) {
	return ReadGuardrails(root)
}

// Scan reports every known store location and whether it exists, without
// reading transcript contents.
func (c *Client) Scan() ([]Store, error) {
	return c.discoverStores()
}

// Stores reports, for every store, the candidate paths this client looks in.
func (c *Client) Stores() []StoreCandidate {
	return c.effectiveStores()
}

// Transcript is one session and its conversation.
type Transcript struct {
	Session Session `json:"session"`
	Events  []Event `json:"events"`
}

// Event is one turn of a conversation. Role is "user", "assistant", "tool", or
// "thinking"; Name carries the tool name on tool events. There are no model,
// agent, or token fields.
type Event struct {
	Role    string    `json:"role"`
	Name    string    `json:"name,omitempty"`
	Content string    `json:"content"`
	Time    time.Time `json:"time,omitzero"`
}

// with returns a copy of c bound to ctx for the duration of one call.
func (c *Client) with(ctx context.Context) *Client {
	if ctx == nil {
		ctx = context.Background()
	}
	cp := *c
	cp.ctx = ctx
	return &cp
}

// canceled reports the bound context's error, if any.
func (c *Client) canceled() error {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Err()
}

// context returns the bound context, or Background for package-level calls.
func (c *Client) context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// logger is a client-scoped debug sink, safe for concurrent use.
type logger struct {
	mu sync.Mutex
	w  io.Writer
}

func newLogger(w io.Writer) *logger {
	return &logger{w: w}
}

func (l *logger) debugf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "crossmem debug ts=%s %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// The package-level functions predate Client and are what the CLI calls. They
// run on a default client built from the environment on first use — the one
// piece of process-wide state left, and only on this path.
var (
	defaultMu  sync.Mutex
	defaultCli *Client
)

func defaultClient() *Client {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultCli == nil {
		c := &Client{log: newLogger(envDebugWriter()), currentIDs: envCurrentSessionIDs()}
		config, err := LoadConfig()
		if err != nil {
			// A broken config must not take the whole CLI down: it is logged
			// and ignored, leaving the built-in candidates in force. `crossmem
			// config` surfaces the error properly.
			c.log.debugf("config load err=%q", err)
			config = Config{Path: config.Path, Exists: config.Exists}
		}
		c.config = config
		defaultCli = c
	}
	return defaultCli
}

// resetConfig drops the default client so the next call re-reads the config
// file and environment.
func resetConfig() {
	defaultMu.Lock()
	defaultCli = nil
	defaultMu.Unlock()
}

// ResetConfig drops the default client used by the package-level functions.
// It exists for tests that repoint CROSSMEM_CONFIG; code embedding crossmem
// should construct its own Client with New instead.
func ResetConfig() {
	resetConfig()
}

// envDebugWriter honours CROSSMEM_DEBUG and CROSSMEM_LOG for the default client.
func envDebugWriter() io.Writer {
	if path := strings.TrimSpace(os.Getenv("CROSSMEM_LOG")); path != "" {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			return file
		}
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CROSSMEM_DEBUG"))) {
	case "1", "true", "yes", "on":
		return os.Stderr
	}
	return nil
}
