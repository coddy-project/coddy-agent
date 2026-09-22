//go:build http

package httpserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const subscriptionUsageToken = "test-subscription-access-token"
const subscriptionDevinToken = "devin-subscription-token"

const subscriptionCodexPayload = `{
  "plan_type":"plus",
  "rate_limit":{"allowed":true,"limit_reached":false,
    "primary_window":{"used_percent":84,"limit_window_seconds":18000,"reset_after_seconds":600,"reset_at":2000000000},
    "secondary_window":{"used_percent":37,"limit_window_seconds":604800,"reset_after_seconds":3600,"reset_at":2000003000}}
}`

// The Devin fixtures are synthetic protobuf built from the editor 3.10.31
// descriptors (GetUserStatusResponse: user_status = 1, plan_info = 2;
// UserStatus.plan_status = 13; PlanStatus.plan_info = 1; PlanInfo plan_name =
// 2, billing_strategy = 35; PlanStatus daily/weekly remaining percent = 14/15,
// resets = 17/18).

type bddPBWriter struct{ buf []byte }

func (w *bddPBWriter) tag(field, wire int) {
	w.buf = binary.AppendUvarint(w.buf, uint64(field<<3|wire))
}

func (w *bddPBWriter) str(field int, s string) {
	w.tag(field, 2)
	w.buf = binary.AppendUvarint(w.buf, uint64(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *bddPBWriter) uint(field int, v uint64) {
	w.tag(field, 0)
	w.buf = binary.AppendUvarint(w.buf, v)
}

func (w *bddPBWriter) msg(field int, body []byte) {
	w.tag(field, 2)
	w.buf = binary.AppendUvarint(w.buf, uint64(len(body)))
	w.buf = append(w.buf, body...)
}

func bddPB(build func(*bddPBWriter)) []byte {
	var w bddPBWriter
	build(&w)
	return w.buf
}

// A QUOTA plan with 23% daily and 87% weekly remaining: 77% / 13% used.
var subscriptionDevinPayload = bddPB(func(w *bddPBWriter) {
	w.msg(1, bddPB(func(u *bddPBWriter) {
		u.str(3, "account display name, not the plan")
		u.msg(13, bddPB(func(s *bddPBWriter) {
			s.msg(1, bddPB(func(p *bddPBWriter) {
				p.str(2, "Pro")
				p.uint(35, 2)
			}))
			s.uint(14, 23)
			s.uint(15, 87)
			s.uint(17, 2000000000)
			s.uint(18, 2000100000)
		}))
	}))
})

// The same account answered without plan_status: what the real server returns
// when the request metadata carries a user JWT (field 21) - plan name only, no
// quota windows.
var subscriptionDevinPlanOnlyPayload = bddPB(func(w *bddPBWriter) {
	w.msg(1, bddPB(func(u *bddPBWriter) {
		u.str(3, "account display name, not the plan")
	}))
	w.msg(2, bddPB(func(p *bddPBWriter) {
		p.str(2, "Pro")
		p.uint(35, 2)
	}))
})

type subscriptionUsageBDD struct {
	t        *testing.T
	home     string
	kind     string
	mgr      *session.Manager
	server   *Server
	api      *httptest.Server
	ts       *httptest.Server
	requests atomic.Int32
	usage    acp.ProviderUsageUpdate
	body     []byte
	updates  chan acp.ProviderUsageUpdate
	secrets  []string
}

func (s *subscriptionUsageBDD) start(kind string) error {
	s.kind, s.home = kind, s.t.TempDir()
	prov := config.ProviderConfig{Name: "subscription", Type: kind, Proxy: "none"}
	switch kind {
	case "codex":
		if err := s.startCodex(); err != nil {
			return err
		}
	case "devin":
		s.startDevin()
		prov.APIKey = subscriptionDevinToken
	default:
		return fmt.Errorf("unsupported fixture %q", kind)
	}
	noAuto := false
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.home, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{prov},
		Models:    []config.ModelEntry{{Model: "subscription/model", MaxTokens: 1000, MaxContextTokens: 100000}},
		Agent:     config.Agent{Model: "subscription/model"},
	}
	cfg.Rules.AutoDiscover = &noAuto
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	log := slog.New(slog.DiscardHandler)
	s.mgr = session.NewManager(cfg, noopSender{}, runner, log, s.home, &session.FileStore{Root: s.t.TempDir()})
	s.server = New(cfg, s.mgr, log, s.home)
	s.ts = httptest.NewServer(s.server.Handler())
	s.updates = make(chan acp.ProviderUsageUpdate, 8)
	s.mgr.AddUsageObserver(func(id string, u acp.ProviderUsageUpdate) {
		if id != "" {
			select {
			case s.updates <- u:
			default:
			}
		}
	})
	return nil
}

// startCodex stands up a Codex backend: GET /wham/usage under a ChatGPT-style
// base, plus a managed auth file for the provider.
func (s *subscriptionUsageBDD) startCodex() error {
	s.t.Setenv("CODEX_HOME", s.t.TempDir())
	s.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/wham/usage" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+subscriptionUsageToken || r.Header.Get("ChatGPT-Account-Id") != "test-subscription-account" {
			http.Error(w, "incorrect credential", http.StatusUnauthorized)
			return
		}
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, subscriptionCodexPayload)
	}))
	s.t.Setenv(llm.EnvCodexBaseURL, s.api.URL+"/backend-api/codex")
	authPath := config.CodexAuthPath(s.home, "subscription")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"`+subscriptionUsageToken+`","account_id":"test-subscription-account"}}`), 0o600); err != nil {
		return err
	}
	s.secrets = []string{subscriptionUsageToken, "test-subscription-account"}
	return nil
}

// startDevin stands up the seat-management API: the user-JWT mint and the
// GetUserStatus RPC the usage read calls.
func (s *subscriptionUsageBDD) startDevin() {
	s.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exa.auth_pb.AuthService/GetUserJwt":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), subscriptionDevinToken) {
				http.Error(w, "incorrect credential", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/proto")
			_, _ = w.Write(bddPB(func(p *bddPBWriter) { p.str(1, "synthetic-user-jwt") }))
		case "/exa.seat_management_pb.SeatManagementService/GetUserStatus":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), subscriptionDevinToken) {
				http.Error(w, "incorrect credential", http.StatusUnauthorized)
				return
			}
			s.requests.Add(1)
			w.Header().Set("Content-Type", "application/proto")
			if strings.Contains(string(raw), "synthetic-user-jwt") {
				// The real server answers a JWT-authenticated read with the
				// plan alone: plan_status (UserStatus field 13, the quota) is
				// omitted whenever metadata field 21 is present.
				_, _ = w.Write(subscriptionDevinPlanOnlyPayload)
				return
			}
			_, _ = w.Write(subscriptionDevinPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	s.t.Setenv(llm.EnvDevinAPIServerURL, s.api.URL)
	s.t.Setenv(llm.EnvDevinCLICredentials, filepath.Join(s.t.TempDir(), "absent.toml"))
	s.secrets = []string{subscriptionDevinToken, "synthetic-user-jwt"}
}

func (s *subscriptionUsageBDD) close() {
	if s.ts != nil {
		s.ts.Close()
	}
	if s.server != nil {
		s.server.Drain()
	}
	if s.mgr != nil {
		s.mgr.ShutdownProviderUsage(3 * time.Second)
	}
	if s.api != nil {
		s.api.Close()
	}
}

func (s *subscriptionUsageBDD) readUsage() error {
	res, err := http.Get(s.ts.URL + "/coddy/providers/subscription/usage")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.body, err = io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var out struct {
		OK    bool                    `json:"ok"`
		Usage acp.ProviderUsageUpdate `json:"usage"`
	}
	if err := json.Unmarshal(s.body, &out); err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || !out.OK {
		return fmt.Errorf("subscription usage not available: HTTP %d %s", res.StatusCode, s.body)
	}
	s.usage = out.Usage
	return nil
}

func (s *subscriptionUsageBDD) plan(plan string) error {
	if s.usage.Plan != plan || s.usage.Provider != "subscription" || s.usage.ProviderType != s.kind {
		return fmt.Errorf("unexpected plan/provider: %+v", s.usage)
	}
	return nil
}

func (s *subscriptionUsageBDD) window(id, label string, pct int) error {
	for _, w := range s.usage.Windows {
		if w.ID == id {
			if w.Label != label || w.UsedPercent != float64(pct) {
				return fmt.Errorf("unexpected %s window: %+v", id, w)
			}
			return nil
		}
	}
	return fmt.Errorf("missing %s window: %+v", id, s.usage.Windows)
}

func (s *subscriptionUsageBDD) resetsWithoutCounts() error {
	if s.usage.Unlimited || s.usage.Wallet != nil || s.usage.Rate != nil {
		return fmt.Errorf("invented account limits: %+v", s.usage)
	}
	if len(s.usage.Windows) == 0 {
		return fmt.Errorf("no quota windows")
	}
	for _, w := range s.usage.Windows {
		if _, err := time.Parse(time.RFC3339, w.ResetsAt); err != nil {
			return fmt.Errorf("invalid reset for %s: %w", w.ID, err)
		}
		if w.ResetInSec <= 0 || w.Used != nil || w.Limit != nil || w.Remaining != nil {
			return fmt.Errorf("invented counts or missing reset: %+v", w)
		}
	}
	return nil
}

func (s *subscriptionUsageBDD) credentials() error {
	if s.requests.Load() == 0 {
		return fmt.Errorf("provider was never read with the expected credential")
	}
	for _, secret := range s.secrets {
		if strings.Contains(string(s.body), secret) {
			return fmt.Errorf("credential leaked in usage response")
		}
	}
	return nil
}

func (s *subscriptionUsageBDD) turn() error {
	created, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.home})
	if err != nil {
		return err
	}
	_, err = s.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "read subscription usage after this turn"}}}, noopSender{}, nil)
	return err
}

func (s *subscriptionUsageBDD) updatedPlan(plan string) error {
	select {
	case u := <-s.updates:
		s.usage = u
		return s.plan(plan)
	case <-time.After(3 * time.Second):
		return fmt.Errorf("turn did not publish subscription usage")
	}
}

func TestSubscriptionUsageBDD(t *testing.T) {
	var s *subscriptionUsageBDD
	suite := godog.TestSuite{
		Name: "subscription_usage",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s = &subscriptionUsageBDD{t: t}
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a subscription usage server for "([^"]+)"$`, func(kind string) error { return s.start(kind) })
			sc.Step(`^I read the subscription usage over REST$`, func() error { return s.readUsage() })
			sc.Step(`^the subscription usage names the plan "([^"]+)"$`, func(plan string) error { return s.plan(plan) })
			sc.Step(`^the subscription has a "([^"]+)" window labelled "([^"]+)" at (\d+) percent$`, func(id, label string, pct int) error { return s.window(id, label, pct) })
			sc.Step(`^the subscription windows carry reset times without invented request counts$`, func() error { return s.resetsWithoutCounts() })
			sc.Step(`^subscription credentials were used upstream but never returned$`, func() error { return s.credentials() })
			sc.Step(`^a subscription prompt turn finishes$`, func() error { return s.turn() })
			sc.Step(`^the subscription usage update names the plan "([^"]+)"$`, func(plan string) error { return s.updatedPlan(plan) })
		},
		Options: &godog.Options{Format: "progress", Paths: []string{"../../features/subscription_usage.feature"}, TestingT: t, Strict: true},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("subscription usage BDD status %d", status)
	}
}
