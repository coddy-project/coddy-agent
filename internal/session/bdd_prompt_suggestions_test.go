package session

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/cucumber/godog"
)

func TestPromptSuggestionsFeature(t *testing.T) {
	var mgr *Manager
	var state *State
	var calls *atomic.Int32
	var before []llm.Message
	var answers []string
	suite := godog.TestSuite{
		Name:    "prompt-suggestions",
		Options: &godog.Options{Format: "progress", Paths: []string{"../../features/prompt_suggestions.feature"}, TestingT: t},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a conversation with its first completed reply$`, func() error {
				mgr, state, calls = newSuggestionTestManager(t, answerSuggestion)
				state.ReplaceMessagesWithoutPersist(state.GetMessages()[:2])
				before, answers = state.GetMessages(), nil
				return nil
			})
			sc.Step(`^a finished conversation with prompt suggestions (unset|disabled)$`, func(setting string) error {
				mgr, state, calls = newSuggestionTestManager(t, answerSuggestion)
				if setting == "disabled" {
					off := false
					mgr.activeCfg().Agent.PromptSuggestions = &off
				}
				before, answers = state.GetMessages(), nil
				return nil
			})
			sc.Step(`^two surfaces request its next prompt$`, func() error {
				for range 2 {
					text, err := mgr.GeneratePromptSuggestion(context.Background(), state.ID)
					if err != nil && !IsPromptSuggestionSuppressed(err) {
						return err
					}
					answers = append(answers, text)
				}
				return nil
			})
			sc.Step(`^both surfaces offer "([^"]*)"$`, func(want string) error {
				if !reflect.DeepEqual(answers, []string{want, want}) {
					return fmt.Errorf("suggestions = %v", answers)
				}
				return nil
			})
			sc.Step(`^neither surface offers a suggestion$`, func() error {
				if !reflect.DeepEqual(answers, []string{"", ""}) {
					return fmt.Errorf("suggestions = %v", answers)
				}
				return nil
			})
			sc.Step(`^only one suggestion model call was made$`, func() error {
				if calls.Load() != 1 {
					return fmt.Errorf("model calls = %d", calls.Load())
				}
				return nil
			})
			sc.Step(`^no suggestion model call was made$`, func() error {
				if calls.Load() != 0 {
					return fmt.Errorf("model calls = %d", calls.Load())
				}
				return nil
			})
			sc.Step(`^the recorded conversation is unchanged$`, func() error {
				if !reflect.DeepEqual(before, state.GetMessages()) {
					return fmt.Errorf("suggestion changed the transcript")
				}
				return nil
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("prompt suggestion scenarios failed")
	}
}
