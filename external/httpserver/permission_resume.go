//go:build http

package httpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// tryResumePendingPermission continues a persisted permission gate after HTTP restart or a dead stream.
func (s *Server) tryResumePendingPermission(ctx context.Context, sessionID, toolCallID string, res *acp.PermissionResult) bool {
	sessionID = strings.TrimSpace(sessionID)
	toolCallID = strings.TrimSpace(toolCallID)
	if sessionID == "" || toolCallID == "" || res == nil {
		return false
	}
	st := s.persistedSessionState(ctx, sessionID)
	if st == nil {
		return false
	}
	// A child session's prompts are relayed to its parent, so it never has a
	// pending gate of its own; and a resume would run an agent on a read-only
	// transcript.
	if st.IsSubagentRun() {
		return false
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		return false
	}
	pending, err := session.ReadPendingPermission(sd)
	if err != nil || pending == nil {
		return false
	}
	if strings.TrimSpace(pending.ToolCall.ToolCallID) != toolCallID {
		return false
	}
	s.permissionResumeWG.Add(1)
	go func() {
		defer s.permissionResumeWG.Done()
		s.runPermissionResume(context.WithoutCancel(ctx), sessionID, toolCallID, res)
	}()
	return true
}

// persistedSessionState returns the live state for a valid session id, loading
// the bundle into the manager when only the disk has it. It returns nil when
// neither exists or the load fails; callers treat that as "nothing to act on".
func (s *Server) persistedSessionState(ctx context.Context, sessionID string) *session.State {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	if st := s.mgr.SessionByID(sessionID); st != nil {
		return st
	}
	fs := s.mgr.FileStore()
	if fs == nil || !fs.HasPersistedSnapshot(sessionID) {
		return nil
	}
	if _, err := s.mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{
		SessionID: sessionID,
		CWD:       s.defaultCWD,
	}); err != nil {
		return nil
	}
	return s.mgr.SessionByID(sessionID)
}

// waitPermissionResumeDrained blocks until in-flight persisted permission resume goroutines finish.
func (s *Server) waitPermissionResumeDrained() {
	if s == nil {
		return
	}
	s.permissionResumeWG.Wait()
}

func (s *Server) runPermissionResume(ctx context.Context, sessionID, toolCallID string, res *acp.PermissionResult) {
	st := s.mgr.SessionByID(sessionID)
	if st == nil {
		return
	}
	// This turn does not go through HandleSessionPromptWithSender, so it is
	// admitted through the manager's shared path: the turn lock, the
	// registration a watching client sees, the cancel a deletion uses, and the
	// refusal while the session is being deleted.
	turnCtx, finish, err := s.mgr.BeginTurn(ctx, sessionID, nil)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrSessionTurnBusy):
			s.log.Warn("permission resume: session busy", "session", sessionID)
		case errors.Is(err, session.ErrSessionDeleting):
			s.log.Warn("permission resume: session is being deleted", "session", sessionID)
		default:
			s.log.Warn("permission resume: admission", "session", sessionID, "error", err)
		}
		return
	}
	defer finish()
	ctx = turnCtx

	// A resumed turn is one somebody is watching by definition - they just answered its
	// permission prompt - so it publishes like any other composer turn. The sender stays
	// non-interactive: this turn has no HTTP response of its own for a client to read.
	rel := s.beginComposerRelay(sessionID)
	defer s.endComposerRelay(sessionID, rel)
	bridge := NewRelaySender(s.activeCfg(), rel, st.GetMode())
	bridge.SetSessionDir(strings.TrimSpace(st.GetPersistedSessionDir()))
	defer func() { _ = bridge.FinishStream() }()
	ag := agent.NewAgent(s.activeCfg(), st, bridge, s.log)
	ag.SetConfigReloader(func(ctx context.Context) ([]string, error) {
		warnings, err := s.mgr.ReloadConfigForSession(ctx, st)
		if err == nil {
			s.ReplaceConfig(s.mgr.Cfg())
			s.invalidateSlashCache()
		}
		return warnings, err
	})
	ag.SetProviderFactory(s.agentProviderFactory)
	// A resumed turn keeps running the ReAct loop, so it may spawn subagents
	// like the turn it continues.
	ag.SetSubagentRuntime(s.mgr)
	if _, err := ag.ResumeAfterPermission(ctx, toolCallID, res); err != nil {
		s.log.Warn("permission resume failed", "session", sessionID, "toolCallId", toolCallID, "error", err)
		return
	}
	if fs := s.mgr.FileStore(); fs != nil {
		if err := fs.Save(st); err != nil {
			s.log.Warn("permission resume persist", "session", sessionID, "error", err)
		}
	}
	st.BumpActivitySeq()
}
