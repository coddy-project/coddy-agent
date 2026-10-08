package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// goalPayload is the session goal in the shape the server answers with
// everywhere: GET, PATCH and DELETE /coddy/sessions/{id}/goal, the goal of
// GET .../messages and the session_goal frame of GET /coddy/events.
type goalPayload struct {
	SessionID string           `json:"sessionId"`
	Goal      *acp.SessionGoal `json:"goal"`
	Version   uint64           `json:"version"`
	Notice    string           `json:"notice,omitempty"`
}

// update is the payload as the session update a local manager publishes.
func (p goalPayload) update(sessionID string) acp.SessionGoalUpdate {
	if p.SessionID != "" {
		sessionID = p.SessionID
	}
	return acp.SessionGoalUpdate{
		SessionUpdate: acp.UpdateTypeSessionGoal,
		SessionID:     sessionID,
		Goal:          p.Goal,
		Version:       p.Version,
		Notice:        p.Notice,
	}
}

func goalPath(sessionID string) string {
	return "/coddy/sessions/" + url.PathEscape(sessionID) + "/goal"
}

// mirrorGoal adopts a snapshot of the server's goal unless a newer one is
// held, and reports whether it did. Versions order the copies of one change
// that reach this client down several streams (a turn's, the events stream,
// a REST answer).
func (h *Handler) mirrorGoal(sessionID string, goal *acp.SessionGoal, version uint64) bool {
	st := h.session(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	if version != 0 && version < st.goalVersion {
		return false
	}
	st.goalVersion = version
	st.goal = cloneGoal(goal)
	return true
}

// cloneGoal copies a goal snapshot with its slices, so the mirror never
// shares them with an update handed to the surface.
func cloneGoal(g *acp.SessionGoal) *acp.SessionGoal {
	if g == nil {
		return nil
	}
	out := *g
	if g.LastCheck != nil {
		check := *g.LastCheck
		check.Remaining = append([]string(nil), check.Remaining...)
		out.LastCheck = &check
	}
	out.Checklist = append([]acp.GoalItem(nil), g.Checklist...)
	return &out
}

// heldGoal is the goal this client holds for a session as an update. h.mu is
// not held.
func (h *Handler) heldGoal(sessionID string) acp.SessionGoalUpdate {
	st := h.session(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	return acp.SessionGoalUpdate{
		SessionUpdate: acp.UpdateTypeSessionGoal,
		SessionID:     sessionID,
		Goal:          cloneGoal(st.goal),
		Version:       st.goalVersion,
	}
}

// SessionGoal returns the goal this client holds for a session: the last
// snapshot it adopted - a loaded session's, an update's, an answer's. The
// console shows it on entering the session; nothing goes over the network.
func (h *Handler) SessionGoal(sessionID string) (acp.SessionGoalUpdate, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionGoalUpdate{}, fmt.Errorf("remote: the goal needs a session id")
	}
	return h.heldGoal(sid), nil
}

// FetchSessionGoal reads the session's goal from the server (GET
// /coddy/sessions/{id}/goal) and mirrors it. A session the server has not
// created yet has none: it is pinned on its first prompt.
func (h *Handler) FetchSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionGoalUpdate{}, fmt.Errorf("remote: the goal needs a session id")
	}
	var out goalPayload
	err := h.getJSON(ctx, goalPath(sid), &out)
	if isNotFound(err) {
		return h.heldGoal(sid), nil
	}
	if err != nil {
		return acp.SessionGoalUpdate{}, err
	}
	return h.adoptGoalAnswer(sid, out), nil
}

// PauseSessionGoal stops the automatic continuations of the session's goal
// through PATCH /coddy/sessions/{id}/goal {"status":"paused"}, the setter the
// server's browser uses, and mirrors the goal it answers with. The change and
// its notice arrive on the events stream too, like a change another client
// makes.
func (h *Handler) PauseSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionGoalUpdate{}, fmt.Errorf("remote: the goal needs a session id")
	}
	var out goalPayload
	if err := h.patchJSONInto(ctx, goalPath(sid), map[string]string{"status": acp.GoalStatusPaused}, &out); err != nil {
		return acp.SessionGoalUpdate{}, err
	}
	return h.adoptGoalAnswer(sid, out), nil
}

// ClearSessionGoal removes the session's goal through DELETE
// /coddy/sessions/{id}/goal and mirrors the answer, which has no goal.
func (h *Handler) ClearSessionGoal(ctx context.Context, sessionID string) (acp.SessionGoalUpdate, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionGoalUpdate{}, fmt.Errorf("remote: the goal needs a session id")
	}
	var out goalPayload
	if err := h.deleteJSON(ctx, goalPath(sid), &out); err != nil {
		return acp.SessionGoalUpdate{}, err
	}
	return h.adoptGoalAnswer(sid, out), nil
}

// adoptGoalAnswer mirrors a REST answer and returns it as an update.
func (h *Handler) adoptGoalAnswer(sessionID string, out goalPayload) acp.SessionGoalUpdate {
	u := out.update(sessionID)
	h.mirrorGoal(u.SessionID, u.Goal, u.Version)
	return u
}

// applyGoalEvent forwards a server's event: session_goal to the surface,
// after mirroring it. A copy older than the goal held is still forwarded: its
// notice reports a change the surface has not shown, and the surface orders
// the snapshot by its version.
func (h *Handler) applyGoalEvent(data string) {
	var payload goalPayload
	if json.Unmarshal([]byte(data), &payload) != nil || payload.SessionID == "" {
		return
	}
	u := payload.update(payload.SessionID)
	h.mirrorGoal(u.SessionID, u.Goal, u.Version)
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(u.SessionID, u)
	}
}

// sendHeldGoal tells the surface the goal a session it just opened carries,
// as the local manager does on session ready.
func (h *Handler) sendHeldGoal(sessionID string) {
	u := h.heldGoal(sessionID)
	if u.Goal == nil {
		return
	}
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(sessionID, u)
	}
}
