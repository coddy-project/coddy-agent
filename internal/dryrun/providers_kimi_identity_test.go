package dryrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiDryRunIdentityCredential(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := config.ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, p.KimiIdentity))
	attempt, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteLogin(context.Background(), "global", attempt, llm.KimiCredential{AccessToken: "fixture"}); err != nil {
		t.Fatal(err)
	}
	hit := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit <- struct{}{}; w.WriteHeader(502) }))
	defer proxy.Close()
	p.Proxy = proxy.URL
	cfg := &config.Config{Providers: []config.ProviderConfig{p}}
	r := &runner{req: Request{Cfg: cfg, Paths: config.Paths{Home: home}, Timeout: time.Second}, rep: &Report{}}
	_ = r.probeProvider(context.Background(), &cfg.Providers[0], nil)
	select {
	case <-hit:
	default:
		t.Fatal("identity credential did not reach proxy")
	}
}
