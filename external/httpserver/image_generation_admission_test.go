//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestNativeImageGenerationTurnModelAdmission(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, stream := range []bool{false, true} {
			for _, allowed := range []bool{false, true} {
				for _, inline := range []bool{false, true} {
					t.Run(fmt.Sprint(inline)+endpoint+"/"+fmt.Sprint(stream)+"/actualAllowed="+fmt.Sprint(allowed), func(t *testing.T) {
						stand := newChatImageStand(t, false)
						cfg := stand.srv.mgr.Cfg()
						cfg.Models = append(cfg.Models, config.ModelEntry{Model: "local/disallowed", MaxContextTokens: 128000, MaxTokens: 1000})
						created, err := stand.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						st := stand.srv.mgr.SessionByID(created.SessionID)
						base, actual := "local/gpt-test", "local/disallowed"
						if allowed {
							base, actual = actual, base
						}
						st.SetSelectedModelID(base)
						text := "Draw a square"
						if inline {
							text = "/model " + actual + " --once " + text
						} else {
							st.ArmTurnOverride(session.SettingModel, actual, 1)
						}
						payload := map[string]any{"model": "agent", "stream": stream, "metadata": map[string]any{"image_generation": true}}
						if endpoint == "/v1/responses" {
							payload["input"] = text
						} else {
							payload["messages"] = []map[string]string{{"role": "user", "content": text}}
						}
						data, _ := json.Marshal(payload)
						req, _ := http.NewRequest(http.MethodPost, stand.server.URL+endpoint, strings.NewReader(string(data)))
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("X-Coddy-Session-ID", created.SessionID)
						res, err := stand.server.Client().Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(res.Body)
						_ = res.Body.Close()
						if err != nil {
							t.Fatal(err)
						}
						want := http.StatusBadRequest
						if allowed {
							want = http.StatusOK
						}
						if res.StatusCode != want {
							t.Fatalf("status %d, want %d: %s", res.StatusCode, want, body)
						}
						if !allowed {
							if !strings.Contains(string(body), "metadata.image_generation") || !strings.Contains(string(body), actual) {
								t.Fatalf("missing validation error: %s", body)
							}
							if stand.requests.Load() != 0 {
								t.Fatal("rejected request reached provider")
							}
							overrides := st.TurnOverrides()
							if (!inline && (len(overrides) != 1 || overrides[0].TurnsLeft != 1 || overrides[0].Active)) || (inline && len(overrides) != 0) {
								t.Fatalf("rejection consumed override: %+v", overrides)
							}
						} else if len(st.TurnOverrides()) != 0 {
							t.Fatalf("accepted turn did not consume override: %+v", st.TurnOverrides())
						}
						if st.GetSelectedModelID() != base {
							t.Fatal("session model changed")
						}
						unlock, err := stand.srv.mgr.AcquireComposerTurnLock(created.SessionID, st)
						if err != nil {
							t.Fatalf("turn lock leaked: %v", err)
						}
						unlock()
					})
				}
			}
		}
	}
}

func TestNativeImageGenerationBusyDoesNotConsumeOverride(t *testing.T) {
	for _, endpoint := range []string{"/v1/responses", "/v1/chat/completions"} {
		t.Run(endpoint, func(t *testing.T) {
			stand := newChatImageStand(t, false)
			created, err := stand.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			st := stand.srv.mgr.SessionByID(created.SessionID)
			st.ArmTurnOverride(session.SettingModel, "local/gpt-test", 2)
			before := st.TurnOverrides()
			unlock, err := stand.srv.mgr.AcquireComposerTurnLock(created.SessionID, st)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			payload := `{"model":"agent","input":"draw","stream":true,"metadata":{"image_generation":true}}`
			if endpoint == "/v1/chat/completions" {
				payload = `{"model":"agent","messages":[{"role":"user","content":"draw"}],"stream":true,"metadata":{"image_generation":true}}`
			}
			req := httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Coddy-Session-ID", created.SessionID)
			response := httptest.NewRecorder()
			stand.srv.Handler().ServeHTTP(response, req)
			if response.Code != http.StatusConflict {
				t.Fatalf("busy status %d: %s", response.Code, response.Body.String())
			}
			if !reflect.DeepEqual(before, st.TurnOverrides()) || stand.requests.Load() != 0 {
				t.Fatal("busy request consumed an override or reached provider")
			}
		})
	}
}

func TestNativeImageGenerationSettingsOnlyArmsWithoutConsumption(t *testing.T) {
	stand := newChatImageStand(t, false)
	created, err := stand.srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"agent","input":"/model local/gpt-test --once","metadata":{"image_generation":true}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", created.SessionID)
	response := httptest.NewRecorder()
	stand.srv.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("settings-only response %d: %s", response.Code, response.Body.String())
	}
	overrides := stand.srv.mgr.SessionByID(created.SessionID).TurnOverrides()
	if len(overrides) != 1 || overrides[0].TurnsLeft != 1 || overrides[0].Active || stand.requests.Load() != 0 {
		t.Fatalf("settings-only prompt ran or consumed an override: %+v", overrides)
	}
}
