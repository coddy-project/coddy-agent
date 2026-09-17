//go:build cli

package cli

// Edges of the gate queue (gates.go). The happy paths - a background subagent
// asking between turns, and waiting behind a turn's own prompt - are in
// features/cli_tui.feature.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func gateRequest(ctx context.Context, title string) permRequest {
	return permRequest{
		ctx: ctx,
		params: acp.PermissionRequestParams{
			SessionID: "sess_gate",
			ToolCall:  acp.PermissionToolCall{ToolCallID: "call_" + title, Title: title},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
			EffectivePermissionMode: config.PermModeAsk,
		},
		reply: make(chan *acp.PermissionResult, 1),
	}
}

// modalText is the plain text of whatever modal is on screen.
func modalText(t *testing.T, a *App) string {
	t.Helper()
	if a.modal == nil {
		return ""
	}
	var b strings.Builder
	for _, line := range a.modal.Render(80) {
		b.WriteString(tui.StripTerminalSequences(line))
		b.WriteString("\n")
	}
	return b.String()
}

// A prompt that waited behind another one and whose asker gave up in the
// meantime - its subagent was stopped, or answered in a browser - never reaches
// the screen: the next one that is still waiting does.
func TestAQueuedGateWhoseAskerGaveUpIsSkipped(t *testing.T) {
	a := newTestApp(t)
	first := gateRequest(context.Background(), "first prompt")
	gaveUp, cancel := context.WithCancel(context.Background())
	stale := gateRequest(gaveUp, "stale prompt")
	third := gateRequest(context.Background(), "third prompt")

	a.openPermissionModal(first)
	a.openPermissionModal(stale)
	a.openPermissionModal(third)
	if got := modalText(t, a); !strings.Contains(got, "first prompt") {
		t.Fatalf("the first prompt is not on screen:\n%s", got)
	}
	cancel()

	a.modal.(*permissionModal).HandleInput([]byte("\r"))
	if res := <-first.reply; res == nil || res.OptionID != "allow" {
		t.Fatalf("first prompt answered %+v, want allow", res)
	}
	if got := modalText(t, a); !strings.Contains(got, "third prompt") || strings.Contains(got, "stale prompt") {
		t.Fatalf("after the first answer the screen shows:\n%s", got)
	}
	select {
	case res := <-stale.reply:
		t.Fatalf("the abandoned prompt was answered %+v", res)
	default:
	}
}

// A prompt whose asker gave up before it reached the screen - the run was
// stopped while the request sat in the loop's channel - is never opened, not
// even for the one frame it would take the withdrawal to catch up with it.
func TestAGateWhoseAskerAlreadyGaveUpIsNotOpened(t *testing.T) {
	a := newTestApp(t)
	gaveUp, cancel := context.WithCancel(context.Background())
	cancel()
	a.openPermissionModal(gateRequest(gaveUp, "already withdrawn"))
	if a.modal != nil {
		t.Fatalf("a withdrawn prompt reached the screen:\n%s", modalText(t, a))
	}
	next := gateRequest(context.Background(), "next prompt")
	a.openPermissionModal(next)
	if got := modalText(t, a); !strings.Contains(got, "next prompt") {
		t.Fatalf("the next prompt was not opened:\n%s", got)
	}
}

// A prompt on screen whose asker gives up is taken down, and the one waiting
// behind it takes its place.
func TestAGateWhoseAskerGivesUpIsTakenDown(t *testing.T) {
	a := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.openPermissionModal(gateRequest(ctx, "withdrawn prompt"))
	next := gateRequest(context.Background(), "next prompt")
	a.openPermissionModal(next)

	cancel()
	select {
	case msg := <-a.updatesCh:
		a.applyLoopMessage(msg)
	case <-time.After(2 * time.Second):
		t.Fatal("giving up never reached the UI loop")
	}
	if got := modalText(t, a); !strings.Contains(got, "next prompt") {
		t.Fatalf("the withdrawn prompt was not replaced by the next one:\n%s", got)
	}
}

// The end of a turn takes down that turn's orphaned prompt, but a background
// subagent's prompt belongs to no turn: its asker is still waiting for an answer.
func TestTheEndOfATurnLeavesASubagentPromptOnScreen(t *testing.T) {
	a := newTestApp(t)
	a.turnActive = true
	a.turnSessionID = "sess_turn"
	a.openPermissionModal(gateRequest(context.Background(), "[subagent writer] Run: echo checked"))

	a.applyLoopMessage(updateMsg{update: turnDone{sessionID: "sess_turn"}})
	if got := modalText(t, a); !strings.Contains(got, "[subagent writer]") {
		t.Fatalf("the end of an unrelated turn took the subagent's prompt down:\n%s", got)
	}

	orphaned, cancel := context.WithCancel(context.Background())
	b := newTestApp(t)
	b.turnActive = true
	b.turnSessionID = "sess_turn"
	b.openPermissionModal(gateRequest(orphaned, "Run: sleep 6"))
	cancel()
	b.applyLoopMessage(updateMsg{update: turnDone{sessionID: "sess_turn"}})
	if b.modal != nil {
		t.Fatalf("the turn's own orphaned prompt stayed on screen:\n%s", modalText(t, b))
	}
}
