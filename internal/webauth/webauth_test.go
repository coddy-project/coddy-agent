package webauth

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// cheapParams keeps the argon2 cost out of the test suite's runtime while still
// exercising the real encode/decode path.
var cheapParams = HashParams{Memory: 64, Time: 1, Threads: 1, SaltLen: 8, KeyLen: 16}

func mustHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := HashPasswordWith(plain, cheapParams)
	if err != nil {
		t.Fatalf("hash %q: %v", plain, err)
	}
	return h
}

func TestHashPasswordRoundTrip(t *testing.T) {
	h := mustHash(t, "correct-horse-battery-staple")
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("hash is not in PHC argon2id form: %q", h)
	}
	ok, err := VerifyPassword(h, "correct-horse-battery-staple")
	if err != nil || !ok {
		t.Fatalf("verify correct password: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(h, "correct-horse-battery-stapl")
	if err != nil {
		t.Fatalf("verify wrong password errored: %v", err)
	}
	if ok {
		t.Fatal("a wrong password verified")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a := mustHash(t, "same")
	b := mustHash(t, "same")
	if a == b {
		t.Fatal("two hashes of the same password are identical, so the salt is not random")
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("an empty password must not be hashable")
	}
}

func TestDefaultHashParamsVerify(t *testing.T) {
	// The shipped parameters are exercised once: a typo in them would otherwise
	// only show up on a real server.
	h, err := HashPassword("default-params")
	if err != nil {
		t.Fatalf("hash with default params: %v", err)
	}
	ok, err := VerifyPassword(h, "default-params")
	if err != nil || !ok {
		t.Fatalf("verify with default params: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	good := mustHash(t, "pw")
	cases := map[string]string{
		"empty":              "",
		"plaintext":          "hunter2",
		"bcrypt":             "$2y$10$abcdefghijklmnopqrstuv",
		"wrong algorithm":    "$argon2i$v=19$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"wrong version":      "$argon2id$v=16$m=64,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"truncated":          good[:len(good)-4],
		"no params":          "$argon2id$v=19$$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"zero memory":        "$argon2id$v=19$m=0,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"bad base64 salt":    "$argon2id$v=19$m=64,t=1,p=1$!!!!$aGFzaGhhc2hoYXNoaGFzaA",
		"padded base64 salt": "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHQ=$aGFzaGhhc2hoYXNoaGFzaA",
	}
	for name, enc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifyPassword(enc, "pw")
			if ok {
				t.Fatalf("malformed hash %q verified a password", enc)
			}
			if !errors.Is(err, ErrMalformedHash) {
				t.Fatalf("want ErrMalformedHash, got %v", err)
			}
			if IsHash(enc) {
				t.Fatalf("IsHash accepted %q", enc)
			}
		})
	}
	if !IsHash(good) {
		t.Fatal("IsHash rejected a hash it produced")
	}
}

func TestSessionIssueAndLookup(t *testing.T) {
	st := NewSessionStore()
	cred := CredentialFingerprint("operator", mustHash(t, "pw"))
	tok, sess, err := st.Issue("operator", cred, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok == "" || len(tok) < 32 {
		t.Fatalf("session token looks too short: %q", tok)
	}
	if sess.User != "operator" {
		t.Fatalf("session user = %q", sess.User)
	}
	got, ok := st.Lookup(tok, cred)
	if !ok || got.User != "operator" {
		t.Fatalf("lookup of a live session failed: %+v ok=%v", got, ok)
	}
	if _, ok := st.Lookup("not-a-token", cred); ok {
		t.Fatal("an unknown token was accepted")
	}
	if _, ok := st.Lookup("", cred); ok {
		t.Fatal("an empty token was accepted")
	}
}

func TestSessionTokensAreUnique(t *testing.T) {
	st := NewSessionStore()
	cred := CredentialFingerprint("operator", "hash")
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, _, err := st.Issue("operator", cred, time.Hour)
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		if seen[tok] {
			t.Fatalf("duplicate session token at %d", i)
		}
		seen[tok] = true
	}
}

func TestSessionExpires(t *testing.T) {
	now := time.Now()
	st := NewSessionStore()
	st.Now = func() time.Time { return now }
	cred := CredentialFingerprint("operator", "hash")
	tok, sess, err := st.Issue("operator", cred, 2*time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if want := now.Add(2 * time.Hour); !sess.ExpiresAt.Equal(want) {
		t.Fatalf("expiry = %v, want %v", sess.ExpiresAt, want)
	}
	now = now.Add(119 * time.Minute)
	if _, ok := st.Lookup(tok, cred); !ok {
		t.Fatal("session expired before its TTL")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := st.Lookup(tok, cred); ok {
		t.Fatal("session survived its TTL")
	}
	if n := st.Len(); n != 0 {
		t.Fatalf("expired session was not swept: %d left", n)
	}
}

func TestSessionZeroTTLFallsBackToDefault(t *testing.T) {
	now := time.Now()
	st := NewSessionStore()
	st.Now = func() time.Time { return now }
	_, sess, err := st.Issue("operator", "cred", 0)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if want := now.Add(DefaultSessionTTL); !sess.ExpiresAt.Equal(want) {
		t.Fatalf("expiry with a zero TTL = %v, want %v", sess.ExpiresAt, want)
	}
}

func TestSessionCredentialRotationInvalidates(t *testing.T) {
	st := NewSessionStore()
	oldCred := CredentialFingerprint("operator", mustHash(t, "old"))
	newCred := CredentialFingerprint("operator", mustHash(t, "new"))
	tok, _, err := st.Issue("operator", oldCred, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, ok := st.Lookup(tok, newCred); ok {
		t.Fatal("a session survived the password it was opened with")
	}
	if _, ok := st.Lookup(tok, oldCred); ok {
		t.Fatal("the stale session was not dropped on the first miss")
	}
}

func TestCredentialFingerprintChangesWithUserAndHash(t *testing.T) {
	a := CredentialFingerprint("operator", "hash")
	if a != CredentialFingerprint("operator", "hash") {
		t.Fatal("fingerprint is not stable")
	}
	if a == CredentialFingerprint("operator2", "hash") {
		t.Fatal("fingerprint ignores the user name")
	}
	if a == CredentialFingerprint("operator", "hash2") {
		t.Fatal("fingerprint ignores the password hash")
	}
	if strings.Contains(a, "hash") {
		t.Fatalf("fingerprint leaks the hash: %q", a)
	}
}

func TestSessionRevoke(t *testing.T) {
	st := NewSessionStore()
	cred := CredentialFingerprint("operator", "hash")
	tok, _, _ := st.Issue("operator", cred, time.Hour)
	st.Revoke(tok)
	if _, ok := st.Lookup(tok, cred); ok {
		t.Fatal("a revoked session still resolves")
	}
	st.Revoke(tok) // idempotent
	st.Revoke("")

	a, _, _ := st.Issue("a", cred, time.Hour)
	b, _, _ := st.Issue("b", cred, time.Hour)
	st.RevokeAll()
	if _, ok := st.Lookup(a, cred); ok {
		t.Fatal("RevokeAll left a session behind")
	}
	if _, ok := st.Lookup(b, cred); ok {
		t.Fatal("RevokeAll left a session behind")
	}
}

func TestSessionStoreIsBounded(t *testing.T) {
	now := time.Now()
	st := NewSessionStore()
	st.Now = func() time.Time {
		now = now.Add(time.Millisecond)
		return now
	}
	cred := CredentialFingerprint("operator", "hash")
	var first string
	for i := 0; i < maxSessions+20; i++ {
		tok, _, err := st.Issue("operator", cred, time.Hour)
		if err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
		if i == 0 {
			first = tok
		}
	}
	if n := st.Len(); n > maxSessions {
		t.Fatalf("store grew past the cap: %d", n)
	}
	if _, ok := st.Lookup(first, cred); ok {
		t.Fatal("the oldest session survived eviction")
	}
}

func TestSessionStoreIsConcurrencySafe(t *testing.T) {
	st := NewSessionStore()
	cred := CredentialFingerprint("operator", "hash")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				tok, _, err := st.Issue("operator", cred, time.Hour)
				if err != nil {
					t.Errorf("issue: %v", err)
					return
				}
				st.Lookup(tok, cred)
				st.Revoke(tok)
				st.Len()
			}
		}()
	}
	wg.Wait()
}

func TestThrottleProgressiveDelay(t *testing.T) {
	now := time.Now()
	th := &Throttle{Now: func() time.Time { return now }, Base: 100 * time.Millisecond, Max: 800 * time.Millisecond}
	const addr = "203.0.113.9:51234"

	if d := th.Delay(addr); d != 0 {
		t.Fatalf("first attempt waits %v, want 0", d)
	}
	want := []time.Duration{100, 200, 400, 800, 800}
	for i, w := range want {
		th.Penalize(addr)
		if d := th.Delay(addr); d != w*time.Millisecond {
			t.Fatalf("after %d failures delay = %v, want %v", i+1, d, w*time.Millisecond)
		}
	}
	th.Reset(addr)
	if d := th.Delay(addr); d != 0 {
		t.Fatalf("a correct password left a delay of %v", d)
	}
}

func TestThrottleForgetsAfterTheWindow(t *testing.T) {
	now := time.Now()
	th := &Throttle{Now: func() time.Time { return now }, Base: time.Second, Window: 10 * time.Minute}
	const addr = "198.51.100.4"
	th.Penalize(addr)
	th.Penalize(addr)
	if th.Delay(addr) == 0 {
		t.Fatal("failures were not counted")
	}
	now = now.Add(11 * time.Minute)
	if d := th.Delay(addr); d != 0 {
		t.Fatalf("the window did not expire: %v", d)
	}
	th.Penalize(addr)
	if d := th.Delay(addr); d != time.Second {
		t.Fatalf("the counter did not restart after the window: %v", d)
	}
}

func TestThrottleNeverDelaysLoopback(t *testing.T) {
	th := &Throttle{Base: time.Second}
	for _, addr := range []string{"127.0.0.1:5000", "[::1]:5000", "localhost:5000", "127.0.0.53"} {
		for i := 0; i < 20; i++ {
			th.Penalize(addr)
		}
		if d := th.Delay(addr); d != 0 {
			t.Fatalf("loopback %q was throttled by %v", addr, d)
		}
	}
}

func TestThrottleTableIsBounded(t *testing.T) {
	now := time.Now()
	th := &Throttle{Now: func() time.Time { return now }}
	for i := 0; i < maxThrottleEntries+50; i++ {
		th.Penalize(netAddrFor(i))
	}
	th.mu.Lock()
	n := len(th.entries)
	th.mu.Unlock()
	if n > maxThrottleEntries {
		t.Fatalf("throttle table grew past the cap: %d", n)
	}
}

// netAddrFor spreads synthetic sources over a /16 so the bound test sees
// distinct rows rather than one busy address.
func netAddrFor(i int) string {
	return "203.0." + itoa(i/256) + "." + itoa(i%256)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func TestNormalizeAddr(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:8080":  "127.0.0.1",
		"[::1]:8080":      "::1",
		"::1":             "::1",
		"203.0.113.7":     "203.0.113.7",
		"[2001:db8::1]":   "2001:db8::1",
		"::ffff:10.0.0.1": "10.0.0.1",
		"  10.0.0.2  ":    "10.0.0.2",
		"":                "",
	}
	for in, want := range cases {
		if got := NormalizeAddr(in); got != want {
			t.Fatalf("NormalizeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "127.0.0.53", "::1", "localhost", ""} {
		if !IsLoopbackAddr(addr) {
			t.Fatalf("%q should be loopback", addr)
		}
	}
	for _, addr := range []string{"10.0.0.1", "203.0.113.7", "2001:db8::1", "example.test"} {
		if IsLoopbackAddr(addr) {
			t.Fatalf("%q should not be loopback", addr)
		}
	}
}

func TestConcurrentVerifyIsBoundedNotBroken(t *testing.T) {
	// The derivation runs under a small semaphore so a flood of attempts cannot
	// ask for gigabytes at once. What that must not do is change any answer.
	h := mustHash(t, "pw")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := i%2 == 0
			plain := "pw"
			if !want {
				plain = "not-pw"
			}
			ok, err := VerifyPassword(h, plain)
			if err != nil {
				t.Errorf("verify: %v", err)
				return
			}
			if ok != want {
				t.Errorf("verify(%q) = %v, want %v", plain, ok, want)
			}
		}(i)
	}
	wg.Wait()
}

func TestVerifyPasswordRefusesRuinousCostParameters(t *testing.T) {
	// A hash carries its own cost, so a hash is also an instruction to
	// allocate. One that asks for four gigabytes per attempt is refused as
	// malformed rather than obeyed once per visitor.
	cases := map[string]string{
		"four gigabytes":    "$argon2id$v=19$m=4194304,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"a thousand passes": "$argon2id$v=19$m=64,t=1000,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"a hundred lanes":   "$argon2id$v=19$m=64,t=1,p=100$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
	}
	for name, enc := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifyPassword(enc, "pw")
			if ok || !errors.Is(err, ErrMalformedHash) {
				t.Fatalf("ok=%v err=%v, want a malformed-hash refusal", ok, err)
			}
			if IsHash(enc) {
				t.Fatal("IsHash accepted it, so config validation would let it through")
			}
		})
	}
	// The shipped parameters are comfortably inside the bounds.
	if !IsHash(mustHashDefault(t, "pw")) {
		t.Fatal("the default parameters are outside the bounds this build verifies")
	}
}

func mustHashDefault(t *testing.T, plain string) string {
	t.Helper()
	h, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

func TestPenalizeCountsBeforeItAnswers(t *testing.T) {
	// Ten attempts that arrive together would all read a delay of zero if each
	// waited to be judged wrong first.
	th := &Throttle{Base: 10 * time.Millisecond, Max: time.Second}
	const addr = "203.0.113.20:5000"
	var got []time.Duration
	for i := 0; i < 4; i++ {
		got = append(got, th.Penalize(addr))
	}
	want := []time.Duration{0, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt %d waited %v, want %v (all: %v)", i+1, got[i], want[i], got)
		}
	}
	th.Reset(addr)
	if d := th.Penalize(addr); d != 0 {
		t.Fatalf("a correct password left a penalty of %v", d)
	}
}

func TestPenalizeSparesLoopback(t *testing.T) {
	th := &Throttle{Base: time.Second}
	for i := 0; i < 20; i++ {
		if d := th.Penalize("127.0.0.1:5000"); d != 0 {
			t.Fatalf("loopback was throttled by %v", d)
		}
	}
}

func TestClientAddrBelievesAProxyOnlyFromLoopback(t *testing.T) {
	cases := []struct {
		name                         string
		remote, forwardedFor, realIP string
		want                         string
	}{
		{
			name: "a proxy on this machine speaks for its client",
			// Without this the documented deployment - nginx in front of a
			// loopback listener - would exempt the whole internet.
			remote: "127.0.0.1:54321", forwardedFor: "203.0.113.7, 10.0.0.1", want: "203.0.113.7",
		},
		{
			name:   "X-Real-IP when there is no forwarded chain",
			remote: "127.0.0.1:54321", realIP: "198.51.100.3", want: "198.51.100.3",
		},
		{
			name:   "a direct client cannot claim to be someone else",
			remote: "203.0.113.9:40000", forwardedFor: "127.0.0.1", want: "203.0.113.9",
		},
		{
			name:   "loopback with no headers stays loopback",
			remote: "127.0.0.1:54321", want: "127.0.0.1",
		},
		{
			name:   "a garbage header falls back to the connection",
			remote: "127.0.0.1:54321", forwardedFor: "   ", want: "127.0.0.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientAddr(tc.remote, tc.forwardedFor, tc.realIP); got != tc.want {
				t.Fatalf("ClientAddr = %q, want %q", got, tc.want)
			}
		})
	}
}
