//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newInfoTestServer serves the API over a configuration built in code whose
// fallback permission mode (Tools.PermissionMode, what new sessions start in
// while nothing was chosen) is mode, with the manager every reload path goes
// through, so a test can swap the configuration the way a settings save does.
func newInfoTestServer(t *testing.T, mode string) (*httptest.Server, *session.Manager) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = root
	cfg.Tools.PermissionMode = mode
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts, mgr
}

func readInfo(t *testing.T, ts *httptest.Server) map[string]interface{} {
	t.Helper()
	resp, err := http.Get(ts.URL + "/coddy/info")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /coddy/info: status %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The web UI's start screen has no session to read a settings snapshot from,
// yet its permission chip has to name the mode the first turn will run under
// and its pick has to be compared with that mode. GET /coddy/info, which the
// page already reads through any environment, names it: the mode a new
// session starts in (here the fallback of a configuration built in code, ask
// when it names none), and the new value as soon as a reload installs one.
func TestCoddyInfoNamesTheConfiguredPermissionMode(t *testing.T) {
	cases := []struct {
		configured string
		want       string
	}{
		{config.PermModeBypass, config.PermModeBypass},
		{config.PermModeAcceptEdits, config.PermModeAcceptEdits},
		{config.PermModeAsk, config.PermModeAsk},
		{"", config.PermModeAsk},
		{"sometimes", config.PermModeAsk},
	}
	for _, tc := range cases {
		t.Run("configured="+tc.configured, func(t *testing.T) {
			ts, _ := newInfoTestServer(t, tc.configured)
			if got, _ := readInfo(t, ts)["permissionMode"].(string); got != tc.want {
				t.Fatalf("permissionMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoddyInfoFollowsAReloadedPermissionMode(t *testing.T) {
	ts, mgr := newInfoTestServer(t, config.PermModeBypass)
	if got, _ := readInfo(t, ts)["permissionMode"].(string); got != config.PermModeBypass {
		t.Fatalf("before the reload: permissionMode = %q, want bypass", got)
	}
	next := *mgr.Cfg()
	next.Tools.PermissionMode = config.PermModeAsk
	mgr.ReplaceConfig(&next)
	if got, _ := readInfo(t, ts)["permissionMode"].(string); got != config.PermModeAsk {
		t.Fatalf("after the reload: permissionMode = %q, want ask", got)
	}
}

// The served spec names the field, with the values it takes.
func TestOpenAPIDescribesTheInfoPermissionMode(t *testing.T) {
	paths, _ := openAPISpec()["paths"].(map[string]interface{})
	entry, _ := paths["/coddy/info"].(map[string]interface{})
	get, _ := entry["get"].(map[string]interface{})
	responses, _ := get["responses"].(map[string]interface{})
	ok, _ := responses["200"].(map[string]interface{})
	content, _ := ok["content"].(map[string]interface{})
	media, _ := content["application/json"].(map[string]interface{})
	schema, _ := media["schema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	field, _ := props["permissionMode"].(map[string]interface{})
	if field == nil {
		t.Fatalf("GET /coddy/info schema has no permissionMode: %#v", schema)
	}
	enum, _ := field["enum"].([]string)
	want := []string{config.PermModeAsk, config.PermModeAcceptEdits, config.PermModeBypass}
	if len(enum) != len(want) {
		t.Fatalf("permissionMode enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Fatalf("permissionMode enum = %v, want %v", enum, want)
		}
	}
	required, _ := schema["required"].([]string)
	found := false
	for _, r := range required {
		found = found || r == "permissionMode"
	}
	if !found {
		t.Fatalf("permissionMode is not required: %v", required)
	}
}

// The status document: GET /coddy/status answers what the process hands the
// server (Options.Status), behind the gate of the API and with nothing the
// operator did not mean to show.

// aStatusSnapshot is a process that runs the API and has nothing to report.
func aStatusSnapshot() serve.Snapshot {
	return serve.Snapshot{
		Build:      serve.Build{Version: "1.3.12", GoVersion: "go1.26.9", GOOS: "linux", GOARCH: "arm64", Tags: []string{"http", "metrics"}},
		StartedAt:  time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC),
		StartedBy:  serve.StartedBySystemd,
		ConfigFile: "/home/al/.coddy/config.yaml",
		Subsystems: []serve.SubsystemStatus{{Kind: serve.KindHTTP, State: serve.StateRunning, Address: "127.0.0.1:12345"}},
	}
}

// aMetricsBlock is what an exporter holds, with figures a test chose.
func aMetricsBlock() *serve.MetricsBlock {
	return &serve.MetricsBlock{
		Providers: []serve.ProviderStats{{Name: "local-mlx", Attempts: 12, Failures: 3, MeanSeconds: new(2.0)}},
		Series: serve.Series{
			StepSeconds: 15, End: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC),
			AttemptsOK: []float64{math.NaN(), 3}, ProviderErrors: []float64{math.NaN(), 0},
			AttemptSecondsMean: []float64{math.NaN(), 5.8}, TokensIn: []float64{math.NaN(), 38000},
			TokensOut: []float64{math.NaN(), 2100}, TurnsRunning: []float64{1, 2}, TasksUnfinished: []float64{3, 3},
		},
	}
}

// statusOf renders the document of snap, with block when there is one.
func statusOf(snap serve.Snapshot, block *serve.MetricsBlock) func() serve.StatusDocument {
	return func() serve.StatusDocument {
		return serve.RenderStatus(snap, block, time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC), "/home/al")
	}
}

// newStatusTestServer serves the API over cfg with doc as its status document
// (none when doc is nil), and returns what a client sends to reach it.
func newStatusTestServer(t *testing.T, cfg *config.Config, doc func() serve.StatusDocument) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	if cfg == nil {
		cfg = &config.Config{}
	}
	cfg.Paths.Home = root
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	srv := New(cfg, mgr, slog.Default(), root)
	srv.SetStatus(doc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts
}

// getStatus reads /coddy/status, with a bearer token when bearer is not empty.
func getStatus(t *testing.T, ts *httptest.Server, bearer string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/coddy/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func statusKeys(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return out
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCoddyStatusServesTheDocumentAsJSON(t *testing.T) {
	ts := newStatusTestServer(t, nil, statusOf(aStatusSnapshot(), nil))
	resp, body := getStatus(t, ts, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /coddy/status: %d %q\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q: a live document must not be cached", got)
	}
	var doc serve.StatusDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Object != serve.StatusObject || doc.Build.Version != "1.3.12" || doc.Process.StartedBy != serve.StartedBySystemd {
		t.Errorf("document = %+v", doc)
	}
	if doc.Process.ConfigFile != "~/.coddy/config.yaml" {
		t.Errorf("configFile = %q, want the home as a tilde", doc.Process.ConfigFile)
	}
	if len(doc.Subsystems) != 1 || doc.Subsystems[0].Address != "127.0.0.1:12345" {
		t.Errorf("subsystems = %+v", doc.Subsystems)
	}
	if post, err := http.Post(ts.URL+"/coddy/status", "application/json", nil); err == nil {
		_ = post.Body.Close()
		if post.StatusCode == http.StatusOK {
			t.Error("POST /coddy/status was answered")
		}
	}
}

// A server that is given no document does not register the route, so it
// answers as a server of an older version does.
func TestCoddyStatusIsAbsentWithoutADocument(t *testing.T) {
	ts := newStatusTestServer(t, nil, nil)
	resp, body := getStatus(t, ts, "")
	if resp.StatusCode == http.StatusOK && strings.Contains(string(body), serve.StatusObject) {
		t.Fatalf("a server with no document served one:\n%s", body)
	}
	if resp.StatusCode != http.StatusNotFound && !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("GET /coddy/status without a document: %d %q, want 404 (or the web page)", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// The metrics block is the exporter's: there when the callback that builds the
// document was given one, whatever the document says elsewhere, and absent
// without.
func TestCoddyStatusHasTheMetricsBlockOnlyWithOne(t *testing.T) {
	with := newStatusTestServer(t, nil, statusOf(aStatusSnapshot(), aMetricsBlock()))
	_, body := getStatus(t, with, "")
	top := statusKeys(t, body)
	if _, ok := top["metrics"]; !ok {
		t.Fatalf("a document made with a block has no metrics member: %v", sortedKeys(top))
	}
	metrics := statusKeys(t, top["metrics"])
	if got := sortedKeys(metrics); !slices.Equal(got, []string{"providers", "series"}) {
		t.Errorf("metrics members = %v", got)
	}
	series := statusKeys(t, metrics["series"])
	wantSeries := []string{"attemptSecondsMean", "attemptsOk", "end", "providerErrors", "stepSeconds", "tasksUnfinished", "tokensIn", "tokensOut", "turnsRunning"}
	if got := sortedKeys(series); !slices.Equal(got, wantSeries) {
		t.Errorf("series members = %v, want %v", got, wantSeries)
	}
	if string(series["attemptsOk"]) != "[null,3]" || string(series["stepSeconds"]) != "15" {
		t.Errorf("attemptsOk = %s, stepSeconds = %s: a gap is null", series["attemptsOk"], series["stepSeconds"])
	}
	var providers []map[string]json.RawMessage
	if err := json.Unmarshal(metrics["providers"], &providers); err != nil || len(providers) != 1 {
		t.Fatalf("providers = %s (%v)", metrics["providers"], err)
	}
	if string(providers[0]["meanSeconds"]) != "2" || providers[0]["lastFailureAt"] != nil || providers[0]["lastOkAt"] != nil {
		t.Errorf("provider row = %v: the mean is there, the times it never had are not", providers[0])
	}

	without := newStatusTestServer(t, nil, statusOf(aStatusSnapshot(), nil))
	_, body = getStatus(t, without, "")
	if _, ok := statusKeys(t, body)["metrics"]; ok {
		t.Errorf("a document made without a block has a metrics member:\n%s", body)
	}
}

// A document with nothing to report is the always-present members and no
// other: an absent member means nothing, it is never sent as zero.
func TestCoddyStatusLeavesOutWhatIsNotThere(t *testing.T) {
	ts := newStatusTestServer(t, nil, statusOf(serve.Snapshot{}, nil))
	_, body := getStatus(t, ts, "")
	top := statusKeys(t, body)
	if got, want := sortedKeys(top), []string{"build", "generatedAt", "object", "process", "subsystems", "turns"}; !slices.Equal(got, want) {
		t.Errorf("members of an empty document = %v, want %v", got, want)
	}
	for name, want := range map[string][]string{
		"build":   {"arch", "goVersion", "os", "version"},
		"process": {"configFile", "startedAt", "startedBy"},
		"turns":   {"running"},
	} {
		if got := sortedKeys(statusKeys(t, top[name])); !slices.Equal(got, want) {
			t.Errorf("%s members = %v, want %v", name, got, want)
		}
	}
	if string(top["subsystems"]) != "[]" {
		t.Errorf("subsystems = %s, want an empty array", top["subsystems"])
	}

	// And a busy one adds exactly the optional members that have a value.
	snap := aStatusSnapshot()
	snap.RestartRequired, snap.RestartRefused = true, "gateways.telegram.enable needs the gateway tag"
	snap.TurnsRunning, snap.OldestTurnStartedAt = 1, time.Date(2026, 10, 15, 9, 55, 0, 0, time.UTC)
	snap.PermissionPrompts = 2
	snap.Tasks = map[string]map[string]int{"agent": {"running": 1, "queued": 0}}
	snap.Disk = &serve.Disk{AvailableBytes: 0, SizeBytes: 1 << 30}
	snap.Unavailable = []string{serve.UnavailableDisk}
	_, body = getStatus(t, newStatusTestServer(t, nil, statusOf(snap, nil)), "")
	full := statusKeys(t, body)
	want := []string{"attention", "build", "disk", "generatedAt", "object", "process", "prompts", "restartRefused", "restartRequired", "subsystems", "tasks", "turns", "unavailable"}
	if got := sortedKeys(full); !slices.Equal(got, want) {
		t.Errorf("members of a busy document = %v, want %v", got, want)
	}
	if got := string(full["disk"]); got != `{"availableBytes":0,"sizeBytes":1073741824}` {
		t.Errorf("disk = %s: a full disk is a figure of 0, sent", got)
	}
	if got := string(full["tasks"]); got != `{"agent":{"running":1}}` {
		t.Errorf("tasks = %s: only what is above zero", got)
	}
	if got := string(full["prompts"]); got != `{"permission":2}` {
		t.Errorf("prompts = %s: only the kind that waits", got)
	}
}

// The route is behind the gate of the API, whose tokens are the only ones it
// takes: the exporter's token opens /metrics and nothing here.
func TestCoddyStatusIsBehindTheAPIGate(t *testing.T) {
	cfg := &config.Config{}
	cfg.HTTPServer.AuthToken = "api-token"
	cfg.Metrics.Token = "metrics-token"
	ts := newStatusTestServer(t, cfg, statusOf(aStatusSnapshot(), nil))

	for name, bearer := range map[string]string{"no credential": "", "the metrics token": "metrics-token", "a wrong token": "api-tokenx"} {
		resp, body := getStatus(t, ts, bearer)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, resp.StatusCode)
		}
		if resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", name)
		}
		if strings.Contains(string(body), serve.StatusObject) || strings.Contains(string(body), "1.3.12") {
			t.Errorf("%s: a refused request got the document:\n%s", name, body)
		}
	}
	if resp, body := getStatus(t, ts, "api-token"); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), serve.StatusObject) {
		t.Errorf("the API token: %d\n%s", resp.StatusCode, body)
	}

	// Nothing configured, nothing asked: a server with no credential serves it.
	open := newStatusTestServer(t, nil, statusOf(aStatusSnapshot(), nil))
	if resp, _ := getStatus(t, open, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("an API with no credential: %d, want 200", resp.StatusCode)
	}
}

// What an installation holds that is private - the titles, workspaces,
// prompts, tool arguments and error texts of its sessions, its chat ids, user
// names, keys and tokens - is in none of the document, whatever the process
// has been through. Every such value here carries SECRET, in the configuration,
// in the session store and in the runtime, and the document is read through the
// route the web page uses.
func TestCoddyStatusCarriesNoMarkedValue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv(serve.EnvRole, "")
	coddyHome := filepath.Join(root, ".coddy")
	workspace := filepath.Join(root, "SECRET-WORKSPACE")
	otherWorkspace := filepath.Join(root, "SECRET-OTHER-WORKSPACE")
	for _, dir := range []string{coddyHome, workspace, otherWorkspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{}
	cfg.HTTPServer.AuthToken = "SECRET-API-TOKEN"
	cfg.HTTPServer.Login.User = "SECRET-USER"
	cfg.HTTPServer.Login.PasswordHash = "SECRET-PASSWORD-HASH"
	cfg.Metrics = config.MetricsConfig{Enable: true, Host: "127.0.0.1", Port: 1, Token: "SECRET-METRICS-TOKEN"}
	cfg.Gateways.Telegram.Token = "SECRET-TELEGRAM-TOKEN"
	cfg.Gateways.Telegram.Admins = []int64{918273645}
	cfg.Gateways.Telegram.Chats = []config.GatewayChatConfig{{ChatID: -100273645}}
	// The provider answers on a server of its own that counts every request:
	// reading the status never reaches a provider.
	var providerHits atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Close)
	cfg.Providers = []config.ProviderConfig{{Name: "local-mlx", Type: "openai", APIBase: provider.URL + "/SECRET-PATH/v1", APIKey: "SECRET-PROVIDER-KEY"}}

	log := slog.New(slog.DiscardHandler)
	rt := &serve.Runtime{}
	if err := rt.Init(serve.Options{
		CLI: config.CLIPaths{Home: coddyHome, CWD: workspace}, SessionsRoot: filepath.Join(root, "sessions"),
		NeedsSessions: true, Log: log, Cfg: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	// Two sessions, one in each workspace, with a title, a prompt, a tool call
	// whose arguments name a path and an error the user was shown.
	for i, dir := range []string{workspace, otherWorkspace} {
		res, err := rt.Mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
		if err != nil {
			t.Fatal(err)
		}
		st := rt.Mgr.SessionByID(res.SessionID)
		if st == nil {
			t.Fatalf("session %s is not live", res.SessionID)
		}
		st.SetTitlePinned("SECRET-TITLE")
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "SECRET-PROMPT"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "read_file", InputJSON: `{"path":"SECRET-TOOL-ARGUMENT"}`}}})
		st.AppendUILogError(i, "SECRET-ERROR-TEXT")
	}

	// A subsystem that failed alone, with an error text of its own.
	keeper := serve.Subsystem{
		Kind: serve.KindScheduler, Available: true,
		Enabled: func(*config.Config) bool { return true },
		Run: func(ctx context.Context, _ *config.Config) error {
			<-ctx.Done()
			return nil
		},
	}
	failing := serve.Subsystem{
		Kind: serve.KindMetrics, ConfigKey: "metrics.enable", Available: true, Auxiliary: true,
		Enabled: func(c *config.Config) bool { return c.Metrics.Enable },
		Run: func(context.Context, *config.Config) error {
			return errors.New("listen tcp 127.0.0.1:1: SECRET-ERROR-TEXT")
		},
	}
	sup := serve.NewSupervisor(log, []serve.Subsystem{keeper, failing})
	rt.SetSupervisor(sup)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, cfg, make(chan *config.Config)) }()
	t.Cleanup(func() { cancel(); <-done })
	rt.SetMetricsSource(func() *serve.MetricsBlock {
		return &serve.MetricsBlock{
			Providers: []serve.ProviderStats{{Name: "local-mlx", Attempts: 3, Failures: 3, LastFailureAt: time.Now().Add(-time.Minute)}},
			Series:    serve.Series{StepSeconds: 15, End: time.Now(), AttemptsOK: []float64{math.NaN()}},
		}
	})

	srv := New(cfg, rt.Mgr, log, workspace)
	srv.SetStatus(rt.StatusDocument)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})

	var doc serve.StatusDocument
	var body []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		var resp *http.Response
		resp, body = getStatus(t, ts, "SECRET-API-TOKEN")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /coddy/status: %d\n%s", resp.StatusCode, body)
		}
		doc = serve.StatusDocument{}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(doc.Attention, func(i serve.AttentionItem) bool { return i.Code == serve.AttentionSubsystemDown }) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if strings.Contains(string(body), "SECRET") {
		t.Fatalf("the document carries a marked value:\n%s", body)
	}
	// Reading the status is no outbound traffic: not the polling above, nor
	// a few more reads.
	for range 3 {
		if resp, again := getStatus(t, ts, "SECRET-API-TOKEN"); resp.StatusCode != http.StatusOK || strings.Contains(string(again), "SECRET") {
			t.Fatalf("a later read: %d\n%s", resp.StatusCode, again)
		}
	}
	if n := providerHits.Load(); n != 0 {
		t.Errorf("the configured provider received %d requests while the status was read, want none", n)
	}
	// And it is the real document, not an empty one the test passes by.
	want := []serve.AttentionItem{
		{Code: serve.AttentionSubsystemDown, Subsystem: serve.KindMetrics},
		{Code: serve.AttentionProviderFailing, Provider: "local-mlx"},
	}
	if !slices.Equal(doc.Attention, want) {
		t.Errorf("attention = %+v, want %+v", doc.Attention, want)
	}
	if doc.Process.StartedBy != serve.StartedByForeground || doc.Process.ConfigFile != filepath.Join("~", ".coddy", "config.yaml") {
		t.Errorf("process = %+v", doc.Process)
	}
	for _, row := range doc.Subsystems {
		if row.Kind == serve.KindMetrics && (row.State != serve.StateStopped || row.Credential != serve.CredentialToken) {
			t.Errorf("exporter row = %+v, want stopped behind a token", row)
		}
	}
}

// The served spec describes the route: its operation, its 401 and a schema
// whose members and required members are the document's own, part by part, and
// whose enumerations are the Go constants.
func TestOpenAPIDescribesTheStatusRoute(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	type schema struct {
		Required   []string                          `json:"required"`
		Properties map[string]map[string]interface{} `json:"properties"`
	}
	var spec struct {
		Paths      map[string]map[string]map[string]interface{} `json:"paths"`
		Components struct {
			Schemas map[string]schema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	get := spec.Paths["/coddy/status"]["get"]
	if get == nil {
		t.Fatal("paths has no GET /coddy/status")
	}
	if get["operationId"] != "coddyStatusGet" {
		t.Errorf("operationId = %v", get["operationId"])
	}
	responses, _ := get["responses"].(map[string]interface{})
	ok, _ := responses["200"].(map[string]interface{})
	ref := ok["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if ref["$ref"] != "#/components/schemas/CoddyStatus" {
		t.Errorf("200 schema = %v", ref)
	}
	if _, has401 := responses["401"]; !has401 {
		t.Error("the route's 401 is not described")
	}

	// Each schema is a struct of the document: the same member names, and the
	// same members required (those the struct sends even when empty).
	parts := map[string]reflect.Type{
		"CoddyStatus":              reflect.TypeFor[serve.StatusDocument](),
		"CoddyStatusBuild":         reflect.TypeFor[serve.Build](),
		"CoddyStatusProcess":       reflect.TypeFor[serve.ProcessDoc](),
		"CoddyStatusSubsystem":     reflect.TypeFor[serve.SubsystemStatus](),
		"CoddyStatusTurns":         reflect.TypeFor[serve.TurnsDoc](),
		"CoddyStatusPrompts":       reflect.TypeFor[serve.PromptsDoc](),
		"CoddyStatusDisk":          reflect.TypeFor[serve.Disk](),
		"CoddyStatusAttentionItem": reflect.TypeFor[serve.AttentionItem](),
		"CoddyStatusMetrics":       reflect.TypeFor[serve.MetricsBlock](),
		"CoddyStatusProvider":      reflect.TypeFor[serve.ProviderStats](),
		"CoddyStatusSeries":        reflect.TypeFor[serve.Series](),
	}
	for name, typ := range parts {
		got, found := spec.Components.Schemas[name]
		if !found {
			t.Errorf("components.schemas has no %s", name)
			continue
		}
		var members, required []string
		for i := range typ.NumField() {
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
			if tag[0] == "" || tag[0] == "-" {
				continue
			}
			members = append(members, tag[0])
			if !slices.Contains(tag[1:], "omitempty") && !slices.Contains(tag[1:], "omitzero") {
				required = append(required, tag[0])
			}
		}
		specMembers := make([]string, 0, len(got.Properties))
		for member := range got.Properties {
			specMembers = append(specMembers, member)
		}
		sort.Strings(members)
		sort.Strings(required)
		sort.Strings(specMembers)
		gotRequired := slices.Clone(got.Required)
		sort.Strings(gotRequired)
		if !slices.Equal(members, specMembers) {
			t.Errorf("%s describes %v, the document sends %v", name, specMembers, members)
		}
		if !slices.Equal(required, gotRequired) {
			t.Errorf("%s requires %v, the document always sends %v", name, gotRequired, required)
		}
	}

	// The parts are references, so a client generated from the spec gets a type each.
	refTo := func(prop map[string]interface{}) interface{} {
		if items, ok := prop["items"].(map[string]interface{}); ok {
			return items["$ref"]
		}
		return prop["$ref"]
	}
	for schemaName, wants := range map[string]map[string]string{
		"CoddyStatus": {
			"build": "CoddyStatusBuild", "process": "CoddyStatusProcess", "subsystems": "CoddyStatusSubsystem",
			"turns": "CoddyStatusTurns", "prompts": "CoddyStatusPrompts", "disk": "CoddyStatusDisk",
			"attention": "CoddyStatusAttentionItem", "metrics": "CoddyStatusMetrics",
		},
		"CoddyStatusMetrics": {"providers": "CoddyStatusProvider", "series": "CoddyStatusSeries"},
	} {
		for member, want := range wants {
			if got := refTo(spec.Components.Schemas[schemaName].Properties[member]); got != "#/components/schemas/"+want {
				t.Errorf("%s.%s refers to %v, want %s", schemaName, member, got, want)
			}
		}
	}

	// The enumerations are the constants the document is made of.
	enum := func(schemaName, member string) []string {
		prop := spec.Components.Schemas[schemaName].Properties[member]
		if items, ok := prop["items"].(map[string]interface{}); ok {
			prop = items
		}
		var out []string
		list, _ := prop["enum"].([]interface{})
		for _, v := range list {
			out = append(out, v.(string))
		}
		return out
	}
	for _, tc := range []struct {
		schemaName, member string
		want               []string
	}{
		{"CoddyStatus", "object", []string{serve.StatusObject}},
		{"CoddyStatus", "unavailable", []string{serve.UnavailableDisk}},
		{"CoddyStatusProcess", "startedBy", []string{string(serve.StartedBySystemd), string(serve.StartedByDaemon), string(serve.StartedByForeground)}},
		{"CoddyStatusSubsystem", "state", []string{serve.StateRunning, serve.StateStarting, serve.StateStopped, serve.StateRetained}},
		{"CoddyStatusSubsystem", "credential", []string{string(serve.CredentialToken), string(serve.CredentialNone)}},
		{"CoddyStatusAttentionItem", "code", []string{
			string(serve.AttentionRestartRequired), string(serve.AttentionDiskLow), string(serve.AttentionSubsystemDown),
			string(serve.AttentionProviderFailing), string(serve.AttentionSwarmJoinDown),
		}},
	} {
		if got := enum(tc.schemaName, tc.member); !slices.Equal(got, tc.want) {
			t.Errorf("%s.%s enumerates %v, the constants are %v", tc.schemaName, tc.member, got, tc.want)
		}
	}
}
