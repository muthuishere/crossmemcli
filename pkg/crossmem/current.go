package crossmem

import (
	"os"
	"path/filepath"
	"strings"
)

// crossmem is nearly always invoked from inside an agent session ("what was I
// doing in this folder?"). The newest session for the current folder is then
// the caller's own live transcript, so "resume the last session" hands the
// caller back the context it already has — the one result that can never help.
//
// Agent CLIs export the id of the session they are running inside. Each entry
// below was verified against the installed tool rather than assumed:
//
//	CLAUDE_CODE_SESSION_ID  set in this session; equals the transcript filename.
//	DEVIN_SESSION_ID        used by Devin's own shell integration, which resumes
//	                        with --resume "__shell__:$DEVIN_SESSION_ID"; equals
//	                        the sessions.db id.
//	CODEX_THREAD_ID         Codex's thread id; the rollout transcript is named
//	                        rollout-<timestamp>-<thread-id>.jsonl, hence the
//	                        suffix match in isCurrentSession.
//
// OpenCode and the Copilot CLI export no session id (checked: neither binary
// contains one), so their live session cannot be detected automatically —
// CROSSMEM_CURRENT_SESSION is the escape hatch for those and for any tool added
// later.
var currentSessionEnvVars = []string{
	"CLAUDE_CODE_SESSION_ID",
	"DEVIN_SESSION_ID",
	"CODEX_THREAD_ID",
	"CROSSMEM_CURRENT_SESSION",
}

func currentSessionIDs() map[string]bool {
	ids := map[string]bool{}
	for _, name := range currentSessionEnvVars {
		for _, value := range strings.Split(os.Getenv(name), ",") {
			if value = strings.TrimSpace(value); value != "" {
				ids[value] = true
			}
		}
	}
	return ids
}

// isCurrentSession matches a session against the ids of the sessions this
// process is running inside, by id and by transcript filename (JSONL stores
// name the file <session-id>.jsonl).
func isCurrentSession(session Session, ids map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	if session.ID != "" && ids[session.ID] {
		return true
	}
	if session.Path == "" {
		return false
	}
	base := filepath.Base(session.Path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if ids[base] {
		return true
	}
	// Codex names its transcript rollout-<timestamp>-<thread-id>.jsonl, so the
	// id is a suffix of the filename rather than the whole of it.
	for id := range ids {
		if strings.HasSuffix(base, "-"+id) {
			return true
		}
	}
	return false
}

// markCurrent flags the caller's own sessions and, unless they were asked for,
// drops them.
func markCurrent(sessions []Session, includeCurrent bool) []Session {
	ids := currentSessionIDs()
	if len(ids) == 0 {
		return sessions
	}
	kept := sessions[:0]
	for _, session := range sessions {
		session.Current = isCurrentSession(session, ids)
		if session.Current && !includeCurrent {
			continue
		}
		kept = append(kept, session)
	}
	return kept
}
