package providers

import (
	"strings"
	"testing"
)

// A resuming agent needs the END of a session — where the work stopped. A
// head-only truncation spends the budget on the opening and never gets there.
func TestFitPreviewKeepsOpeningAndMostRecentTurns(t *testing.T) {
	chunks := []string{"user: the goal is to ship the exporter"}
	for i := 0; i < 400; i++ {
		chunks = append(chunks, "assistant: "+strings.Repeat("middle ", 40))
	}
	chunks = append(chunks, "user: last thing — the dump still fails on symlinks")

	got := fitPreview(chunks, "\n\n", 4000)

	if len(got) > 4000 {
		t.Fatalf("preview over budget: %d chars", len(got))
	}
	if !strings.Contains(got, "the goal is to ship the exporter") {
		t.Error("opening turn missing: the goal of the session was dropped")
	}
	if !strings.Contains(got, "the dump still fails on symlinks") {
		t.Error("final turn missing: a resuming agent cannot see where work stopped")
	}
	if !strings.Contains(got, "elided") {
		t.Error("elision marker missing: the agent cannot tell the middle was cut")
	}
}

func TestFitPreviewReturnsShortSessionsWhole(t *testing.T) {
	chunks := []string{"user: hello", "assistant: hi"}
	got := fitPreview(chunks, "\n\n", 4000)
	if got != "user: hello\n\nassistant: hi" {
		t.Errorf("short session was altered: %q", got)
	}
}

// One pasted file must not consume the whole budget and crowd out the turns
// around it.
func TestFitPreviewCapsAnOversizedTurn(t *testing.T) {
	chunks := []string{
		"user: here is the file",
		"user: " + strings.Repeat("x", 20000),
		"assistant: got it", "user: a", "assistant: b", "user: c",
		"user: and now the decision we reached",
	}
	got := fitPreview(chunks, "\n\n", 6000)

	if !strings.Contains(got, "chars trimmed") {
		t.Error("oversized turn was not capped")
	}
	if !strings.Contains(got, "and now the decision we reached") {
		t.Error("the paste crowded out the turn that carries the decision")
	}
}

func TestFitPreviewHandlesEmptyInput(t *testing.T) {
	if got := fitPreview(nil, "\n", 100); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := fitPreview([]string{"a"}, "\n", 0); got != "" {
		t.Errorf("expected empty for zero budget, got %q", got)
	}
}
