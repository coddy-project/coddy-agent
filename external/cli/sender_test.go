//go:build cli

package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A subagent's permission request is addressed to the parent session but
// carries the child's own effective mode. A parent in session-level bypass
// must not auto-allow a child whose definition narrowed it to ask.
func TestPrintSenderDecidesBypassFromTheRequestersMode(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Paths:    config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Models:   []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:    config.Agent{Model: "fake/model"},
		Sessions: config.Sessions{Dir: filepath.Join(root, "sessions")},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	store := &session.FileStore{Root: cfg.Sessions.Dir}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopUpdateSender{}, runner, slog.Default(), root, store)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	parent := mgr.SessionByID(res.SessionID)
	parent.SetPermissionMode(config.PermModeBypass)
	t.Cleanup(func() { mgr.ForgetLiveSession(parent.ID) })

	var errOut bytes.Buffer
	p := &printSender{mgr: mgr, cfg: cfg, out: &bytes.Buffer{}, errOut: &errOut}
	params := acp.PermissionRequestParams{
		SessionID: parent.ID,
		ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: "[subagent writer] Run: run_command", Status: "pending"},
	}

	own, err := p.RequestPermission(context.Background(), params)
	if err != nil || own == nil || own.OptionID != "allow" {
		t.Fatalf("parent's own request under session bypass = %+v, %v, want auto-allow", own, err)
	}

	params.EffectivePermissionMode = config.PermModeAsk
	child, err := p.RequestPermission(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if child == nil || child.OptionID == "allow" {
		t.Fatalf("child's ask request under a bypass parent = %+v, want it decided as ask (rejected in print mode)", child)
	}
	if errOut.Len() == 0 {
		t.Fatal("print mode must report the rejected permission")
	}
}

type noopUpdateSender struct{}

func (noopUpdateSender) SendSessionUpdate(string, interface{}) error { return nil }
func (noopUpdateSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}, nil
}
func (noopUpdateSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}
