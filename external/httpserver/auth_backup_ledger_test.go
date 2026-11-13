//go:build http

package httpserver

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

func backupLedgerFixture(t *testing.T) (*Server, *config.Config, []string) {
	t.Helper()
	s := newTOTPLoginServer(t, time.Unix(1800000000, 0))
	state, codes, err := webauth.GenerateBackupCodes(2)
	if err != nil {
		t.Fatal(err)
	}
	c := s.activeCfg()
	c.HTTPServer.Login.BackupCodes = state
	c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	c.Paths.ConfigPath = filepath.Join(c.Paths.Home, "config.yaml")
	raw, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.Paths.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	stale := *c
	return s, &stale, codes
}
func backupAttempt(t *testing.T, s *Server, code string) int {
	t.Helper()
	return challengeVerify(s, challengeStart(t, s), code).Code
}
func TestBackupLedgerRejectsStaleReload(t *testing.T) {
	s, stale, codes := backupLedgerFixture(t)
	if got := backupAttempt(t, s, codes[0]); got != http.StatusOK {
		t.Fatalf("first: %d", got)
	}
	if err := s.mgr.InstallConfig(stale); err != nil {
		t.Fatal(err)
	}
	if got := backupAttempt(t, s, codes[0]); got != http.StatusUnauthorized {
		t.Fatalf("stale reload resurrected code: %d", got)
	}
}
func TestBackupLedgerRejectsRecoveryAndRestart(t *testing.T) {
	s, stale, codes := backupLedgerFixture(t)
	if got := backupAttempt(t, s, codes[0]); got != http.StatusOK {
		t.Fatalf("first: %d", got)
	}
	if err := os.WriteFile(stale.Paths.ConfigPath, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := config.LoadWithPaths(stale.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.HTTPServer.Login.BackupCodes.Hashes) != 2 {
		t.Fatal("backup did not restore original hashes")
	}
	s2 := newLoginServer(t, recovered.HTTPServer.Login)
	s2.ReplaceConfig(recovered)
	if got := backupAttempt(t, s2, codes[0]); got != http.StatusUnauthorized {
		t.Fatalf("recovery resurrected code: %d", got)
	}
	if got := backupAttempt(t, s2, codes[1]); got != http.StatusOK {
		t.Fatalf("unused code after restart: %d", got)
	}
}
func TestBackupLedgerMalformedAndUnreadableFailClosed(t *testing.T) {
	for _, kind := range []string{"malformed", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			s, stale, codes := backupLedgerFixture(t)
			path := filepath.Join(stale.Paths.Home, "webauth-backup-consumed.json")
			if kind == "malformed" {
				if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if got := backupAttempt(t, s, codes[0]); got != http.StatusInternalServerError {
				t.Fatalf("%s ledger: %d", kind, got)
			}
		})
	}
}
func TestBackupLedgerStoresHashesOwnerOnly(t *testing.T) {
	s, stale, codes := backupLedgerFixture(t)
	if got := backupAttempt(t, s, codes[0]); got != http.StatusOK {
		t.Fatalf("first: %d", got)
	}
	path := filepath.Join(stale.Paths.Home, "webauth-backup-consumed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(codes[0])) || !bytes.Contains(data, []byte(stale.HTTPServer.Login.BackupCodes.Hashes[0])) {
		t.Fatal("ledger must contain hash, not plaintext")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("ledger mode %o", info.Mode().Perm())
	}
}

func TestBackupLedgerDirectorySyncFailureDeniesSession(t *testing.T) {
	s, _, codes := backupLedgerFixture(t)
	restore := platform.InjectDirSyncFault()
	defer restore()
	if got := backupAttempt(t, s, codes[0]); got != http.StatusInternalServerError {
		t.Fatalf("directory sync failure: %d", got)
	}
}

func TestBackupLedgerOverlappingOwners(t *testing.T) {
	if !platform.FileLockingAvailable {
		t.Skip("platform has no cross-process file locks")
	}
	s, stale, codes := backupLedgerFixture(t)
	lockPath := filepath.Join(stale.Paths.Home, "webauth-backup-consumed.json.lock")
	release, err := platform.LockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	challenge := challengeStart(t, s)
	done := make(chan int, 1)
	go func() { done <- challengeVerify(s, challenge, codes[1]).Code }()
	select {
	case status := <-done:
		release()
		t.Fatalf("verification bypassed the other ledger owner's lock: %d", status)
	case <-time.After(300 * time.Millisecond):
	}
	// The other owner consumed the first code after the second request started.
	ledger, err := loadBackupLedger(stale.Paths.Home)
	if err != nil {
		release()
		t.Fatal(err)
	}
	ok, err := recordBackupConsumption(stale.Paths.Home, stale.HTTPServer.Login.BackupCodes.Generation, stale.HTTPServer.Login.BackupCodes.Hashes[0], ledger)
	if err != nil || !ok {
		release()
		t.Fatalf("first owner: %v %v", ok, err)
	}
	release()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("second owner: %d", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second owner did not finish")
	}
	if err := s.mgr.InstallConfig(stale); err != nil {
		t.Fatal(err)
	}
	for i, code := range codes {
		if status := backupAttempt(t, s, code); status != http.StatusUnauthorized {
			t.Fatalf("code %d resurrected after stale config: %d", i, status)
		}
	}
}

func TestBackupLedgerLockFailureDeniesSession(t *testing.T) {
	s, stale, codes := backupLedgerFixture(t)
	if err := os.Mkdir(filepath.Join(stale.Paths.Home, "webauth-backup-consumed.json.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if status := backupAttempt(t, s, codes[0]); status != http.StatusInternalServerError {
		t.Fatalf("lock error: %d", status)
	}
}
