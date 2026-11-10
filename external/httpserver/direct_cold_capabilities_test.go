//go:build http

package httpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/tidwall/gjson"
)

func TestColdDirectCodexCatalogEffort(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(endpoint, func(t *testing.T) {
			var lists, inferences atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				inferences.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/responses" || gjson.GetBytes(raw, "reasoning.effort").String() != "xhigh" {
					t.Errorf("wire=%s %s", r.URL.Path, raw)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
			}))
			defer upstream.Close()
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}, {Name: "other", Type: "openai"}}, Models: []config.ModelEntry{{Model: "oauth/gpt-5.5"}, {Model: "other/test"}}}
			mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), t.TempDir(), nil)
			mgr.SetContextWindowLister(func(_ context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error) {
				lists.Add(1)
				if in.Name != "oauth" {
					t.Errorf("unexpected provider %s", in.Name)
				}
				levels := []string{"low", "xhigh"}
				return []llm.ModelEntry{{ID: "gpt-5.5", ReasoningLevels: &levels}}, nil
			}, nil)
			t.Cleanup(func() {
				if err := mgr.WaitContextWindowsIdle(time.Second); err != nil {
					t.Error(err)
				}
			})
			srv := New(cfg, mgr, slog.Default(), t.TempDir())
			defer srv.Drain()
			srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
				in := llm.ProviderInput{Type: "openai", Model: "gpt-5.5", Protocol: "responses", BaseURL: upstream.URL, RetryDisabled: true}
				opts.Apply(&in)
				return llm.NewProvider(in)
			}
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			body := `{"model":"oauth/gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`
			if endpoint == "/v1/responses" {
				body = `{"model":"oauth/gpt-5.5","stream":true,"input":"hi","reasoning":{"effort":"xhigh"}}`
			}
			for _, valid := range []bool{true, false} {
				request := body
				if !valid {
					request = strings.ReplaceAll(body, "xhigh", "invented")
				}
				res, err := http.Post(ts.URL+endpoint, "application/json", strings.NewReader(request))
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := ioReadAllClose(res.Body)
				want := 200
				if !valid {
					want = 400
				}
				if res.StatusCode != want {
					t.Fatalf("status=%d want=%d: %s", res.StatusCode, want, raw)
				}
			}
			if lists.Load() != 1 || inferences.Load() != 1 {
				t.Fatalf("catalog=%d inference=%d", lists.Load(), inferences.Load())
			}
		})
	}
}

func TestDirectCapabilityWarmupSkipsExplicitAndNonCodex(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		levels     *[]string
	}{
		{"explicit", "codex", &[]string{"xhigh"}}, {"disabled", "codex", &[]string{}}, {"gateway", "openai", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "p", Type: tc.kind, APIBase: "https://unused.example"}}, Models: []config.ModelEntry{{Model: "p/gpt-5.5", ReasoningLevels: tc.levels}}}
			mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), t.TempDir(), nil)
			var calls atomic.Int32
			mgr.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) { calls.Add(1); return nil, nil }, nil)
			srv := New(cfg, mgr, slog.Default(), t.TempDir())
			defer srv.Drain()
			got := srv.directCapabilityConfig(context.Background(), cfg, "p/gpt-5.5")
			if calls.Load() != 0 {
				t.Fatal("unnecessary catalog read")
			}
			if got.Models[0].ReasoningLevels != tc.levels {
				t.Fatal("explicit list changed")
			}
		})
	}
}

func TestDirectCapabilityWarmupBoundsAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "p", Type: "codex"}}, Models: []config.ModelEntry{{Model: "p/gpt-5.5"}}}
			mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), t.TempDir(), nil)
			started, release := make(chan struct{}), make(chan struct{})
			mgr.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
				close(started)
				<-release
				return nil, nil
			}, nil)
			t.Cleanup(func() {
				close(release)
				if err := mgr.WaitContextWindowsIdle(time.Second); err != nil {
					t.Error(err)
				}
			})
			srv := New(cfg, mgr, slog.Default(), t.TempDir())
			defer srv.Drain()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *config.Config, 1)
			began := time.Now()
			go func() { done <- srv.directCapabilityConfig(ctx, cfg, "p/gpt-5.5") }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("catalog not requested")
			}
			if cancelled {
				cancel()
			}
			limit := session.ContextWindowWait + time.Second
			if cancelled {
				limit = time.Second
			}
			select {
			case got := <-done:
				if got.Models[0].ReasoningLevels != nil || cfg.Models[0].ReasoningLevels != nil {
					t.Fatal("mutated configured capabilities")
				}
				if !cancelled && time.Since(began) < session.ContextWindowWait {
					t.Fatal("did not wait for bounded discovery")
				}
			case <-time.After(limit):
				t.Fatal("capability wait exceeded caller bound")
			}
		})
	}
}
