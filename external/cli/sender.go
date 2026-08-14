//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// updateMsg carries one session update into the UI loop.
type updateMsg struct {
	sessionID string
	update    interface{}
}

// permRequest is a blocking permission round-trip between a turn worker and
// the UI loop.
type permRequest struct {
	params acp.PermissionRequestParams
	reply  chan *acp.PermissionResult
}

// questRequest is the question-tool round-trip.
type questRequest struct {
	params acp.QuestionRequestParams
	reply  chan *acp.QuestionResult
}

// sender implements acp.UpdateSender for the console app. It is both the
// manager's server sender (mode/config/slash/replay updates) and the sender
// for every prompt turn. All posts land in buffered channels drained by the
// UI goroutine; manager methods that can emit updates are only ever invoked
// from worker goroutines, so posting never deadlocks the UI.
type sender struct {
	app *App
}

// SendSessionUpdate queues one update for the UI loop.
func (s *sender) SendSessionUpdate(sessionID string, update interface{}) error {
	select {
	case s.app.updatesCh <- updateMsg{sessionID: sessionID, update: update}:
	case <-s.app.closed:
	}
	return nil
}

// RequestPermission blocks the calling turn worker until the operator picks
// an option in the modal (or bypass mode short-circuits).
func (s *sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if cfg := s.app.cfg; cfg != nil && cfg.Tools.ResolvedPermMode() == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	req := permRequest{params: params, reply: make(chan *acp.PermissionResult, 1)}
	select {
	case s.app.permCh <- req:
	case <-ctx.Done():
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	case <-s.app.closed:
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	select {
	case res := <-req.reply:
		return res, nil
	case <-ctx.Done():
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	case <-s.app.closed:
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
}

// RequestQuestion blocks the calling turn worker until the operator answers.
func (s *sender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	req := questRequest{params: params, reply: make(chan *acp.QuestionResult, 1)}
	select {
	case s.app.questCh <- req:
	case <-ctx.Done():
		return &acp.QuestionResult{}, nil
	case <-s.app.closed:
		return &acp.QuestionResult{}, nil
	}
	select {
	case res := <-req.reply:
		return res, nil
	case <-ctx.Done():
		return &acp.QuestionResult{}, nil
	case <-s.app.closed:
		return &acp.QuestionResult{}, nil
	}
}
