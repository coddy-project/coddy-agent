package prompts_test

import (
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

func TestConfigValidateTrimsDir(t *testing.T) {
	c := prompts.Config{Dir: "  /tmp/p  "}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Dir != "/tmp/p" {
		t.Errorf("after Validate Dir=%q want %q", c.Dir, "/tmp/p")
	}
}

func TestResolvedDirEmpty(t *testing.T) {
	p := prompts.Config{}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := p.ResolvedDir("/tmp/ws"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolvedDirCWD(t *testing.T) {
	p := prompts.Config{Dir: "${CWD}/prompts"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	got := p.ResolvedDir("/project/root")
	want := filepath.Clean("/project/root/prompts")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
