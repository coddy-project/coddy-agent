//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestServiceTierDirectRequests(t *testing.T) {
	for _, withSession := range []bool{false, true} {
		for _, path := range []string{"chat/completions", "responses"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/session=%v", path, stream, withSession), func(t *testing.T) {
					mgr, srv, root := testHTTPServerPersist(t)
					srv.activeCfg().Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}}
					var sid string
					var selected *session.State
					if withSession {
						cfg := srv.activeCfg()
						cfg.Models = append(cfg.Models, config.ModelEntry{Model: "openai/gpt-6-astra"})
						cfg.Agent.Model = "openai/gpt-6-astra"
						created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
						if err != nil {
							t.Fatal(err)
						}
						sid = created.SessionID
						selected = mgr.SessionByID(sid)
						selected.SetSelectedServiceTier("priority")
					}
					calls := 0
					var received *string
					srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
						calls++
						received = opts.ServiceTier
						return &responsesStubProvider{reply: &llm.Response{Content: "ok", ServiceTier: "default"}}, nil
					}
					ts := httptest.NewServer(srv.Handler())
					defer ts.Close()
					for _, rawTier := range []string{`"unknown-tier"`, `""`, `"priority"`, `null`, "", `42`, `true`, `{}`} {
						input := `"input":"hi"`
						if path == "chat/completions" {
							input = `"messages":[{"role":"user","content":"hi"}]`
						}
						body := fmt.Sprintf(`{"model":"openai/gpt-4o",%s,"stream":%v`, input, stream)
						if rawTier != "" {
							body += `,"service_tier":` + rawTier
						}
						body += "}"
						before := calls
						req, err := http.NewRequest("POST", ts.URL+"/v1/"+path, strings.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						if sid != "" {
							req.Header.Set("X-Coddy-Session-ID", sid)
						}
						res, err := http.DefaultClient.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						data, err := ioReadAllClose(res.Body)
						if err != nil {
							t.Fatal(err)
						}
						if selected != nil && selected.GetSelectedServiceTier() != "priority" {
							t.Fatal("direct request changed session preference")
						}
						if rawTier == "42" || rawTier == "true" || rawTier == "{}" {
							if res.StatusCode != 400 || calls != before {
								t.Fatalf("invalid type: %d %s", res.StatusCode, data)
							}
							continue
						}
						if res.StatusCode != 200 || calls != before+1 {
							t.Fatalf("tier %s: %d %s", rawTier, res.StatusCode, data)
						}
						if rawTier == "" || rawTier == "null" {
							if received != nil {
								t.Fatalf("inherited tier %q", *received)
							}
						} else if received == nil || fmt.Sprintf("%q", *received) != rawTier {
							t.Fatalf("lost tier %s: %v", rawTier, received)
						}
						if !strings.Contains(string(data), `"service_tier":"default"`) {
							t.Fatalf("missing actual tier: %s", data)
						}
					}
				})
			}
		}
	}

}

func TestServiceTierProfileTopLevelRefused(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, path := range []string{"chat/completions", "responses"} {
		input := `"input":"hi"`
		if path == "chat/completions" {
			input = `"messages":[{"role":"user","content":"hi"}]`
		}
		res, err := http.Post(ts.URL+"/v1/"+path, "application/json", strings.NewReader(`{"model":"agent",`+input+`,"service_tier":null}`))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := ioReadAllClose(res.Body)
		if res.StatusCode != 400 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, data)
		}
	}
}

func TestServiceTierSessionMetadataAndPatch(t *testing.T) {
	mgr, srv, root := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}}
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "openai/gpt-6-astra"})
	cfg.Agent.Model = "openai/gpt-6-astra"
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, path := range []string{"chat/completions", "responses"} {
		input := `"input":"hi"`
		if path == "chat/completions" {
			input = `"messages":[{"role":"user","content":"hi"}]`
		}
		req, err := http.NewRequest("POST", ts.URL+"/v1/"+path, strings.NewReader(`{"model":"agent",`+input+`,"metadata":{"service_tier":"priority"}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Coddy-Session-ID", created.SessionID)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := ioReadAllClose(res.Body)
		if res.StatusCode != 200 || st.GetSelectedServiceTier() != "priority" {
			t.Fatalf("%s: %d %s", path, res.StatusCode, data)
		}
	}
	for _, tc := range []struct {
		body string
		code int
		tier string
	}{
		{`{"selectedModelId":"","selectedServiceTier":"bad"}`, 400, "priority"},
		{`{"selectedServiceTier":"default"}`, 200, "default"},
		{`{"selectedServiceTier":""}`, 200, ""},
	} {
		req, _ := http.NewRequest("PATCH", ts.URL+"/coddy/sessions/"+created.SessionID, strings.NewReader(tc.body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := ioReadAllClose(res.Body)
		if res.StatusCode != tc.code || st.GetSelectedServiceTier() != tc.tier {
			t.Fatalf("patch %s: %d %s tier=%q", tc.body, res.StatusCode, data, st.GetSelectedServiceTier())
		}
	}
}

func TestServiceTierParallelDirectRequests(t *testing.T) {
	for _, path := range []string{"chat/completions", "responses"} {
		t.Run(path, func(t *testing.T) {
			_, srv, _ := testHTTPServerPersist(t)
			srv.activeCfg().Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}}
			srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
				value := "omitted"
				if opts.ServiceTier != nil {
					value = *opts.ServiceTier
				}
				return &responsesStubProvider{reply: &llm.Response{Content: value}}, nil
			}
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			var wg sync.WaitGroup
			for _, tier := range []string{"priority", "unknown", "omitted"} {
				wg.Go(func() {
					input := `"input":"hi"`
					if path == "chat/completions" {
						input = `"messages":[{"role":"user","content":"hi"}]`
					}
					body := `{"model":"openai/gpt-4o",` + input
					if tier != "omitted" {
						body += `,"service_tier":"` + tier + `"`
					}
					body += "}"
					res, err := http.Post(ts.URL+"/v1/"+path, "application/json", strings.NewReader(body))
					if err != nil {
						t.Error(err)
						return
					}
					data, err := ioReadAllClose(res.Body)
					if err != nil {
						t.Error(err)
						return
					}
					if res.StatusCode != 200 {
						t.Errorf("status=%d %s", res.StatusCode, data)
						return
					}
					field := "choices.0.message.content"
					if path == "responses" {
						field = "output.0.content.0.text"
					}
					if got := gjson.GetBytes(data, field).String(); got != tier {
						t.Errorf("tier=%q got=%q", tier, got)
					}
					if gjson.GetBytes(data, "service_tier").Exists() {
						t.Error("invented actual tier")
					}
				})
			}
			wg.Wait()
		})
	}
}
