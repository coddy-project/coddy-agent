package tools

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type switchSender struct{}

func (switchSender) SendSessionUpdate(string, interface{}) error { return nil }

func (switchSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (switchSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// switch_model is built from a configuration alone, with no manager in sight
// (SwitchModelTool(cfg), modelSwitchOffered(cfg)): what it lists for a model of
// a remote Coddy is what the configuration's lineage answers, so the levels the
// remote's listing offers appear in its text once the listing has been read, and
// not before.
func TestSwitchModelListsTheLevelsOfAListedCoddyModel(t *testing.T) {
	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir()},
		Providers: []config.ProviderConfig{{Name: "far", Type: "coddy", APIBase: "http://far.example"}},
		Models:    []config.ModelEntry{{Model: "far/coder"}},
		Agent:     config.Agent{Model: "far/coder"},
	}
	mgr := session.NewManager(cfg, switchSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
		return []llm.ModelEntry{{ID: "coder", Revision: "r1", ReasoningLevels: []string{"low", "high"}, AllowReasoningOff: true}}, nil
	}, nil)
	t.Cleanup(func() { _ = mgr.WaitContextWindowsIdle(5 * time.Second) })

	const line = "- far/coder (reasoning: low, high, off)"
	if d := SwitchModelTool(cfg).Definition.Description; strings.Contains(d, "(reasoning") {
		t.Fatalf("a model nothing is known about already lists levels: %q", d)
	}
	if modelSwitchOffered(cfg) {
		t.Fatal("a lone model with no known levels has nothing to switch")
	}

	mgr.AwaitContextWindows(context.Background(), cfg, []string{"far/coder"}, 5*time.Second)
	if err := mgr.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if d := SwitchModelTool(cfg).Definition.Description; !strings.Contains(d, line) {
		t.Fatalf("switch_model does not list the listing's levels: %q", d)
	}
	if !modelSwitchOffered(cfg) {
		t.Fatal("a lone model that offers levels has something to switch")
	}

	// A configuration of the same lineage (a reload shares the Paths) is bound
	// the same way, and a key it writes wins key by key: the written levels, the
	// listing's off.
	reloaded := *cfg
	reloaded.Models = []config.ModelEntry{{Model: "far/coder", ReasoningLevels: &[]string{"low"}}}
	if d := SwitchModelTool(&reloaded).Definition.Description; !strings.Contains(d, "- far/coder (reasoning: low, off)") {
		t.Fatalf("a written list must win over the listing's levels and leave its off: %q", d)
	}
}
