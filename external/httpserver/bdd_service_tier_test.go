//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

func TestBDDServiceTier(t *testing.T) {
	var st *session.State
	var ts *httptest.Server
	var received *string
	var response []byte
	suite := godog.TestSuite{Name: "service tier", Options: &godog.Options{TestingT: t, Format: "pretty", Paths: []string{"../../features/openai_service_tier.feature"}}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an OpenAI session with the Fast tier$`, func() error {
			mgr, srv, root := testHTTPServerPersist(t)
			cfg := srv.activeCfg()
			cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}}
			cfg.Models = []config.ModelEntry{{Model: "openai/gpt-6-astra"}}
			cfg.Agent.Model = "openai/gpt-6-astra"
			result, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
			if err != nil {
				return err
			}
			st = mgr.SessionByID(result.SessionID)
			tier := "priority"
			if _, err = mgr.ApplySessionSettings(context.Background(), st.ID, session.SettingsChange{ServiceTier: &tier}); err != nil {
				return err
			}
			srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
				received = opts.ServiceTier
				return &responsesStubProvider{reply: &llm.Response{Content: "ok", ServiceTier: "default"}}, nil
			}
			ts = httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)
			return nil
		})
		sc.Step(`^a direct Responses request asks for Ultrafast in that session$`, func() error {
			res := postResponses(t, ts, `{"model":"openai/gpt-6-astra","input":"hi","service_tier":"ultrafast"}`, map[string]string{"X-Coddy-Session-ID": st.ID})
			var err error
			response, err = ioReadAllClose(res.Body)
			if err != nil {
				return err
			}
			if res.StatusCode != 200 {
				return fmt.Errorf("status=%d: %s", res.StatusCode, response)
			}
			return nil
		})
		sc.Step(`^the provider receives "([^"]*)"$`, func(want string) error {
			if received == nil || *received != want {
				return fmt.Errorf("tier=%v, want %q", received, want)
			}
			return nil
		})
		sc.Step(`^the response reports "([^"]*)" while the session keeps "([^"]*)"$`, func(actual, selected string) error {
			if gjson.GetBytes(response, "service_tier").String() != actual || st.GetSelectedServiceTier() != selected {
				return fmt.Errorf("response=%s selection=%q", response, st.GetSelectedServiceTier())
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("service tier scenario failed")
	}
}
