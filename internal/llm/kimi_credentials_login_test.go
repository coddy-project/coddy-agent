package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestKimiLoginAttemptsInvalidated(t *testing.T) {
	for _, action := range []string{"begin", "remove", "cancel", "complete"} {
		t.Run(action, func(t *testing.T) {
			s := NewKimiCredentialStore(privateKimiTestDir(t))
			first, err := s.BeginLogin(context.Background(), "global")
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "begin":
				second, err := s.BeginLogin(context.Background(), "global")
				if err != nil {
					t.Fatal(err)
				}
				if second == first {
					t.Fatal("attempt reused")
				}
			case "remove":
				if err := s.Remove("global"); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if err := s.CancelLogin(context.Background(), "global", first); err != nil {
					t.Fatal(err)
				}
			case "complete":
				if err := s.CompleteLogin(context.Background(), "global", first, kimiTestCredential("new", "refresh", 0)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.CompleteLogin(context.Background(), "global", first, kimiTestCredential("stale", "refresh", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
				t.Fatalf("stale completion: %v", err)
			}
		})
	}
}

func TestKimiLoginForceAfterLogoutDoesNotRotateNewAccount(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	a, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", a, kimiTestCredential("old", "old-refresh", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	server, httpServer := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, s, httpServer.URL, httpServer.Client())
	r.hookAfterLoad = func() {
		if err := s.Remove("global"); err != nil {
			t.Error(err)
			return
		}
		b, err := s.BeginLogin(context.Background(), "global")
		if err != nil {
			t.Error(err)
			return
		}
		if err := s.CompleteLogin(context.Background(), "global", b, kimiTestCredential("new", "new-refresh", time.Now().Unix()+7200)); err != nil {
			t.Error(err)
		}
	}
	got, err := r.ForceAccessToken(context.Background())
	if err != nil || got != "new" || server.calls.Load() != 0 {
		t.Fatalf("token=%q calls=%d err=%v", got, server.calls.Load(), err)
	}
}

func TestKimiLoginSaveCannotBypassAttempt(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := s.Save("global", kimiTestCredential("a", "r", 0)); err == nil {
		t.Fatal("Save bypasses login attempt")
	}
}

func (s *KimiCredentialStore) saveForTest(region string, credential KimiCredential) error {
	attempt, err := s.BeginLogin(context.Background(), region)
	if err != nil {
		return err
	}
	return s.CompleteLogin(context.Background(), region, attempt, credential)
}

func TestKimiLoginRecoversMalformedCredential(t *testing.T) {
	for _, withIntent := range []bool{false, true} {
		t.Run(fmt.Sprint(withIntent), func(t *testing.T) {
			s := NewKimiCredentialStore(privateKimiTestDir(t))
			if err := os.WriteFile(s.CredentialPath("global"), []byte(`{"access_token":`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Load("global"); !errors.Is(err, ErrKimiCredentialCorrupt) {
				t.Fatalf("corrupt load: %v", err)
			}
			if withIntent {
				if err := s.writeIntent("global", kimiRefreshIntent{Generation: 0, Target: 1}); err != nil {
					t.Fatal(err)
				}
			}
			attempt, err := s.BeginLogin(context.Background(), "global")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "refresh", 0)); err != nil {
				t.Fatal(err)
			}
			got, state, err := s.Load("global")
			if err != nil || state != KimiCredentialValid || got.AccessToken != "fresh" {
				t.Fatalf("credential=%+v state=%v err=%v", got, state, err)
			}
			if withIntent && got.Generation == 1 {
				t.Fatal("credential generation matches abandoned intent")
			}
			if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("stale", "refresh", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
				t.Fatalf("replay: %v", err)
			}
		})
	}
}

func TestKimiLoginRejectsUnsafeCorruptCredential(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	outside := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(outside, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, s.CredentialPath("global")); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "r", 0)); err == nil {
		t.Fatal("unsafe credential accepted")
	}
}

func TestKimiLoginRejectsGenerationOverflow(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	data := []byte(`{"access_token":"old","generation":18446744073709551615}`)
	if err := os.WriteFile(s.CredentialPath("global"), data, 0600); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "r", 0)); err == nil {
		t.Fatal("generation overflow accepted")
	}
}

func TestKimiLoginConsumeSyncFailureCannotReplay(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := os.WriteFile(s.CredentialPath("global"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	restore := platform.InjectDirSyncFault()
	err = s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "r", 0))
	restore()
	if err == nil {
		t.Fatal("directory sync failure ignored")
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "r", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
		t.Fatalf("replay: %v", err)
	}
}

func TestKimiLoginRecoverCredentialWriteFailureCannotReplay(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := os.WriteFile(s.CredentialPath("global"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	restore := platform.InjectWriteFault(1)
	err = s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "refresh", 0))
	restore()
	if !errors.Is(err, platform.ErrInjectedWriteFault) {
		t.Fatalf("expected credential write fault: %v", err)
	}
	if _, _, err := s.Load("global"); !errors.Is(err, ErrKimiCredentialCorrupt) {
		t.Fatalf("old credential: %v", err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "refresh", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
		t.Fatalf("replay: %v", err)
	}
}

func TestKimiLoginRecoversWrongCredentialTypes(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := os.WriteFile(s.CredentialPath("global"), []byte(`{"access_token":42,"generation":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("global"); !errors.Is(err, ErrKimiCredentialCorrupt) {
		t.Fatalf("load: %v", err)
	}
	if err := s.writeIntent("global", kimiRefreshIntent{Generation: 7, Target: 8}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "refresh", 0)); err != nil {
		t.Fatal(err)
	}
	got, state, err := s.Load("global")
	if err != nil || state != KimiCredentialValid || got.AccessToken != "fresh" || got.Generation == 8 {
		t.Fatalf("credential=%+v state=%v err=%v", got, state, err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("stale", "refresh", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
		t.Fatalf("replay: %v", err)
	}
}

func TestKimiLoginWrongCredentialTypesPreservesOverflow(t *testing.T) {
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := os.WriteFile(s.CredentialPath("global"), []byte(`{"access_token":42,"generation":18446744073709551615}`), 0600); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("fresh", "refresh", 0)); err == nil {
		t.Fatal("generation overflow accepted")
	}
}
