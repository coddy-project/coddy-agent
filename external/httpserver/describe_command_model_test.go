//go:build http

package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestDescribePreservesSmallerConfiguredTokenLimit(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "openai/small", MaxTokens: 32})
	var got int
	srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
		if opts.MaxTokens != nil {
			got = *opts.MaxTokens
		}
		return fakeProvider{reply: "Repair naming"}, nil
	}
	ts := newDescribeTestServer(t, srv)
	resp, err := ts.Client().Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"repair naming","model":"openai/small"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	if got != 32 {
		t.Fatalf("max tokens = %d; want 32", got)
	}
}

func TestDescribeRequestedACPUsesFirstNonACPBeforeSessionExists(t *testing.T) {
	for _, body := range []string{
		`{"text":"review the branch","model":"agent/default"}`,
		`{"text":"/model --once agent/default review the branch","model":"openai/other"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, srv, _ := testHTTPServerPersist(t)
			cfg := srv.activeCfg()
			cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: "agent"}})
			cfg.Models = append(cfg.Models, config.ModelEntry{Model: "openai/other"}, config.ModelEntry{Model: "agent/default"})
			cfg.Agent.Model = "openai/other"
			want := cfg.FirstNonACPModelID()
			if want == "" || want == cfg.Agent.Model {
				t.Fatalf("invalid fixture: first non-ACP %q, default %q", want, cfg.Agent.Model)
			}
			var got string
			srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
				got = model
				return fakeProvider{reply: "Review the branch"}, nil
			}
			srv.providerFactory = func(*config.Config) (llm.Provider, error) {
				got = cfg.Agent.Model
				return fakeProvider{reply: "Review the branch"}, nil
			}
			ts := newDescribeTestServer(t, srv)
			resp, err := ts.Client().Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("close response: %v", err)
				}
			}()
			if resp.StatusCode != http.StatusOK || got != want {
				t.Fatalf("status = %d, model = %q; want 200 and %q", resp.StatusCode, got, want)
			}
		})
	}
}

func TestDescribeUsesTurnModelCommandBeforeSessionExists(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "openai/selected"}, config.ModelEntry{Model: "openai/once"})
	var got string
	srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		got = model
		return fakeProvider{reply: "Repair naming\ntags: sessions"}, nil
	}
	ts := newDescribeTestServer(t, srv)
	resp, err := ts.Client().Post(ts.URL+"/coddy/describe", "application/json", strings.NewReader(`{"text":"/model --once openai/once repair naming","model":"openai/selected"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK || got != "openai/once" {
		t.Fatalf("status = %d, model = %q; want 200 and openai/once", resp.StatusCode, got)
	}
}
