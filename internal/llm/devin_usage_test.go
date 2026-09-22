package llm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const devinUsageTestPath = "/exa.seat_management_pb.SeatManagementService/GetUserStatus"

// Synthetic fixtures use descriptors extracted from the official Devin editor
// 3.10.31 (underlying 1.126.0, 2026-09-16), not a live account response.
// Archive: https://windsurf-stable.codeiumdata.com/linux-x64/stable/b98cc43128712ba73c60cca73876f58a710aaa27/Devin-linux-x64-3.10.31.tar.gz
// Archive SHA256: b3ddf1098c11255354d60a0778d7033989eaed85a37bbd55df90167f42eb5346
// extension.js SHA256: 87f4a2024627be722a36cf609e4ee02df96d4f6cecbd753b63da72c6d3ce2201
// Search typeName exa.codeium_common_pb.{PlanInfo,PlanStatus,UserStatus}
// and exa.seat_management_pb.GetUserStatusResponse to reproduce the tags.
func devinUsagePB(build func(*pbWriter)) []byte {
	var w pbWriter
	build(&w)
	return w.buf
}

func devinUsagePlan(name string, strategy int) []byte {
	return devinUsagePB(func(w *pbWriter) { w.str(2, name); w.uint(35, uint64(strategy)) })
}

func devinUsageResponse(plan, status []byte) []byte {
	return devinUsagePB(func(w *pbWriter) {
		if status != nil {
			w.msg(1, devinUsagePB(func(u *pbWriter) {
				u.str(3, "Account display name is NOT a plan")
				u.msg(13, status)
			}))
		}
		if plan != nil {
			w.msg(2, plan)
		}
	})
}

func devinUsageOptionalDouble(w *pbWriter, field int, v float64) {
	w.tag(field, pbFixed64)
	w.buf = binary.LittleEndian.AppendUint64(w.buf, math.Float64bits(v))
}

func devinUsageServer(t *testing.T, usage http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var mints atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.auth_pb.AuthService/GetUserJwt":
			mints.Add(1)
			w.Header().Set("Content-Type", "application/proto")
			_, _ = w.Write(devinUsagePB(func(p *pbWriter) { p.str(1, "synthetic-user-jwt") }))
		case devinUsageTestPath:
			usage(w, r)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	devinTestEnv(t, ts.URL)
	t.Setenv("USAGE_TEST_API_KEY", "")
	return ts, &mints
}

func devinUsageTestProvider() config.ProviderConfig {
	return config.ProviderConfig{Name: "usage-test", Type: "devin", APIKey: "fixture-token", Proxy: "none"}
}

func TestDevinUsageVerifiedWire(t *testing.T) {
	quota := devinUsagePB(func(w *pbWriter) {
		w.msg(1, devinUsagePB(func(p *pbWriter) {
			p.str(2, "Pro")
			p.uint(35, 2)
			p.boolean(36, true)
			p.boolean(37, true)
		}))
		w.uint(14, 23)
		w.uint(15, 87)
		w.uint(17, 2000000000)
		w.uint(18, 2000100000)
		w.uint(16, 5000000) // Money is not a request count.
		w.uint(8, 999)
		w.uint(9, 999) // Old credits are not quota usage.
	})
	acu := devinUsagePB(func(w *pbWriter) {
		w.msg(1, devinUsagePlan("ACU plan", 3))
		devinUsageOptionalDouble(w, 19, 2.5)
		devinUsageOptionalDouble(w, 20, 0)
	})
	for _, tc := range []struct {
		name string
		raw  []byte
		want DevinUsage
	}{
		{"remaining not used and hidden flags", devinUsageResponse(devinUsagePlan("Fallback", 1), quota), DevinUsage{PlanName: "Pro", BillingStrategy: 2, HasPlanStatus: true, HideDailyQuota: true, HideWeeklyQuota: true, DailyRemainingPercent: 23, WeeklyRemainingPercent: 87, DailyResetAt: 2000000000, WeeklyResetAt: 2000100000}},
		{"omitted scalar percent means zero", devinUsageResponse(nil, devinUsagePB(func(w *pbWriter) { w.msg(1, devinUsagePlan("Pro", 2)); w.uint(17, 2000000000) })), DevinUsage{PlanName: "Pro", BillingStrategy: 2, HasPlanStatus: true, DailyResetAt: 2000000000}},
		{"ACU optional zero preserved", devinUsageResponse(nil, acu), DevinUsage{PlanName: "ACU plan", BillingStrategy: 3, HasPlanStatus: true, ACUConsumed: usageFloat(2.5), ACULimit: usageFloat(0)}},
		{"ACU absent is nil", devinUsageResponse(nil, devinUsagePB(func(w *pbWriter) { w.msg(1, devinUsagePlan("ACU", 3)) })), DevinUsage{PlanName: "ACU", BillingStrategy: 3, HasPlanStatus: true}},
		{"plan only", devinUsageResponse(devinUsagePlan("Free", 0), nil), DevinUsage{PlanName: "Free"}},
		{"fallback plan with empty status", devinUsageResponse(devinUsagePlan("Pro", 2), []byte{}), DevinUsage{PlanName: "Pro", BillingStrategy: 2, HasPlanStatus: true}},
		{"unknown billing stays plan only", devinUsageResponse(devinUsagePlan("Future", 77), nil), DevinUsage{PlanName: "Future", BillingStrategy: 77}},
		{"credits have no invented monthly reset", devinUsageResponse(nil, devinUsagePB(func(w *pbWriter) {
			w.msg(1, devinUsagePlan("Credits", 1))
			w.uint(8, 100)
			w.uint(5, 30)
			w.msg(3, devinUsagePB(func(p *pbWriter) { p.uint(1, 2000000000) }))
		})), DevinUsage{PlanName: "Credits", BillingStrategy: 1, HasPlanStatus: true}},
		{"old UserStatus field12 ignored", devinUsagePB(func(w *pbWriter) {
			w.msg(1, devinUsagePB(func(u *pbWriter) { u.msg(12, devinUsagePlan("Wrong", 2)) }))
			w.msg(2, devinUsagePlan("Correct", 1))
		}), DevinUsage{PlanName: "Correct", BillingStrategy: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devinUsageServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tc.raw) })
			got, err := DevinUsageForProvider(context.Background(), devinUsageTestProvider(), "")
			if err != nil || !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func usageFloat(v float64) *float64 { return &v }

// The seat-management server omits plan_status (UserStatus field 13) whenever
// the request metadata carries field 21 (user_jwt): a JWT-authenticated call
// takes a code path that attaches no quota, which is what the UI showed as
// "quota data unavailable". The api key in field 3 alone authenticates this
// RPC, so the usage path must neither mint a JWT nor send one.
func TestDevinUsageRequestOmitsUserJWT(t *testing.T) {
	_, mints := devinUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		f, err := pbFields(raw)
		if err != nil || len(f) != 1 || f[0].field != 1 {
			t.Errorf("metadata envelope: %v %v", f, err)
			return
		}
		fields, err := pbFields(f[0].raw)
		if err != nil {
			t.Error(err)
			return
		}
		for _, field := range fields {
			if field.field == 21 {
				t.Errorf("metadata carries user_jwt; the server suppresses plan_status for it")
			}
		}
		_, _ = w.Write(devinUsageResponse(devinUsagePlan("Pro", 2), devinUsagePB(func(w *pbWriter) {
			w.uint(14, 34)
			w.uint(15, 16)
			w.uint(17, 2000000000)
			w.uint(18, 2000100000)
		})))
	})
	got, err := DevinUsageForProvider(context.Background(), devinUsageTestProvider(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasPlanStatus || got.DailyRemainingPercent != 34 || got.WeeklyRemainingPercent != 16 {
		t.Fatalf("quota %+v", got)
	}
	if mints.Load() != 0 {
		t.Fatalf("usage path minted %d JWTs", mints.Load())
	}
}

func TestDevinUsageCredentialsAndJWT(t *testing.T) {
	for _, source := range []string{"literal", "command", "env", "managed", "cli"} {
		t.Run(source, func(t *testing.T) {
			var usageCalls atomic.Int32
			ts, mints := devinUsageServer(t, func(w http.ResponseWriter, r *http.Request) {
				usageCalls.Add(1)
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/proto" || r.Header.Get("Connect-Protocol-Version") != "1" {
					t.Errorf("request %s %v", r.Method, r.Header)
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				f, err := pbFields(raw)
				if err != nil || len(f) != 1 || f[0].field != 1 {
					t.Errorf("metadata envelope: %v %v", f, err)
					return
				}
				fields, err := pbFields(f[0].raw)
				if err != nil {
					t.Error(err)
					return
				}
				values := map[int]string{}
				for _, f := range fields {
					values[f.field] = f.text()
				}
				if values[3] != normalizeDevinToken(source) || values[2] != devinClientVersion || values[10] == "" || values[25] == "" {
					t.Errorf("metadata = %v", values)
				}
				if _, sent := values[21]; sent {
					t.Errorf("metadata carries user_jwt: %v", values)
				}
				_, _ = w.Write(devinUsageResponse(devinUsagePlan("Pro", 2), nil))
			})
			dir := t.TempDir()
			auth := filepath.Join(dir, "auth.json")
			cli := filepath.Join(dir, "credentials.toml")
			if err := saveDevinAuth(auth, devinAuthFile{SessionToken: "managed", APIServerURL: ts.URL}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cli, []byte(fmt.Sprintf("windsurf_api_key = %q\napi_server_url = %q\n", "cli", ts.URL)), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvDevinCLICredentials, cli)
			p := devinUsageTestProvider()
			p.APIKey = ""
			p.APIBase = "http://api-base-must-not-be-used.invalid"
			switch source {
			case "literal":
				p.APIKey = "literal"
				p.APIKeyCommand = "echo ignored"
				t.Setenv("USAGE_TEST_API_KEY", "ignored")
			case "command":
				p.APIKeyCommand = "echo command"
				t.Setenv("USAGE_TEST_API_KEY", "ignored")
			case "env":
				t.Setenv("USAGE_TEST_API_KEY", "env")
			case "cli":
				if err := RemoveDevinAuth(auth); err != nil {
					t.Fatal(err)
				}
			}
			if source == "managed" || source == "cli" {
				t.Setenv(EnvDevinAPIServerURL, "")
			}
			cred, err := resolveDevinCredential(p.EffectiveAPIKey(), auth)
			if err != nil {
				t.Fatal(err)
			}
			hc, err := HTTPClientForProviderProxy("none")
			if err != nil {
				t.Fatal(err)
			}
			// The chat path mints through this helper; the usage path must
			// neither call it nor send the token (field 21 suppresses
			// plan_status), so mints stays at this one explicit call.
			if _, err := devinJWTFor(context.Background(), hc, cred); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := DevinUsageForProvider(context.Background(), p, auth); err != nil {
					t.Fatal(err)
				}
			}
			if mints.Load() != 1 || usageCalls.Load() != 2 {
				t.Fatalf("mints %d calls %d", mints.Load(), usageCalls.Load())
			}
		})
	}
}

func TestDevinUsageProxy(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Host != "synthetic-api.invalid" {
			t.Errorf("proxy host %s", r.URL.Host)
		}
		if strings.HasSuffix(r.URL.Path, "GetUserJwt") {
			_, _ = w.Write(devinUsagePB(func(p *pbWriter) { p.str(1, "jwt") }))
			return
		}
		if r.URL.Path != devinUsageTestPath {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write(devinUsageResponse(devinUsagePlan("Pro", 2), nil))
	}))
	defer proxy.Close()
	devinTestEnv(t, "http://synthetic-api.invalid")
	p := devinUsageTestProvider()
	p.Proxy = proxy.URL
	if _, err := DevinUsageForProvider(context.Background(), p, ""); err != nil {
		t.Fatal(err)
	}
	// One request through the proxy: the usage call alone (no JWT mint).
	if requests.Load() != 1 {
		t.Fatalf("proxy requests %d", requests.Load())
	}
}

func TestDevinUsageInvalidWire(t *testing.T) {
	plan := devinUsagePlan("Pro", 2)
	status := func(build func(*pbWriter)) []byte { return devinUsageResponse(plan, devinUsagePB(build)) }
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil}, {"truncated", []byte{0x0a, 0x09, 0x01}},
		{"account name is not plan", devinUsagePB(func(w *pbWriter) { w.msg(1, devinUsagePB(func(u *pbWriter) { u.str(3, "Account") })) })},
		{"empty plan", devinUsageResponse([]byte{}, nil)},
		{"zero field tag", append(devinUsageResponse(plan, nil), 0, 0)},
		{"message wire type", devinUsagePB(func(w *pbWriter) { w.uint(1, 1); w.msg(2, plan) })},
		{"plan wrong wire", devinUsageResponse(devinUsagePB(func(w *pbWriter) { w.uint(2, 7) }), nil)},
		{"percent wrong wire", status(func(w *pbWriter) { w.double(14, 2) })},
		{"percent above100", status(func(w *pbWriter) { w.uint(14, 101) })},
		{"negative percent", status(func(w *pbWriter) { w.uint(15, ^uint64(0)) })},
		{"overflow percent", status(func(w *pbWriter) { w.uint(14, 1<<32) })},
		{"negative reset", status(func(w *pbWriter) { w.uint(17, ^uint64(0)) })},
		{"NaN ACU", status(func(w *pbWriter) { devinUsageOptionalDouble(w, 19, math.NaN()) })},
		{"infinite limit", status(func(w *pbWriter) { devinUsageOptionalDouble(w, 20, math.Inf(1)) })},
		{"negative ACU", status(func(w *pbWriter) { devinUsageOptionalDouble(w, 19, -1) })},
		{"ACU wrong wire", status(func(w *pbWriter) { w.uint(19, 2) })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			devinUsageServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tc.raw) })
			got, err := DevinUsageForProvider(context.Background(), devinUsageTestProvider(), "")
			if got != nil {
				t.Fatalf("unexpected usage %+v", got)
			}
			_ = devinUsageAssertError(t, err, ProviderUsageInvalid, 0)
		})
	}
}

func devinUsageAssertError(t *testing.T, err error, kind string, status int) *ProviderUsageError {
	t.Helper()
	var e *ProviderUsageError
	if !errors.As(err, &e) || e.Kind != kind || (status != 0 && e.Status != status) {
		t.Fatalf("error = %v; want %s status %d", err, kind, status)
	}
	for _, secret := range []string{"LEAK", "fixture-token", "synthetic-user-jwt", "127.0.0.1", "http://"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe error %v", err)
		}
	}
	return e
}

func TestDevinUsageHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, kind string
		retry      bool
	}{
		{"unauthorized", 401, `{"code":"unauthenticated","message":"LEAK"}`, ProviderUsageUnauthorized, false},
		{"forbidden", 403, `{"message":"LEAK"}`, ProviderUsageUnavailable, false},
		{"version gate", 400, `{"code":"failed_precondition","message":"LEAK"}`, ProviderUsageUnavailable, false},
		{"temporary", 503, `{"message":"LEAK"}`, ProviderUsageUnavailable, true},
		{"rate limited", 429, `{"message":"LEAK"}`, ProviderUsageUnavailable, true},
		{"RPC unauthorized", 200, `{"code":"unauthenticated","message":"LEAK"}`, ProviderUsageUnauthorized, false},
		{"oversized", 200, strings.Repeat("x", (256<<10)+1), ProviderUsageInvalid, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retry {
					w.Header().Set("Retry-After", "17")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer ts.Close()
			devinTestEnv(t, ts.URL)
			_, err := DevinUsageForProvider(context.Background(), devinUsageTestProvider(), "")
			status := tc.status
			if status == 200 {
				status = 0
			}
			e := devinUsageAssertError(t, err, tc.kind, status)
			if tc.retry && e.RetryAfter != 17*time.Second {
				t.Fatalf("retry %v", e.RetryAfter)
			}
		})
	}
}

func TestDevinUsageRedirectAndCancellation(t *testing.T) {
	var leaked atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer sink.Close()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer ts.Close()
	devinTestEnv(t, ts.URL)
	_, err := DevinUsageForProvider(context.Background(), devinUsageTestProvider(), "")
	_ = devinUsageAssertError(t, err, ProviderUsageUnavailable, 307)
	if leaked.Load() != 0 {
		t.Fatal("redirect forwarded credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = DevinUsageForProvider(ctx, devinUsageTestProvider(), "")
	_ = devinUsageAssertError(t, err, ProviderUsageUnavailable, 0)
}

func TestDevinUsageMissingCredentialAndCommandFailure(t *testing.T) {
	devinTestEnv(t, "http://unused.invalid")
	t.Setenv("USAGE_TEST_API_KEY", "")
	p := devinUsageTestProvider()
	p.APIKey = ""
	_, err := DevinUsageForProvider(context.Background(), p, "")
	_ = devinUsageAssertError(t, err, ProviderUsageUnauthorized, 0)
	p.APIKeyCommand = "exit 1"
	_, err = DevinUsageForProvider(context.Background(), p, "")
	_ = devinUsageAssertError(t, err, ProviderUsageUnavailable, 0)
}

func TestDevinUsageFingerprint(t *testing.T) {
	devinTestEnv(t, "http://endpoint.invalid")
	t.Setenv("USAGE_TEST_API_KEY", "")
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.json")
	cli := filepath.Join(dir, "credentials.toml")
	t.Setenv(EnvDevinCLICredentials, cli)
	p := devinUsageTestProvider()
	p.APIKey = ""
	if got := DevinUsageFingerprint(p, auth); got != "" {
		t.Fatalf("empty source %q", got)
	}
	writeCLI := func(token string) {
		t.Helper()
		if err := os.WriteFile(cli, []byte(fmt.Sprintf("windsurf_api_key = %q\n", token)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeCLI("first")
	first := DevinUsageFingerprint(p, auth)
	writeCLI("second")
	second := DevinUsageFingerprint(p, auth)
	if first == "" || first == second {
		t.Fatal("external CLI account rotation did not invalidate")
	}
	if err := saveDevinAuth(auth, devinAuthFile{SessionToken: "managed"}); err != nil {
		t.Fatal(err)
	}
	managed := DevinUsageFingerprint(p, auth)
	writeCLI("irrelevant")
	if got := DevinUsageFingerprint(p, auth); got != managed {
		t.Fatal("shadowed CLI affected fingerprint")
	}
	t.Setenv("USAGE_TEST_API_KEY", "env")
	env := DevinUsageFingerprint(p, auth)
	if env == managed {
		t.Fatal("env did not take precedence")
	}
	marker := filepath.Join(dir, "must-not-exist")
	p.APIKeyCommand = fmt.Sprintf("echo LEAK > %q", marker)
	command := DevinUsageFingerprint(p, auth)
	if command == env {
		t.Fatal("command source not fingerprinted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("fingerprint executed credential command")
	}
	p.APIKey = "literal"
	literal := DevinUsageFingerprint(p, auth)
	p.APIKeyCommand = "echo changed"
	t.Setenv("USAGE_TEST_API_KEY", "changed")
	if got := DevinUsageFingerprint(p, auth); got != literal {
		t.Fatal("shadowed command/env affected literal fingerprint")
	}
	p.APIBase = "http://ignored.invalid"
	if got := DevinUsageFingerprint(p, auth); got != literal {
		t.Fatal("ignored APIBase affected fingerprint")
	}
	p.Proxy = "http://proxy.invalid"
	if got := DevinUsageFingerprint(p, auth); got == literal {
		t.Fatal("proxy rotation not fingerprinted")
	}
	p.Proxy = "none"
	t.Setenv(EnvDevinAPIServerURL, "http://other.invalid")
	if got := DevinUsageFingerprint(p, auth); got == literal {
		t.Fatal("endpoint rotation not fingerprinted")
	}
	for _, v := range []string{first, second, managed, env, command, literal} {
		if len(v) < 16 || strings.Contains(v, "literal") || strings.Contains(v, "LEAK") {
			t.Fatalf("unsafe fingerprint %q", v)
		}
	}
}
