package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiUsageSubscriptionOnly(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{{"", true}, {"subscription", true}, {"platform", false}} {
		p := config.ProviderConfig{Name: "kimi", Type: "kimi", Mode: tc.mode}
		if got := ProviderUsageEnabled(&p); got != tc.want {
			t.Errorf("mode %q: enabled = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestKimiUsageWindows(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reset := "2026-01-01T05:00:00Z"
	u := &llm.KimiUsage{
		Limit5H:    &llm.KimiUsageWindow{UsedRatio: .25, ResetAt: &reset},
		Limit7D:    &llm.KimiUsageWindow{UsedRatio: .5},
		MonthTotal: &llm.KimiUsageWindow{UsedRatio: .75},
		MonthCode:  &llm.KimiUsageWindow{UsedRatio: 1},
	}
	out := mapKimiUsage(u, "work", now)
	if out.ProviderType != "kimi" || out.Provider != "work" || out.Wallet != nil || out.Blocked {
		t.Fatalf("unexpected snapshot: %+v", out)
	}
	ids := []string{"5h", "7d", "month_total", "month_code"}
	for i, id := range ids {
		if len(out.Windows) != 4 {
			t.Fatalf("windows: %+v", out.Windows)
		}
		w := out.Windows[i]
		if w.ID != id || w.UsedPercent != float64((i+1)*25) {
			t.Errorf("window %d: %+v", i, w)
		}
	}
	if out.Windows[0].ResetInSec != 18000 || out.Windows[1].ResetsAt != "" {
		t.Fatalf("reset: %+v", out.Windows)
	}
}

func TestKimiUsageFingerprintTracksCredentialsAndProxy(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := llm.NewKimiCredentialStore(dir)
	p := config.ProviderConfig{Name: "work", Type: "kimi"}
	before := providerUsageFingerprint(p, dir, false)
	if err := kimiTestLogin(store, "first"); err != nil {
		t.Fatal(err)
	}
	after := providerUsageFingerprint(p, dir, false)
	if before == after {
		t.Fatal("login did not invalidate fingerprint")
	}
	p.Proxy = "none"
	if after == providerUsageFingerprint(p, dir, false) {
		t.Fatal("proxy did not invalidate fingerprint")
	}
	if err := store.Remove("global"); err != nil {
		t.Fatal(err)
	}
	if after == providerUsageFingerprint(p, dir, false) {
		t.Fatal("logout did not invalidate fingerprint")
	}
}

func TestKimiUsageUsesProviderProxy(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	requests := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.Host
		http.Error(w, "test proxy", http.StatusBadGateway)
	}))
	defer proxy.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := kimiTestLogin(llm.NewKimiCredentialStore(dir), "test-token"); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "work", Type: "kimi", Proxy: proxy.URL}
	_, err := (&Manager{}).fetchProviderUsage(context.Background(), p, dir, false)
	if err == nil {
		t.Fatal("expected proxy failure")
	}
	select {
	case got := <-requests:
		if got != "CONNECT api.kimi.ai:443" {
			t.Fatalf("proxy request: %s", got)
		}
	default:
		t.Fatal("proxy not contacted")
	}
}

func TestKimiCachedUsageHidesChangedCredential(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	stand := newUsageStand(t)
	m := newUsageManagerWith(t, stand, &usageCapture{}, nil, func(cfg *config.Config) {
		cfg.Providers = []config.ProviderConfig{{Name: "work", Type: "kimi", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	})
	if err := os.Chmod(m.Cfg().Paths.Home, 0700); err != nil {
		t.Fatal(err)
	}
	p := m.Cfg().FindProvider("work")
	dir := usageAuthPath(m.Cfg(), p)
	store := llm.NewKimiCredentialStore(dir)
	if err := kimiTestLogin(store, "first"); err != nil {
		t.Fatal(err)
	}
	m.usage.mu.Lock()
	e := m.usageEntryLocked(p.Name, providerUsageFingerprint(*p, dir, m.Cfg().ProviderMayUseCLILogin(p.Name, p.Type)))
	e.update = &acp.ProviderUsageUpdate{Provider: "work", ProviderType: "kimi"}
	m.usage.mu.Unlock()
	if len(m.CachedProviderUsage()) != 1 {
		t.Fatal("current snapshot missing")
	}
	if err := kimiTestLogin(store, "second"); err != nil {
		t.Fatal(err)
	}
	if len(m.CachedProviderUsage()) != 0 {
		t.Fatal("stale account snapshot leaked")
	}
}

func kimiTestLogin(store *llm.KimiCredentialStore, token string) error {
	ctx := context.Background()
	attempt, err := store.BeginLogin(ctx, "global")
	if err != nil {
		return err
	}
	return store.CompleteLogin(ctx, "global", attempt, llm.KimiCredential{AccessToken: token})
}

func TestKimiUsageCLIFallbackAndFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code-env-0e4f99c69cc27850.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(token string) []byte {
		b := []byte(fmt.Sprintf(`{"access_token":%q,"refresh_token":"keep","expires_at":%d}`, token, time.Now().Add(time.Hour).Unix()))
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return b
	}
	before := write("cli-one")
	requests := make(chan string, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests <- r.Host; w.WriteHeader(http.StatusBadGateway) }))
	defer proxy.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", Proxy: proxy.URL}
	a := providerUsageFingerprint(p, dir, true)
	_, err := (&Manager{}).fetchProviderUsage(context.Background(), p, dir, true)
	if !llm.KimiRefreshSupported() {
		if !errors.Is(err, llm.ErrKimiStoreUnsupported) {
			t.Fatalf("unsupported managed store: %v", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(before, after) {
			t.Fatal("CLI credential changed")
		}
		write("cli-two")
		if a != providerUsageFingerprint(p, dir, true) {
			t.Fatal("unavailable managed store must not select CLI credentials")
		}
		return
	}
	if err == nil {
		t.Fatal("expected proxy error")
	}
	select {
	case host := <-requests:
		if host != "api.kimi.ai:443" {
			t.Fatal(host)
		}
	default:
		t.Fatal("CLI fallback never reached proxy")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("CLI modified")
	}
	_, err = (&Manager{}).fetchProviderUsage(context.Background(), p, dir, false)
	var usageErr *llm.ProviderUsageError
	if !errors.As(err, &usageErr) || usageErr.Kind != llm.ProviderUsageUnauthorized {
		t.Fatalf("disabled fallback: %v", err)
	}
	select {
	case <-requests:
		t.Fatal("disabled fallback contacted proxy")
	default:
	}
	write("cli-two")
	if a == providerUsageFingerprint(p, dir, true) {
		t.Fatal("CLI replacement did not invalidate cache")
	}
	if a == providerUsageFingerprint(p, dir, false) {
		t.Fatal("CLI eligibility did not invalidate cache")
	}
}

func TestKimiUsageCLIUnauthorized(t *testing.T) {
	upstream := &llm.ProviderUsageError{Kind: llm.ProviderUsageUnauthorized, Status: 401}
	err := kimiUsageCredentialError(llm.KimiSourceCLI, upstream)
	if !errors.Is(err, llm.ErrKimiCLILoginRequired) {
		t.Fatalf("CLI 401: %v", err)
	}
	if got := kimiUsageCredentialError(llm.KimiSourceManaged, upstream); errors.Is(got, llm.ErrKimiCLILoginRequired) {
		t.Fatal("managed misidentified as CLI")
	}
}

func TestKimiCachedUsageHidesChangedCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code-env-0e4f99c69cc27850.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(token string) {
		t.Helper()
		b := fmt.Sprintf(`{"access_token":%q,"expires_at":%d}`, token, time.Now().Add(time.Hour).Unix())
		if err := os.WriteFile(path, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	stand := newUsageStand(t)
	m := newUsageManagerWith(t, stand, &usageCapture{}, nil, func(cfg *config.Config) { cfg.Providers = []config.ProviderConfig{{Name: "kimi", Type: "kimi"}} })
	if err := os.Chmod(m.Cfg().Paths.Home, 0700); err != nil {
		t.Fatal(err)
	}
	p := m.Cfg().FindProvider("kimi")
	dir := usageAuthPath(m.Cfg(), p)
	m.usage.mu.Lock()
	e := m.usageEntryLocked(p.Name, providerUsageFingerprint(*p, dir, true))
	e.update = &acp.ProviderUsageUpdate{Provider: "kimi"}
	m.usage.mu.Unlock()
	if len(m.CachedProviderUsage()) != 1 {
		t.Fatal("current CLI snapshot missing")
	}
	write("two")
	if len(m.CachedProviderUsage()) != 0 {
		t.Fatal("stale CLI snapshot exposed")
	}
}
