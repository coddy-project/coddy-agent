//go:build cli

package cli

import (
	"log/slog"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A listing stands in for the manager's cache of what a remote Coddy shares.
type footerListing map[string]config.ListedModel

func (l footerListing) ListedModel(_ *config.Config, provider, apiModel string) (config.ListedModel, bool) {
	m, ok := l[provider+"/"+apiModel]
	return m, ok
}

// noSessionBackend is a console backend with no local session state, the shape
// the remote console has: the footer falls back on the model's configured row.
type noSessionBackend struct{ backend }

func (noSessionBackend) SessionByID(string) *session.State { return nil }

func footerReasoningApp(t *testing.T, row config.ModelEntry, listing footerListing) *App {
	t.Helper()
	paths := config.Paths{Home: t.TempDir(), CWD: t.TempDir()}.WithListing()
	paths.AttachListing(listing)
	cfg := &config.Config{
		Paths:     paths,
		Providers: []config.ProviderConfig{{Name: "lab", Type: "coddy", APIBase: "http://127.0.0.1:0", APIKey: "test"}},
		Models:    []config.ModelEntry{row},
	}
	a := newApp(cfg, noSessionBackend{}, slog.New(slog.DiscardHandler), &bddTerminal{cols: 80, rows: 24}, "dark", true)
	a.modelID = row.Model
	return a
}

// The footer shows what the session would use for a model whose row says
// nothing: for a model of a remote Coddy, the default of the remote's listing.
func TestFooterReasoningFollowsTheListingOfARemoteModel(t *testing.T) {
	listing := footerListing{"lab/terra": {ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high"}}
	a := footerReasoningApp(t, config.ModelEntry{Model: "lab/terra"}, listing)
	a.refreshFooterModel()
	if a.foot.reasoning != "high" {
		t.Fatalf("footer reasoning = %q, want the listing's default %q", a.foot.reasoning, "high")
	}

	// A model the listing does not carry has nothing to show.
	a.modelID = "lab/unknown"
	a.config().Models = append(a.config().Models, config.ModelEntry{Model: "lab/unknown"})
	a.refreshFooterModel()
	if a.foot.reasoning != "" {
		t.Fatalf("footer reasoning for an unlisted model = %q, want none", a.foot.reasoning)
	}
}

// A default the row writes is shown as written, as before the listing existed.
func TestFooterReasoningShowsAWrittenDefaultAsWritten(t *testing.T) {
	listing := footerListing{"lab/terra": {ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high"}}
	a := footerReasoningApp(t, config.ModelEntry{Model: "lab/terra", ReasoningDefault: "medium"}, listing)
	a.refreshFooterModel()
	if a.foot.reasoning != "medium" {
		t.Fatalf("footer reasoning = %q, want the written default %q", a.foot.reasoning, "medium")
	}
}
