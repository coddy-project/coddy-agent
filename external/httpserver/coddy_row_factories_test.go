//go:build http

package httpserver

// The server-side provider factories build the provider of a `coddy` row (a
// model another Coddy shares) for the callers that are not the agent loop: the
// direct completion routes, prompt enhancement and the describe call. Every
// caller of such a row waits for a free stream slot of the remote and is held
// to the liveness guard of the stream whatever models[].stream says
// (docs/plans/remote-model-provider.md, 4.1b and 4.3).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func coddyRowConfig(base string, mut func(*config.Config)) *config.Config {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "remote", Type: "coddy", APIBase: base, APIKey: "token", Proxy: "none", BusyWaitMS: 5000}},
		Models:    []config.ModelEntry{{Model: "remote/coder", MaxTokens: 256}},
		Agent:     config.Agent{Model: "remote/coder"},
	}
	if mut != nil {
		mut(cfg)
	}
	return cfg
}

func writeFrames(w http.ResponseWriter, frames ...any) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, f := range frames {
		raw, _ := llm.EncodeWireFrame(f)
		_, _ = w.Write(raw)
	}
}

func TestDirectFactoriesMakeACoddyRowWaitForAFreeSlot(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(llm.WireError{Status: 429, Kind: llm.WireKindBusy, RetryAfterS: 1})
			return
		}
		writeFrames(w, llm.WireFinalFromResponse(&llm.Response{Content: "served after the wait"}))
	}))
	defer remote.Close()

	for name, build := range map[string]func(*config.Config) (llm.Provider, error){
		"direct completion": func(c *config.Config) (llm.Provider, error) {
			return defaultMakeLLMFromYAML(c, "remote/coder", llm.RequestOptions{})
		},
		"describe call": defaultProviderFromAgentModel,
	} {
		t.Run(name, func(t *testing.T) {
			requests.Store(0)
			p, err := build(coddyRowConfig(remote.URL, nil))
			if err != nil {
				t.Fatal(err)
			}
			var sleeps atomic.Int32
			ctx := llm.WithBusyWaitSleep(context.Background(), func(ctx context.Context, _ time.Duration) error {
				sleeps.Add(1)
				return ctx.Err()
			})
			resp, err := p.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}, nil)
			if err != nil || resp.Content != "served after the wait" {
				t.Fatalf("resp %+v err %v", resp, err)
			}
			if sleeps.Load() != 2 || requests.Load() != 3 {
				t.Fatalf("%d sleeps and %d requests, want 2 and 3", sleeps.Load(), requests.Load())
			}
		})
	}
}

// A coddy row with stream: false changes nothing on the wire, so it keeps the
// liveness guard: a remote that goes silent after its first bytes is cut.
func TestDirectFactoriesKeepTheIdleGuardOfACoddyRowThatIsNotStreamed(t *testing.T) {
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(llm.CoddyHeartbeat))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer remote.Close()
	defer close(release)

	off, idle, noRetry := false, 150, 0
	cfg := coddyRowConfig(remote.URL, func(c *config.Config) {
		c.Models[0].Stream = &off
		c.Agent.LLMStreamIdleTimeoutMS = &idle
		// A stall before any output is repeated like any transport failure; the
		// test wants to see the first one.
		c.Agent.LLMRetryMax = &noRetry
	})
	p, err := defaultMakeLLMFromYAML(cfg, "remote/coder", llm.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err = p.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}, nil)
	if !llm.IsStreamStalled(err) {
		t.Fatalf("a silent remote was not cut as a stall: %v (after %v)", err, time.Since(started))
	}
}
