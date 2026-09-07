//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestDesignPlansCRUD(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	newRes, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := newRes.SessionID

	createBody, _ := json.Marshal(map[string]string{"slug": "demo", "content": plans.DefaultContent("demo", "Demo")})
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+id+"/plans", bytes.NewReader(createBody))
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+id+"/plans", nil)
	req.SetPathValue("id", id)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+id+"/plans/demo", nil)
	req.SetPathValue("id", id)
	req.SetPathValue("slug", "demo")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	putBody, _ := json.Marshal(map[string]string{"body": "# Updated\n\nOnly body changed."})
	req = httptest.NewRequest(http.MethodPut, "/coddy/sessions/"+id+"/plans/demo", bytes.NewReader(putBody))
	req.SetPathValue("id", id)
	req.SetPathValue("slug", "demo")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put body: %d %s", rec.Code, rec.Body.String())
	}
	got, err := plans.Read(filepath.Join(root, "sessions", id), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "# Updated\n\nOnly body changed." {
		t.Fatalf("body: %q", got.Body)
	}
	if got.Name != "Demo" {
		t.Fatalf("name should be preserved: %q", got.Name)
	}

	req = httptest.NewRequest(http.MethodDelete, "/coddy/sessions/"+id+"/plans/demo", nil)
	req.SetPathValue("id", id)
	req.SetPathValue("slug", "demo")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestDesignPlanPutBodyBootstrapFromTranscript(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	newRes, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := newRes.SessionID
	st := mgr.SessionByID(id)
	if st == nil {
		t.Fatal("session missing")
	}
	bootstrap := plans.DefaultContent("orphan-plan", "Orphan plan")
	st.AppendPlanDocument(plans.Document{
		Slug:    "orphan-plan",
		Name:    "Orphan plan",
		Content: bootstrap,
		Body:    "# Draft\n",
	})

	putBody, _ := json.Marshal(map[string]string{
		"body":    "# Edited in markdown\n",
		"content": bootstrap,
	})
	req := httptest.NewRequest(http.MethodPut, "/coddy/sessions/"+id+"/plans/orphan-plan", bytes.NewReader(putBody))
	req.SetPathValue("id", id)
	req.SetPathValue("slug", "orphan-plan")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put body bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	sd := filepath.Join(root, "sessions", id)
	got, err := plans.Read(sd, "orphan-plan")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "# Edited in markdown" {
		t.Fatalf("body: %q", got.Body)
	}
	msgs := mgr.SessionByID(id).GetMessages()
	var found bool
	for _, m := range msgs {
		if m.PlanDocument == nil || m.PlanDocument.Slug != "orphan-plan" {
			continue
		}
		found = true
		if m.PlanDocument.Body != "# Edited in markdown" {
			t.Fatalf("transcript body: %q", m.PlanDocument.Body)
		}
		if !strings.Contains(m.PlanDocument.Content, "name: Orphan plan") {
			t.Fatal("transcript content not updated")
		}
	}
	if !found {
		t.Fatal("plan_document row missing")
	}
}

// The explicit run route is gated the same way as the runPlanSlug prompt
// metadata: a read-only ask session answers 409 and keeps its mode instead of
// silently escalating to agent.
func TestDesignPlanRunRefusedInAskMode(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	runs := 0
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		runs++
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	newRes, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := newRes.SessionID
	st := mgr.SessionByID(id)
	st.SetMode(string(session.ModeAsk))

	createBody, _ := json.Marshal(map[string]string{"slug": "demo", "content": plans.DefaultContent("demo", "Demo")})
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+id+"/plans", bytes.NewReader(createBody))
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/coddy/sessions/"+id+"/plans/demo", strings.NewReader(`{"runPlan":true}`))
	req.SetPathValue("id", id)
	req.SetPathValue("slug", "demo")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("run in ask mode: status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ask mode") {
		t.Fatalf("error does not explain the refusal: %s", rec.Body.String())
	}
	if runs != 0 {
		t.Fatalf("the plan ran %d time(s) in ask mode", runs)
	}
	if got := st.GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("mode switched to %q", got)
	}
}
