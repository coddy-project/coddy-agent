package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestContextWindowKimiInputAndIsolation(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Mode: "subscription", Region: "china", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Proxy: "none"}, {Name: "work", Type: "kimi", Mode: "platform", Region: "global", Proxy: "none", APIKey: "fixture"}}, Models: []config.ModelEntry{{Model: "kimi/model"}, {Model: "work/model"}}}
	l := &windowListing{windows: map[string]int{"model": 77777}}
	m := newWindowTestManager(t, cfg, l, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"kimi/model", "work/model"}, time.Second)
	if n, src := m.ContextWindow(cfg, "kimi/model"); n != 77777 || src != ContextWindowFromProvider {
		t.Fatalf("window %d/%s", n, src)
	}
	l.mu.Lock()
	inputs := append([]llm.ProviderInput(nil), l.inputs...)
	l.mu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("requests %d", len(inputs))
	}
	for _, in := range inputs {
		p := cfg.FindProvider(in.Name)
		if in.Mode != p.Mode || in.Region != p.Region || in.KimiIdentity != p.KimiIdentity || in.AuthPath != config.ProviderConfigAuthPath(home, *p) || in.NoCLILogin != !cfg.ProviderMayUseCLILogin(p.Name, p.Type) || in.ProxyURL != p.Proxy {
			t.Fatalf("wrong input %+v", in)
		}
	}
	for _, change := range []func(*config.ProviderConfig){func(p *config.ProviderConfig) { p.Region = "global" }, func(p *config.ProviderConfig) { p.KimiIdentity = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" }, func(p *config.ProviderConfig) { p.Mode = "platform" }} {
		next := *cfg
		next.Providers = append([]config.ProviderConfig(nil), cfg.Providers...)
		change(&next.Providers[0])
		if n, _ := m.ContextWindow(&next, "kimi/model"); n != config.DefaultContextWindowTokens {
			t.Fatalf("leaked window %d", n)
		}
	}
}

func TestContextWindowKimiCLIReplacementInvalidates(t *testing.T) {
	home := t.TempDir()
	cli := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", cli)
	path := filepath.Join(cli, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(token string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":%q,"expires_at":%d}`, token, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("old")
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Region: "china"}}, Models: []config.ModelEntry{{Model: "kimi/model"}}}
	l := &windowListing{windows: map[string]int{"model": 77777}}
	m := newWindowTestManager(t, cfg, l, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"kimi/model"}, time.Second)
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != 77777 {
		t.Fatalf("initial %d", n)
	}
	write("new")
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != config.DefaultContextWindowTokens {
		t.Fatalf("stale credential window %d", n)
	}
}

func TestContextWindowKimiManagedReplacementInvalidates(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	store := llm.NewKimiCredentialStore(config.ProviderConfigAuthPath(home, p))
	login := func(token string) {
		t.Helper()
		id, err := store.BeginLogin(context.Background(), "global")
		if err != nil {
			t.Fatal(err)
		}
		if err = store.CompleteLogin(context.Background(), "global", id, llm.KimiCredential{AccessToken: token}); err != nil {
			t.Fatal(err)
		}
	}
	login("old")
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{p}, Models: []config.ModelEntry{{Model: "kimi/model"}}}
	l := &windowListing{windows: map[string]int{"model": 77777}}
	m := newWindowTestManager(t, cfg, l, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"kimi/model"}, time.Second)
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != 77777 {
		t.Fatalf("initial %d", n)
	}
	login("new")
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != config.DefaultContextWindowTokens {
		t.Fatalf("old window leaked %d", n)
	}
}

func TestContextWindowKimiInFlightCredentialChange(t *testing.T) {
	cli := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", cli)
	path := filepath.Join(cli, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(token string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":%q,"expires_at":%d}`, token, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("old")
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi", Region: "china"}}, Models: []config.ModelEntry{{Model: "kimi/model"}}}
	l := &windowListing{windows: map[string]int{"model": 77777}, gate: make(chan struct{})}
	m := newWindowTestManager(t, cfg, l, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"kimi/model"}, 20*time.Millisecond)
	write("new")
	l.mu.Lock()
	close(l.gate)
	l.gate = nil
	l.mu.Unlock()
	if err := m.WaitContextWindowsIdle(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	write("old")
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != config.DefaultContextWindowTokens {
		t.Fatalf("old in-flight listing retained %d", n)
	}
}

func TestContextWindowKimiForgetDoesNotServeOldWindow(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir()}, Providers: []config.ProviderConfig{{Name: "kimi", Type: "kimi"}}, Models: []config.ModelEntry{{Model: "kimi/model"}}}
	l := &windowListing{windows: map[string]int{"model": 77777}}
	m := newWindowTestManager(t, cfg, l, nil)
	m.AwaitContextWindows(context.Background(), cfg, []string{"kimi/model"}, time.Second)
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != 77777 {
		t.Fatalf("initial %d", n)
	}
	m.ForgetContextWindows("kimi")
	if n, _ := m.ContextWindow(cfg, "kimi/model"); n != config.DefaultContextWindowTokens {
		t.Fatalf("forgotten window leaked %d", n)
	}
}
