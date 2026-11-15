package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// kimiRefreshServer records refresh calls and answers with a rotating pair.
type kimiRefreshServer struct {
	t     *testing.T
	calls atomic.Int32
	// hold, when non-nil, blocks every request until closed.
	hold  chan struct{}
	reply func(call int32, w http.ResponseWriter)
}

func (s *kimiRefreshServer) handler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.t.Errorf("form: %v", err)
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/api/oauth/token") {
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
	if got := r.Form.Get("grant_type"); got != "refresh_token" {
		s.t.Errorf("grant_type = %q", got)
	}
	if got := r.Form.Get("client_id"); got != kimiClientID {
		s.t.Errorf("client_id = %q", got)
	}
	n := s.calls.Add(1)
	if s.hold != nil {
		<-s.hold
	}
	s.reply(n, w)
}

func okReply(n int32, w http.ResponseWriter) {
	_, _ = fmt.Fprintf(w, `{"access_token":"a%d","refresh_token":"r%d","expires_in":7200,"token_type":"Bearer"}`, n+1, n+1)
}

func newKimiRefreshServer(t *testing.T, reply func(int32, http.ResponseWriter)) (*kimiRefreshServer, *httptest.Server) {
	t.Helper()
	s := &kimiRefreshServer{t: t, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	return s, srv
}

func newTestRefresher(t *testing.T, store *KimiCredentialStore, host string, client *http.Client) *KimiCredentialRefresher {
	t.Helper()
	r, err := NewKimiCredentialRefresher(store, "global", host, client, WithKimiSleep(func(time.Duration) {}))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func expiringKimiCredential() KimiCredential {
	return kimiTestCredential("old-access", "old-refresh", time.Now().Unix()+30)
}

func TestKimiRefresherClientRequired(t *testing.T) {
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if _, err := NewKimiCredentialRefresher(store, "global", "https://auth.kimi.ai", nil); err == nil {
		t.Fatal("nil client must be rejected, never replaced by a default")
	}
	if _, err := NewKimiCredentialRefresher(store, "global", "https://auth.kimi.ai", 42); err == nil {
		t.Fatal("non-client argument must be rejected")
	}
	if _, err := NewKimiCredentialRefresher(store, "global", "https://auth.kimi.ai", "none"); err != nil {
		t.Fatalf("proxy setting must build a client: %v", err)
	}
}

func TestKimiRefresherRejectsBadArgs(t *testing.T) {
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	for _, tc := range []struct{ region, host string }{
		{"unknown-region", "https://auth.kimi.ai"},
		{"global", ""},
		{"global", "not-a-url"},
		{"global", "ftp://auth.kimi.ai"},
		{"global", "https://user@auth.kimi.ai"},
		{"global", "https://auth.kimi.ai#frag"},
	} {
		if _, err := NewKimiCredentialRefresher(store, tc.region, tc.host, http.DefaultClient); err == nil {
			t.Errorf("region=%q host=%q: expected rejection", tc.region, tc.host)
		}
	}
}

func TestKimiAccessTokenFreshSkipsRemote(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", kimiTestCredential("a1", "r1", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	tok, err := r.AccessToken(context.Background())
	if err != nil || tok != "a1" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("a credential outside the refresh window must not call the server")
	}
}

func TestKimiAccessTokenNoExpirySkipsRemote(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", kimiTestCredential("a1", "r1", 0)); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if tok, err := r.AccessToken(context.Background()); err != nil || tok != "a1" || s.calls.Load() != 0 {
		t.Fatalf("token=%q calls=%d err=%v", tok, s.calls.Load(), err)
	}
}

func TestKimiAccessTokenMissingRevokedUncertain(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())

	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialMissing) {
		t.Fatalf("missing: %v", err)
	}

	// Revoked tombstone: re-login required, never a refresh call.
	if err := os.WriteFile(store.CredentialPath("global"),
		[]byte(`{"access_token":"","refresh_token":"","expires_at":0,"token_type":"Bearer","generation":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUnauthorized) {
		t.Fatalf("revoked: %v", err)
	}

	// Stale abandoned intent: the refresh outcome is unknown, re-login required.
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.intentPath("global"), []byte(`{"region":"global","generation":3,"target_generation":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("uncertain: %v", err)
	}
	if s.calls.Load() != 0 {
		t.Fatalf("no remote call allowed in dead-end states, got %d", s.calls.Load())
	}
}

func TestKimiAccessTokenRefreshSuccess(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())

	tok, err := r.AccessToken(context.Background())
	if err != nil || tok != "a2" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("exactly one rotation expected, got %d", s.calls.Load())
	}
	got, st, err := store.Load("global")
	if err != nil || st != KimiCredentialValid {
		t.Fatalf("state=%v err=%v", st, err)
	}
	if got.AccessToken != "a2" || got.RefreshToken != "r2" || got.Generation != 2 {
		t.Fatalf("rotated credential must be persisted whole: %+v", got)
	}
	if _, err := os.Stat(store.intentPath("global")); !os.IsNotExist(err) {
		t.Fatal("intent must be removed after a durable commit")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.CredentialPath("global"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %o, want 0600", info.Mode().Perm())
		}
	}
}

// Many callers in one process must share exactly one remote rotation.
func TestKimiAccessTokenConcurrentSingleFlight(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)

	const n = 8
	var wg sync.WaitGroup
	tokens := make([]string, n)
	errs := make([]error, n)
	refreshers := make([]*KimiCredentialRefresher, n)
	for i := range refreshers {
		refreshers[i] = newTestRefresher(t, store, srv.URL, srv.Client())
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = refreshers[i].AccessToken(context.Background())
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil || tokens[i] != "a2" {
			t.Fatalf("goroutine %d: token=%q err=%v", i, tokens[i], errs[i])
		}
	}
	if s.calls.Load() != 1 {
		t.Fatalf("concurrent callers must coalesce to one rotation, got %d", s.calls.Load())
	}
}

// A second OS process holding the same store must not trigger a second
// rotation: the file lock and the post-lock re-read make one refresh serve all.
func TestKimiAccessTokenCrossProcessSingleRotation(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)

	run := func() (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestKimiCredentialsHelperProcess")
		platform.AdaptCommand(cmd)
		cmd.Env = append(os.Environ(),
			"KIMI_CRED_HELPER=1", "KIMI_CRED_MODE=access",
			"KIMI_CRED_DIR="+dir, "KIMI_CRED_REGION=global", "KIMI_CRED_HOST="+srv.URL)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	type result struct {
		out string
		err error
	}
	res := make([]result, 2)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i].out, res[i].err = run()
		}(i)
	}
	wg.Wait()
	for i, r := range res {
		if r.err != nil || !strings.Contains(r.out, "TOKEN:a2") {
			t.Fatalf("child %d: out=%q err=%v", i, r.out, r.err)
		}
	}
	if s.calls.Load() != 1 {
		t.Fatalf("two processes must share one rotation, got %d", s.calls.Load())
	}
}

// A logout racing a refresh holds the same lock: either it runs first and the
// refresh sees the credential gone, or it runs after and removes the new one.
// A stale refresh response must never resurrect a deleted credential.
func TestKimiAccessTokenLogoutOrdering(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	s.hold = make(chan struct{})
	defer close(s.hold)

	r := newTestRefresher(t, store, srv.URL, srv.Client())
	// Remove the credential between the pre-lock read and the lock
	// acquisition, deterministically.
	r.hookAfterLoad = func() {
		if err := store.Remove("global"); err != nil {
			t.Errorf("remove: %v", err)
		}
	}
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialMissing) {
		t.Fatalf("refresh after logout must report missing, got %v", err)
	}
	if _, err := os.Stat(store.CredentialPath("global")); !os.IsNotExist(err) {
		t.Fatal("a stale refresh response must not resurrect a removed credential")
	}
	if s.calls.Load() != 0 {
		t.Fatal("no remote call may follow a missing post-lock read")
	}
}

// A login that lands while a force-refresh waits for the lock is a peer
// rotation: the waiter takes the newer generation instead of rotating again.
func TestKimiAccessTokenForcePeerRotation(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	r.hookAfterLoad = func() {
		if err := store.saveForTest("global", kimiTestCredential("peer-access", "peer-refresh", time.Now().Unix()+7200)); err != nil {
			t.Errorf("peer save: %v", err)
		}
	}
	tok, err := r.ForceAccessToken(context.Background())
	if err != nil || tok != "peer-access" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("a forced refresh must not rotate when the store moved while waiting")
	}
}

// A forced refresh on an unchanged credential does rotate.
func TestKimiAccessTokenForceUnchanged(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if tok, err := r.ForceAccessToken(context.Background()); err != nil || tok != "a2" || s.calls.Load() != 1 {
		t.Fatalf("token=%q calls=%d err=%v", tok, s.calls.Load(), err)
	}
}

// A 401/invalid_grant without a peer rotation tombstones the credential:
// the next process sees "rejected, sign in again", not a dead refresh token.
func TestKimiAccessTokenInvalidGrantTombstones(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	})
	r := newTestRefresher(t, store, srv.URL, srv.Client())

	_, err := r.AccessToken(context.Background())
	if !errors.Is(err, ErrKimiCredentialUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("unauthorized must not be retried, got %d calls", s.calls.Load())
	}
	if strings.Contains(err.Error(), "old-refresh") {
		t.Fatal("errors must never carry token material")
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialRevoked {
		t.Fatalf("credential must be tombstoned: state=%v err=%v", st, err)
	}
	if _, err := os.Stat(store.intentPath("global")); !os.IsNotExist(err) {
		t.Fatal("intent must be cleared after a tombstone commit")
	}
	// A second attempt knows the outcome already: no new remote call.
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUnauthorized) {
		t.Fatalf("tombstoned: %v", err)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("a tombstoned credential must not be retried, got %d calls", s.calls.Load())
	}
}

// A peer process that rotated while our request was in flight makes the 401 a
// stale-token race: adopt its generation, do not tombstone its credential.
func TestKimiAccessTokenUnauthorizedPeerRotation(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	_, srv := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) {
		// A writer that does not hold our lock lands a newer generation.
		if err := store.saveUnlocked("global", kimiTestCredential("peer-access", "peer-refresh", time.Now().Unix()+7200)); err != nil {
			t.Errorf("peer write: %v", err)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	})
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	tok, err := r.AccessToken(context.Background())
	if err != nil || tok != "peer-access" {
		t.Fatalf("a peer rotation must win over our 401: token=%q err=%v", tok, err)
	}
	got, st, _ := store.Load("global")
	if st != KimiCredentialValid || got.RefreshToken != "peer-refresh" {
		t.Fatalf("the peer credential must not be tombstoned: %+v state=%v", got, st)
	}
}

func TestKimiAccessTokenFailureLeavesIntent(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	for _, status := range []int{http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			store := NewKimiCredentialStore(privateKimiTestDir(t))
			if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
				t.Fatal(err)
			}
			s, srv := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) { w.WriteHeader(status) })
			r := newTestRefresher(t, store, srv.URL, srv.Client())
			if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
				t.Fatalf("got %v", err)
			}
			if s.calls.Load() != 1 {
				t.Fatalf("calls=%d", s.calls.Load())
			}
			if _, st, err := store.Load("global"); err != nil || st != KimiCredentialUncertain {
				t.Fatalf("state=%v err=%v", st, err)
			}
		})
	}
}

// The durable-intent chain: remote rotated, local write failed. The intent on
// disk outlives the call, and every later load fails closed to re-login; the
// old refresh token is never sent again.
func TestKimiAccessTokenPersistenceFailureUncertain(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	var restore func()
	s, srv := newKimiRefreshServer(t, func(n int32, w http.ResponseWriter) {
		restore = platform.InjectWriteFault(0)
		okReply(n, w)
	})
	r := newTestRefresher(t, store, srv.URL, srv.Client())

	_, err := r.AccessToken(context.Background())
	if restore != nil {
		restore()
	}
	if !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("refresh + failed persistence must be uncertain, got %v", err)
	}
	if _, st, _ := store.Load("global"); st != KimiCredentialUncertain {
		t.Fatalf("an unfinished intent must poison the credential: state=%v", st)
	}
	// The next attempt fails closed without touching the remote again.
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("poisoned credential must fail closed, got %v", err)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("the old refresh token must never be retried, got %d calls", s.calls.Load())
	}
}

// A directory-sync failure after the rename commits is uncertain at call time
// but self-heals: the written generation matches the intent target.
func TestKimiAccessTokenDirSyncFailure(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	var restore func()
	s, srv := newKimiRefreshServer(t, func(n int32, w http.ResponseWriter) {
		restore = platform.InjectDirSyncFault()
		okReply(n, w)
	})
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	_, err := r.AccessToken(context.Background())
	if restore != nil {
		restore()
	}
	if err == nil {
		t.Fatal("an unconfirmed directory sync must be reported")
	}
	if s.calls.Load() != 1 {
		t.Fatalf("calls=%d", s.calls.Load())
	}
	// The committed generation matches the intent's target: the next load is
	// valid again and the intent is inert.
	got, st, _ := store.Load("global")
	if st != KimiCredentialValid || got.AccessToken != "a2" {
		t.Fatalf("committed generation must read valid: %+v state=%v", got, st)
	}
}

// A malformed success body is a hard error, not a rotation and not a tombstone.
func TestKimiAccessTokenMalformedSuccess(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) {
		_, _ = fmt.Fprint(w, `{"access_token":"a2","expires_in":7200}`) // no refresh_token
	})
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	_, err := r.AccessToken(context.Background())
	if err == nil || errors.Is(err, ErrKimiCredentialUnauthorized) {
		t.Fatalf("malformed success must be a plain error, got %v", err)
	}
	got, st, _ := store.Load("global")
	if st != KimiCredentialUncertain || got.RefreshToken != "old-refresh" {
		t.Fatalf("credential must be uncertain: %+v state=%v", got, st)
	}
	if s.calls.Load() != 1 {
		t.Fatalf("malformed success must not retry, got %d", s.calls.Load())
	}
}

func TestKimiAccessTokenNoRefreshToken(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	cred := expiringKimiCredential()
	cred.RefreshToken = ""
	if err := store.saveForTest("global", cred); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUnauthorized) {
		t.Fatalf("a credential without refresh token needs re-login, got %v", err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("no remote call without a refresh token")
	}
}

// Where the platform cannot promise an inter-process lock or a durable
// directory flush, refresh fails closed instead of racing rotation.
func TestKimiAccessTokenUnsupportedPlatform(t *testing.T) {
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if !KimiRefreshSupported() {
		s, srv := newKimiRefreshServer(t, okReply)
		r := newTestRefresher(t, store, srv.URL, srv.Client())
		if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiStoreUnsupported) {
			t.Fatalf("unsupported store: %v", err)
		}
		if err := store.saveForTest("global", expiringKimiCredential()); !errors.Is(err, ErrKimiStoreUnsupported) {
			t.Fatalf("unsupported mutation: %v", err)
		}
		if s.calls.Load() != 0 {
			t.Fatal("unsupported storage must not contact the server")
		}
		return
	}
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)

	defer func(prevA, prevB bool) { kimiLockAvailable, kimiDirSyncDurable = prevA, prevB }(kimiLockAvailable, kimiDirSyncDurable)

	kimiLockAvailable = false
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("no lock primitive must fail closed, got %v", err)
	}
	if err := store.saveForTest("global", expiringKimiCredential()); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("a mutating op without a lock primitive must fail closed, got %v", err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("no remote call on an unsupported platform")
	}

	kimiLockAvailable, kimiDirSyncDurable = true, false
	if runtime.GOOS == "windows" {
		if token, err := r.AccessToken(context.Background()); err != nil || token != "a2" || s.calls.Load() != 1 {
			t.Fatalf("best-effort refresh: token=%q calls=%d err=%v", token, s.calls.Load(), err)
		}
	} else {
		if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiStoreUnsupported) {
			t.Fatalf("no durable dir sync must fail closed on refresh, got %v", err)
		}
		if s.calls.Load() != 0 {
			t.Fatal("no remote call without durable intent")
		}
	}
}

// The crash window itself: a process that wrote its intent and died leaves the
// next process with an uncertain credential, never a blind retry.
func TestKimiRefreshIntentSurvivesProcessDeath(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := privateKimiTestDir(t)
	store := NewKimiCredentialStore(dir)
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil { // generation 1
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestKimiCredentialsHelperProcess")
	platform.AdaptCommand(cmd)
	cmd.Env = append(os.Environ(),
		"KIMI_CRED_HELPER=1", "KIMI_CRED_MODE=intent",
		"KIMI_CRED_DIR="+dir, "KIMI_CRED_REGION=global")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	if _, err := os.Stat(store.intentPath("global")); err != nil {
		t.Fatal("the intent must be durable across process death")
	}
	if _, st, err := store.Load("global"); err != nil || st != KimiCredentialUncertain {
		t.Fatalf("an intent outliving its writer must fail closed: state=%v err=%v", st, err)
	}
}

// Helper child process for the cross-process tests. It runs inside the same
// test binary, so it uses the real store and lock code against the temp dir.
func TestKimiCredentialsHelperProcess(t *testing.T) {
	if os.Getenv("KIMI_CRED_HELPER") != "1" {
		return
	}
	dir := os.Getenv("KIMI_CRED_DIR")
	region := os.Getenv("KIMI_CRED_REGION")
	host := os.Getenv("KIMI_CRED_HOST")
	store := NewKimiCredentialStore(dir)
	switch os.Getenv("KIMI_CRED_MODE") {
	case "access":
		r, err := NewKimiCredentialRefresher(store, region, host, http.DefaultClient)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		tok, err := r.AccessToken(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Println("TOKEN:" + tok)
	case "intent":
		// A process that wrote its durable intent and died before the refresh
		// request went out. Generation 1 -> 2 matches the stored generation.
		if err := store.writeIntent(region, kimiRefreshIntent{Region: region, Generation: 1, Target: 2}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(4)
		}
	}
	os.Exit(0)
}

func TestKimiAccessTokenFreshWithAbandonedIntent(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", kimiTestCredential("a1", "r1", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	if err := store.writeIntent("global", kimiRefreshIntent{Generation: 1, Target: 2}); err != nil {
		t.Fatal(err)
	}
	s, srv := newKimiRefreshServer(t, okReply)
	r := newTestRefresher(t, store, srv.URL, srv.Client())
	if _, err := r.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("expected uncertain, got %v", err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("unexpected refresh")
	}
}

func TestKimiAccessTokenLostResponseFailsClosed(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", expiringKimiCredential()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	ref := newTestRefresher(t, store, srv.URL, srv.Client())
	if _, err := ref.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("expected uncertain, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ambiguous request repeated %d times", calls.Load())
	}
	if _, err := ref.AccessToken(context.Background()); !errors.Is(err, ErrKimiCredentialUncertain) {
		t.Fatalf("expected persistent uncertainty, got %v", err)
	}
}

func TestKimiAccessTokenFreshPeerRefreshWaitsForCommit(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	if err := store.saveForTest("global", kimiTestCredential("a1", "r1", time.Now().Unix()+7200)); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	allow := make(chan struct{})
	s, srv := newKimiRefreshServer(t, func(n int32, w http.ResponseWriter) { close(entered); <-allow; okReply(n, w) })
	peer := newTestRefresher(t, store, srv.URL, srv.Client())
	reader := newTestRefresher(t, store, srv.URL, srv.Client())
	done := make(chan error, 1)
	calls := 0
	reader.hookAfterLoad = func() {
		calls++
		switch calls {
		case 1:
			go func() { _, err := peer.ForceAccessToken(context.Background()); done <- err }()
			<-entered
		case 2:
			close(allow)
		}
	}
	token, err := reader.AccessToken(context.Background())
	if err != nil || token != "a2" {
		t.Fatalf("reader token=%q err=%v", token, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 2 || s.calls.Load() != 1 {
		t.Fatalf("hooks=%d requests=%d", calls, s.calls.Load())
	}
}
