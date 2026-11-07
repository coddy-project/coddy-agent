package session

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestKimiUsageIdentityPaths(t *testing.T) {
	home := t.TempDir()
	_ = os.Chmod(home, 0700)
	a := config.ProviderConfig{Name: "same", Type: "kimi", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := a
	b.KimiIdentity = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cfg := &config.Config{Paths: config.Paths{Home: home}}
	pa, pb := usageAuthPath(cfg, &a), usageAuthPath(cfg, &b)
	if pa != config.KimiIdentityDir(home, a.KimiIdentity) || pb != config.KimiIdentityDir(home, b.KimiIdentity) || pa == pb {
		t.Fatalf("identity paths %q %q", pa, pb)
	}
	a.Name = "renamed"
	if usageAuthPath(cfg, &a) != pa {
		t.Fatal("rename changed identity path")
	}
	if providerUsageFingerprint(a, pa, true) == providerUsageFingerprint(b, pb, true) {
		t.Fatal("identities share fingerprint")
	}
}

func TestKimiUsageNoIdentityCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":"cli","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	hit := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{}; w.WriteHeader(502) }))
	defer proxy.Close()
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", Region: "china", Proxy: proxy.URL}
	_, err := (&Manager{}).fetchProviderUsage(context.Background(), p, "", true)
	if err == nil {
		t.Fatal("expected proxy failure")
	}
	select {
	case <-hit:
	default:
		t.Fatalf("no-ID fallback failed: %v", err)
	}
	_, _ = (&Manager{}).fetchProviderUsage(context.Background(), p, "", false)
	select {
	case <-hit:
		t.Fatal("disallowed CLI contacted proxy")
	default:
	}
	p.Mode = "platform"
	_, _ = (&Manager{}).fetchProviderUsage(context.Background(), p, "", true)
	select {
	case <-hit:
		t.Fatal("platform contacted proxy")
	default:
	}
}

func TestKimiUsageIdentityFailureClosed(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	cli := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", cli)
	file := filepath.Join(cli, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(fmt.Sprintf(`{"access_token":"cli","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	hit := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{}; w.WriteHeader(502) }))
	defer proxy.Close()
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", Region: "china", Proxy: proxy.URL, KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	dir := config.KimiIdentityDir(home, p.KimiIdentity)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, cred, intent string }{{"corrupt", "bad", ""}, {"uncertain", `{"access_token":"x","generation":1}`, `{"target_generation":2}`}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "kimi-china-auth.json"), []byte(tc.cred), 0600); err != nil {
				t.Fatal(err)
			}
			intent := filepath.Join(dir, "kimi-china-refresh.intent")
			if tc.intent != "" {
				if err := os.WriteFile(intent, []byte(tc.intent), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				_ = os.Remove(intent)
			}
			_, err := (&Manager{}).fetchProviderUsage(context.Background(), p, dir, true)
			if err == nil {
				t.Fatal("expected error")
			}
			select {
			case <-hit:
				t.Fatal("fell back to CLI")
			default:
			}
		})
	}
}
