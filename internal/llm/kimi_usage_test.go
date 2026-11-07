package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

func TestKimiUsageWindows(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Accept") != "application/json" {
			t.Error("missing headers")
		}
		if r.UserAgent() != "coddy/"+version.Get() || r.Header.Get("X-Msh-Platform") != "kimi_code_cli" || r.Header.Get("X-Msh-Version") != version.Get() || !kimiDeviceIDPattern.MatchString(r.Header.Get("X-Msh-Device-Id")) {
			t.Errorf("missing or invalid identity headers: %v", r.Header)
		}
		if _, err := fmt.Fprint(w, `{"usages":{"limit_5h":{"used_ratio":0},"limit_7d":{"used_ratio":"0.25","reset_time":"2026-09-17T00:00:00Z"},"limit_month_total":{"used_ratio":0.4},"limit_month_code":{"used_ratio":"0.75"}},"boosterWallet":{"balance":{"type":"BOOSTER","amount":"1"}}}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	u, err := fetchKimiUsage(context.Background(), "secret", s.URL, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		w     *KimiUsageWindow
		ratio float64
	}{{u.Limit5H, 0}, {u.Limit7D, .25}, {u.MonthTotal, .4}, {u.MonthCode, .75}} {
		if tc.w == nil || tc.w.UsedRatio != tc.ratio {
			t.Fatalf("window: %+v", tc.w)
		}
	}
	if u.Limit5H.ResetAt != nil || u.Limit7D.ResetAt == nil {
		t.Fatal("reset presence lost")
	}
}

func TestKimiUsageMalformed(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `{}`, `{"usages":null}`, `{"usages":[]} `, `{"usages":{}} trailing`} {
		t.Run(payload, func(t *testing.T) {
			if _, err := decodeKimiUsage([]byte(payload)); err == nil {
				t.Fatal("accepted invalid payload")
			}
		})
	}
	u, err := decodeKimiUsage([]byte(`{"usages":{"limit_5h":{"used_ratio":""},"limit_7d":{"used_ratio":"NaN"},"limit_month_total":{"used_ratio":-1},"limit_month_code":{"used_ratio":"0.5","reset_time":5}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if u.Limit5H != nil || u.Limit7D != nil || u.MonthTotal != nil || u.MonthCode == nil || u.MonthCode.ResetAt != nil {
		t.Fatalf("unexpected windows: %+v", u)
	}
}

func TestKimiUsageHTTPFailures(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret", http.StatusUnauthorized) }))
	defer s.Close()
	_, err := fetchKimiUsage(context.Background(), "secret", s.URL, s.Client())
	var ue *ProviderUsageError
	if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnauthorized || ue.Status != http.StatusUnauthorized || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error %v", err)
	}
	if _, err = fetchKimiUsage(context.Background(), "secret", s.URL, nil); err == nil {
		t.Fatal("nil client accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchKimiUsage(ctx, "secret", s.URL, s.Client())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestKimiUsageRegionAndProxy(t *testing.T) {
	for region, want := range map[string]string{"": "https://api.kimi.ai/coding/v1/usages", "global": "https://api.kimi.ai/coding/v1/usages", "china": "https://api.kimi.com/coding/v1/usages"} {
		got, err := kimiUsageEndpoint(region)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", region, got, err)
		}
	}
	if _, err := kimiUsageEndpoint("other"); err == nil {
		t.Fatal("invalid region accepted")
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "api.kimi.ai:443" {
			t.Errorf("proxy got %s %s", r.Method, r.Host)
		}
		http.Error(w, "refused", http.StatusForbidden)
	}))
	defer proxy.Close()
	_, err := FetchKimiUsage(context.Background(), "global", "secret", proxy.URL)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("proxy error: %v", err)
	}
}

func TestKimiUsageRedirectRefused(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	_, err := fetchKimiUsage(context.Background(), "secret", s.URL, s.Client())
	if err == nil || reached {
		t.Fatal("redirect followed")
	}
}
