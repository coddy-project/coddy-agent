package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// usageAnswer is what a test remote answers the usage route with.
type usageAnswer struct {
	status int
	header map[string]string
	body   string
}

type usageSeen struct {
	method string
	uri    string
	auth   string
	accept string
	body   int64
}

// newUsageRemote serves every request with the scripted answer and records it.
func newUsageRemote(t *testing.T, answer usageAnswer) (*httptest.Server, func() []usageSeen) {
	t.Helper()
	var mu sync.Mutex
	var seen []usageSeen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, usageSeen{method: r.Method, uri: r.RequestURI, auth: r.Header.Get("Authorization"), accept: r.Header.Get("Accept"), body: r.ContentLength})
		mu.Unlock()
		for k, v := range answer.header {
			w.Header().Set(k, v)
		}
		if answer.status == 0 {
			answer.status = http.StatusOK
		}
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []usageSeen {
		mu.Lock()
		defer mu.Unlock()
		return append([]usageSeen(nil), seen...)
	}
}

func usageInput(base string) ProviderInput {
	return ProviderInput{Name: "remote", Type: "coddy", BaseURL: base, APIKey: "shared-token", ProxyURL: "none"}
}

const okUsageDoc = `{"supported":true,"account_wide":true,"stale":false,
 "windows":[{"id":"session","label":"3h","used_percent":62,"reset_in_s":777,"exhausted":false},{"id":"week","label":"week","used_percent":7.5}],
 "blocked":true,"blockers":["session_exhausted"],"retry_in_s":120}`

func asUsageError(t *testing.T, err error) *ProviderUsageError {
	t.Helper()
	var ue *ProviderUsageError
	if !errors.As(err, &ue) {
		t.Fatalf("error %T %v is not a *ProviderUsageError", err, err)
	}
	return ue
}

func TestCoddyUsageForProviderReadsTheProjection(t *testing.T) {
	srv, seen := newUsageRemote(t, usageAnswer{body: okUsageDoc, header: map[string]string{"Content-Type": "application/json"}})
	in := usageInput(srv.URL + "/swarm/nodes/n1/")
	u, err := CoddyUsageForProvider(context.Background(), in, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Supported || !u.AccountWide || u.Stale || !u.Blocked || u.RetryInS != 120 ||
		len(u.Blockers) != 1 || u.Blockers[0] != "session_exhausted" || len(u.Windows) != 2 {
		t.Fatalf("decoded %+v", u)
	}
	w := u.Windows[0]
	if w.ID != "session" || w.Label != "3h" || w.UsedPercent != 62 || w.ResetInS == nil || *w.ResetInS != 777 || w.Exhausted {
		t.Errorf("window 0 %+v", w)
	}
	if u.Windows[1].ResetInS != nil || u.Windows[1].UsedPercent != 7.5 {
		t.Errorf("window 1 %+v", u.Windows[1])
	}

	got := seen()
	if len(got) != 1 {
		t.Fatalf("%d requests", len(got))
	}
	if got[0].method != http.MethodGet || got[0].uri != "/swarm/nodes/n1/coddy/llm/models/coder/usage" ||
		got[0].auth != "Bearer shared-token" || got[0].accept != "application/json" || got[0].body > 0 {
		t.Errorf("request %+v", got[0])
	}
}

func TestCoddyUsageForProviderEscapesTheAliasAndSendsNoCredentialWhenThereIsNone(t *testing.T) {
	srv, seen := newUsageRemote(t, usageAnswer{body: `{"supported":false}`})
	in := usageInput(srv.URL)
	in.APIKey = ""
	if _, err := CoddyUsageForProvider(context.Background(), in, "gpt 5/mini"); err != nil {
		t.Fatal(err)
	}
	got := seen()
	if len(got) != 1 || got[0].uri != "/coddy/llm/models/gpt%205%2Fmini/usage" {
		t.Fatalf("requests %+v", got)
	}
	if got[0].auth != "" {
		t.Errorf("an open remote got an Authorization header: %q", got[0].auth)
	}
}

func TestCoddyUsageForProviderMapsEveryAnswerTheRemoteCanGive(t *testing.T) {
	const hop = `{"error":{"node":"n1","message":"no such node"}}`
	wire := func(status int, kind, code string) string {
		return `{"status":` + itoa(status) + `,"kind":"` + kind + `","code":"` + code + `","message":"m","emitted":false}`
	}
	cases := []struct {
		name        string
		answer      usageAnswer
		unsupported bool   // answered {Supported: false} with no error
		kind        string // the ProviderUsageError kind, empty for a document
		retryAfter  time.Duration
	}{
		{name: "document", answer: usageAnswer{body: okUsageDoc}},
		{name: "supported false", answer: usageAnswer{body: `{"supported":false}`}, unsupported: true},
		{name: "phase 1 remote: reserved route", answer: usageAnswer{status: 404, body: wire(404, "invalid", "not_found")}, unsupported: true},
		{name: "unknown alias", answer: usageAnswer{status: 404, body: wire(404, "invalid", "unknown_model")}, kind: ProviderUsageInvalid},
		{name: "relay hop error 404", answer: usageAnswer{status: 404, body: hop}, kind: ProviderUsageUnavailable},
		{name: "html 404", answer: usageAnswer{status: 404, body: "<!doctype html><html><body>not found</body></html>", header: map[string]string{"Content-Type": "text/html"}}, kind: ProviderUsageUnavailable},
		{name: "plain 404", answer: usageAnswer{status: 404, body: "404 page not found\n"}, kind: ProviderUsageUnavailable},
		{name: "empty 404", answer: usageAnswer{status: 404}, kind: ProviderUsageUnavailable},
		{name: "not_found code on a 400", answer: usageAnswer{status: 400, body: wire(400, "invalid", "not_found")}, kind: ProviderUsageUnavailable},
		{name: "unknown_model code on a 400", answer: usageAnswer{status: 400, body: wire(400, "invalid", "unknown_model")}, kind: ProviderUsageUnavailable},
		{name: "protocol mismatch", answer: usageAnswer{status: 400, body: wire(400, "invalid", "protocol_mismatch")}, kind: ProviderUsageUnavailable},
		{name: "bad request", answer: usageAnswer{status: 400, body: wire(400, "invalid", "bad_json")}, kind: ProviderUsageUnavailable},
		{name: "401", answer: usageAnswer{status: 401, body: wire(401, "auth", "")}, kind: ProviderUsageUnauthorized},
		{name: "401 without a body", answer: usageAnswer{status: 401}, kind: ProviderUsageUnauthorized},
		{name: "403", answer: usageAnswer{status: 403, body: wire(403, "auth", "")}, kind: ProviderUsageUnauthorized},
		{name: "403 html", answer: usageAnswer{status: 403, body: "<html>forbidden</html>"}, kind: ProviderUsageUnauthorized},
		{name: "429 retry-after", answer: usageAnswer{status: 429, header: map[string]string{"Retry-After": "7"}}, kind: ProviderUsageUnavailable, retryAfter: 7 * time.Second},
		{name: "429 wire rate", answer: usageAnswer{status: 429, body: `{"status":429,"kind":"rate","retry_after_s":3,"emitted":false}`}, kind: ProviderUsageUnavailable, retryAfter: 3 * time.Second},
		{name: "408", answer: usageAnswer{status: 408}, kind: ProviderUsageUnavailable},
		{name: "502", answer: usageAnswer{status: 502, body: "bad gateway"}, kind: ProviderUsageUnavailable},
		{name: "503 retry-after", answer: usageAnswer{status: 503, header: map[string]string{"Retry-After": "11"}}, kind: ProviderUsageUnavailable, retryAfter: 11 * time.Second},
		{name: "504", answer: usageAnswer{status: 504}, kind: ProviderUsageUnavailable},
		{name: "500", answer: usageAnswer{status: 500, body: "boom"}, kind: ProviderUsageUnavailable},
		{name: "html 200", answer: usageAnswer{body: "<!doctype html><html><body>coddy</body></html>", header: map[string]string{"Content-Type": "text/html"}}, kind: ProviderUsageUnavailable},
		{name: "json 200 that is no usage document", answer: usageAnswer{body: `{"data":[]}`}, kind: ProviderUsageUnavailable},
		{name: "empty object 200", answer: usageAnswer{body: `{}`}, kind: ProviderUsageUnavailable},
		{name: "array 200", answer: usageAnswer{body: `[]`}, kind: ProviderUsageUnavailable},
		{name: "supported of the wrong type", answer: usageAnswer{body: `{"supported":"yes"}`}, kind: ProviderUsageUnavailable},
		{name: "204", answer: usageAnswer{status: 204}, kind: ProviderUsageUnavailable},
		{name: "truncated json", answer: usageAnswer{body: `{"supported":true,"windows":[{"id":`}, kind: ProviderUsageUnavailable},
		{name: "oversize body", answer: usageAnswer{body: `{"supported":true,"windows":[],"pad":"` + strings.Repeat("x", 1<<20) + `"}`}, kind: ProviderUsageUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newUsageRemote(t, tc.answer)
			u, err := CoddyUsageForProvider(context.Background(), usageInput(srv.URL), "coder")
			switch {
			case tc.kind != "":
				if u != nil {
					t.Fatalf("a document came with the failure: %+v", u)
				}
				ue := asUsageError(t, err)
				if ue.Kind != tc.kind {
					t.Fatalf("kind %q (%v), want %q", ue.Kind, ue, tc.kind)
				}
				if ue.RetryAfter != tc.retryAfter {
					t.Errorf("retry after %v, want %v", ue.RetryAfter, tc.retryAfter)
				}
				if tc.answer.status > 0 && ue.Status != tc.answer.status {
					t.Errorf("status %d, want %d", ue.Status, tc.answer.status)
				}
			case tc.unsupported:
				if err != nil || u == nil || u.Supported || len(u.Windows) != 0 {
					t.Fatalf("want {supported: false} and no error, got %+v %v", u, err)
				}
			default:
				if err != nil || u == nil || !u.Supported {
					t.Fatalf("want a document, got %+v %v", u, err)
				}
			}
		})
	}
}

// A body the remote sends is untrusted as far as memory and the terminal go:
// what the manager receives is bounded and clean.
func TestCoddyUsageForProviderBoundsAndCleansWhatTheRemoteSent(t *testing.T) {
	var windows []string
	for i := 0; i < 40; i++ {
		windows = append(windows, `{"id":"window-`+itoa(i)+`","label":"5h","used_percent":1}`)
	}
	body := `{"supported":true,"account_wide":true,"windows":[
	  {"id":"over","label":"\u001b[31mred\u0007","used_percent":250,"reset_in_s":-5},
	  {"id":"under","label":"x","used_percent":-4,"reset_in_s":99999999999},
	  {"id":"","label":"nameless","used_percent":5},
	  ` + strings.Join(windows, ",") + `],
	  "blocked":true,"blockers":["a\u001b[2Jb","wallet_empty"],"retry_in_s":-3}`
	srv, _ := newUsageRemote(t, usageAnswer{body: body})
	u, err := CoddyUsageForProvider(context.Background(), usageInput(srv.URL), "coder")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Windows) > 16 {
		t.Errorf("%d windows kept", len(u.Windows))
	}
	for _, w := range u.Windows {
		if w.UsedPercent < 0 || w.UsedPercent > 100 {
			t.Errorf("window %q used_percent %v is outside 0..100", w.ID, w.UsedPercent)
		}
		if w.ResetInS != nil && (*w.ResetInS < 0 || *w.ResetInS > int(coddyUsageSecondsCap.Seconds())) {
			t.Errorf("window %q reset_in_s %d is out of range", w.ID, *w.ResetInS)
		}
		for _, s := range []string{w.ID, w.Label} {
			if strings.ContainsAny(s, "\x1b\x07") {
				t.Errorf("window %q carries a control character: %q", w.ID, s)
			}
		}
	}
	for _, b := range u.Blockers {
		if strings.ContainsAny(b, "\x1b") {
			t.Errorf("blocker %q carries a control character", b)
		}
	}
	if u.RetryInS < 0 {
		t.Errorf("retry_in_s %d", u.RetryInS)
	}
	// The first ones stay, in order, and a window without an id is dropped.
	if len(u.Windows) != 16 || u.Windows[0].ID != "over" || u.Windows[1].ID != "under" || u.Windows[2].ID != "window-0" {
		t.Errorf("windows kept: %+v", u.Windows)
	}
	if u.Windows[0].UsedPercent != 100 || u.Windows[1].UsedPercent != 0 {
		t.Errorf("the percentage is not clamped: %+v %+v", u.Windows[0], u.Windows[1])
	}
	if u.Windows[0].ResetInS != nil {
		t.Errorf("a negative reset_in_s is kept: %d", *u.Windows[0].ResetInS)
	}
	if got := u.Windows[1].ResetInS; got == nil || *got != int(coddyUsageSecondsCap.Seconds()) {
		t.Errorf("a runaway reset_in_s is not cut to the cap: %v", got)
	}
	if len(u.Blockers) != 2 || u.RetryInS != 0 {
		t.Errorf("blockers %q retry %d", u.Blockers, u.RetryInS)
	}
}

// The credential never reaches an error, even when the remote echoes it.
func TestCoddyUsageErrorsNeverCarryTheCredential(t *testing.T) {
	for _, answer := range []usageAnswer{
		{status: 500, body: "internal error for Bearer shared-token and shared-token again"},
		{status: 404, body: `{"status":404,"kind":"invalid","code":"x","message":"no such thing for shared-token","emitted":false}`},
		{status: 401, body: `{"status":401,"kind":"auth","message":"token shared-token refused","emitted":false}`},
		{status: 200, body: "shared-token is not json"},
	} {
		srv, _ := newUsageRemote(t, answer)
		_, err := CoddyUsageForProvider(context.Background(), usageInput(srv.URL), "coder")
		if err == nil {
			t.Fatalf("answer %d: no error", answer.status)
		}
		if strings.Contains(err.Error(), "shared-token") {
			t.Errorf("answer %d: the credential reached the error: %v", answer.status, err)
		}
	}

	// A transport failure names the address without its userinfo.
	srv, _ := newUsageRemote(t, usageAnswer{})
	base := strings.Replace(srv.URL, "http://", "http://alice:s3cret@", 1)
	srv.Close()
	_, err := CoddyUsageForProvider(context.Background(), usageInput(base), "coder")
	if err == nil {
		t.Fatal("a closed remote answered")
	}
	if ue := asUsageError(t, err); ue.Kind != ProviderUsageUnavailable || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("transport failure: %v", err)
	}
}

func TestCoddyUsageForProviderIsBoundedByItsOwnTimeoutAndTheCallersContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	prev := coddyUsageTimeout
	coddyUsageTimeout = 100 * time.Millisecond
	t.Cleanup(func() { coddyUsageTimeout = prev })
	start := time.Now()
	_, err := CoddyUsageForProvider(context.Background(), usageInput(srv.URL), "coder")
	if ue := asUsageError(t, err); ue.Kind != ProviderUsageUnavailable {
		t.Fatalf("kind %q", ue.Kind)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the read took %v", d)
	}

	coddyUsageTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start = time.Now()
	_, err = CoddyUsageForProvider(ctx, usageInput(srv.URL), "coder")
	if ue := asUsageError(t, err); ue.Kind != ProviderUsageUnavailable {
		t.Fatalf("kind %q", ue.Kind)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("a cancelled read took %v", d)
	}
}

func TestCoddyUsageForProviderRefusesWhatItCannotAskFor(t *testing.T) {
	if _, err := CoddyUsageForProvider(context.Background(), usageInput(""), "coder"); asUsageError(t, err).Kind != ProviderUsageUnavailable {
		t.Errorf("an empty api_base: %v", err)
	}
	if _, err := CoddyUsageForProvider(context.Background(), usageInput("ftp://remote.example"), "coder"); asUsageError(t, err).Kind != ProviderUsageUnavailable {
		t.Errorf("a non-http api_base: %v", err)
	}
	srv, seen := newUsageRemote(t, usageAnswer{body: okUsageDoc})
	if _, err := CoddyUsageForProvider(context.Background(), usageInput(srv.URL), "  "); asUsageError(t, err).Kind != ProviderUsageUnavailable {
		t.Errorf("an empty alias: %v", err)
	}
	bad := usageInput(srv.URL)
	bad.ProxyURL = "ftp://127.0.0.1:21"
	if _, err := CoddyUsageForProvider(context.Background(), bad, "coder"); asUsageError(t, err).Kind != ProviderUsageUnavailable {
		t.Errorf("a bad proxy setting: %v", err)
	}
	if n := len(seen()); n != 0 {
		t.Errorf("%d requests left for a read that could not be built", n)
	}
}

func fingerprintRow() config.ProviderConfig {
	return config.ProviderConfig{Name: "remote", Type: "coddy", APIBase: "https://relay.example/swarm/nodes/n1", APIKey: "tok", Proxy: "none"}
}

func TestCoddyUsageFingerprintTracksEverythingTheReadDependsOn(t *testing.T) {
	t.Setenv("REMOTE_API_KEY", "")
	base := CoddyUsageFingerprint(fingerprintRow(), "coder")
	if len(base) != 16 || strings.Trim(base, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint %q is not 16 hex digits", base)
	}
	if again := CoddyUsageFingerprint(fingerprintRow(), "coder"); again != base {
		t.Fatalf("not stable: %q then %q", base, again)
	}

	same := map[string]func(*config.ProviderConfig){
		"trailing slash":        func(p *config.ProviderConfig) { p.APIBase += "/" },
		"spaces":                func(p *config.ProviderConfig) { p.APIBase = "  " + p.APIBase + " " },
		"host case":             func(p *config.ProviderConfig) { p.APIBase = "https://RELAY.example/swarm/nodes/n1" },
		"query and fragment":    func(p *config.ProviderConfig) { p.APIBase += "?x=1#y" },
		"name":                  func(p *config.ProviderConfig) { p.Name = "other" },
		"timeout and busy wait": func(p *config.ProviderConfig) { p.TimeoutMS, p.BusyWaitMS = 5, 6 },
	}
	for name, mutate := range same {
		row := fingerprintRow()
		mutate(&row)
		if got := CoddyUsageFingerprint(row, "coder"); got != base {
			t.Errorf("%s: the fingerprint moved (%q -> %q)", name, base, got)
		}
	}
	// An empty proxy and "inherit" are one route.
	a, b := fingerprintRow(), fingerprintRow()
	a.Proxy, b.Proxy = "", "inherit"
	if CoddyUsageFingerprint(a, "coder") != CoddyUsageFingerprint(b, "coder") {
		t.Error("an empty proxy and inherit differ")
	}

	differ := map[string]func(*config.ProviderConfig){
		"api_base host":     func(p *config.ProviderConfig) { p.APIBase = "https://other.example/swarm/nodes/n1" },
		"api_base path":     func(p *config.ProviderConfig) { p.APIBase = "https://relay.example/swarm/nodes/n2" },
		"api_base scheme":   func(p *config.ProviderConfig) { p.APIBase = "http://relay.example/swarm/nodes/n1" },
		"api_base userinfo": func(p *config.ProviderConfig) { p.APIBase = "https://u:p@relay.example/swarm/nodes/n1" },
		"proxy":             func(p *config.ProviderConfig) { p.Proxy = "http://127.0.0.1:3128" },
		"key":               func(p *config.ProviderConfig) { p.APIKey = "tok2" },
		"no key":            func(p *config.ProviderConfig) { p.APIKey = "" },
		"key command":       func(p *config.ProviderConfig) { p.APIKey = ""; p.APIKeyCommand = "pass show coddy" },
		"type":              func(p *config.ProviderConfig) { p.Type = "openai" },
	}
	seen := map[string]string{base: "base"}
	for name, mutate := range differ {
		row := fingerprintRow()
		mutate(&row)
		got := CoddyUsageFingerprint(row, "coder")
		if got == base {
			t.Errorf("%s: the fingerprint did not move", name)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%s collides with %s", name, prev)
		}
		seen[got] = name
	}
	if CoddyUsageFingerprint(fingerprintRow(), "reviewer") == base {
		t.Error("a renamed alias keeps the fingerprint")
	}
	if CoddyUsageFingerprint(fingerprintRow(), " coder ") != base {
		t.Error("the alias is compared trimmed")
	}
}

func TestCoddyUsageFingerprintReadsTheEnvironmentKeyAndNeverRunsTheCommand(t *testing.T) {
	row := fingerprintRow()
	row.APIKey = ""
	t.Setenv("REMOTE_API_KEY", "")
	noKey := CoddyUsageFingerprint(row, "coder")
	t.Setenv("REMOTE_API_KEY", "from-env")
	withEnv := CoddyUsageFingerprint(row, "coder")
	if withEnv == noKey {
		t.Error("the conventional environment key is part of the credential")
	}
	t.Setenv("REMOTE_API_KEY", "rotated")
	if CoddyUsageFingerprint(row, "coder") == withEnv {
		t.Error("a rotated environment key keeps the fingerprint")
	}
	for _, secret := range []string{"from-env", "rotated"} {
		if strings.Contains(CoddyUsageFingerprint(row, "coder"), secret) {
			t.Error("the fingerprint contains the key")
		}
	}

	// A row with no credential at all is still a subject: an open remote needs none.
	t.Setenv("REMOTE_API_KEY", "")
	if noKey == "" {
		t.Error("a row without a credential has no fingerprint")
	}

	// The credential command stands for its output by its text and never runs.
	marker := filepath.Join(t.TempDir(), "ran")
	row.APIKeyCommand = "touch " + marker
	_ = CoddyUsageFingerprint(row, "coder")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the fingerprint ran api_key_command")
	}
}

// A usage window resets days ahead (a weekly one 3.5 days away is 301800 s): the
// day-long cap of a slept pause must not shorten it.
func TestCoddyUsageKeepsAResetDaysAhead(t *testing.T) {
	for _, in := range []int{301800, 7 * 24 * 3600, 30 * 24 * 3600} {
		if got := boundedSeconds(in); got != in {
			t.Errorf("boundedSeconds(%d) = %d, a reset days ahead was cut", in, got)
		}
	}
	if got := boundedSeconds(1 << 40); got != int(coddyUsageSecondsCap.Seconds()) {
		t.Errorf("a runaway value is not cut to a year: %d", got)
	}
	if got := boundedSeconds(-5); got != 0 {
		t.Errorf("a negative value is kept: %d", got)
	}
}
