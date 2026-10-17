//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/tidwall/gjson"
)

func TestChatCompletionsAdaptiveSmallCap(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer backend.Close()
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: t.TempDir(), CWD: root},
		Providers: []config.ProviderConfig{{Name: "reviewer", Type: "anthropic", APIBase: backend.URL, APIKey: "test", Proxy: "none"}},
		Models: []config.ModelEntry{
			{Model: "reviewer/claude-opus-5-5", MaxTokens: 8192, ReasoningDefault: "high", AllowReasoningOff: true},
			{Model: "reviewer/claude-sonnet-4-5", MaxTokens: 8192, ReasoningDefault: "high"},
		},
		Agent: config.Agent{Model: "reviewer/claude-opus-5-5"},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, mgr, slog.Default(), root)
	defer srv.Drain()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		for _, model := range []string{"claude-opus-5-5", "claude-sonnet-4-5"} {
			body := fmt.Sprintf(`{"model":"reviewer/%s","messages":[{"role":"user","content":"hi"}],"%s":512,"stream":false}`, model, field)
			res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := ioReadAllClose(res.Body)
			wantStatus := http.StatusOK
			if model == "claude-sonnet-4-5" {
				wantStatus = http.StatusBadRequest
			}
			if res.StatusCode != wantStatus {
				t.Fatalf("%s/%s: status %d, want %d: %s", model, field, res.StatusCode, wantStatus, raw)
			}
		}
	}
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"model":"reviewer/claude-opus-5-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"off","stream":%v}`, stream)
		res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := ioReadAllClose(res.Body)
		// allow_reasoning_off cannot offer off for a model that always thinks.
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(gjson.GetBytes(raw, "error.message").String(), `reasoning_effort "off" is not offered`) {
			t.Fatalf("off (stream %v): status %d, want 400 off not offered: %s", stream, res.StatusCode, raw)
		}
		if sid := res.Header.Get("X-Coddy-Session-ID"); sid != "" {
			t.Errorf("off request created session %s", sid)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want two adaptive requests", len(requests))
	}
	for _, raw := range requests {
		if gjson.Get(raw, "max_tokens").Int() != 512 || gjson.Get(raw, "thinking.type").String() != "adaptive" || gjson.Get(raw, "thinking.budget_tokens").Exists() || gjson.Get(raw, "output_config.effort").String() != "high" || gjson.Get(raw, "thinking.display").String() != "summarized" {
			t.Errorf("adaptive cap or effort changed: %s", raw)
		}
	}
}
