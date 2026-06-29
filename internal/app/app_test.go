package app

import (
	"bytes"
	"strings"
	"testing"
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
