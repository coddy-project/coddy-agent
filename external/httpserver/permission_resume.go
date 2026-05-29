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
	st := s.mgr.SessionByID(sessionID)
	if st == nil {
		fs := s.mgr.FileStore()
		if fs == nil || !fs.HasPersistedSnapshot(sessionID) {
			return false
		}
		if _, err := s.mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{
			SessionID: sessionID,
			CWD:       s.defaultCWD,
		}); err != nil {
			return false
		}
		st = s.mgr.SessionByID(sessionID)
	}
	if st == nil {
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
	go s.runPermissionResume(context.WithoutCancel(ctx), sessionID, toolCallID, res)
	return true
}

func (s *Server) runPermissionResume(ctx context.Context, sessionID, toolCallID string, res *acp.PermissionResult) {
	st := s.mgr.SessionByID(sessionID)
	if st == nil {
		return
	}
	unlock, err := s.mgr.AcquireComposerTurnLock(sessionID, st)
	if err != nil {
		if errors.Is(err, session.ErrSessionTurnBusy) {
			s.log.Warn("permission resume: session busy", "session", sessionID)
		} else {
			s.log.Warn("permission resume: lock", "session", sessionID, "error", err)
		}
		return
	}
	defer unlock()

	bridge := NewSender(s.activeCfg(), nil, false, st.GetMode())
	bridge.SetSessionDir(strings.TrimSpace(st.GetPersistedSessionDir()))
	ag := agent.NewAgent(s.activeCfg(), st, bridge, s.log)
	ag.SetProviderFactory(s.agentProviderFactory)
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
