package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestKimiCredentialRejectsUnsafeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows mutations are unsupported")
	}
	for _, mode := range []string{"symlink", "writable"} {
		t.Run(mode, func(t *testing.T) {
			parent := privateKimiTestDir(t)
			real := filepath.Join(parent, "real")
			if err := os.Mkdir(real, 0700); err != nil {
				t.Fatal(err)
			}
			dir := real
			if mode == "symlink" {
				dir = filepath.Join(parent, "link")
				if err := os.Symlink(real, dir); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(real, 0777); err != nil {
				t.Fatal(err)
			}
			if _, err := NewKimiCredentialStore(dir).BeginLogin(context.Background(), "global"); err == nil {
				t.Fatal("unsafe directory accepted")
			}
		})
	}
}

func TestKimiCredentialRejectsSymlinkRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	external := filepath.Join(privateKimiTestDir(t), "credential")
	if err := os.WriteFile(external, []byte(`{"access_token":"foreign"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, s.CredentialPath("global")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("global"); err == nil {
		t.Fatal("followed credential symlink")
	}
}

func TestKimiCredentialRejectsSymlinkLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	_, lock, _, err := s.paths("global")
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(privateKimiTestDir(t), "lock")
	if err := os.WriteFile(external, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginLogin(context.Background(), "global"); err == nil {
		t.Fatal("followed lock symlink")
	}
}

func TestKimiCredentialRejectsSymlinkIntent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := os.WriteFile(s.CredentialPath("global"), []byte(`{"access_token":"valid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(privateKimiTestDir(t), "intent")
	if err := os.WriteFile(external, []byte(`{"target_generation":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, s.intentPath("global")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("global"); err == nil {
		t.Fatal("followed intent symlink")
	}
}

func TestKimiCredentialRejectsSymlinkLoginMarker(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" && runtime.GOOS != "darwin" {
		t.Skip("secure store unsupported")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	marker, err := s.loginPath("global")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(privateKimiTestDir(t), "marker")
	if err := os.WriteFile(external, []byte(attempt), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, marker); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("token", "refresh", 0)); err == nil {
		t.Fatal("followed login marker symlink")
	}
}

func TestKimiCredentialAnchorsDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" && runtime.GOOS != "darwin" {
		t.Skip("secure store unsupported")
	}
	parent := privateKimiTestDir(t)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "store")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	s := NewKimiCredentialStore(dir)
	cred, lock, _, _ := s.paths("global")
	opened, release, err := s.acquireScopedLock(context.Background(), lock, cred)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	old := filepath.Join(parent, "old")
	if err := os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := opened.writeDurable(cred, []byte("private")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(cred))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement received credential: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(old, filepath.Base(cred))); err != nil || string(data) != "private" {
		t.Fatalf("anchored write = %q, %v", data, err)
	}
}

func TestKimiCredentialParentTrust(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" && runtime.GOOS != "darwin" {
		t.Skip("secure store unsupported")
	}
	parent := privateKimiTestDir(t)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(parent, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(filepath.Join(link, "store"))
	if _, err := store.BeginLogin(context.Background(), "global"); err != nil {
		t.Fatalf("trusted symlink parent: %v", err)
	}
	if err := os.Chmod(real, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginLogin(context.Background(), "global"); err == nil {
		t.Fatal("accepted writable ancestor")
	}
}

func TestKimiCredentialRejectsLinkInWritableParent(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" && runtime.GOOS != "darwin" {
		t.Skip("secure store unsupported")
	}
	base := privateKimiTestDir(t)
	trusted := filepath.Join(base, "trusted")
	untrusted := filepath.Join(base, "untrusted")
	if err := os.Mkdir(trusted, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(untrusted, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(untrusted, 0777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(untrusted, "home")
	if err := os.Symlink(trusted, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKimiCredentialStore(filepath.Join(link, "store")).BeginLogin(context.Background(), "global"); err == nil {
		t.Fatal("accepted symlink in writable directory")
	}
}

func TestKimiCredentialBeginSyncFailureInvalidatesOldAttempt(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("durability unsupported")
	}
	s := NewKimiCredentialStore(privateKimiTestDir(t))
	old, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	restore := platform.InjectDirSyncFault()
	_, err = s.BeginLogin(context.Background(), "global")
	restore()
	if err == nil {
		t.Fatal("sync failure not reported")
	}
	if err := s.CompleteLogin(context.Background(), "global", old, kimiTestCredential("old", "r", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
		t.Fatalf("stale attempt: %v", err)
	}
}
