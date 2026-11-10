package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"log/slog"
	"strings"
	"testing"
)

func TestSessionServiceTierPrecedence(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "api", Type: "openai"}, {Name: "claude", Type: "anthropic"}}, Models: []config.ModelEntry{{Model: "api/gpt-6-astra", ServiceTier: "priority"}, {Model: "api/custom", ServiceTier: "flex"}, {Model: "claude/test"}, {Model: "api/gpt-6-luna"}}}
	st := &State{}
	if got, _ := st.ServiceTierFor(cfg, "api/custom"); got != "flex" {
		t.Fatal(got)
	}
	st.SetSelectedServiceTier("ultrafast")
	for _, tc := range []struct {
		model, want string
		fallback    bool
	}{{"api/gpt-6-astra", "ultrafast", false}, {"api/custom", "ultrafast", false}, {"claude/test", "default", true}, {"api/gpt-6-luna", "default", true}} {
		got, fallback := st.ServiceTierFor(cfg, tc.model)
		if got != tc.want || fallback != tc.fallback {
			t.Fatalf("%s=%s/%v", tc.model, got, fallback)
		}
	}
	if st.GetSelectedServiceTier() != "ultrafast" {
		t.Fatal("selection erased")
	}
	st.SetSelectedServiceTier("")
	if got, _ := st.ServiceTierFor(cfg, "api/gpt-6-astra"); got != "priority" {
		t.Fatal(got)
	}
}

func TestServiceTierPersists(t *testing.T) {
	fs := &FileStore{Root: t.TempDir()}
	dir, err := fs.EnsureLayout("sess_tier")
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: "sess_tier", CWD: t.TempDir(), SessionDir: dir}
	st.SetSelectedServiceTier("priority")
	if err = fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &State{}
	restored.RestoreServiceTierWithoutPersist(snap.Meta.SelectedServiceTier)
	if restored.GetSelectedServiceTier() != "priority" {
		t.Fatal("tier lost")
	}
}

func TestManualModelChangeResetsTierWithNotice(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "api", Type: "openai"}}, Models: []config.ModelEntry{{Model: "api/gpt-6-astra"}, {Model: "api/gpt-6-luna"}}}
	mgr := NewManager(cfg, &contextUsageCapture{}, nil, slog.Default(), t.TempDir(), nil)
	st := &State{ID: "sess_tier", SelectedModelID: "api/gpt-6-astra", SelectedServiceTier: "ultrafast"}
	model := "api/gpt-6-luna"
	ch := SettingsChange{Model: &model, Source: "web"}
	values, err := mgr.validateSettingsChange(st, ch)
	if err != nil {
		t.Fatal(err)
	}
	notice := mgr.writeSettings(st.ID, st, ch, values)
	if st.GetSelectedServiceTier() != "" || !strings.Contains(noteSettingsChange(st, ch, notice), "Service tier reset") {
		t.Fatalf("tier=%s notice=%s", st.GetSelectedServiceTier(), notice)
	}
}
