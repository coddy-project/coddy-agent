//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// registerGoalRoutes wires the session goal (docs/features/session-supervisor.md):
// read it, pause it or set its objective without starting a turn, clear it.
// Setting a goal and starting to work on it in one step is a prompt, /goal
// <objective>, sent like any other through POST /v1/responses; so is /goal
// resume.
func (s *Server) registerGoalRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/goal", s.coddyGoalGet)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}/goal", s.coddyGoalPatch)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/goal", s.coddyGoalDelete)
}

// goalPatchBody changes one thing: the status to paused, or the objective.
type goalPatchBody struct {
	Status    *string `json:"status,omitempty"`
	Objective *string `json:"objective,omitempty"`
}

// writeGoal answers with the goal as it now stands, in the shape of the
// session_goal frame of GET /coddy/events, version included, so a client
// applying both keeps whichever is newer.
func (s *Server) writeGoal(w http.ResponseWriter, id string) {
	u, err := s.mgr.SessionGoal(id)
	if err != nil {
		s.queueError(w, http.StatusNotFound, "not_found", err)
		return
	}
	writeJSON(w, http.StatusOK, goalPayload(u))
}

func goalPayload(u acp.SessionGoalUpdate) map[string]interface{} {
	return map[string]interface{}{
		"object":    "coddy.session_goal",
		"sessionId": u.SessionID,
		"goal":      u.Goal,
		"version":   u.Version,
		"notice":    u.Notice,
	}
}

func (s *Server) coddyGoalGet(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	s.writeGoal(w, id)
}

func (s *Server) coddyGoalPatch(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	var body goalPatchBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		s.queueError(w, http.StatusBadRequest, "invalid_request", errors.New("invalid JSON body"))
		return
	}
	switch {
	case body.Status != nil && body.Objective != nil, body.Status == nil && body.Objective == nil:
		s.queueError(w, http.StatusBadRequest, "invalid_request", errors.New(`send either "status" or "objective"`))
		return
	case body.Objective != nil:
		if _, err := s.mgr.SetGoalObjective(id, *body.Objective); err != nil {
			s.queueError(w, http.StatusBadRequest, "invalid_request", err)
			return
		}
	case strings.TrimSpace(*body.Status) == acp.GoalStatusPaused:
		if _, err := s.mgr.PauseGoal(id); err != nil {
			s.queueError(w, http.StatusConflict, "no_goal", err)
			return
		}
	default:
		s.queueError(w, http.StatusBadRequest, "invalid_request",
			errors.New(`only "paused" can be set here; resume a goal by sending /goal resume as a prompt`))
		return
	}
	s.writeGoal(w, id)
}

func (s *Server) coddyGoalDelete(w http.ResponseWriter, r *http.Request) {
	id, st := s.queueSession(w, r)
	if st == nil {
		return
	}
	if err := s.mgr.ClearGoal(id); err != nil {
		s.queueError(w, http.StatusNotFound, "not_found", err)
		return
	}
	s.writeGoal(w, id)
}

// sessionGoalFrame renders a change of a session's goal as one SSE frame of
// GET /coddy/events: the whole goal (null once cleared) with its version.
func sessionGoalFrame(u acp.SessionGoalUpdate) []byte {
	body, err := json.Marshal(goalPayload(u))
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+32)
	frame = append(frame, "event: session_goal\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishSessionGoalEvent is the Manager observer this server registers in
// New: a goal set in a console or checked by the supervisor shows in every
// browser tab, not only the one reading the turn's stream.
func (s *Server) publishSessionGoalEvent(u acp.SessionGoalUpdate) {
	if s.events == nil {
		return
	}
	if frame := sessionGoalFrame(u); frame != nil {
		s.events.publish(frame)
	}
}
