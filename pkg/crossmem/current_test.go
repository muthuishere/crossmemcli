package crossmem

import "testing"

// The whole point: crossmem runs inside an agent session, and that session's
// own transcript is the newest one for the folder. Returning it means handing
// the caller back the context it already has.
func TestCurrentSessionIsExcludedByDefault(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "1cb27481-live")
	sessions := []Session{
		{Provider: "claude", ID: "1cb27481-live", Path: "/p/1cb27481-live.jsonl", Ref: "/p/1cb27481-live.jsonl"},
		{Provider: "claude", ID: "older", Path: "/p/older.jsonl", Ref: "/p/older.jsonl"},
	}

	kept := defaultClient().markCurrent(append([]Session{}, sessions...), false)
	if len(kept) != 1 || kept[0].ID != "older" {
		t.Fatalf("the live session was not dropped: %+v", kept)
	}

	// --include-current keeps it, flagged, so a caller can still see it.
	all := defaultClient().markCurrent(append([]Session{}, sessions...), true)
	if len(all) != 2 {
		t.Fatalf("include-current should keep every session, got %d", len(all))
	}
	if !all[0].Current || all[1].Current {
		t.Fatalf("Current flag set wrong: %+v", all)
	}
}

// The id also has to match by transcript filename: JSONL stores name the file
// <session-id>.jsonl, and that is the only link back to the running agent.
func TestCurrentSessionMatchesByTranscriptFilename(t *testing.T) {
	ids := map[string]bool{"abc-123": true}
	if !isCurrentSession(Session{Path: "/store/abc-123.jsonl"}, ids) {
		t.Fatal("should match on transcript filename")
	}
	if isCurrentSession(Session{Path: "/store/def-456.jsonl"}, ids) {
		t.Fatal("must not match a different transcript")
	}
	// No env var set anywhere: nothing is current, nothing is dropped.
	if isCurrentSession(Session{ID: "abc-123"}, map[string]bool{}) {
		t.Fatal("with no known session id, no session is current")
	}
}

func TestCurrentSessionEscapeHatchEnvVar(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CROSSMEM_CURRENT_SESSION", "one, two")
	ids := envCurrentSessionIDs()
	if !ids["one"] || !ids["two"] {
		t.Fatalf("comma-separated ids not parsed: %v", ids)
	}
}

// Each agent names its live session differently, and the id has to be matched
// against the shape that provider actually stores. Verified against the real
// tools: Claude names the transcript <session-id>.jsonl, Devin's id is the
// sessions.db row id, and Codex names its rollout
// rollout-<timestamp>-<thread-id>.jsonl so the id is only a suffix.
func TestCurrentSessionMatchingPerProvider(t *testing.T) {
	cases := []struct {
		name    string
		envVar  string
		envID   string
		session Session
	}{
		{
			name:    "claude transcript filename",
			envVar:  "CLAUDE_CODE_SESSION_ID",
			envID:   "1cb27481-5bcf-4365-b32f-0c9dd9b229dd",
			session: Session{Provider: "claude", ID: "1cb27481-5bcf-4365-b32f-0c9dd9b229dd", Path: "/p/1cb27481-5bcf-4365-b32f-0c9dd9b229dd.jsonl"},
		},
		{
			name:    "devin sessions.db id",
			envVar:  "DEVIN_SESSION_ID",
			envID:   "crimson-lung",
			session: Session{Provider: "devin", ID: "crimson-lung", Ref: "devin:crimson-lung", Path: "/p/sessions.db"},
		},
		{
			name:    "codex thread id is a filename suffix",
			envVar:  "CODEX_THREAD_ID",
			envID:   "01a00acd-7653-7050-aa47-76404aff4114",
			session: Session{Provider: "codex", ID: "rollout-2026-08-16T19-10-32-01a00acd-7653-7050-aa47-76404aff4114", Path: "/p/rollout-2026-08-16T19-10-32-01a00acd-7653-7050-aa47-76404aff4114.jsonl"},
		},
		{
			name:    "escape hatch for tools with no session variable",
			envVar:  "CROSSMEM_CURRENT_SESSION",
			envID:   "ses_fb3c664c8ffekIdgDaohc2tL5J",
			session: Session{Provider: "opencode", ID: "ses_fb3c664c8ffekIdgDaohc2tL5J", Ref: "opencode:ses_fb3c664c8ffekIdgDaohc2tL5J"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range currentSessionEnvVars {
				t.Setenv(name, "")
			}
			t.Setenv(tc.envVar, tc.envID)
			if !isCurrentSession(tc.session, envCurrentSessionIDs()) {
				t.Fatalf("%s did not identify the live session", tc.envVar)
			}
			// A different session of the same provider must not be swept up.
			other := tc.session
			other.ID = "someone-else"
			other.Path = "/p/rollout-2026-08-16T21-41-02-deadbeef-0000-0000-0000-000000000000.jsonl"
			if isCurrentSession(other, envCurrentSessionIDs()) {
				t.Fatalf("%s matched an unrelated session", tc.envVar)
			}
		})
	}
}
