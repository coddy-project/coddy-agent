//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type planRunNoopSender struct{}

func (planRunNoopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (planRunNoopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (planRunNoopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (s *Server) registerDesignPlanRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/plans", s.coddyDesignPlansList)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/plans", s.coddyDesignPlansCreate)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/plans/{slug}", s.coddyDesignPlanGet)
	s.mux.HandleFunc("PUT /coddy/sessions/{id}/plans/{slug}", s.coddyDesignPlanPut)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}/plans/{slug}", s.coddyDesignPlanPatch)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/plans/{slug}", s.coddyDesignPlanDelete)
}

func (s *Server) coddyDesignPlansList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return
	}
	items, err := plans.List(sd)
	if err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.design_plans",
		"plans":  items,
	})
}

func (s *Server) coddyDesignPlansCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Slug    string `json:"slug"`
		Content string `json:"content,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return
	}
	doc, err := plans.Create(sd, strings.TrimSpace(body.Slug), body.Content)
	if err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	st.AppendPlanDocument(*doc)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(designPlanResponse(doc))
}

func (s *Server) coddyDesignPlanGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return
	}
	doc, err := plans.Read(sd, slug)
	if err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(designPlanResponse(doc))
}

func (s *Server) coddyDesignPlanPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	var reqBody struct {
		Content *string `json:"content,omitempty"`
		Body    *string `json:"body,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return
	}
	var doc *plans.Document
	var err error
	switch {
	case reqBody.Body != nil:
		bootstrap := ""
		if reqBody.Content != nil {
			bootstrap = *reqBody.Content
		} else {
			bootstrap = st.PlanDocumentContentBySlug(slug)
		}
		doc, err = plans.WriteBodyWithFallback(sd, slug, *reqBody.Body, bootstrap)
	case reqBody.Content != nil && strings.TrimSpace(*reqBody.Content) != "":
		doc, err = plans.Write(sd, slug, *reqBody.Content)
	default:
		http.Error(w, `{"error":{"message":"content or body required"}}`, http.StatusBadRequest)
		return
	}
	if err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	st.UpdatePlanDocumentFromWrite(*doc)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.design_plan_updated",
		"plan":   designPlanResponse(doc),
	})
}

func (s *Server) coddyDesignPlanPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	var body struct {
		RunPlan bool `json:"runPlan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}
	if !body.RunPlan {
		http.Error(w, `{"error":{"message":"unsupported patch"}}`, http.StatusBadRequest)
		return
	}
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	// Running a plan is a turn; a child session's transcript is read-only.
	if rejectSubagentTurn(w, st) {
		return
	}
	result, err := s.mgr.RunPlan(r.Context(), id, slug, planRunNoopSender{})
	if err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":     "coddy.design_plan_run",
		"stopReason": result.StopReason,
	})
}

func (s *Server) coddyDesignPlanDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	slug := strings.TrimSpace(r.PathValue("slug"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	if sd == "" {
		http.Error(w, `{"error":{"message":"session not persisted"}}`, http.StatusBadRequest)
		return
	}
	if err := plans.Delete(sd, slug); err != nil {
		s.coddyPlanHTTPError(w, err)
		return
	}
	st.MarkPlanDocumentDiscarded(slug)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"object": "coddy.design_plan_deleted",
		"slug":   slug,
	})
}

func designPlanResponse(doc *plans.Document) map[string]interface{} {
	out := map[string]interface{}{
		"slug":    doc.Slug,
		"name":    doc.Name,
		"content": doc.Content,
		"body":    doc.Body,
	}
	if o := strings.TrimSpace(doc.Overview); o != "" {
		out["overview"] = o
	}
	if len(doc.Todos) > 0 {
		out["todos"] = doc.Todos
	}
	if !doc.UpdatedAt.IsZero() {
		out["updatedAt"] = doc.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}

func (s *Server) coddyPlanHTTPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plans.ErrNotFound):
		http.Error(w, `{"error":{"message":"plan not found"}}`, http.StatusNotFound)
	case errors.Is(err, plans.ErrExists):
		http.Error(w, `{"error":{"message":"plan already exists"}}`, http.StatusConflict)
	case errors.Is(err, plans.ErrInvalidSlug):
		http.Error(w, `{"error":{"message":"invalid plan slug"}}`, http.StatusBadRequest)
	case errors.Is(err, session.ErrSessionTurnBusy):
		http.Error(w, `{"error":{"message":"session busy"}}`, http.StatusConflict)
	case isSubagentReadOnly(err):
		writeSubagentsError(w, http.StatusConflict, err.Error())
	default:
		s.log.Error("design plan", "error", err)
		http.Error(w, `{"error":{"message":"request failed"}}`, http.StatusInternalServerError)
	}
}
