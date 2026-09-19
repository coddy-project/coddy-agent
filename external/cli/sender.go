//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
)

// updateMsg carries one session update into the UI loop.
type updateMsg struct {
	sessionID string
	update    interface{}
}

// permRequest is a blocking permission round-trip between an asker (a turn
// worker, or a background subagent) and the UI loop. ctx is the asker's: once
// it ends nobody reads the reply, so the modal is taken down (gates.go).
type permRequest struct {
	ctx    context.Context
	params acp.PermissionRequestParams
	reply  chan *acp.PermissionResult
}

// questRequest is the question-tool round-trip.
type questRequest struct {
	ctx    context.Context
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

// SendControlUpdate opts the console into backend-local control notifications.
// These share the UI loop's channel but are not part of acp.UpdateSender.
func (s *sender) SendControlUpdate(sessionID string, update any) error {
	return s.SendSessionUpdate(sessionID, update)
}

// RequestPermission blocks the calling turn worker until the operator picks
// an option in the modal (or bypass mode short-circuits). The session-level
// permission override wins over the YAML default, mirroring the agent's own
// effective-mode resolution.
func (s *sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	// A subagent's request arrives under the parent's session id with the
	// child's own mode stamped on it, and the session's gate stamps the mode
	// it decided under; those decide (permission.AutoApproves). A request
	// with neither falls back on the session's own mode.
	if params.EffectivePermissionMode == "" && params.SessionPermissionMode == "" {
		if st := s.app.mgr.SessionByID(params.SessionID); st != nil {
			params.SessionPermissionMode = st.EffectivePermissionMode()
		}
	}
	cfgMode := ""
	if s.app.remoteURL == "" {
		// Local fallback only: a remote server sends a permission event
		// precisely because ITS policy wants a human answer, so the local
		// bypass setting must never auto-approve it.
		if cfg := s.app.config(); cfg != nil {
			cfgMode = cfg.Tools.ResolvedPermMode()
		}
	}
	if permission.AutoApproves(params, cfgMode) {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	req := permRequest{ctx: ctx, params: params, reply: make(chan *acp.PermissionResult, 1)}
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
	req := questRequest{ctx: ctx, params: params, reply: make(chan *acp.QuestionResult, 1)}
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
