//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// describeSessionModel is the model a chat runs on when its operator picked
// one in the composer. It is deliberately not the first one configured.
const describeSessionModel = "openai/gpt-4o-mini"

// newDescribeTestServer serves srv for one test and closes it with the test.
func newDescribeTestServer(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// A chat names itself with the model it talks to. The default model is chosen
// from the configuration alone, so a first alphabetical entry whose credential
// died used to take the naming of every conversation down with it: the answer
// never left the handler, and the chat kept its raw first message as its title
// with no tags at all. The session's own model answers instead, and the default
// is what a request without a session falls back to.
func TestCoddyDescribeNamesWithTheSessionModelWhenTheDefaultIsBroken(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: describeSessionModel, MaxTokens: 100, Temperature: 0.2})

	factoryCalls := 0
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		factoryCalls++
		return nil, errors.New(`provider "codex-rgs": 401 Unauthorized token_revoked`)
	}
	var gotModel string
	srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		gotModel = model
		return fakeProvider{reply: "Rename the session title generator\ntags: sessions, titles"}, nil
	}

	sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := applySessionYAMLModel(cfg, mgr.SessionByID(sn.SessionID), describeSessionModel); err != nil {
		t.Fatal(err)
	}

	ts := newDescribeTestServer(t, srv)
	status, short, tags := postDescribe(t, ts, "почини генератор тайтлов сессий", map[string]string{"X-Coddy-Session-ID": sn.SessionID}, nil)

	if status != http.StatusOK {
		t.Fatalf("status %d, want 200 with the session's own model", status)
	}
	if gotModel != describeSessionModel {
		t.Fatalf("describe asked model %q, want the session's %q", gotModel, describeSessionModel)
	}
	if factoryCalls != 0 {
		t.Fatalf("describe fell back to the default model %d time(s) although the session has one", factoryCalls)
	}
	if short != "Rename the session title generator" {
		t.Fatalf("short = %q", short)
	}
	if len(tags) != 2 || tags[0] != "sessions" || tags[1] != "titles" {
		t.Fatalf("tags = %v, want the tags the model gave", tags)
	}
}

// Without a session there is no composer choice to honour, and the naming goes
// on the configured default exactly as before.
func TestCoddyDescribeWithoutASessionUsesTheDefaultModel(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	factoryCalls := 0
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		factoryCalls++
		return fakeProvider{reply: "Check the deployment\ntags: ops"}, nil
	}
	yamlCalls := 0
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		yamlCalls++
		return fakeProvider{reply: "should not be asked"}, nil
	}

	ts := newDescribeTestServer(t, srv)
	status, short, tags := postDescribe(t, ts, "check the deployment", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if factoryCalls != 1 || yamlCalls != 0 {
		t.Fatalf("factory calls %d, makeLLMFromYAML calls %d: want the default model alone", factoryCalls, yamlCalls)
	}
	if short != "Check the deployment" {
		t.Fatalf("short = %q", short)
	}
	if len(tags) != 1 || tags[0] != "ops" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestCoddyDescribeUsesRequestedModelBeforeSessionExists(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	var gotModel string
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return nil, errors.New("default credential revoked")
	}
	srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		gotModel = model
		return fakeProvider{reply: "Repair session naming\ntags: sessions, titles"}, nil
	}
	ts := newDescribeTestServer(t, srv)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/describe", strings.NewReader(`{"text":"repair session naming","model":"openai/gpt-4o-mini"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Coddy-Session-ID", "sess_not_created")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK || gotModel != describeSessionModel {
		t.Fatalf("status %d, model %q; want 200 and %q", resp.StatusCode, gotModel, describeSessionModel)
	}
	var answer struct {
		Short string   `json:"short"`
		Tags  []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	if answer.Short != "Repair session naming" || len(answer.Tags) != 2 || answer.Tags[0] != "sessions" || answer.Tags[1] != "titles" {
		t.Fatalf("unexpected description: %+v", answer)
	}
}

// A model that only an acp agent serves answers inside a session and would
// take the naming request for a task, so the naming borrows a model of another
// type rather than failing the chat.
func TestCoddyDescribeBorrowsAModelWhenTheSessionModelIsAnAgent(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: "claude-agent-acp"}})
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "agent/default"})

	var gotModel string
	srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		gotModel = model
		return fakeProvider{reply: "Review the branch\ntags: git"}, nil
	}

	sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := applySessionYAMLModel(cfg, mgr.SessionByID(sn.SessionID), "agent/default"); err != nil {
		t.Fatal(err)
	}

	ts := newDescribeTestServer(t, srv)
	status, short, _ := postDescribe(t, ts, "review the branch", map[string]string{"X-Coddy-Session-ID": sn.SessionID}, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if gotModel == "" || gotModel == "agent/default" || gotModel != cfg.FirstNonACPModelID() {
		t.Fatalf("describe asked model %q, want a model an acp agent does not serve (%q)", gotModel, cfg.FirstNonACPModelID())
	}
	if short != "Review the branch" {
		t.Fatalf("short = %q", short)
	}
}
