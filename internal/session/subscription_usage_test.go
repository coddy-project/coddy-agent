package session

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func subscriptionUsageManager(t *testing.T, status int, body string, enabled bool) (*Manager, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(api.Close)
	home := t.TempDir()
	t.Setenv(llm.EnvCodexBaseURL, api.URL+"/backend-api/codex")
	t.Setenv("CODEX_HOME", t.TempDir())
	auth := config.CodexAuthPath(home, "work")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-subscription-key","account_id":"work-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home}, Providers: []config.ProviderConfig{{Name: "work", Type: "codex", Proxy: "none", UsageLimitsPanel: &enabled}}}
	noAuto := false
	cfg.Rules.AutoDiscover = &noAuto
	m := NewManager(cfg, nil, nil, slog.New(slog.DiscardHandler), home, nil)
	t.Cleanup(func() { m.ShutdownProviderUsage(3 * time.Second) })
	return m, &calls
}

func TestSubscriptionUsageCodexMapping(t *testing.T) {
	window := func(pct, seconds, reset int) string {
		return fmt.Sprintf(`{"used_percent":%d,"limit_window_seconds":%d,"reset_at":2000000000,"reset_after_seconds":%d}`, pct, seconds, reset)
	}
	cases := []struct {
		name, body  string
		wantWindows int
		id, label   string
		percent     float64
		blocked     bool
		retry       int
	}{
		{name: "weekly primary is not a five hour session", body: `{"plan_type":"free","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":` + window(25, 604800, 900) + `}}`, wantWindows: 1, id: "week", label: "week", percent: 25},
		{name: "zero used is still a real daily quota", body: `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":` + window(0, 86400, 900) + `}}`, wantWindows: 1, id: "day", label: "day"},
		{name: "plan without quotas is not unlimited", body: `{"plan_type":"team"}`},
		{name: "account block waits for both exhausted windows", body: `{"plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":` + window(100, 18000, 900) + `,"secondary_window":` + window(100, 604800, 1800) + `}}`, wantWindows: 2, id: "session", label: "5h", percent: 100, blocked: true, retry: 1800},
		{name: "additional quota is not an account block", body: `{"plan_type":"plus","additional_rate_limits":[{"limit_name":"Fast model","metered_feature":"fast","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":` + window(100, 18000, 900) + `}}]}`, wantWindows: 1, label: "Fast model · 5h", percent: 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, calls := subscriptionUsageManager(t, http.StatusOK, tc.body, true)
			u, err := m.ProviderUsage(context.Background(), "work", false)
			if err != nil {
				t.Fatal(err)
			}
			if u.Unsupported || u.Error != "" || u.Provider != "work" || u.ProviderType != "codex" || u.Plan == "" {
				t.Fatalf("usage unavailable: %+v", u)
			}
			if u.Unlimited || u.Rate != nil || u.Wallet != nil || u.Blocked != tc.blocked {
				t.Fatalf("invented or incorrect account state: %+v", u)
			}
			if len(u.Windows) != tc.wantWindows {
				t.Fatalf("windows = %+v", u.Windows)
			}
			if len(u.Windows) > 0 {
				w := u.Windows[0]
				if tc.id != "" && w.ID != tc.id || w.Label != tc.label || w.UsedPercent != tc.percent {
					t.Fatalf("window = %+v", w)
				}
				if w.Used != nil || w.Limit != nil || w.Remaining != nil {
					t.Fatalf("invented counts: %+v", w)
				}
			}
			if u.RetryInSec != tc.retry {
				t.Fatalf("retry = %d, want %d", u.RetryInSec, tc.retry)
			}
			if _, err := m.ProviderUsage(context.Background(), "work", false); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatalf("cache did not coalesce reads: %d", calls.Load())
			}
		})
	}
}

func TestSubscriptionUsageCodexFailuresAndDisabledPanel(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, kind string
	}{
		{"rejected", 401, `credential was rejected`, ProviderUsageErrorUnauthorized},
		{"forbidden is not proof of account exhaustion", 403, `challenge`, ProviderUsageErrorUnavailable},
		{"temporarily unavailable", 503, `down`, ProviderUsageErrorUnavailable},
		{"malformed", 200, `{}`, ProviderUsageErrorInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := subscriptionUsageManager(t, tc.status, tc.body, true)
			u, err := m.ProviderUsage(context.Background(), "work", false)
			if err != nil {
				t.Fatal(err)
			}
			if u.Error != tc.kind || u.Blocked || u.Unlimited || u.Unsupported || len(u.Windows) > 0 {
				t.Fatalf("failure = %+v", u)
			}
		})
	}
	t.Run("disabled panel makes no request", func(t *testing.T) {
		m, calls := subscriptionUsageManager(t, 200, `{"plan_type":"plus"}`, false)
		u, err := m.ProviderUsage(context.Background(), "work", true)
		if err != nil {
			t.Fatal(err)
		}
		if !u.Unsupported || !u.Disabled || calls.Load() != 0 {
			t.Fatalf("disabled usage = %+v, requests %d", u, calls.Load())
		}
	})
}

var _ acp.ProviderUsageUpdate

// --- Devin stand -----------------------------------------------------------

// The fixtures are synthetic protobuf built from the editor 3.10.31
// descriptors (GetUserStatusResponse: user_status = 1, plan_info = 2;
// UserStatus.plan_status = 13; PlanStatus.plan_info = 1; PlanInfo plan_name =
// 2, billing_strategy = 35, hide_daily/weekly_quota = 36/37; PlanStatus
// daily/weekly remaining percent = 14/15, resets = 17/18, acu = 19/20).

type devinPBWriter struct{ buf []byte }

func (w *devinPBWriter) tag(field, wire int) {
	w.buf = binary.AppendUvarint(w.buf, uint64(field<<3|wire))
}

func (w *devinPBWriter) str(field int, s string) {
	w.tag(field, 2)
	w.buf = binary.AppendUvarint(w.buf, uint64(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *devinPBWriter) uint(field int, v uint64) {
	w.tag(field, 0)
	w.buf = binary.AppendUvarint(w.buf, v)
}

func (w *devinPBWriter) boolean(field int, v bool) {
	var n uint64
	if v {
		n = 1
	}
	w.uint(field, n)
}

func (w *devinPBWriter) double(field int, v float64) {
	w.tag(field, 1)
	w.buf = binary.LittleEndian.AppendUint64(w.buf, math.Float64bits(v))
}

func (w *devinPBWriter) msg(field int, body []byte) {
	w.tag(field, 2)
	w.buf = binary.AppendUvarint(w.buf, uint64(len(body)))
	w.buf = append(w.buf, body...)
}

func devinPB(build func(*devinPBWriter)) []byte {
	var w devinPBWriter
	build(&w)
	return w.buf
}

func devinPlanInfo(name string, strategy int) []byte {
	return devinPB(func(w *devinPBWriter) { w.str(2, name); w.uint(35, uint64(strategy)) })
}

func devinStatusResponse(plan, status []byte) []byte {
	return devinPB(func(w *devinPBWriter) {
		if status != nil {
			w.msg(1, devinPB(func(u *devinPBWriter) {
				u.str(3, "account display name, not the plan")
				u.msg(13, status)
			}))
		}
		if plan != nil {
			w.msg(2, plan)
		}
	})
}

func devinUsageManager(t *testing.T, status int, body []byte, enabled bool) (*Manager, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.auth_pb.AuthService/GetUserJwt":
			w.Header().Set("Content-Type", "application/proto")
			_, _ = w.Write(devinPB(func(p *devinPBWriter) { p.str(1, "synthetic-user-jwt") }))
		case "/exa.seat_management_pb.SeatManagementService/GetUserStatus":
			calls.Add(1)
			w.WriteHeader(status)
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	home := t.TempDir()
	t.Setenv(llm.EnvDevinAPIServerURL, api.URL)
	t.Setenv(llm.EnvDevinCLICredentials, filepath.Join(t.TempDir(), "absent.toml"))
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home}, Providers: []config.ProviderConfig{{Name: "work", Type: "devin", APIKey: "fixture-token", Proxy: "none", UsageLimitsPanel: &enabled}}}
	noAuto := false
	cfg.Rules.AutoDiscover = &noAuto
	m := NewManager(cfg, nil, nil, slog.New(slog.DiscardHandler), home, nil)
	t.Cleanup(func() { m.ShutdownProviderUsage(3 * time.Second) })
	return m, &calls
}

func TestSubscriptionUsageDevinMapping(t *testing.T) {
	quotaStatus := devinPB(func(w *devinPBWriter) {
		w.msg(1, devinPB(func(p *devinPBWriter) {
			p.str(2, "Pro")
			p.uint(35, 2) // QUOTA billing
		}))
		w.uint(14, 23) // daily remaining
		w.uint(15, 87) // weekly remaining
		w.uint(17, 2000000000)
		w.uint(18, 2000100000)
	})
	hiddenStatus := devinPB(func(w *devinPBWriter) {
		w.msg(1, devinPB(func(p *devinPBWriter) {
			p.str(2, "Pro")
			p.uint(35, 2)
			p.boolean(36, true) // hide daily
			p.boolean(37, true) // hide weekly
		}))
		w.uint(14, 23)
		w.uint(15, 87)
		w.uint(17, 2000000000)
		w.uint(18, 2000100000)
	})
	acuStatus := devinPB(func(w *devinPBWriter) {
		w.msg(1, devinPlanInfo("ACU plan", 3))
		w.double(19, 4)
		w.double(20, 8)
	})
	creditsStatus := devinPB(func(w *devinPBWriter) {
		w.msg(1, devinPlanInfo("Credits", 1))
		w.uint(14, 23)
		w.uint(15, 87)
		w.uint(17, 2000000000)
		w.uint(18, 2000100000)
	})
	cases := []struct {
		name        string
		body        []byte
		wantWindows int
		id, label   string
		percent     float64
		resets      bool
	}{
		{name: "quota reports used percents and resets", body: devinStatusResponse(nil, quotaStatus), wantWindows: 2, id: "day", label: "day", percent: 77, resets: true},
		{name: "hidden quota flags suppress windows", body: devinStatusResponse(devinPlanInfo("Pro", 2), hiddenStatus), wantWindows: 0},
		{name: "acu is percentage only", body: devinStatusResponse(nil, acuStatus), wantWindows: 1, id: "acu", label: "ACU", percent: 50},
		{name: "credits strategy invents no quota", body: devinStatusResponse(nil, creditsStatus), wantWindows: 0},
		{name: "plan only stays plan only", body: devinStatusResponse(devinPlanInfo("Free", 0), nil), wantWindows: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, calls := devinUsageManager(t, http.StatusOK, tc.body, true)
			u, err := m.ProviderUsage(context.Background(), "work", false)
			if err != nil {
				t.Fatal(err)
			}
			if u.Unsupported || u.Error != "" || u.Provider != "work" || u.ProviderType != "devin" || u.Plan == "" {
				t.Fatalf("usage unavailable: %+v", u)
			}
			if u.Unlimited || u.Rate != nil || u.Wallet != nil || u.Blocked {
				t.Fatalf("invented account state: %+v", u)
			}
			if len(u.Windows) != tc.wantWindows {
				t.Fatalf("windows = %+v", u.Windows)
			}
			if len(u.Windows) > 0 {
				w := u.Windows[0]
				if w.ID != tc.id || w.Label != tc.label || w.UsedPercent != tc.percent {
					t.Fatalf("window = %+v", w)
				}
				if (w.ResetsAt != "") != tc.resets {
					t.Fatalf("resets = %+v", w)
				}
				if w.Used != nil || w.Limit != nil || w.Remaining != nil {
					t.Fatalf("invented counts: %+v", w)
				}
			}
			if calls.Load() == 0 {
				t.Fatal("no upstream read happened")
			}
		})
	}
}

func TestSubscriptionUsageDevinFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
		kind   string
	}{
		{"rejected", 401, []byte(`{"code":"unauthenticated"}`), ProviderUsageErrorUnauthorized},
		{"temporarily unavailable", 503, []byte(`{"code":"unavailable"}`), ProviderUsageErrorUnavailable},
		{"malformed", 200, []byte("definitely not protobuf"), ProviderUsageErrorInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := devinUsageManager(t, tc.status, tc.body, true)
			u, err := m.ProviderUsage(context.Background(), "work", false)
			if err != nil {
				t.Fatal(err)
			}
			if u.Error != tc.kind || u.Blocked || u.Unlimited || u.Unsupported || len(u.Windows) > 0 {
				t.Fatalf("failure = %+v", u)
			}
		})
	}
}
