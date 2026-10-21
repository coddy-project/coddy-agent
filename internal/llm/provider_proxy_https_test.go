package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// newProviderHTTPSProxy tunnels the provider's real HTTPS hostnames to a local
// TLS stand. Only the stand's certificate is trusted, and the client's dialer
// refuses every address except this proxy. A lost proxy setting therefore
// fails locally without sending test credentials to the internet. Like the
// proxy feature harness, these tests change process transport state and must
// not run in parallel.
func newProviderHTTPSProxy(t *testing.T, handler http.Handler) string {
	t.Helper()
	origin := httptest.NewTLSServer(handler)
	t.Cleanup(origin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			http.Error(w, "stand unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = downstream.Close() }()
		if _, err := io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		copied := make(chan struct{})
		go func() {
			defer close(copied)
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		}()
		_, _ = io.Copy(downstream, upstream)
		_ = downstream.Close()
		<-copied
	}))
	t.Cleanup(proxy.Close)

	previous := http.DefaultTransport
	guarded := previous.(*http.Transport).Clone()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	guarded.TLSClientConfig = &tls.Config{
		RootCAs: roots, ServerName: origin.Certificate().DNSNames[0], MinVersion: tls.VersionTLS12,
	}
	guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != proxy.Listener.Addr().String() {
			return nil, fmt.Errorf("direct provider dial forbidden: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = guarded
	t.Cleanup(func() { http.DefaultTransport = previous })
	// Even a regression that selects a previously cached direct transport
	// must use the guarded dialer, rather than reach a real provider.
	transportsMu.Lock()
	previousTransports := transports
	transports = map[string]http.RoundTripper{}
	transportsMu.Unlock()
	t.Cleanup(func() {
		transportsMu.Lock()
		created := transports
		transports = previousTransports
		transportsMu.Unlock()
		for _, transport := range created {
			if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
		}
	})
	return proxy.URL
}

func TestProviderProxyHTTPSRefreshCompletionCatalogUsageAndDeviceLogin(t *testing.T) {
	seen := make(chan string, 16)
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Host + r.URL.Path
		w.Header().Set("Connection", "close")
		if !answerProviderRequest(t, w, r) {
			http.NotFound(w, r)
		}
	}))
	t.Setenv(EnvCodexBaseURL, codexDefaultBaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	expiredAuth := func() string {
		return writeCodexUsageAuth(t, t.TempDir(), makeJWT(time.Now().Add(-time.Hour)), "refresh-test", "account-test")
	}
	p, err := NewProvider(ProviderInput{Type: "codex", Model: "m", AuthPath: expiredAuth(), ProxyURL: proxy, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil || resp.Content != "pong" {
		t.Fatalf("refresh then completion: response=%+v, error=%v", resp, err)
	}
	if _, err := ListModels(ctx, ProviderInput{Type: "codex", AuthPath: expiredAuth(), ProxyURL: proxy}); err != nil {
		t.Fatalf("refresh then catalog: %v", err)
	}
	if _, err := CodexUsageForProvider(ctx, config.ProviderConfig{Type: "codex", Proxy: proxy}, expiredAuth(), false); err != nil {
		t.Fatalf("refresh then usage: %v", err)
	}
	if err := CodexDeviceSignIn(ctx, "https://auth.openai.com", proxy, t.TempDir()+"/auth.json", nil); err != nil {
		t.Fatalf("device login: %v", err)
	}
	want := []string{
		"auth.openai.com/oauth/token", "chatgpt.com/backend-api/codex/responses",
		"auth.openai.com/oauth/token", "chatgpt.com/backend-api/codex/models",
		"auth.openai.com/oauth/token", "chatgpt.com/backend-api/wham/usage",
		"auth.openai.com/api/accounts/deviceauth/usercode",
		"auth.openai.com/api/accounts/deviceauth/token", "auth.openai.com/oauth/token",
	}
	var got []string
	for len(seen) > 0 {
		got = append(got, <-seen)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("HTTPS requests through proxy = %v, want %v", got, want)
	}
}

func TestOpenCodeHTTPSProxy(t *testing.T) {
	for _, base := range []string{opencodeGoBaseURL, opencodeConsoleBaseURL} {
		t.Run(strings.TrimPrefix(base, "https://"), func(t *testing.T) {
			// A counter rather than a buffered channel: a regression that sends
			// more requests than a channel holds would block the handler, and
			// the stand's Close would then wait for it until the test timeout.
			var seen atomic.Int32
			proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "opencode.ai" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-api-key") != "" || r.Header.Get("x-opencode-session") == "" || r.Header.Get("User-Agent") != opencodeUserAgent() {
					t.Error("missing OpenCode host or client headers through HTTPS proxy")
				}
				seen.Add(1)
				w.Header().Set("Connection", "close")
				if !answerProviderRequest(t, w, r) {
					http.NotFound(w, r)
				}
			}))
			t.Setenv(EnvOpenCodeBaseURL, "")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, model := range []string{"glm-5.3", "gpt-6-luna", "claude-haiku-5-5", "gemini-4-argon-contributor", "qwen3.8-max", "minimax-m3"} {
				p, err := NewProvider(ProviderInput{Type: "opencode", Model: model, BaseURL: base, APIKey: "test-key", ProxyURL: proxy, RetryDisabled: true})
				if err != nil {
					t.Fatal(err)
				}
				messages := []Message{{Role: RoleUser, Content: "ping"}}
				if resp, err := p.Complete(ctx, messages, nil); err != nil || resp.Content != "pong" {
					t.Fatalf("%s completion: response=%+v, error=%v", model, resp, err)
				}
				var streamed strings.Builder
				resp, err := p.Stream(ctx, messages, nil, func(chunk StreamChunk) { streamed.WriteString(chunk.TextDelta) })
				if err != nil || resp.Content != "pong" || streamed.String() != "pong" {
					t.Fatalf("%s stream: response=%+v, chunks=%q, error=%v", model, resp, streamed.String(), err)
				}
			}
			if _, err := ListModels(ctx, ProviderInput{Type: "opencode", BaseURL: base, APIKey: "test-key", ProxyURL: proxy}); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			cfg := &config.Config{Paths: config.Paths{Home: home, ConfigPath: home + "/config.yaml"}}
			if _, err := ApplyOpenCodeLoginToConfig(ctx, cfg, "oc", base, "test-key", proxy); err != nil {
				t.Fatal(err)
			}
			if n := seen.Load(); n != 14 {
				t.Fatalf("HTTPS proxy carried %d requests, want 14", n)
			}
		})
	}
}

func TestOpenCodeProxyFailureDoesNotDialOrigin(t *testing.T) {
	for _, base := range []string{opencodeGoBaseURL, opencodeConsoleBaseURL} {
		t.Run(strings.TrimPrefix(base, "https://"), func(t *testing.T) {
			origin := newOpenCodeStand(t)
			t.Setenv(EnvOpenCodeBaseURL, origin.srv.URL+"/inference/go/v1")
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "proxy unavailable", http.StatusBadGateway)
			}))
			defer proxy.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, model := range []string{"glm-5.3", "gpt-6-luna", "claude-haiku-5-5", "gemini-4-argon-contributor", "qwen3.8-max", "minimax-m3"} {
				p, err := NewProvider(ProviderInput{Type: "opencode", Model: model, BaseURL: base, APIKey: "test-key", ProxyURL: proxy.URL, RetryDisabled: true})
				if err != nil {
					t.Fatal(err)
				}
				messages := []Message{{Role: RoleUser, Content: "ping"}}
				if _, err := p.Complete(ctx, messages, nil); err == nil {
					t.Errorf("%s completion succeeded despite proxy failure", model)
				}
				if _, err := p.Stream(ctx, messages, nil, func(StreamChunk) {}); err == nil {
					t.Errorf("%s stream succeeded despite proxy failure", model)
				}
			}
			if _, err := ListModels(ctx, ProviderInput{Type: "opencode", BaseURL: base, APIKey: "test-key", ProxyURL: proxy.URL}); err == nil {
				t.Error("catalog succeeded despite proxy failure")
			}
			cfg := &config.Config{Paths: config.Paths{ConfigPath: t.TempDir() + "/config.yaml"}}
			if _, err := ApplyOpenCodeLoginToConfig(ctx, cfg, "oc", base, "test-key", proxy.URL); err == nil {
				t.Error("login catalog succeeded despite proxy failure")
			}
			if got := origin.seen(t); len(got) != 0 {
				t.Fatalf("requests bypassed the failed proxy: %+v", got)
			}
		})
	}
}
