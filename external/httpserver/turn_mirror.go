//go:build http

package httpserver

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// MirrorTurn publishes a turn this server did not start into the session's
// composer relay, so a browser watching that session follows it live.
//
// This is the path a Telegram message takes once the gateway and the API share
// a process. The chat keeps the turn: it renders the answer and it is the only
// surface a permission prompt or a question can be answered from, because it is
// the only one with somebody reading. The browser gets the same frames a turn
// started from the composer would have produced, and nothing else - a watcher
// must not be able to answer on the chat's behalf.
//
// It is the same mechanism a background wake turn already uses, which is why a
// tab attaches to it with no new endpoint.
func (s *Server) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	id := strings.TrimSpace(sessionID)
	if s == nil || id == "" {
		return primary, func() {}
	}
	// A relay already registered for this session belongs to a turn that is
	// still running, and beginComposerRelay would close it. That watcher would
	// lose its stream to a turn this one is about to fail anyway: the session
	// turn lock is taken after the mirror is set up, so a chat message arriving
	// during a browser's turn gets a busy answer moments later. Leave the
	// running turn's watchers alone and mirror nothing.
	if s.peekComposerRelay(id) != nil {
		return primary, func() {}
	}
	rel := s.beginComposerRelay(id)
	mode := ""
	sessionDir := ""
	if st := s.mgr.SessionByID(id); st != nil {
		mode = st.GetMode()
		sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	}
	watcher := NewRelaySender(s.activeCfg(), rel, mode)
	watcher.SetSessionDir(sessionDir)
	mirrored := &mirroredSender{primary: primary, watcher: watcher}
	return mirrored, func() {
		_ = watcher.FinishStream()
		s.endComposerRelay(id, rel)
	}
}

var _ session.TurnMirror = (*Server)(nil)

// mirroredSender fans session updates to the surface that started the turn and
// to the watchers, while everything that needs an answer stays with the surface
// that has a human in front of it.
type mirroredSender struct {
	primary acp.UpdateSender
	watcher acp.UpdateSender
}

func (m *mirroredSender) SendSessionUpdate(sessionID string, update interface{}) error {
	// The watcher is a spectator: its failures are its own, and they must not
	// turn into an error the chat sees.
	if m.watcher != nil {
		_ = m.watcher.SendSessionUpdate(sessionID, update)
	}
	if m.primary == nil {
		return nil
	}
	return m.primary.SendSessionUpdate(sessionID, update)
}

func (m *mirroredSender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if m.primary == nil {
		return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
	}
	return m.primary.RequestPermission(ctx, params)
}

func (m *mirroredSender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if m.primary == nil {
		return &acp.QuestionResult{}, nil
	}
	return m.primary.RequestQuestion(ctx, params)
}
