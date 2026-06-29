package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var repoGuardrailFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	filepath.Join(".agents", "AGENTS.md"),
	filepath.Join(".claude", "CLAUDE.md"),
}

type GuardrailFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Text  string `json:"text,omitempty"`
}

func BuildGuardrails(folder string) (string, error) {
	files, err := ReadGuardrails(folder)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "# Guardrails")
	fmt.Fprintln(&b)
	if len(files) == 0 {
		fmt.Fprintln(&b, "_No repo guardrail files found._")
		return b.String(), nil
	}
	for _, file := range files {
		fmt.Fprintf(&b, "## %s\n\n", file.Path)
		fmt.Fprintln(&b, strings.TrimSpace(file.Text))
		fmt.Fprintln(&b)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}

func ReadGuardrails(folder string) ([]GuardrailFile, error) {
	root, err := filepath.Abs(expandHome(folder))
	if err != nil {
		return nil, err
	}
	var files []GuardrailFile
	for _, rel := range repoGuardrailFiles {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > 256*1024 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, GuardrailFile{
			Path:  path,
			Bytes: info.Size(),
			Text:  string(data),
		})
	}
	return files, nil
}

func guardrailManifest(files []GuardrailFile) []GuardrailFile {
	out := make([]GuardrailFile, 0, len(files))
	for _, file := range files {
		out = append(out, GuardrailFile{Path: file.Path, Bytes: file.Bytes})
	}
	return out
}

func marshalIndent(value any) []byte {
	data, _ := json.MarshalIndent(value, "", "  ")
	return append(data, '\n')
}
