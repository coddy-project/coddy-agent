//go:build http

package httpserver

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. Here, what Coddy adds to one direct completion (issue #585): the
// provider answers at once, so a run measures the request, the session and the
// store and not a model.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
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

// BenchmarkAuthGate measures admission only; it does not include routing or
// serializing a real protected resource.
func BenchmarkAuthGate(b *testing.B) {
	for _, kind := range []string{"bearer", "password-cookie"} {
		b.Run(kind, func(b *testing.B) {
			c := &config.Config{}
			s := &Server{mux: http.NewServeMux(), sessions: webauth.NewSessionStore()}
			s.mux.HandleFunc("GET /probe", func(http.ResponseWriter, *http.Request) {})
			r := httptest.NewRequest("GET", "/probe", nil)
			switch kind {
			case "bearer":
				s.SetExtraAuthTokens([]string{"machine-auth"})
				r.Header.Set("Authorization", "Bearer machine-auth")
			default:
				c.HTTPServer.Login.User = "operator"
				c.HTTPServer.Login.PasswordHash = "sample-hash"
				s.cfgAt.Store(c)
				token, _, err := s.sessions.Issue("operator", s.loginPolicyNow().account.fingerprint(), time.Hour)
				if err != nil {
					b.Fatal(err)
				}
				r.AddCookie(sessionCookie(r, token, 0))
			}
			w := httptest.NewRecorder()
			next := s.authGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			b.ResetTimer()
			for range b.N {
				next.ServeHTTP(w, r)
			}
			if w.Code != 200 {
				b.Fatal(w.Code)
			}
		})
	}
}

func BenchmarkOIDCRole(b *testing.B) {
	raw := []byte(`["reader","coddy-operator","writer"]`)
	for range b.N {
		if !hasOIDCRole(raw, "coddy-operator") {
			b.Fatal("role not found")
		}
	}
}

func benchOIDCServer() *Server {
	s := &Server{external: newExternalAuth()}
	c := &config.Config{}
	c.HTTPServer.Login.OIDC.RedirectURL = testOIDCOrigin + oidcCallbackPath
	s.cfgAt.Store(c)
	return s
}

func benchIssueOIDC(s *Server, browser string, id *oidc.IDToken) (string, bool) {
	a := &s.external
	a.mu.Lock()
	if a.flows == nil {
		a.flows = make(map[string]*oidcFlow)
	}
	flow := &oidcFlow{generation: a.generation, used: true, until: time.Now().Add(oidcFlowTTL)}
	a.flows[browser] = flow
	a.mu.Unlock()
	key, _, ok := s.commitOIDCSession(context.Background(), browser, flow, id)
	a.mu.Lock()
	delete(a.flows, browser)
	a.mu.Unlock()
	return key, ok
}

func BenchmarkOIDCSessionLookup(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := benchOIDCServer()
			id := &oidc.IDToken{Issuer: "https://idp.example", Subject: "one", Expiry: time.Now().Add(time.Hour)}
			req := make([]*http.Request, count)
			for i := range req {
				k, ok := benchIssueOIDC(s, authOpaque(), id)
				if !ok {
					b.Fatal("session capacity")
				}
				req[i] = httptest.NewRequest("GET", testOIDCOrigin+"/coddy/sessions", nil)
				req[i].AddCookie(&http.Cookie{Name: oidcSessionCookie, Value: k})
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					s.external.mu.Lock()
					_, ok := s.lookupOIDC(req[i%len(req)])
					s.external.mu.Unlock()
					if !ok {
						b.Error("lookup failed")
						return
					}
					i++
				}
			})
		})
	}
}

func BenchmarkOIDCIssueRevoke(b *testing.B) {
	s := benchOIDCServer()
	id := &oidc.IDToken{Issuer: "https://idp.example", Subject: "one", Expiry: time.Now().Add(time.Hour)}
	b.RunParallel(func(pb *testing.PB) {
		browser := authOpaque()
		for pb.Next() {
			key, ok := benchIssueOIDC(s, browser, id)
			if !ok {
				b.Error("issue failed")
				return
			}
			a := &s.external
			a.mu.Lock()
			a.revoke(key, a.sessions[key])
			a.mu.Unlock()
		}
	})
}

func BenchmarkOIDCWarmVerify(b *testing.B) {
	f := newOIDCFixture(b)
	c := externalSettings(f.state.srv.activeCfg())
	ctx := context.Background()
	p, err := f.state.srv.external.discover(ctx, c)
	if err != nil {
		b.Fatal(err)
	}
	raw, err := signOIDCTestClaims(f.signer, map[string]any{"iss": c.issuer, "aud": c.client, "sub": "one", "nonce": "test", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "roles": []string{c.role}})
	if err != nil {
		b.Fatal(err)
	}
	if _, err = p.verify(ctx, raw, c, "test"); err != nil {
		b.Fatal(err)
	}
	before := f.calls.Load()
	b.ResetTimer()
	for range b.N {
		if _, err = p.verify(ctx, raw, c, "test"); err != nil {
			b.Fatal(err)
		}
	}
	if f.calls.Load() != before {
		b.Fatal("verification did not use warm keys")
	}
}

func BenchmarkOIDCBoundedCleanup(b *testing.B) {
	now := time.Now()
	a := externalAuth{flows: make(map[string]*oidcFlow, oidcFlowLimit), tickets: make(map[string]logoutTicket, logoutLimit)}
	keys := make([]string, max(oidcFlowLimit, logoutLimit))
	for i := range keys {
		keys[i] = authOpaque()
	}
	flow := &oidcFlow{until: now}
	ticket := logoutTicket{until: now}
	b.ResetTimer()
	for range b.N {
		for _, k := range keys[:oidcFlowLimit] {
			a.flows[k] = flow
		}
		for _, k := range keys[:logoutLimit] {
			a.tickets[k] = ticket
		}
		a.sweep(now)
	}
}

func BenchmarkAuthGateDuringTelegramReplay(b *testing.B) {
	for _, blocked := range []bool{false, true} {
		name := "idle"
		if blocked {
			name = "blocked-replay"
		}
		b.Run(name, func(b *testing.B) {
			srv := newTelegramServer(b, "token", 7)
			if blocked {
				arrived, unblock, done := pauseTelegramReplay(b, srv)
				waitAuthBarrier(b, arrived)
				b.Cleanup(func() {
					unblock()
					if w := <-done; w.Code != 200 {
						b.Errorf("replay completion: %d", w.Code)
					}
				})
			}
			req := httptest.NewRequest("GET", "/coddy/sessions", nil)
			req.Header.Set("Authorization", "Bearer api-token")
			admitted := false
			h := srv.authGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { admitted = true }))
			w := httptest.NewRecorder()
			b.ResetTimer()
			for range b.N {
				h.ServeHTTP(w, req)
			}
			b.StopTimer()
			if !admitted {
				b.Fatal("the bearer request was not admitted")
			}
		})
	}
}
