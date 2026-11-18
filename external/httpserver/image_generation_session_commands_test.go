//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestImageGenerationSessionCommandsAreTransactional(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(endpoint+fmt.Sprint(stream), func(t *testing.T) {
				stand := newChatImageStand(t, false)
				cfg := stand.srv.mgr.Cfg()
				cfg.Models = append(cfg.Models, config.ModelEntry{Model: "local/disallowed", MaxContextTokens: 128000, MaxTokens: 1000})
				created, err := stand.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				st := stand.srv.mgr.SessionByID(created.SessionID)
				st.SetSelectedModelID("local/gpt-test")
				st.ArmTurnOverride(session.SettingModel, "local/gpt-test", 2)
				before := st.TurnOverrides()
				var changes []acp.SessionSettingsUpdate
				remove := stand.srv.mgr.AddSessionSettingsObserver(func(change acp.SessionSettingsUpdate) { changes = append(changes, change) })
				defer remove()
				text := "/model local/disallowed /plan draw"
				payload := map[string]any{"model": "agent", "stream": stream, "metadata": map[string]any{"image_generation": true}}
				if endpoint == "/v1/responses" {
					payload["input"] = text
				} else {
					payload["messages"] = []map[string]string{{"role": "user", "content": text}}
				}
				data, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Coddy-Session-ID", created.SessionID)
				response := httptest.NewRecorder()
				stand.srv.Handler().ServeHTTP(response, req)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status %d: %s", response.Code, response.Body.String())
				}
				if st.GetSelectedModelID() != "local/gpt-test" || st.GetMode() != "agent" || !reflect.DeepEqual(before, st.TurnOverrides()) {
					t.Fatalf("rejection changed session settings: model=%s mode=%s overrides=%+v", st.GetSelectedModelID(), st.GetMode(), st.TurnOverrides())
				}
				if len(changes) != 0 || stand.requests.Load() != 0 || len(st.GetMessages()) != 0 {
					t.Fatalf("rejection published or ran work: changes=%d provider=%d messages=%d", len(changes), stand.requests.Load(), len(st.GetMessages()))
				}
			})
		}
	}
}

func TestImageGenerationSessionCommandsAndMetadataSemantics(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, tc := range []struct {
			name, initial, text, metadataModel, selected string
			status                                       int
		}{
			{name: "accepted-session-model", initial: "local/disallowed", text: "/model local/gpt-test draw", selected: "local/gpt-test", status: http.StatusOK},
			{name: "session-model-with-once", initial: "local/gpt-test", text: "/model local/disallowed /model local/gpt-test --once draw", selected: "local/disallowed", status: http.StatusOK},
			{name: "metadata-remains-immediate", initial: "local/gpt-test", text: "draw", metadataModel: "local/disallowed", selected: "local/disallowed", status: http.StatusBadRequest},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				stand := newChatImageStand(t, false)
				cfg := stand.srv.mgr.Cfg()
				cfg.Models = append(cfg.Models, config.ModelEntry{Model: "local/disallowed", MaxContextTokens: 128000, MaxTokens: 1000})
				created, err := stand.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				st := stand.srv.mgr.SessionByID(created.SessionID)
				st.SetSelectedModelID(tc.initial)
				metadata := map[string]any{"image_generation": true}
				if tc.metadataModel != "" {
					metadata["model"] = tc.metadataModel
				}
				payload := map[string]any{"model": "agent", "metadata": metadata}
				if endpoint == "/v1/responses" {
					payload["input"] = tc.text
				} else {
					payload["messages"] = []map[string]string{{"role": "user", "content": tc.text}}
				}
				data, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Coddy-Session-ID", created.SessionID)
				response := httptest.NewRecorder()
				stand.srv.Handler().ServeHTTP(response, req)
				if response.Code != tc.status {
					t.Fatalf("status %d want %d: %s", response.Code, tc.status, response.Body.String())
				}
				if st.GetSelectedModelID() != tc.selected {
					t.Fatalf("selected model %s want %s", st.GetSelectedModelID(), tc.selected)
				}
				if len(st.TurnOverrides()) != 0 || st.TurnSetting(session.SettingModel) != "" {
					t.Fatalf("turn settings leaked: %+v", st.TurnOverrides())
				}
			})
		}
	}
}
