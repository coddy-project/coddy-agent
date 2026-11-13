//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type selectedTitleModelState struct {
	sessTagsState
	selected, used string
	defaultCalls   int
}

func (s *selectedTitleModelState) unavailableDefault() error {
	s.srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		s.defaultCalls++
		return nil, errors.New("default credential revoked")
	}
	return nil
}

func (s *selectedTitleModelState) selectedAnswers(model, title, tags string) error {
	s.selected = model
	s.srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		s.used = model
		if model != s.selected {
			return nil, fmt.Errorf("unexpected model %q", model)
		}
		return fakeProvider{reply: title + "\ntags: " + tags}, nil
	}
	return nil
}

func (s *selectedTitleModelState) newChatDescribes(text, model string) (retErr error) {
	id := session.NewSessionID()
	if s.mgr.SessionByID(id) != nil {
		return fmt.Errorf("session %q already exists", id)
	}
	body, err := json.Marshal(map[string]string{"text": text, "model": model})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/describe", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", id)
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, res.Body.Close()) }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("describe returned %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(&s.describe)
}

func (s *selectedTitleModelState) selectedWasUsed() error {
	if s.used != s.selected || s.defaultCalls != 0 {
		return fmt.Errorf("model %q used, default called %d times; want %q and no default calls", s.used, s.defaultCalls, s.selected)
	}
	return nil
}

func TestSessionTitleSelectedModelFeature(t *testing.T) {
	s := &selectedTitleModelState{}
	suite := godog.TestSuite{
		Name: "session-title-selected-model",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = selectedTitleModelState{}
				return ctx, s.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a running coddy HTTP server$`, s.startServer)
			sc.Step(`^the default title model is unavailable$`, s.unavailableDefault)
			sc.Step(`^the selected title model "([^"]*)" answers "([^"]*)" with tags "([^"]*)"$`, s.selectedAnswers)
			sc.Step(`^a new chat asks for a description of "([^"]*)" with model "([^"]*)"$`, s.newChatDescribes)
			sc.Step(`^the selected title model was used instead of the default$`, s.selectedWasUsed)
			sc.Step(`^the description is "([^"]*)"$`, s.descriptionIs)
			sc.Step(`^the description proposes tags "([^"]*)"$`, s.descriptionProposesTags)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_title_selected_model.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("session title selected model feature failed")
	}
}
