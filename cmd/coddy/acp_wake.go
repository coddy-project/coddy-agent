package main

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// acpPromptRunner is the manager surface a woken turn needs. It is an interface
// so the wake can be tested without a session store behind it.
type acpPromptRunner interface {
	HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
}

// acpWakeRunner is the turn a finished background task starts under `coddy
// acp`.
//
// The turn goes through the manager's ordinary prompt path: it takes the
// composer turn lock, and a refusal comes back as session.ErrSessionTurnBusy
// for the waker to wait out. Its updates travel to the client on the same
// session/update notifications a prompted turn uses, so the editor renders the
// woken turn in the thread it belongs to - opening with the wake - and a gated
// tool call inside it asks the editor like any other: ACP has a person at the
// far end.
func acpWakeRunner(mgr acpPromptRunner, sender acp.UpdateSender) agent.RunTurnFunc {
	return func(ctx context.Context, wake agent.Wake) error {
		_, err := mgr.HandleSessionPromptWithSender(ctx, wake.PromptParams(), sender, wake.RunOpts())
		return err
	}
}

// acpWakeNotice is the ACP server as the manager and the waker see it: every
// update passes through, and a background_wake update is followed by the same
// wake as agent text. An editor that renders none of Coddy's own updates - most
// render only the standard ones - would otherwise show an answer nobody asked
// for; with the note, the woken turn opens with what woke the agent, live and
// when session/load replays it. The note is a Markdown quote, so an editor that
// renders Markdown sets it apart from the answer after it.
type acpWakeNotice struct {
	acp.UpdateSender
}

func (n acpWakeNotice) SendSessionUpdate(sessionID string, update interface{}) error {
	if err := n.UpdateSender.SendSessionUpdate(sessionID, update); err != nil {
		return err
	}
	wake, ok := update.(acp.BackgroundWakeUpdate)
	if !ok {
		return nil
	}
	lines := strings.Split(session.BackgroundWakeNote(wake), "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return n.UpdateSender.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: strings.Join(lines, "\n") + "\n\n"},
	})
}
