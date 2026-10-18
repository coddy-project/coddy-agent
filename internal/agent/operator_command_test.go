package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type hintOperator struct{ recordingClient }

func (*hintOperator) OperatorCommandsAvailable() bool { return true }
func (*hintOperator) RequestOperatorCommand(context.Context, acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	return nil, nil
}

func hintAgent(t *testing.T, enabled bool) *Agent {
	t.Helper()
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: t.TempDir()}}
	cfg.Tools.OperatorCommand.Enabled = &enabled
	st := &session.State{ID: "sess_hint", CWD: cfg.Paths.CWD, Mode: session.ModeAgent}
	return NewAgent(cfg, st, &hintOperator{}, slog.Default())
}

// TestOperatorCommandHintFollowsTheSetting: a refusal never points at a tool
// the operator turned off.
func TestOperatorCommandHintFollowsTheSetting(t *testing.T) {
	if hint := hintAgent(t, true).operatorCommandHint("run_command", true); !strings.Contains(hint, "operator_command") {
		t.Fatalf("enabled: hint = %q", hint)
	}
	off := hintAgent(t, false)
	if hint := off.operatorCommandHint("run_command", true); hint != "" {
		t.Fatalf("turned off: hint = %q", hint)
	}
	if off.operatorCommandsOffered() {
		t.Fatal("a turned-off tool is offered")
	}
}
