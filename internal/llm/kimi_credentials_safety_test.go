package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKimiCredentialsLockHonorsCancellation(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	cred, lock, _, err := s.paths("global")
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.acquireLock(context.Background(), lock, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		unlock, err := s.acquireLock(ctx, lock, cred)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lock wait ignored context")
	}
}

func TestKimiCredentialsRejectArbitraryProductionHost(t *testing.T) {
	_, err := NewKimiCredentialRefresher(NewKimiCredentialStore(privateKimiTestDir(t)), "global", "https://attacker.example", "none")
	if err == nil {
		t.Fatal("arbitrary production OAuth host accepted")
	}
}

func TestKimiCredentialsMutationsRequireDurability(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	previous := kimiDirSyncDurable
	kimiDirSyncDurable = false
	defer func() { kimiDirSyncDurable = previous }()
	if err := s.saveForTest("global", kimiTestCredential("a", "r", 0)); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("save: %v", err)
	}
	if err := s.Remove("global"); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("remove: %v", err)
	}
}

func TestKimiCredentialsFastPathAccountReplacement(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := s.saveForTest("global", kimiTestCredential("old", "old-r", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	_, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, s, srv.URL, srv.Client())
	r.hookAfterLoad = func() {
		if err := s.saveForTest("global", kimiTestCredential("new", "new-r", time.Now().Unix()+7200)); err != nil {
			t.Error(err)
		}
	}
	got, err := r.AccessToken(context.Background())
	if err != nil || got != "new" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestKimiCredentialMissingWithIntentIsUncertain(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := s.writeIntent("global", kimiRefreshIntent{Generation: 1, Target: 2}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{true, false} {
		_, st, err := s.loadState("global", check)
		if err != nil || st != KimiCredentialUncertain {
			t.Fatalf("check=%v state=%v err=%v", check, st, err)
		}
	}
	_, server := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, s, server.URL, server.Client())
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("got %v", err)
	}
}

func TestKimiCredentialPeerCleanupFailureReported(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	old := kimiTestCredential("old", "old-refresh", 0)
	if err := s.saveForTest("global", old); err != nil {
		t.Fatal(err)
	}
	if err := s.saveUnlocked("global", kimiTestCredential("new", "new-refresh", 0)); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(s.Dir(), "not-removable")
	if err := os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "entry"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, server := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, s, server.URL, server.Client())
	if _, err := r.resolveUnauthorized(old, bad); err == nil {
		t.Fatal("cleanup error was hidden")
	}
}
