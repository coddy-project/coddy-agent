package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"
)

func TestSupervisorModelControlsFeature(t *testing.T) {
	var result GoalCheckResult
	var callErr error
	suite := godog.TestSuite{
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/supervisor_model_controls.feature"}, TestingT: t},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the supervisor checks a goal through a Responses-only model with Flex service$`, func() error {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					if r.URL.Path != "/v1/responses" || body["service_tier"] != "flex" {
						http.Error(w, "wrong protocol or service tier", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"verdict\":\"met\"}"}]}]}`)
				}))
				defer srv.Close()
				cfg := judgeConfig(srv.URL)
				cfg.Models[0].Protocol = "responses"
				cfg.Models[0].ServiceTier = "flex"
				result, callErr = judgeSessionGoal(context.Background(), cfg, &State{ID: "supervisor-controls"}, GoalCheckRequest{Objective: "check the fix"})
				return nil
			})
			sc.Step(`^the goal check succeeds with the configured protocol and service tier$`, func() error {
				if callErr != nil {
					return callErr
				}
				if result.Verdict != GoalVerdictMet {
					return fmt.Errorf("verdict=%s", result.Verdict)
				}
				return nil
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("supervisor model controls failed")
	}
}
