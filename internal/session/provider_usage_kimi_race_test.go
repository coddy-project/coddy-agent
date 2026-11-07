package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiUsageInFlightCLIReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code-env-0e4f99c69cc27850.json")
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
	entered, release := make(chan struct{}), make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	stand := newUsageStand(t)
	m := newUsageManagerWith(t, stand, &usageCapture{}, nil, func(c *config.Config) {
		c.Providers = []config.ProviderConfig{{Name: "kimi", Type: "kimi", Proxy: proxy.URL}}
	})
	if err := os.Chmod(m.Cfg().Paths.Home, 0700); err != nil {
		t.Fatal(err)
	}
	p := m.Cfg().FindProvider("kimi")
	source, err := llm.NewKimiSubscriptionSource(nil, p.Mode, p.Region, p.Proxy, !m.Cfg().ProviderMayUseCLILogin(p.Name, p.Type))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = source.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		u, err := m.ProviderUsage(context.Background(), "kimi", false)
		if err == nil {
			err = fmt.Errorf("snapshot: %+v", u)
		}
		result <- err
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("request ended early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach proxy")
	}
	write("new")
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, errUsageSuperseded) {
			t.Fatalf("expected superseded, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
	if len(m.CachedProviderUsage()) != 0 {
		t.Fatal("old snapshot cached")
	}
}

func TestKimiUsageInFlightManagedAndSettingsChanges(t *testing.T) {
	for _, change := range []string{"logout", "relogin", "region", "mode", "proxy", "identity"} {
		t.Run(change, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer proxy.Close()
			stand := newUsageStand(t)
			m := newUsageManagerWith(t, stand, &usageCapture{}, nil, func(c *config.Config) {
				c.Providers = []config.ProviderConfig{{Name: "kimi", Type: "kimi", Proxy: proxy.URL, KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
			})
			if err := os.Chmod(m.Cfg().Paths.Home, 0700); err != nil {
				t.Fatal(err)
			}
			p := m.Cfg().FindProvider("kimi")
			store := llm.NewKimiCredentialStore(usageAuthPath(m.Cfg(), p))
			if err := kimiTestLogin(store, "old"); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { _, err := m.ProviderUsage(context.Background(), "kimi", false); result <- err }()
			select {
			case <-entered:
			case err := <-result:
				t.Fatalf("early result: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("no request")
			}
			switch change {
			case "logout", "relogin":
				if err := store.Remove("global"); err != nil {
					t.Fatal(err)
				}
				if change == "relogin" {
					if err := kimiTestLogin(store, "new"); err != nil {
						t.Fatal(err)
					}
				}
			default:
				next := *m.Cfg()
				next.Providers = append([]config.ProviderConfig(nil), next.Providers...)
				switch change {
				case "region":
					next.Providers[0].Region = "china"
				case "mode":
					next.Providers[0].Mode = "platform"
				case "proxy":
					next.Providers[0].Proxy = "none"
				case "identity":
					next.Providers[0].KimiIdentity = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				}
				m.cfgAt.Store(&next)
			}
			close(release)
			select {
			case err := <-result:
				if !errors.Is(err, errUsageSuperseded) {
					t.Fatalf("got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not finish")
			}
			if len(m.CachedProviderUsage()) != 0 {
				t.Fatal("stale snapshot")
			}
		})
	}
}
