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
}

func BuildGuardrails(folder string) (string, error) {
	files, err := ReadGuardrails(folder)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "# Active Repo Instructions")
	fmt.Fprintln(&b)
	if len(files) == 0 {
		fmt.Fprintln(&b, "_No repo instruction files found._")
		return b.String(), nil
	}
	fmt.Fprintln(&b, "Read these files before acting in this repository:")
	fmt.Fprintln(&b)
	for index, file := range files {
		fmt.Fprintf(&b, "%d. `%s`\n", index+1, file.Path)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Treat these files as authoritative instructions. Session history below is context only.")
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
		files = append(files, GuardrailFile{
			Path:  path,
			Bytes: info.Size(),
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
