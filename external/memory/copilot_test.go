//go:build memory

package memory

import (
	"context"
	"path/filepath"
	"testing"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestRunBeforeTurnWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = false
	cfg.Memory.ApplyDefaults()

	out, dur, err := RunBeforeTurn(context.Background(), nil, cfg, filepath.Join(tmp, "w"), "hello world", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dur != 0 {
		t.Fatalf("duration = %d want 0", dur)
	}
	if out.ContextText != "" {
		t.Fatalf("context = %q want empty", out.ContextText)
	}
}

// A read-only pass (ask mode) offers the copilot recall tools only, so stored
// memory cannot change even when the model asks for it.
func TestBeforeTurnToolsReadOnlyOffersRecallOnly(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.ApplyDefaults()
	cfg.Paths.Home = tmp
	store, err := memstorage.NewStore(&cfg.Memory, cfg.Paths, filepath.Join(tmp, "w"))
	if err != nil {
		t.Fatal(err)
	}
	names := func(list []*tooling.Tool) map[string]bool {
		out := map[string]bool{}
		for _, tl := range list {
			out[tl.Definition.Name] = true
		}
		return out
	}
	ro := names(beforeTurnTools(store, &cfg.Memory, true))
	for _, mut := range []string{memtools.NameSave, memtools.NameMkdir, memtools.NameDelete} {
		if ro[mut] {
			t.Errorf("read-only pass still offers %s", mut)
		}
	}
	for _, want := range []string{memtools.NameSearch, memtools.NameList, memtools.NameRead} {
		if !ro[want] {
			t.Errorf("read-only pass lost %s", want)
		}
	}
	if rw := names(beforeTurnTools(store, &cfg.Memory, false)); !rw[memtools.NameSave] {
		t.Error("the full pass should still offer save")
	}
}
