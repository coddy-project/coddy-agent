//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func onboardingStatusFor(t *testing.T, yml string, setup ...func(home string)) onboardingStatusDTO {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: home, CWD: home, ConfigPath: cfgPath})
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range setup {
		fn(home)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/coddy/onboarding/status")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body onboardingStatusDTO
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestOnboardingStatusFirstRun(t *testing.T) {
	body := onboardingStatusFor(t, `providers: []
models: []
`, func(home string) {
		if err := os.Remove(filepath.Join(home, "config.yaml")); err != nil {
			t.Fatal(err)
		}
	})
	if !body.FirstRun || body.HasConfig || body.HasProviders || body.HasModels || body.HasAgentModel || body.HasAgentCredentials {
		t.Fatalf("unexpected first-run status: %+v", body)
	}
}

func TestOnboardingStatusOnlyAgentProviderGatesPicker(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("NEURALDEEP_API_KEY", "")
	body := onboardingStatusFor(t, `
providers:
  - name: neuraldeep
    type: neuraldeep
    api_key: "nd-test"
  - name: openai
    type: openai
models:
  - model: "neuraldeep/qwen3.8-27b"
agent:
  model: "neuraldeep/qwen3.8-27b"
`)
	if !body.HasAgentCredentials {
		t.Fatalf("agent credentials are ready: %+v", body)
	}
	if len(body.MissingAPIKeys) != 1 || body.MissingAPIKeys[0] != "openai" {
		t.Fatalf("missing_api_keys = %v, want [openai]", body.MissingAPIKeys)
	}

	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	body = onboardingStatusFor(t, `
providers:
  - name: neuraldeep
    type: neuraldeep
    api_key: "nd-test"
  - name: openai
    type: openai
models:
  - model: "neuraldeep/qwen3.8-27b"
agent:
  model: "neuraldeep/qwen3.8-27b"
`)
	if len(body.MissingAPIKeys) != 0 {
		t.Fatalf("environment credential was not counted: %v", body.MissingAPIKeys)
	}
}

func TestOnboardingStatusRejectsCredentiallessAgentProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	body := onboardingStatusFor(t, `
providers:
  - name: openai
    type: openai
models:
  - model: "openai/gpt-5.6-terra"
agent:
  model: "openai/gpt-5.6-terra"
`)
	if body.HasAgentCredentials || !body.HasAgentModel {
		t.Fatalf("unexpected status: %+v", body)
	}
}

func TestOnboardingStatusDoesNotRunCredentialCommand(t *testing.T) {
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "command-ran"))
	body := onboardingStatusFor(t, `
providers:
  - name: openai
    type: openai
    api_key_command: "touch `+marker+`"
models:
  - model: "openai/gpt-5.6-terra"
agent:
  model: "openai/gpt-5.6-terra"
`)
	if !body.HasAgentCredentials {
		t.Fatalf("api_key_command counts without running: %+v", body)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("status ran the credential command: %v", err)
	}
}

func TestOnboardingStatusCountsOpenCodeAndDevinLogins(t *testing.T) {
	for _, providerType := range []string{"opencode", "devin"} {
		t.Run(providerType, func(t *testing.T) {
			body := onboardingStatusFor(t, `
providers:
  - name: `+providerType+`
    type: `+providerType+`
models:
  - model: "`+providerType+`/model"
agent:
  model: "`+providerType+`/model"
`, func(home string) {
				var path, credential string
				if providerType == "opencode" {
					path = config.OpenCodeAuthPath(home, providerType)
					credential = `{"api_key":"oc_test_12345678901234567890"}`
				} else {
					path = config.DevinAuthPath(home, providerType)
					credential = `{"session_token":"devin-session-token$test","obtained_at":"2026-01-01T00:00:00Z"}`
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(credential), 0o600); err != nil {
					t.Fatal(err)
				}
			})
			if !body.HasAgentCredentials {
				t.Fatalf("stored %s login was not counted: %+v", providerType, body)
			}
		})
	}
}
