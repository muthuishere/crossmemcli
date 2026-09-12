package providers

import (
	"os"
	"path/filepath"
	"testing"
)

// Agents write their tool output back into the transcript as a user turn. If
// those count, "the last thing you asked" becomes a shell transcript instead of
// a question, which is the whole value of the field.
func TestUserTextsIgnoresToolResultsAndInjectedBlocks(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "real question",
			line: `{"type":"user","message":{"role":"user","content":"wire the oauth callback"}}`,
			want: "wire the oauth callback",
		},
		{
			name: "tool result carries toolUseResult",
			line: `{"type":"user","toolUseResult":{"stdout":"ok"},"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		},
		{
			name: "tool result block without the field",
			line: `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"files listed"}]}}`,
		},
		{
			name: "harness-injected block",
			line: `{"type":"user","message":{"role":"user","content":"<task-notification> done </task-notification>"}}`,
		},
		{
			name: "assistant turn is not a question",
			line: `{"type":"assistant","message":{"role":"assistant","content":"here you go"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := userTexts([]byte(tc.line), "claude")
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("expected no user text, got %q", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("got %q, want [%q]", got, tc.want)
			}
		})
	}
}

// The opening question comes from the head of the file and the closing one from
// the tail, so a transcript far larger than the tail window must still yield
// both — the tail seek lands mid-line and that fragment must be discarded.
func TestJSONLQuestionsReadsHeadAndTailOfALargeTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	write := func(line string) {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"type":"user","message":{"role":"user","content":"FIRST QUESTION"}}`)
	padding := make([]byte, 4096)
	for i := range padding {
		padding[i] = 'x'
	}
	// Comfortably exceed questionTailMax so the tail path is exercised.
	for i := 0; i < (questionTailMax/len(padding))+40; i++ {
		write(`{"type":"assistant","message":{"role":"assistant","content":"` + string(padding) + `"}}`)
	}
	write(`{"type":"user","message":{"role":"user","content":"LAST QUESTION"}}`)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	first, last := jsonlQuestions(path, "claude")
	if first != "FIRST QUESTION" {
		t.Fatalf("first = %q", first)
	}
	if last != "LAST QUESTION" {
		t.Fatalf("last = %q", last)
	}
}

// A session with a single question opens and closes with the same one.
func TestJSONLQuestionsFallsBackToFirstWhenNoLaterQuestion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"user","message":{"role":"user","content":"only question"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":"answer"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	first, last := jsonlQuestions(path, "claude")
	if first != "only question" || last != "only question" {
		t.Fatalf("first=%q last=%q", first, last)
	}
}
