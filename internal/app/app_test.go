package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/muthuishere/crossmemcli/pkg/crossmem"
)

func TestHelpCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"help", "load"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: crossmem load [options] [folder]") {
		t.Fatalf("load help missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "dev") {
		t.Fatalf("version output missing dev version: %q", stdout.String())
	}
}

func TestSkillsSubcommandRemoved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"skills", "install"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected skills subcommand to fail")
	}
	if !strings.Contains(err.Error(), `unknown command "skills"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The --provider help text is hand-written, so it can silently fall behind
// when a provider is added. Every provider the engine knows must appear in it.
func TestProviderHelpListsEveryProvider(t *testing.T) {
	for _, help := range map[string]string{"list": listHelpText, "load": loadHelpText, "update": updateHelpText, "export": exportHelpText} {
		for _, provider := range crossmem.Providers() {
			if !strings.Contains(help, provider) {
				t.Fatalf("help text does not mention provider %q:\n%s", provider, help)
			}
		}
	}
}

func TestExportImportSyncInHelp(t *testing.T) {
	for _, cmd := range []string{"export", "import", "sync"} {
		var stdout, stderr bytes.Buffer
		if err := Run([]string{"help", cmd}, &stdout, &stderr); err != nil {
			t.Fatalf("help %s returned error: %v", cmd, err)
		}
		if !strings.Contains(stdout.String(), "Usage: crossmem "+cmd) {
			t.Fatalf("%s help missing usage:\n%s", cmd, stdout.String())
		}
	}
}

func TestExportHelpIsQAOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"help", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("help export: %v", err)
	}
	out := stdout.String()
	for _, leaked := range []string{"--qa", "--raw", "--dump", "raw.jsonl", "manifest.json"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("export help still mentions %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{"qa.jsonl", "sessionId", "folder", "import"} {
		if !strings.Contains(out, want) {
			t.Fatalf("export help missing %q:\n%s", want, out)
		}
	}
}

func TestImportHelpIsQAOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"help", "import"}, &stdout, &stderr); err != nil {
		t.Fatalf("help import: %v", err)
	}
	out := stdout.String()
	for _, leaked := range []string{"restore a dump", "manifest.json", "--force"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("import help still mentions %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{"qa.jsonl", "--in", "--merge", "export"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import help missing %q:\n%s", want, out)
		}
	}
}

func TestConfigCommandReportsStores(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"config"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"config:", "devin:sqlite-sessions", "opencode:sqlite-sessions", "looks in:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("config output missing %q:\n%s", want, out)
		}
	}
}
