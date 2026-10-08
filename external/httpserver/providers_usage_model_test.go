//go:build http

package httpserver

// GET /coddy/providers/{name}/usage?model=<alias>: the usage of a provider row
// of type coddy is read per alias, so the route takes the alias, requires it for
// such a row, binds it to the row's configured models and ignores it for every
// other type (docs/plans/remote-model-provider-phase2.md, 4.4).

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// remoteUsageStub is a remote Coddy's usage route: a document per alias, a 404
// unknown_model for the rest, and a count of what asked.
type remoteUsageStub struct {
	srv *httptest.Server

	mu   sync.Mutex
	docs map[string]string
	hits map[string]int
	keys []string
}

func newRemoteUsageStub(t *testing.T, docs map[string]string) *remoteUsageStub {
	t.Helper()
	st := &remoteUsageStub{docs: docs, hits: map[string]int{}}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix, suffix = "/coddy/llm/models/", "/usage"
		if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
			http.NotFound(w, r)
			return
		}
		alias := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
		st.mu.Lock()
		st.hits[alias]++
		st.keys = append(st.keys, r.Header.Get("Authorization"))
		doc, ok := st.docs[alias]
		st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			writeSharedError(w, http.StatusNotFound, sharedInvalid(llm.WireCodeUnknownModel, "no shared model is offered under that name"))
			return
		}
		_, _ = fmt.Fprint(w, doc)
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (st *remoteUsageStub) count(alias string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.hits[alias]
}

func (st *remoteUsageStub) total() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, v := range st.hits {
		n += v
	}
	return n
}

const remoteUsageDoc = `{"supported":true,"account_wide":true,"stale":false,` +
	`"windows":[{"id":"session","label":"5h","used_percent":62,"reset_in_s":600,"exhausted":false}],"blocked":false}`

// usageRouteStand is a server over a coddy row, a NeuralDeep row and an openai row.
type usageRouteStand struct {
	t    *testing.T
	ts   *httptest.Server
	srv  *Server
	mgr  *session.Manager
	stub *remoteUsageStub
	hub  *usageHub
}

func newUsageRouteStand(t *testing.T) *usageRouteStand {
	t.Helper()
	home := t.TempDir()
	hub := newUsageHub(t)
	stub := newRemoteUsageStub(t, map[string]string{
		"coder": remoteUsageDoc,
		"plain": `{"supported":false}`,
	})
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{
			{Name: "remote", Type: "coddy", APIBase: stub.srv.URL, APIKey: "remote-token"},
			{Name: "neuraldeep", Type: "neuraldeep", APIKey: usageHubKey},
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "remote/coder", MaxTokens: 100, MaxContextTokens: 1000},
			{Model: "remote/plain", MaxTokens: 100, MaxContextTokens: 1000},
			{Model: "neuraldeep/qwen", MaxTokens: 100, MaxContextTokens: 1000},
			{Model: "stub/model", MaxTokens: 100, MaxContextTokens: 1000},
		},
		Agent: config.Agent{Model: "stub/model"},
	}
	log := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, nil, log, home, nil)
	srv := New(cfg, mgr, log, home)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
		mgr.ShutdownProviderUsage(0)
	})
	return &usageRouteStand{t: t, ts: ts, srv: srv, mgr: mgr, stub: stub, hub: hub}
}

func (s *usageRouteStand) get(path string) (int, map[string]any) {
	s.t.Helper()
	res, err := http.Get(s.ts.URL + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		s.t.Fatalf("%s: not JSON: %v", path, err)
	}
	return res.StatusCode, out
}

// A coddy row has no usage of its own, only its models do.
func TestProviderUsageRouteNeedsTheAliasForACoddyRow(t *testing.T) {
	s := newUsageRouteStand(t)
	for _, path := range []string{"/coddy/providers/remote/usage", "/coddy/providers/remote/usage?model=", "/coddy/providers/remote/usage?model=%20%20"} {
		status, out := s.get(path)
		if status != http.StatusBadRequest || out["ok"] != false || out["error"] != "invalid" {
			t.Fatalf("%s: status=%d body=%v", path, status, out)
		}
		if detail, _ := out["detail"].(string); !strings.Contains(detail, "model") {
			t.Fatalf("%s: the refusal does not say what is missing: %v", path, out)
		}
	}
	if n := s.stub.total(); n != 0 {
		t.Fatalf("the remote was asked %d times for a request that named no alias", n)
	}
}

// An alias that is not a configured model of the row is refused before anything
// is created or read, so a client cannot grow the cache or make the host ask the
// remote on arbitrary names.
func TestProviderUsageRouteBindsTheAliasToTheConfiguredModels(t *testing.T) {
	s := newUsageRouteStand(t)
	for _, alias := range []string{"nope", "stub%2Fmodel", "remote%2Fcoder", "Coder", "coder%2Fx"} {
		status, out := s.get("/coddy/providers/remote/usage?model=" + alias)
		if status != http.StatusNotFound || out["ok"] != false || out["error"] != "unknown_model" {
			t.Fatalf("%q: status=%d body=%v", alias, status, out)
		}
		if _, has := out["usage"]; has {
			t.Fatalf("%q: an unknown alias answered a snapshot: %v", alias, out)
		}
	}
	if n := s.stub.total(); n != 0 {
		t.Fatalf("the remote was asked %d times for aliases that no model row names", n)
	}
	// The name of the provider is still checked first.
	if status, out := s.get("/coddy/providers/nope/usage?model=coder"); status != http.StatusNotFound || out["error"] != "unknown provider" {
		t.Fatalf("unknown provider: status=%d body=%v", status, out)
	}
}

func TestProviderUsageRouteReadsAPerAliasSnapshotOfACoddyRow(t *testing.T) {
	s := newUsageRouteStand(t)
	status, out := s.get("/coddy/providers/remote/usage?model=coder")
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("status=%d body=%v", status, out)
	}
	usage, _ := out["usage"].(map[string]any)
	if usage["provider"] != "remote" || usage["providerType"] != "coddy" || usage["model"] != "coder" {
		t.Fatalf("the snapshot does not name its subject: %v", usage)
	}
	windows, _ := usage["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("windows: %v", usage["windows"])
	}
	w, _ := windows[0].(map[string]any)
	if w["id"] != "session" || w["usedPercent"] != float64(62) || w["resetsAt"] == "" || w["resetsAt"] == nil {
		t.Fatalf("the window was not mapped: %v", w)
	}
	if in, _ := w["resetInSec"].(float64); in <= 0 || in > 600 {
		t.Fatalf("resetInSec %v, want a countdown from 600", w["resetInSec"])
	}
	for _, k := range []string{"plan", "keyName", "wallet"} {
		if _, has := usage[k]; has {
			t.Fatalf("the snapshot of a remote's account carries %q: %v", k, usage)
		}
	}
	s.stub.mu.Lock()
	key := s.stub.keys[0]
	s.stub.mu.Unlock()
	if key != "Bearer remote-token" {
		t.Fatalf("the remote was asked with %q", key)
	}
	// A second read inside the manager's TTL is its cache's, not another request.
	if status, out = s.get("/coddy/providers/remote/usage?model=coder"); status != http.StatusOK || out["ok"] != true {
		t.Fatalf("second read: status=%d body=%v", status, out)
	}
	if n := s.stub.count("coder"); n != 1 {
		t.Fatalf("the remote was asked %d times for two reads", n)
	}
	// Another alias of the row is its own subject with its own read.
	status, out = s.get("/coddy/providers/remote/usage?model=plain")
	if status != http.StatusOK || out["ok"] != false || out["unsupported"] != true || out["provider"] != "remote" || out["providerType"] != "coddy" || out["model"] != "plain" {
		t.Fatalf("an alias whose remote reports no usage: status=%d body=%v", status, out)
	}
	if _, has := out["disabled"]; has {
		t.Fatalf("a remote without a usage source is not a switched-off panel: %v", out)
	}
	if n := s.stub.count("plain"); n != 1 {
		t.Fatalf("the remote was asked %d times for the second alias", n)
	}
}

// Every other type ignores the alias: the answer, the subject and the number of
// reads are what they were without it.
func TestProviderUsageRouteIgnoresTheAliasForOtherTypes(t *testing.T) {
	s := newUsageRouteStand(t)
	for _, q := range []string{"", "?model=whatever", "?model=qwen"} {
		status, out := s.get("/coddy/providers/neuraldeep/usage" + q)
		if status != http.StatusOK || out["ok"] != true {
			t.Fatalf("neuraldeep%s: status=%d body=%v", q, status, out)
		}
		usage, _ := out["usage"].(map[string]any)
		if usage["provider"] != "neuraldeep" || usage["providerType"] != "neuraldeep" {
			t.Fatalf("neuraldeep%s: %v", q, usage)
		}
		if _, has := usage["model"]; has {
			t.Fatalf("neuraldeep%s: the snapshot of a row that is read as a whole names a model: %v", q, usage)
		}
	}
	if n := s.hub.count(); n != 1 {
		t.Fatalf("the hub was asked %d times: the alias made a second subject", n)
	}
	for _, q := range []string{"", "?model=whatever"} {
		status, out := s.get("/coddy/providers/stub/usage" + q)
		if status != http.StatusOK || out["ok"] != false || out["unsupported"] != true || out["provider"] != "stub" || out["providerType"] != "openai" {
			t.Fatalf("stub%s: status=%d body=%v", q, status, out)
		}
		if _, has := out["model"]; has {
			t.Fatalf("stub%s: an unsupported answer of another type names a model: %v", q, out)
		}
	}
}
