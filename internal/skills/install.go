package skills

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed bundled/crossmem-loader/SKILL.md
var skillMD string

//go:embed bundled/crossmem-loader/README.md
var readmeMD string

type InstallResult struct {
	Name string
	Path string
}

func Install() ([]InstallResult, error) {
	home := os.Getenv("HOME")
	targets := []string{
		filepath.Join(home, ".claude", "skills", "crossmem-loader"),
		filepath.Join(home, ".agents", "skills", "crossmem-loader"),
	}
	results := make([]InstallResult, 0, len(targets))
	for _, target := range targets {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(target, "README.md"), []byte(readmeMD), 0o644); err != nil {
			return nil, err
		}
		results = append(results, InstallResult{Name: "crossmem-loader", Path: target})
	}
	return results, nil
}
