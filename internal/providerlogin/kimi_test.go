package providerlogin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiManagedReadiness(t *testing.T) {
	home := isolate(t)
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = []config.ProviderConfig{{Name: "kimi", Type: "kimi", Region: "china", KimiIdentity: id}}
	if ProviderReady(cfg, "kimi") {
		t.Fatal("missing credential is not ready")
	}
	if !llm.KimiRefreshSupported() {
		if _, err := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id)).BeginLogin(context.Background(), "china"); !errors.Is(err, llm.ErrKimiStoreUnsupported) {
			t.Fatalf("unsupported managed login: %v", err)
		}
		cfg.Providers[0].Mode = "platform"
		if ProviderReady(cfg, "kimi") {
			t.Fatal("platform without API key must not be ready")
		}
		return
	}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "china")
	if err != nil {
		t.Fatal(err)
	}
	err = store.CompleteLogin(context.Background(), "china", attempt, llm.KimiCredential{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if !ProviderReady(cfg, "kimi") {
		t.Fatal("managed China login must be ready")
	}
	cfg.Providers[0].Region = "global"
	if ProviderReady(cfg, "kimi") {
		t.Fatal("China credential must not serve global")
	}
	cfg.Providers[0].Mode = "platform"
	cfg.Providers[0].Region = "china"
	if ProviderReady(cfg, "kimi") {
		t.Fatal("OAuth must not serve platform")
	}
}

func TestKimiLoginRequiresIdentity(t *testing.T) {
	home := t.TempDir()
	_, err := StartKimiLogin(context.Background(), home, config.ProviderConfig{Name: "kimi", Type: "kimi", Proxy: ":invalid"})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("missing identity error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "providers", "kimi")); !os.IsNotExist(err) {
		t.Fatalf("name-based store created: %v", err)
	}
}

func TestEnsureKimiIdentityPreservesConfig(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	data := "providers:\n  - name: kimi\n    type: kimi\n  - name: other\n    type: openai\nmodels:\n  - model: other/example\nagent:\n  model: other/example\n"
	if err := os.WriteFile(cfg.Paths.ConfigPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	row, err := EnsureKimiIdentity(loaded, config.ProviderConfig{Name: "kimi", Type: "kimi"})
	if err != nil {
		t.Fatal(err)
	}
	if !config.ValidKimiIdentity(row.KimiIdentity) {
		t.Fatal("missing saved identity")
	}
	current, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if current.FindProvider("kimi").KimiIdentity != row.KimiIdentity || current.FindProvider("other") == nil || current.Agent.Model != "other/example" || current.FindModelEntry("other/example") == nil {
		t.Fatal("identity or existing config lost")
	}
}

func TestEnsureKimiIdentityRejectsUnsavedID(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: id}
	cfg.Providers = []config.ProviderConfig{p}
	if _, err := EnsureKimiIdentity(cfg, p); err == nil {
		t.Fatal("unsaved identity must not be reported as persisted")
	}
}

func TestEnsureKimiIdentityNewChinaRow(t *testing.T) {
	home := t.TempDir()
	cfg := freshConfig(home)
	row, err := EnsureKimiIdentity(cfg, config.ProviderConfig{Name: "kimi", Type: "kimi", Mode: "subscription", Region: "china", Proxy: "http://proxy.example:3128"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		t.Fatal(err)
	}
	p := saved.FindProvider("kimi")
	if p == nil || p.KimiIdentity != row.KimiIdentity || p.Mode != "subscription" || p.Region != "china" || p.Proxy != "http://proxy.example:3128" {
		t.Fatalf("new provider: %+v", p)
	}
}

func TestKimiLoginCompletesForIdentity(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
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
	login := &KimiLogin{store: store, region: "global", attempt: attempt, waitToken: func(context.Context) (llm.KimiToken, error) {
		return llm.KimiToken{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
	}}
	if err := login.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	cred, state, err := store.Load("global")
	if err != nil || state != llm.KimiCredentialValid || cred.AccessToken != "synthetic" {
		t.Fatalf("credential state=%v err=%v", state, err)
	}
}

func TestKimiLoginStaleCompletion(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	for _, action := range []string{"logout", "new login"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			id, err := config.NewKimiIdentity()
			if err != nil {
				t.Fatal(err)
			}
			store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
			attempt, err := store.BeginLogin(context.Background(), "china")
			if err != nil {
				t.Fatal(err)
			}
			login := &KimiLogin{store: store, region: "china", attempt: attempt, waitToken: func(context.Context) (llm.KimiToken, error) {
				return llm.KimiToken{AccessToken: "stale", RefreshToken: "secret", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
			}}
			if action == "logout" {
				err = store.Remove("china")
			} else {
				_, err = store.BeginLogin(context.Background(), "china")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := login.Wait(context.Background()); !errors.Is(err, llm.ErrKimiLoginAttemptStale) {
				t.Fatalf("stale attempt: %v", err)
			}
			_, state, err := store.Load("china")
			if err != nil || state != llm.KimiCredentialMissing {
				t.Fatalf("unexpected stored credential: %v %v", state, err)
			}
		})
	}
}

func TestKimiLoginPlatformRejected(t *testing.T) {
	if !Supports("kimi") {
		t.Fatal("Kimi device login is not available")
	}
	cfg := freshConfig(isolate(t))
	p := &config.ProviderConfig{Name: "kimi", Type: "kimi", Mode: "platform"}
	if _, err := Run(context.Background(), cfg, p, Hooks{}); err == nil {
		t.Fatal("platform must reject subscription login")
	}
}
