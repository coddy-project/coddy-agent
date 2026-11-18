//go:build http

package httpserver

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. Here, what Coddy adds to one direct completion (issue #585): the
// provider answers at once, so a run measures the request, the session and the
// store and not a model.

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func benchmarkDirectCompletion(b *testing.B, sessionID func(i int) string) {
	b.Helper()
	root := b.TempDir()
	cfg := &config.Config{
		Paths:  config.Paths{Home: root + "/home", CWD: root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	logger := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, nil, logger, root, &session.FileStore{Root: root + "/sessions"})
	srv := New(cfg, mgr, logger, root)
	b.Cleanup(srv.Drain)
	provider := &directRecordingProvider{}
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return provider, nil }
	ts := httptest.NewServer(srv.Handler())
	b.Cleanup(ts.Close)
	const body = `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"the question"}]}`
	b.ResetTimer()
	for i := range b.N {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if id := sessionID(i); id != "" {
			req.Header.Set("X-Coddy-Session-ID", id)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			b.Fatalf("status %d", res.StatusCode)
		}
	}
}

// BenchmarkDirectCompletion compares a stateless call with one that creates a
// session each time, which is what every call without the header used to do.
func BenchmarkDirectCompletion(b *testing.B) {
	b.Run("no_session", func(b *testing.B) {
		benchmarkDirectCompletion(b, func(int) string { return "" })
	})
	b.Run("new_session_each_call", func(b *testing.B) {
		benchmarkDirectCompletion(b, func(i int) string { return fmt.Sprintf("sess_bench_%d", i) })
	})
}

func BenchmarkDirectRequestOptionsOutputCap(b *testing.B) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai"}, {Name: "codex", Type: "codex"}},
		Models:    []config.ModelEntry{{Model: "local/llama-3.1-8b"}, {Model: "codex/gpt-5.5"}},
	}
	for _, model := range []string{"local/llama-3.1-8b", "codex/gpt-5.5"} {
		b.Run(model, func(b *testing.B) {
			for b.Loop() {
				maxTokens := 256
				_, err := directRequestOptions(cfg, model, chatCompletionRequest{MaxTokens: &maxTokens})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
