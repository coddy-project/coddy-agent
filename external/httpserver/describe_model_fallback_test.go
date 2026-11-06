//go:build http

package httpserver

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestCoddyDescribeFallsBackWhenSessionModelCannotBeBuilt(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	cfg := srv.activeCfg()
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: describeSessionModel, MaxTokens: 100})
	sn, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := applySessionYAMLModel(cfg, mgr.SessionByID(sn.SessionID), describeSessionModel); err != nil {
		t.Fatal(err)
	}
	var attempted string
	srv.makeLLMFromYAML = func(_ *config.Config, model string, _ llm.RequestOptions) (llm.Provider, error) {
		attempted = model
		return nil, errors.New("session model unavailable")
	}
	defaultCalls := 0
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		defaultCalls++
		return fakeProvider{reply: "Repair session naming\ntags: sessions, titles"}, nil
	}
	status, title, tags := postDescribe(t, newDescribeTestServer(t, srv), "repair session naming", map[string]string{"X-Coddy-Session-ID": sn.SessionID}, nil)
	if status != http.StatusOK || attempted != describeSessionModel || defaultCalls != 1 || title != "Repair session naming" || len(tags) != 2 || tags[0] != "sessions" || tags[1] != "titles" {
		t.Fatalf("status %d, attempted %q, default calls %d, title %q, tags %v", status, attempted, defaultCalls, title, tags)
	}
}
