package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// What a model of a remote Coddy can do comes from the remote's listing, which
// the session manager's cache holds. The agent reads it through the same
// config methods every surface uses, on the configuration it holds - including
// the one it loads for itself after config_commit (react.go) or a resumed call
// (resume_permission.go) - so none of its reads needs a binding of its own.

type agentListing struct {
	mu   sync.Mutex
	rows map[string]llm.ModelEntry
}

func (l *agentListing) put(rows ...llm.ModelEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rows == nil {
		l.rows = make(map[string]llm.ModelEntry)
	}
	for _, r := range rows {
		l.rows[r.ID] = r
	}
}

func (l *agentListing) list(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]llm.ModelEntry, 0, len(l.rows))
	for _, r := range l.rows {
		r.ReasoningLevels = append([]string(nil), r.ReasoningLevels...)
		out = append(out, r)
	}
	return out, nil
}

const agentCoddyConfig = `skills:
  auto_discovery: true
providers:
  - name: far
    type: coddy
    api_base: http://far.example
models:
  - model: far/coder
  - model: far/pinned
    multimodal: false
  - model: far/gone
agent:
  model: far/coder
`

// agentListingFixture is a configuration loaded from a file (so it has a
// lineage), a manager that is its listing source, and a listing that has been
// read once.
type agentListingFixture struct {
	dir     string
	cfg     *config.Config
	mgr     *session.Manager
	listing *agentListing
}

func newAgentListingFixture(t *testing.T) *agentListingFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(agentCoddyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: dir, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	fx := &agentListingFixture{dir: dir, cfg: cfg, listing: &agentListing{}}
	fx.listing.put(
		llm.ModelEntry{ID: "coder", Revision: "r1", Multimodal: true, ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high", AllowReasoningOff: true},
		llm.ModelEntry{ID: "pinned", Revision: "r1", Multimodal: true, ReasoningLevels: []string{"low"}},
	)
	fx.mgr = session.NewManager(cfg, resumePermissionSender{}, nil, slog.Default(), dir, nil)
	fx.mgr.SetContextWindowLister(fx.listing.list, nil)
	t.Cleanup(func() { _ = fx.mgr.WaitContextWindowsIdle(5 * time.Second) })
	fx.read(t)
	return fx
}

func (fx *agentListingFixture) read(t *testing.T) {
	t.Helper()
	fx.mgr.AwaitContextWindows(context.Background(), fx.cfg, []string{"far/coder"}, 5*time.Second)
	if err := fx.mgr.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
}

// refresh makes the manager read the listing again after the remote changed
// (what a stale_revision answer does).
func (fx *agentListingFixture) refresh(t *testing.T, stale string) {
	t.Helper()
	if _, err := fx.mgr.RefreshProviderModelEntry(context.Background(), fx.cfg, "far", "coder", stale, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func (fx *agentListingFixture) agent(model string) *Agent {
	st := &session.State{ID: "sess_listing", CWD: fx.dir, Mode: session.ModeAgent}
	if model != "" {
		st.SetSelectedModelID(model)
	}
	return NewAgent(fx.cfg, st, resumePermissionSender{}, nil)
}

// A coddy model reads images when the remote's listing says it does, unless the
// row writes the key; a model the listing does not know fails closed, and the
// refusal the `read` tool is given names the key an operator can set.
func TestModelReadsImagesOfACoddyRowFollowsTheListing(t *testing.T) {
	fx := newAgentListingFixture(t)

	listed := fx.agent("far/coder")
	if !listed.modelReadsImages() {
		t.Fatal("a model the listing marks multimodal does not read images")
	}
	if err := listed.toolImageRefusal(); err != nil {
		t.Fatalf("refused: %v", err)
	}
	env := listed.buildToolEnv(string(session.ModeAgent), "")
	if env.ImageRefusal == nil || env.ImageRefusal() != nil {
		t.Fatal("the tool environment refuses a picture for a listed multimodal model")
	}

	pinned := fx.agent("far/pinned")
	if pinned.modelReadsImages() {
		t.Fatal("multimodal: false on the row must win over the listing")
	}
	if err := pinned.toolImageRefusal(); err == nil || !strings.Contains(err.Error(), "models[].multimodal") {
		t.Fatalf("refusal = %v, want one that names models[].multimodal", err)
	}

	// The remote stops accepting images: the same agent, the same configuration,
	// the next read says no (nothing was cached in the agent).
	fx.listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", Multimodal: false, ReasoningLevels: []string{"low"}})
	fx.refresh(t, "r1")
	if listed.modelReadsImages() {
		t.Fatal("the agent kept an answer the listing no longer gives")
	}
	if err := listed.buildToolEnv(string(session.ModeAgent), "").ImageRefusal(); err == nil {
		t.Fatal("the tool environment still lets a picture through")
	}

	// A model the config lists but the remote does not carry fails closed.
	if unknown := fx.agent("far/gone"); unknown.modelReadsImages() {
		t.Fatal("a model the listing does not carry reads images")
	}
}

// config_commit loads the configuration again for the rest of the turn
// (react.go): that private load shares the lineage of the one the manager
// serves, so its capability reads are the listing's the moment it exists, with
// no binding line in the agent.
func TestPrivateReloadAfterConfigCommitAnswersFromTheListing(t *testing.T) {
	fx := newAgentListingFixture(t)
	ag := fx.agent("")
	ag.SetConfigReloader(func(context.Context) ([]string, error) { return nil, nil })
	provider := &configReloadProvider{}
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	ent := func(c *config.Config) *config.ModelEntry { return c.FindModelEntry("far/coder") }
	want := []string{"low", "high", "off"}
	if got := ag.cfg.ReasoningChoicesFor(ent(ag.cfg)); !reflect.DeepEqual(got, want) {
		t.Fatalf("before the reload: %v", got)
	}
	before := ag.cfg

	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "disable skill discovery"}}); err != nil {
		t.Fatal(err)
	}
	if ag.cfg == before {
		t.Fatal("the turn did not load its own configuration after config_commit")
	}
	if got := ag.cfg.ReasoningChoicesFor(ent(ag.cfg)); !reflect.DeepEqual(got, want) {
		t.Fatalf("the agent's private configuration answered %v, want the listing's %v", got, want)
	}
	if got := ag.cfg.DefaultReasoningLevelFor(ent(ag.cfg)); got != "high" {
		t.Fatalf("default = %q", got)
	}
	if !ag.cfg.ModelMultimodal(ent(ag.cfg)) || !ag.cfg.ReasoningOffOffered(ent(ag.cfg)) {
		t.Fatal("images or off lost across the private reload")
	}
	// The registry built for it too: switch_model lists the model with the
	// levels the listing gave.
	if def := ag.registry.AllToolDefinitions(); !hasToolLine(def, "switch_model", "- far/coder (reasoning: low, high, off)") {
		t.Fatal("switch_model of the reloaded registry does not list the listing's levels")
	}
}

func hasToolLine(defs []llm.ToolDefinition, name, line string) bool {
	for _, d := range defs {
		if d.Name == name {
			return strings.Contains(d.Description, line)
		}
	}
	return false
}

// A call resumed after a permission answer builds its own environment
// (resume_permission.go), and its config_commit loads the configuration for
// itself as well: same lineage, same answers.
func TestPrivateReloadOfAResumedCallAnswersFromTheListing(t *testing.T) {
	fx := newAgentListingFixture(t)
	ag := fx.agent("")
	ag.SetConfigReloader(func(context.Context) ([]string, error) { return nil, nil })
	env := ag.buildToolEnv(string(session.ModeAgent), "")
	if env.ReloadConfig == nil {
		t.Fatal("the resumed environment has no config reload")
	}
	before := ag.cfg
	if _, err := env.ReloadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ag.cfg == before {
		t.Fatal("the resumed call did not load its own configuration")
	}
	ent := ag.cfg.FindModelEntry("far/coder")
	if got, want := ag.cfg.ReasoningChoicesFor(ent), []string{"low", "high", "off"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the resumed call's configuration answered %v, want %v", got, want)
	}
	if !ag.modelReadsImages() {
		t.Fatal("the agent reads images through the reloaded configuration no more")
	}
	// A listing that moves after the reload is seen by the reloaded config too.
	fx.listing.put(llm.ModelEntry{ID: "coder", Revision: "r2", ReasoningLevels: []string{"low"}})
	fx.refresh(t, "r1")
	if got, want := ag.cfg.ReasoningChoicesFor(ent), []string{"low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the listing narrowed: %v, want %v", got, want)
	}
	if ag.modelReadsImages() {
		t.Fatal("images still read after the listing dropped them")
	}
}
