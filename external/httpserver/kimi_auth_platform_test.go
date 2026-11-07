//go:build http

package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestKimiPlatformLogoutDoesNotRemoveSubscription(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("secure Kimi credential store is unavailable")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(context.Background(), "global", attempt, llm.KimiCredential{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", KimiIdentity: id}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	server := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/coddy/providers/kimi/kimi-auth?mode=platform", nil))
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	_, state, err := store.Load("global")
	if err != nil || state != llm.KimiCredentialValid {
		t.Fatalf("subscription credential removed: state=%v error=%v", state, err)
	}
}

func TestKimiLogoutWithoutIdentityDoesNotCancelLogin(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	server := New(cfg, session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil), slog.Default(), t.TempDir())
	cancelled := false
	server.codexAuthLogins["attempt"] = &codexAuthLoginAttempt{ProviderName: "kimi:kimi", Status: "pending", CreatedAt: time.Now(), cancel: func() { cancelled = true }}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/coddy/providers/kimi/kimi-auth", nil))
	if response.Code != http.StatusBadRequest || cancelled {
		t.Fatalf("logout without identity: status=%d cancelled=%v", response.Code, cancelled)
	}
}
