package providers

import (
	"strings"
	"testing"
)

// Copilot's VS Code chatSessions store accumulates turns as kind:2 append lines,
// with the assistant reply in response[].value. The extractor must read both.
func TestExtractCopilotKind2Appends(t *testing.T) {
	line := `{"kind":2,"k":["requests"],"v":[{"message":{"text":"add a flag"},"response":[{"kind":"server","didStartServerIds":[]},{"value":"Sure, here is the flag."},{"value":""}]}]}`
	got := extractJSONLText([]byte(line), "copilot")
	if !strings.Contains(got, "user: add a flag") {
		t.Fatalf("missing user text, got %q", got)
	}
	if !strings.Contains(got, "assistant: Sure, here is the flag.") {
		t.Fatalf("missing assistant text, got %q", got)
	}
}

// Older snapshots keep turns inline in the kind:0 v.requests with the reply in
// responseMarkdownInfo.markdown; that path must still work.
func TestExtractCopilotKind0Snapshot(t *testing.T) {
	line := `{"kind":0,"v":{"requests":[{"message":{"text":"hello"},"responseMarkdownInfo":{"markdown":"hi there"}}]}}`
	got := extractJSONLText([]byte(line), "copilot")
	if !strings.Contains(got, "user: hello") || !strings.Contains(got, "assistant: hi there") {
		t.Fatalf("unexpected: %q", got)
	}
}

// Input-box edits and other patches carry no conversation and must be ignored.
func TestExtractCopilotIgnoresNonRequestPatches(t *testing.T) {
	line := `{"kind":1,"k":["inputState","inputText"],"v":"draft text"}`
	if got := extractJSONLText([]byte(line), "copilot"); got != "" {
		t.Fatalf("expected empty for input patch, got %q", got)
	}
}

func TestExtractOpenCodeTextPart(t *testing.T) {
	msg := `{"role":"assistant","model":"x"}`
	part := `{"type":"text","text":"the answer is 42"}`
	if got := extractOpenCode(msg, part); got != "assistant: the answer is 42" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractOpenCodeSkipsNonText(t *testing.T) {
	msg := `{"role":"assistant"}`
	part := `{"type":"tool","tool":"bash"}`
	if got := extractOpenCode(msg, part); got != "" {
		t.Fatalf("expected empty for tool part, got %q", got)
	}
}
