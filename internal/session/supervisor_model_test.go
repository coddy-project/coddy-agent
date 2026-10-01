package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestSupervisorModelChecksGoalWithoutTools(t *testing.T) {
	var request struct {
		Messages []map[string]interface{} `json:"messages"`
		Tools    []interface{}            `json:"tools"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"{\"done\":true,\"remaining\":\"\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	cfg := &config.Config{
		Providers:  []config.ProviderConfig{{Name: "test", Type: "openai", APIKey: "key", APIBase: server.URL + "/v1"}},
		Models:     []config.ModelEntry{{Model: "test/test-model"}},
		Supervisor: config.Supervisor{Model: "test/test-model"},
	}
	st := &State{ID: "sess_model", Mode: ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "ship the fix"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
	got, err := judgeSessionGoal(context.Background(), cfg, st, "ship the fix")
	if err != nil || !got.Done {
		t.Fatalf("verdict=%+v err=%v", got, err)
	}
	if len(request.Tools) != 0 || len(request.Messages) != 2 || !strings.Contains(request.Messages[1]["content"].(string), "ship the fix") {
		t.Fatalf("supervisor request = %+v", request)
	}
}
