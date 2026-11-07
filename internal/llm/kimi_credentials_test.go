package llm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func privateKimiTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func kimiTestCredential(access, refresh string, expiresAt int64) KimiCredential {
	return KimiCredential{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		ExpiresIn:    3600,
		TokenType:    "Bearer",
	}
}

func TestKimiCanonicalRegion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "global"},
		{"global", "global"},
		{"china", "china"},
	} {
		got, err := KimiCanonicalRegion(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("KimiCanonicalRegion(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"mainland-cn", "mars", "cn", "..", "global/../x"} {
		if _, err := KimiCanonicalRegion(bad); err == nil {
			t.Errorf("KimiCanonicalRegion(%q): expected error", bad)
		}
	}
}

func TestKimiCredentialStoreSaveLoadRemove(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := filepath.Join(privateKimiTestDir(t), "providers", "demo")
	store := NewKimiCredentialStore(dir)

	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialMissing {
		t.Fatalf("empty store: state=%v err=%v", st, err)
	}

	want := kimiTestCredential("a1", "r1", time.Now().Unix()+3600)
	if err := store.saveForTest("global", want); err != nil {
		t.Fatal(err)
	}
	got, st, err := store.Load("global")
	if err != nil || st != KimiCredentialValid {
		t.Fatalf("state=%v err=%v", st, err)
	}
	if got.AccessToken != "a1" || got.RefreshToken != "r1" || got.ExpiresAt != want.ExpiresAt || got.TokenType != "Bearer" {
		t.Fatalf("credential mismatch: %+v", got)
	}
	if got.Generation != 1 {
		t.Fatalf("first save must start generation at 1, got %d", got.Generation)
	}

	want.AccessToken = "a2"
	if err := store.saveForTest("global", want); err != nil {
		t.Fatal(err)
	}
	got, _, _ = store.Load("global")
	if got.Generation != 2 || got.AccessToken != "a2" {
		t.Fatalf("generation must bump per durable write: %+v", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.CredentialPath("global"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %o, want 0600", info.Mode().Perm())
		}
		dinfo, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if dinfo.Mode().Perm() != 0o700 {
			t.Fatalf("credential dir mode = %o, want 0700", dinfo.Mode().Perm())
		}
	}

	if err := store.Remove("global"); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialMissing {
		t.Fatalf("after remove: state=%v err=%v", st, err)
	}
	if err := store.Remove("global"); err != nil {
		t.Fatalf("remove must be idempotent: %v", err)
	}
}

func TestKimiCredentialStoreCorrupt(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := os.WriteFile(store.CredentialPath("global"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load("global"); !errors.Is(err, ErrKimiCredentialCorrupt) {
		t.Fatalf("corrupt file must fail closed, got state err=%v", err)
	}
}

func TestKimiCredentialStoreRevoked(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	tombstone := []byte(`{"access_token":"","refresh_token":"","expires_at":0,"token_type":"Bearer","generation":4}`)
	if err := os.WriteFile(store.CredentialPath("global"), tombstone, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialRevoked {
		t.Fatalf("tombstone must read as revoked: state=%v err=%v", st, err)
	}
}

func TestKimiCredentialStoreRejectsBadRegion(t *testing.T) {
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	for _, bad := range []string{"..", "../x", "a/b", "unknown-region"} {
		if _, _, err := store.Load(bad); err == nil {
			t.Errorf("Load(%q): expected rejection", bad)
		}
		if err := store.saveForTest(bad, kimiTestCredential("a", "r", 0)); err == nil {
			t.Errorf("Save(%q): expected rejection", bad)
		}
	}
}

// An intent that outlives its writer is the crash marker: a credential whose
// generation is not the intent's target can no longer be trusted.
func TestKimiCredentialStoreIntentStates(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	cred := kimiTestCredential("a1", "r1", time.Now().Unix()+3600)
	if err := store.saveForTest("global", cred); err != nil { // generation 1
		t.Fatal(err)
	}

	// A completed intent (its target was committed) is inert and must not
	// downgrade the credential.
	if err := os.WriteFile(store.intentPath("global"), []byte(`{"region":"global","generation":1,"target_generation":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialValid {
		t.Fatalf("completed intent: state=%v err=%v", st, err)
	}

	// An abandoned intent (target never committed) makes the credential
	// uncertain: the refresh token may have rotated on the server.
	if err := os.WriteFile(store.intentPath("global"), []byte(`{"region":"global","generation":1,"target_generation":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialUncertain {
		t.Fatalf("abandoned intent: state=%v err=%v", st, err)
	}

	// A corrupt intent cannot prove the refresh completed: fail closed.
	if err := os.WriteFile(store.intentPath("global"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialUncertain {
		t.Fatalf("corrupt intent: state=%v err=%v", st, err)
	}

	// No credential at all: a leftover intent cannot manufacture uncertainty
	// about a file that is gone (logout/removal wins).
	if err := store.Remove("global"); err != nil {
		t.Fatal(err)
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialMissing {
		t.Fatalf("missing credential: state=%v err=%v", st, err)
	}
}

func TestKimiCredentialStoreSaveClearsStaleIntent(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	cred := kimiTestCredential("a1", "r1", time.Now().Unix()+3600)
	if err := store.saveForTest("global", cred); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.intentPath("global"), []byte(`{"region":"global","generation":1,"target_generation":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, st, _ := store.Load("global"); st != KimiCredentialUncertain {
		t.Fatalf("precondition: state=%v", st)
	}
	// A fresh login supersedes the crashed refresh.
	if err := store.saveForTest("global", kimiTestCredential("a9", "r9", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	got, st, err := store.Load("global")
	if err != nil || st != KimiCredentialValid || got.AccessToken != "a9" {
		t.Fatalf("after re-login: state=%v err=%v", st, err)
	}
	if _, err := os.Stat(store.intentPath("global")); !os.IsNotExist(err) {
		t.Fatal("stale intent must be removed by a successful save")
	}
}

func TestKimiCredentialWireFormat(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", kimiTestCredential("a1", "r1", 123)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.CredentialPath("global"))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"access_token", "refresh_token", "expires_at", "expires_in", "generation"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("wire format missing %q: %s", key, raw)
		}
	}
}
