//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func unknownModelResponse() *httptest.ResponseRecorder {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"missing/model","messages":[{"role":"user","content":"hi"}]}`)))
	return w
}

func checkUnknownModel(w *httptest.ResponseRecorder) error {
	if w.Code != http.StatusNotFound || w.Header().Get("Content-Type") != "application/json" {
		return fmt.Errorf("status %d, content type %q, body %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	var body struct {
		Error struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return err
	}
	if body.Error.Type != "invalid_request_error" || body.Error.Code != "model_not_found" || body.Error.Param != "model" {
		return fmt.Errorf("unexpected error: %s", w.Body.String())
	}
	return nil
}

func TestOpenAIErrorShapeFeature(t *testing.T) {
	var w *httptest.ResponseRecorder
	suite := godog.TestSuite{Name: "openai-error-shape", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an OpenAI client requests an unknown model$`, func() { w = unknownModelResponse() })
		sc.Step(`^it receives a JSON model_not_found error with status 404$`, func() error { return checkUnknownModel(w) })
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_error_shape.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("feature failed")
	}
}

func TestOpenAIUnknownModelError(t *testing.T) {
	if err := checkUnknownModel(unknownModelResponse()); err != nil {
		t.Fatal(err)
	}
}
