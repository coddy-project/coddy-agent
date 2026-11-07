//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestKimiAuthHTTPStatusAndInvalidRegion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	s := New(cfg, mgr, slog.Default(), t.TempDir())
	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/coddy/providers/kimi/kimi-auth", nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"source":"none"`) {
		t.Fatalf("status: %d %s", get.Code, get.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/coddy/providers/kimi/kimi-auth/device", strings.NewReader(`{"region":"other"}`))
	req.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, req)
	if result.Code != 400 {
		t.Fatalf("invalid region: %d %s", result.Code, result.Body.String())
	}
}

func TestKimiAuthHTTPUnsavedProxy(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("device login requires secure durable managed storage before contacting the proxy")
	}
	hits := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits <- r.Method + " " + r.Host; w.WriteHeader(502) }))
	defer proxy.Close()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Proxy: "none"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	data, err := json.Marshal(map[string]string{"proxy": proxy.URL, "region": "china"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/coddy/providers/kimi/kimi-auth/device", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	select {
	case hit := <-hits:
		if hit != "CONNECT auth.kimi.com:443" {
			t.Fatalf("proxy received %q", hit)
		}
	default:
		t.Fatal("unsaved proxy was not used")
	}
}

func TestKimiAuthHTTPManagedLogout(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Region: "china", KimiIdentity: id}}}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "china")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(context.Background(), "china", attempt, llm.KimiCredential{AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	for _, tc := range []struct{ method, url, source string }{{"GET", "/coddy/providers/kimi/kimi-auth?region=global", "none"}, {"GET", "/coddy/providers/kimi/kimi-auth", "managed"}, {"DELETE", "/coddy/providers/kimi/kimi-auth", "none"}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.url, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"source":"`+tc.source+`"`) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.url, w.Code, w.Body.String())
		}
	}
}

func TestKimiAuthCancelWithoutInvalidationFails(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	s.codexAuthLogins["test"] = &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "pending", CreatedAt: time.Now()}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("DELETE", "/coddy/providers/kimi/kimi-auth/device/test", nil))
	if w.Code != 500 {
		t.Fatalf("cancel without a durable invalidator: %d %s", w.Code, w.Body.String())
	}
}

func TestKimiAuthCancelInvalidationError(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	s.codexAuthLogins["test"] = &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "pending", CreatedAt: time.Now(), cancelDurable: func() error { return errors.New("private store detail") }}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("DELETE", "/coddy/providers/kimi/kimi-auth/device/test", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private store detail") {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
}

func TestKimiAuthInvalidationFailureBlocksStartAndLogout(t *testing.T) {
	for _, action := range []string{"start", "logout"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			id, err := config.NewKimiIdentity()
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", KimiIdentity: id}}}
			runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
				return "", nil
			}
			s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
			old := &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "pending", CreatedAt: time.Now(), cancelDurable: func() error { return errors.New("secret failure detail") }}
			s.codexAuthLogins["old"] = old
			method, url, body := "POST", "/coddy/providers/kimi/kimi-auth/device", `{}`
			if action == "logout" {
				method, url, body = "DELETE", "/coddy/providers/kimi/kimi-auth", ""
			}
			r := httptest.NewRequest(method, url, strings.NewReader(body))
			if method == "POST" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
			}
			if s.codexAuthLogins["old"] != old || len(s.codexAuthLogins) != 1 {
				t.Fatalf("attempt lost: %+v", s.codexAuthLogins)
			}
		})
	}
}

func TestKimiAuthStartingAttemptBlocksSupersedeAndLogout(t *testing.T) {
	for _, action := range []string{"start", "logout"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			id, err := config.NewKimiIdentity()
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", KimiIdentity: id}}}
			runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
				return "", nil
			}
			s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
			s.codexAuthLogins["starting"] = &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "pending", CreatedAt: time.Now()}
			method, url, body := "POST", "/coddy/providers/kimi/kimi-auth/device", `{}`
			if action == "logout" {
				method, url, body = "DELETE", "/coddy/providers/kimi/kimi-auth", ""
			}
			r := httptest.NewRequest(method, url, strings.NewReader(body))
			if method == "POST" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 500 {
				t.Fatalf("%s returned %d: %s", action, w.Code, w.Body.String())
			}
		})
	}
}

func TestKimiAuthUnsavedCompletionRequiresSave(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	s.codexAuthLogins["test"] = &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "completed", Connected: true, CreatedAt: time.Now()}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/coddy/providers/kimi/kimi-auth/device/test", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"connected":false`) || !strings.Contains(w.Body.String(), `"save_required":true`) {
		t.Fatalf("unsaved login: %d %s", w.Code, w.Body.String())
	}
}
