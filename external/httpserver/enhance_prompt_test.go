//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const enhanceAltModel = "openai/gpt-4o-mini"

func postEnhancePrompt(t *testing.T, url, sessionID, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/coddy/enhance-prompt", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", sessionID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	bodyBytes, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, bodyBytes
}

func TestCoddyEnhancePromptRewritesDraftWithoutFollowingIt(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.makeLLMFromYAML = func(*config.Config, string) (llm.Provider, error) {
		return fakeProvider{reply: "```\n\"Refactor the memory endpoint and add tests.\"\n```"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, body := postEnhancePrompt(t, ts.URL, "", `{"text":"fix memory thing"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
	var out struct {
		Object string `json:"object"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "coddy.enhance_prompt" {
		t.Fatalf("unexpected object %q", out.Object)
	}
	if out.Text != "Refactor the memory endpoint and add tests." {
		t.Fatalf("unexpected enhanced text %q", out.Text)
	}
}

func TestCoddyEnhancePromptUsesSelectedSessionModel(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: enhanceAltModel, MaxTokens: 4096, Temperature: 0.2})
	var gotModel string
	srv.makeLLMFromYAML = func(_ *config.Config, model string) (llm.Provider, error) {
		gotModel = model
		return fakeProvider{reply: "Better draft."}, nil
	}
	sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(sn.SessionID)
	if st == nil {
		t.Fatal("session not found")
	}
	if err := applySessionYAMLModel(cfg, st, enhanceAltModel); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, body := postEnhancePrompt(t, ts.URL, sn.SessionID, `{"text":"fix memory thing"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
	if gotModel != enhanceAltModel {
		t.Fatalf("enhance model: want session override %q got %q", enhanceAltModel, gotModel)
	}
}

func TestCoddyEnhancePromptRejectsBlankDraft(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, _ := postEnhancePrompt(t, ts.URL, "", `{"text":"   "}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", res.StatusCode)
	}
}

func TestCoddyEnhancePromptReportsNoConfiguredModel(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Agent.Model = ""
	cfg.Models = nil
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, _ := postEnhancePrompt(t, ts.URL, "", `{"text":"fix memory thing"}`)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d", res.StatusCode)
	}
}
