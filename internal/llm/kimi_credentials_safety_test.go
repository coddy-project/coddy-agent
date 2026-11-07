package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestKimiCredentialsLockHonorsCancellation(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
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
	if runtime.GOOS == "windows" {
		if err := s.saveForTest("global", kimiTestCredential("a", "r", 0)); err != nil {
			t.Fatal(err)
		}
		if err := s.Remove("global"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := s.saveForTest("global", kimiTestCredential("a", "r", 0)); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("save: %v", err)
	}
	if err := s.Remove("global"); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("remove: %v", err)
	}
}

func TestKimiCredentialsFastPathAccountReplacement(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
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
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
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
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
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

func TestKimiCredentialsLocalLockHasBoundedWait(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	s.lockWait = 80 * time.Millisecond
	cred, lock, _, err := s.paths("global")
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.acquireLock(context.Background(), lock, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result := make(chan error, 1)
	go func() {
		unlock, err := s.acquireLock(context.Background(), lock, cred)
		if err == nil {
			unlock()
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "busy over") {
			t.Fatalf("expected bounded contention, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("local lock wait exceeded the bound")
	}
}

func TestKimiCredentialsEarlierContextDeadlineWins(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	s.lockWait = time.Second
	cred, lock, _, err := s.paths("global")
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.acquireLock(context.Background(), lock, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.acquireLock(ctx, lock, cred); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestKimiCredentialsOneBudgetAcrossBothLocks(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	s.lockWait = 600 * time.Millisecond
	cred, lock, _, err := s.paths("global")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	held, err := kimiTryFlock(f)
	if err != nil || !held {
		t.Fatalf("flock: held=%v err=%v", held, err)
	}
	mu := kimiIdentityLock(cred)
	<-mu
	type result struct {
		err     error
		elapsed time.Duration
	}
	resultCh := make(chan result, 1)
	go func() {
		start := time.Now()
		unlock, err := s.acquireLock(context.Background(), lock, cred)
		if err == nil {
			unlock()
		}
		resultCh <- result{err, time.Since(start)}
	}()
	time.Sleep(350 * time.Millisecond)
	mu <- struct{}{}
	res := <-resultCh
	kimiUnlock(f)
	if res.err == nil || !strings.Contains(res.err.Error(), "busy over") {
		t.Fatalf("error=%v", res.err)
	}
	if res.elapsed > 850*time.Millisecond {
		t.Fatalf("two separate budgets: elapsed=%s", res.elapsed)
	}
	release, err := s.acquireLock(context.Background(), lock, cred)
	if err != nil {
		t.Fatalf("lock not released after timeout: %v", err)
	}
	release()
}
