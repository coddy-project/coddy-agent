package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestKimiFactoryAllowsManualModel(t *testing.T) {
	p, err := NewProvider(ProviderInput{Type: "kimi", Model: "model", AuthPath: t.TempDir(), ProxyURL: "none", RetryDisabled: true})
	if err != nil || p == nil {
		t.Fatalf("manual model: provider=%v error=%v", p, err)
	}
}

func TestKimiDiscoveryUsesPlatformProxy(t *testing.T) {
	hits := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.Method + " " + r.Host
		http.Error(w, "test", http.StatusBadGateway)
	}))
	defer proxy.Close()
	_, err := ListModels(context.Background(), ProviderInput{Type: "kimi", Mode: "platform", Region: "china", APIKey: "secret", ProxyURL: proxy.URL})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected safe error, got %v", err)
	}
	select {
	case got := <-hits:
		if got != "CONNECT api.moonshot.cn:443" {
			t.Fatalf("unexpected proxy request %q", got)
		}
	default:
		t.Fatal("proxy not reached")
	}
}

func TestKimiDiscoveryCredentialSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "kimi-code.json")
	original := fmt.Sprintf(`{"access_token":"cli-token","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
	if err := os.WriteFile(cli, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	hits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; http.Error(w, "blocked", http.StatusBadGateway) }))
	defer proxy.Close()
	const identity = "0123456789abcdef0123456789abcdef"
	storeHome := t.TempDir()
	if err := os.Chmod(storeHome, 0700); err != nil {
		t.Fatal(err)
	}
	storeDir := config.KimiIdentityDir(storeHome, identity)
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(storeDir)
	in := ProviderInput{Type: "kimi", Region: "china", KimiIdentity: identity, AuthPath: store.Dir(), ProxyURL: proxy.URL}
	call := func() error { _, err := ListModels(context.Background(), in); return err }
	if err := call(); err == nil || hits != 1 {
		t.Fatalf("CLI fallback: err=%v hits=%d", err, hits)
	}
	if err := os.WriteFile(store.CredentialPath("china"), []byte(`{"access_token":"managed-token","generation":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := call(); err == nil || hits != 2 {
		t.Fatalf("managed: err=%v hits=%d", err, hits)
	}
	if err := os.WriteFile(store.CredentialPath("china"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := call(); !errors.Is(err, ErrKimiCredentialCorrupt) || hits != 2 {
		t.Fatalf("corrupt: err=%v hits=%d", err, hits)
	}
	if err := os.WriteFile(store.CredentialPath("china"), []byte(`{"access_token":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := call(); !errors.Is(err, ErrKimiCredentialUnauthorized) || hits != 2 {
		t.Fatalf("revoked: err=%v hits=%d", err, hits)
	}
	if err := os.WriteFile(store.CredentialPath("china"), []byte(`{"access_token":"managed","generation":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.intentPath("china"), []byte(`{"target_generation":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := call(); !errors.Is(err, ErrKimiCredentialUncertain) || hits != 2 {
		t.Fatalf("uncertain: err=%v hits=%d", err, hits)
	}
	if err := os.Remove(store.intentPath("china")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.CredentialPath("china")); err != nil {
		t.Fatal(err)
	}
	in.NoCLILogin = true
	if err := call(); !errors.Is(err, ErrKimiCLILoginRequired) || hits != 2 {
		t.Fatalf("disabled: err=%v hits=%d", err, hits)
	}
	in.NoCLILogin = false
	in.Region = "global"
	if err := call(); !errors.Is(err, ErrKimiCLILoginRequired) || hits != 2 {
		t.Fatalf("region: err=%v hits=%d", err, hits)
	}
	in.Mode = "platform"
	if err := call(); err == nil || hits != 2 {
		t.Fatalf("platform without key: err=%v hits=%d", err, hits)
	}
	b, err := os.ReadFile(cli)
	if err != nil || string(b) != original {
		t.Fatal("CLI credentials changed")
	}
}

func TestKimiFactoryManualCLIUsesProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kimi-code.json"), []byte(fmt.Sprintf(`{"access_token":"cli-token","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	if err := os.Chmod(store, 0700); err != nil {
		t.Fatal(err)
	}
	hits := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.Method + " " + r.Host
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer proxy.Close()
	p, err := NewProvider(ProviderInput{Type: "kimi", Model: "manual", Region: "china", AuthPath: store, ProxyURL: proxy.URL, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Complete(context.Background(), nil, nil)
	if err == nil || strings.Contains(err.Error(), "cli-token") {
		t.Fatalf("unsafe error %v", err)
	}
	select {
	case got := <-hits:
		if got != "CONNECT api.kimi.com:443" {
			t.Fatalf("proxy request %q", got)
		}
	default:
		t.Fatal("catalog did not reach proxy")
	}
}
