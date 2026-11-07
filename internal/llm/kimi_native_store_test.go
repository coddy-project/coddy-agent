//go:build windows || darwin

package llm

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestKimiNativeManagedStore(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Fatal("managed Kimi store is unavailable")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if _, state, err := s.Load("global"); err != nil || state != KimiCredentialMissing {
		t.Fatalf("empty store: %v, %v", state, err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, KimiCredential{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh"}); err != nil {
		t.Fatal(err)
	}
	cred, state, err := s.Load("global")
	if err != nil || state != KimiCredentialValid || cred.AccessToken != "synthetic-access" {
		t.Fatalf("load: %#v, %v, %v", cred, state, err)
	}
	if err := s.writeIntent("global", kimiRefreshIntent{Target: cred.Generation + 1}); err != nil {
		t.Fatal(err)
	}
	if _, state, err := s.Load("global"); err != nil || state != KimiCredentialUncertain {
		t.Fatalf("ambiguous intent: %v, %v", state, err)
	}
	if err := s.Remove("global"); err != nil {
		t.Fatal(err)
	}
	if _, state, err := s.Load("global"); err != nil || state != KimiCredentialMissing {
		t.Fatalf("removed store: %v, %v", state, err)
	}
	if _, err := os.Stat(s.intentPath("global")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("intent after removal: %v", err)
	}
}

func TestKimiNativeRefreshAndAmbiguousOutcome(t *testing.T) {
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	server, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if token, err := r.AccessToken(context.Background()); err != nil || token != "a2" || server.calls.Load() != 1 {
		t.Fatalf("refresh: token=%q calls=%d err=%v", token, server.calls.Load(), err)
	}
	bad, badServer := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r = newTestRefresher(t, store, badServer.URL, badServer.Client())
	for i := 0; i < 2; i++ {
		if _, err := r.ForceAccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
			t.Fatalf("ambiguous refresh %d: %v", i, err)
		}
	}
	if bad.calls.Load() != 1 {
		t.Fatalf("ambiguous refresh retried: %d", bad.calls.Load())
	}
}
