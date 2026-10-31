package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	var request atomic.Value
	var mainContext []llm.Message
	suite := godog.TestSuite{
		Name:    "prompt-suggestions",
		Options: &godog.Options{Format: "progress", Paths: []string{"../../features/prompt_suggestions.feature"}, TestingT: t},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a finished conversation with a main agent context$`, func() {
				mgr, state, calls = newSuggestionTestManager(t, func(w http.ResponseWriter, r *http.Request) {
					var captured suggestionRequest
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					request.Store(captured)
					answerSuggestion(w, r)
				})
				mainContext = []llm.Message{
					{Role: llm.RoleSystem, Content: "MAIN_SYSTEM_PROMPT"},
					{Role: llm.RoleUser, Content: "FIRST_REQUEST_MARKER"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", InputJSON: `{"path":"README.md"}`}}},
					{Role: llm.RoleTool, ToolCallID: "call_1", Content: "TOOL_RESULT_MARKER"},
					{Role: llm.RoleAssistant, Content: "completed the last step"},
				}
				state.ReplaceMessagesWithoutPersist(mainContext[1:])
				state.SetPromptSuggestionFork(mainContext, []llm.ToolDefinition{{Name: "read", Description: "Read a file", InputSchema: map[string]interface{}{"type": "object"}}})
				before, answers = state.GetMessages(), nil
			})
			sc.Step(`^the prediction request inherits the complete main context$`, func() error {
				captured, _ := request.Load().(suggestionRequest)
				if err := validateSuggestionMainContext(captured, mainContext); err != nil {
					return err
				}
				if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "read" {
					return fmt.Errorf("prediction tools = %#v, want the main agent tools", captured.Tools)
				}
				return nil
			})
			sc.Step(`^a conversation with its first completed reply$`, func() error {
				mgr, state, calls = newSuggestionTestManager(t, answerSuggestion)
				state.ReplaceMessagesWithoutPersist(state.GetMessages()[:2])
				setSuggestionTestFork(state)
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
