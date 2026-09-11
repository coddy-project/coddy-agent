package dryrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

const modeline = "# yaml-language-server: $schema=https://coddy.dev/config.schema.json\n"

// prepare writes body as <home>/config.yaml and runs the static stage.
func prepare(t *testing.T, body string) (*Prepared, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(modeline+body), 0o644); err != nil {
		t.Fatal(err)
	}
	prep, rep, err := Prepare(config.CLIPaths{Home: home, CWD: home, Config: path})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prep == nil {
		var buf bytes.Buffer
		rep.Write(&buf)
		t.Fatalf("the fixture does not pass the static check:\n%s", buf.String())
	}
	return prep, home
}

// run prepares body and runs the probes, letting mut shape the request.
func run(t *testing.T, body string, mut func(*Request)) *Report {
	t.Helper()
	prep, _ := prepare(t, body)
	req := Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator}
	if mut != nil {
		mut(&req)
	}
	return Run(context.Background(), req)
}

func find(t *testing.T, rep *Report, path string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Path == path {
			return c
		}
	}
	t.Fatalf("no check for %s in %+v", path, rep.Checks)
	return Check{}
}

func modelServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		var rows []string
		for _, id := range ids {
			rows = append(rows, fmt.Sprintf(`{"id":%q}`, id))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(rows, ","))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderListingIsOKAndModelsAreChecked(t *testing.T) {
	srv := modelServer(t, "qwen")
	rep := run(t, fmt.Sprintf(`providers:
  - name: local
    type: openai
    api_base: %s/v1
models:
  - model: local/qwen
  - model: local/other
agent:
  model: local/qwen
`, srv.URL), nil)
	p := find(t, rep, "providers[local]")
	if p.Status != StatusOK || !strings.Contains(p.Message, "1 model") {
		t.Errorf("provider check %+v", p)
	}
	if m := find(t, rep, "models[local/qwen]"); m.Status != StatusOK {
		t.Errorf("listed model %+v", m)
	}
	other := find(t, rep, "models[local/other]")
	if other.Status != StatusWarning || !strings.Contains(other.Message, "not in the model list") || other.Line == 0 {
		t.Errorf("unlisted model %+v", other)
	}
	if rep.Errors() != 0 {
		t.Errorf("errors %d in %+v", rep.Errors(), rep.Checks)
	}
}

func TestProviderRejectedCredentialIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	rep := run(t, fmt.Sprintf("providers:\n  - name: hosted\n    type: openai\n    api_base: %s/v1\n    api_key: nope\nmodels:\n  - model: hosted/x\nagent:\n  model: hosted/x\n", srv.URL), nil)
	p := find(t, rep, "providers[hosted]")
	if p.Status != StatusError || !strings.Contains(p.Message, "HTTP 401") || !strings.Contains(p.Fix, "api_key") {
		t.Errorf("provider check %+v", p)
	}
	if p.Line != 3 {
		t.Errorf("line %d, want the provider entry (3)", p.Line)
	}
	if m := find(t, rep, "models[hosted/x]"); m.Status != StatusSkipped {
		t.Errorf("a model of a failed provider is skipped, got %+v", m)
	}
}

func TestProviderNotFoundHintsAtTheAPIPrefix(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	rep := run(t, fmt.Sprintf("providers:\n  - name: local\n    type: openai\n    api_base: %s\n", srv.URL), nil)
	p := find(t, rep, "providers[local]")
	if p.Status != StatusError || !strings.Contains(p.Message, "HTTP 404") || !strings.Contains(p.Fix, "/v1") {
		t.Errorf("provider check %+v", p)
	}
}

func TestProviderUnreachableIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	rep := run(t, fmt.Sprintf("providers:\n  - name: local\n    type: openai\n    api_base: %s/v1\n", url), nil)
	p := find(t, rep, "providers[local]")
	if p.Status != StatusError || !strings.Contains(p.Message, "cannot reach") {
		t.Errorf("provider check %+v", p)
	}
}

func TestOfficialEndpointWithoutCredentialNeedsNoNetwork(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	rep := run(t, "providers:\n  - name: openai\n    type: openai\nmodels:\n  - model: openai/gpt\nagent:\n  model: openai/gpt\n", nil)
	p := find(t, rep, "providers[openai]")
	if p.Status != StatusError || !strings.Contains(p.Message, "no credential") || !strings.Contains(p.Fix, "OPENAI_API_KEY") {
		t.Errorf("provider check %+v", p)
	}
}

func TestListenerFreeAndInUse(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freeAddr := free.Addr().String()
	_ = free.Close()
	port := held.Addr().(*net.TCPAddr).Port
	rep := run(t, fmt.Sprintf("httpserver:\n  host: 127.0.0.1\n  port: %d\n", port), func(r *Request) {
		r.Listeners = []Listener{{Path: "httpserver", Addr: held.Addr().String()}, {Path: "swarm", Addr: freeAddr}}
	})
	h := find(t, rep, "httpserver")
	if h.Status != StatusError || !strings.Contains(h.Message, "in use") || h.Line != 4 {
		t.Errorf("held listener %+v", h)
	}
	if s := find(t, rep, "swarm"); s.Status != StatusOK || !strings.Contains(s.Message, "free") {
		t.Errorf("free listener %+v", s)
	}
}

func TestTelegramTokenProbe(t *testing.T) {
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getMe") || !strings.Contains(r.URL.Path, "/bot123:abc/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"username":"dry_bot"}}`)
		} else {
			_, _ = fmt.Fprint(w, `{"ok":false,"description":"Unauthorized"}`)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv(TelegramAPIBaseEnv, srv.URL)
	body := "gateways:\n  telegram:\n    enable: true\n    token: \"123:abc\"\n"

	status = http.StatusOK
	if c := find(t, run(t, body, nil), "gateways.telegram"); c.Status != StatusOK || !strings.Contains(c.Message, "@dry_bot") {
		t.Errorf("accepted token %+v", c)
	}
	status = http.StatusUnauthorized
	if c := find(t, run(t, body, nil), "gateways.telegram"); c.Status != StatusError || !strings.Contains(c.Message, "rejected") || c.Line != 5 {
		t.Errorf("rejected token %+v", c)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if c := find(t, run(t, "gateways:\n  telegram:\n    enable: true\n", nil), "gateways.telegram"); c.Status != StatusError || !strings.Contains(c.Message, "no token") {
		t.Errorf("missing token %+v", c)
	}
}

func TestMCPCommandLookup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup of a shell script is a POSIX fixture")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "coddy-dry-run-tool"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rep := run(t, "mcp_servers:\n  - name: found\n    command: coddy-dry-run-tool\n  - name: missing\n    command: definitely-not-installed-coddy-mcp\n  - name: off\n    command: definitely-not-installed-coddy-mcp\n    disabled: true\n", nil)
	if c := find(t, rep, "mcp_servers[found]"); c.Status != StatusOK || !strings.Contains(c.Message, "resolves to") {
		t.Errorf("found %+v", c)
	}
	m := find(t, rep, "mcp_servers[missing]")
	if m.Status != StatusError || !strings.Contains(m.Message, "not found") || m.Line != 6 {
		t.Errorf("missing %+v", m)
	}
	if c := find(t, rep, "mcp_servers[off]"); c.Status != StatusSkipped {
		t.Errorf("disabled %+v", c)
	}
}

func TestMCPRemoteReachability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != "secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	rep := run(t, fmt.Sprintf("mcp_servers:\n  - name: up\n    url: %s/mcp\n    headers:\n      - name: X-Token\n        value: secret\n  - name: down\n    url: %s/mcp\n", srv.URL, downURL), nil)
	if c := find(t, rep, "mcp_servers[up]"); c.Status != StatusOK || !strings.Contains(c.Message, "HTTP 405") {
		t.Errorf("up %+v", c)
	}
	if c := find(t, rep, "mcp_servers[down]"); c.Status != StatusError || !strings.Contains(c.Message, "cannot reach") {
		t.Errorf("down %+v", c)
	}
}

func TestRemoteTargetProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)
	ok := run(t, "agent:\n  max_turns: 3\n", func(r *Request) { r.Remote = &remote.Options{BaseURL: srv.URL, Token: "good"} })
	if c := find(t, ok, "--remote"); c.Status != StatusOK || !strings.Contains(c.Message, "accepts the token") {
		t.Errorf("good token %+v", c)
	}
	bad := run(t, "agent:\n  max_turns: 3\n", func(r *Request) { r.Remote = &remote.Options{BaseURL: srv.URL, Token: "bad"} })
	if c := find(t, bad, "--remote"); c.Status != StatusError || !strings.Contains(c.Message, "HTTP 401") || !strings.Contains(c.Fix, "--remote-token") {
		t.Errorf("bad token %+v", c)
	}
}

func TestConfiguredRemotesDownAreWarnings(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	rep := run(t, fmt.Sprintf("httpserver:\n  remotes:\n    - name: nas\n      url: %s\n", url), nil)
	c := find(t, rep, "httpserver.remotes[nas]")
	if c.Status != StatusWarning || !strings.Contains(c.Message, "cannot reach") || c.Line != 5 {
		t.Errorf("remote %+v (line 5 is the url)", c)
	}
}

func TestSessionsDirAndLogFile(t *testing.T) {
	state := t.TempDir()
	// The store and the logger create missing directories themselves, so a
	// missing path is fine; a path that runs through a regular file is not.
	if err := os.WriteFile(filepath.Join(state, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := run(t, fmt.Sprintf("sessions:\n  dir: %s\nlogger:\n  outputs: [file]\n  file: %s\n",
		filepath.Join(state, "sessions"), filepath.Join(state, "afile", "coddy.log")), nil)
	if c := find(t, rep, "sessions.dir"); c.Status != StatusOK || !strings.Contains(c.Message, "will be created") {
		t.Errorf("sessions dir %+v", c)
	}
	if c := find(t, rep, "logger.file"); c.Status != StatusError || c.Line != 6 {
		t.Errorf("log file %+v", c)
	}
}

func TestPromptsDirAndTemplates(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	rep := run(t, "prompts:\n  dir: "+missing+"\n", nil)
	if c := find(t, rep, "prompts.dir"); c.Status != StatusError || !strings.Contains(c.Message, "does not exist") || c.Line != 3 {
		t.Errorf("missing dir %+v", c)
	}
	present := t.TempDir()
	if err := os.WriteFile(filepath.Join(present, "agent.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep = run(t, "prompts:\n  dir: "+present+"\n", nil)
	if c := find(t, rep, "prompts.dir"); c.Status != StatusOK {
		t.Errorf("present dir %+v", c)
	}
	if c := find(t, rep, "prompts.plan_prompt"); c.Status != StatusWarning || !strings.Contains(c.Message, "plan.md") {
		t.Errorf("missing template %+v", c)
	}
	for _, c := range rep.Checks {
		if c.Path == "prompts.agent_prompt" {
			t.Errorf("a template that exists is not reported: %+v", c)
		}
	}
}

func TestExplicitSkillsDirMissingIsAWarningDefaultsAreSilent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "skills-gone")
	rep := run(t, "skills:\n  dirs: [\""+missing+"\"]\n", nil)
	if c := find(t, rep, "skills.dirs[0]"); c.Status != StatusWarning || !strings.Contains(c.Message, "does not exist") || c.Line != 3 {
		t.Errorf("explicit dir %+v", c)
	}
	rep = run(t, "agent:\n  max_turns: 3\n", nil)
	for _, c := range rep.Checks {
		if strings.HasPrefix(c.Path, "skills.dirs") && c.Status != StatusOK {
			t.Errorf("a default dir that is absent must stay quiet: %+v", c)
		}
	}
}

func TestHooksFileParses(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(good, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := run(t, fmt.Sprintf("hooks:\n  files: [%q, %q]\n", good, bad), nil)
	if c := find(t, rep, "hooks.files[0]"); c.Status != StatusOK {
		t.Errorf("good %+v", c)
	}
	if c := find(t, rep, "hooks.files[1]"); c.Status != StatusError || c.Line != 3 {
		t.Errorf("bad %+v", c)
	}
}

func TestSwarmTLSAndCAFiles(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := run(t, fmt.Sprintf("swarm:\n  enable: true\n  auth_token: t\n  tls:\n    cert_file: %s\n    key_file: %s\n  join:\n    - url: http://127.0.0.1:9\n      pairing_token: p\n    - url: http://127.0.0.1:9\n      pairing_token: p\n      dial:\n        ca_file: %s\n", junk, junk, junk), nil)
	if c := find(t, rep, "swarm.tls"); c.Status != StatusError || c.Line != 5 {
		t.Errorf("tls %+v", c)
	}
	if c := find(t, rep, "swarm.join[0]"); c.Status != StatusError || !strings.Contains(c.Message, "cannot reach") || c.Line != 9 {
		t.Errorf("join %+v", c)
	}
	if c := find(t, rep, "swarm.join[1].dial.ca_file"); c.Status != StatusError || !strings.Contains(c.Message, "certificate") {
		t.Errorf("ca %+v", c)
	}
	if c := find(t, rep, "swarm.join[1]"); c.Status != StatusSkipped {
		t.Errorf("a peer behind an unusable CA bundle is not probed: %+v", c)
	}
}

func TestSubsystemErrorIsReported(t *testing.T) {
	rep := run(t, "agent:\n  max_turns: 3\n", func(r *Request) {
		r.SubsystemErr = errors.New("httpserver.enable is true but this binary has no httpserver support")
	})
	if c := find(t, rep, "serve"); c.Status != StatusError || !strings.Contains(c.Message, "no httpserver support") {
		t.Errorf("subsystem %+v", c)
	}
}

func TestReportWriteFormat(t *testing.T) {
	rep := &Report{File: "/x/config.yaml", Checks: []Check{
		{Status: StatusOK, Path: "providers[a]", Message: "lists 2 models"},
		{Status: StatusError, Path: "providers[b]", Message: "credential rejected", Fix: "check api_key", Line: 7, Column: 5},
		{Status: StatusWarning, Path: "skills.dirs[0]", Message: "/y does not exist"},
		{Status: StatusSkipped, Path: "models[b/x]", Message: "provider b failed"},
	}}
	var buf bytes.Buffer
	rep.Write(&buf)
	txt := buf.String()
	for _, want := range []string{
		"ok       providers[a]: lists 2 models\n",
		"error    providers[b]: credential rejected\n         at /x/config.yaml:7:5\n         fix: check api_key\n",
		"warning  skills.dirs[0]: /y does not exist\n",
		"skipped  models[b/x]: provider b failed\n",
		"dry run: 1 error, 1 warning, 1 ok\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("report lacks %q:\n%s", want, txt)
		}
	}
	if rep.Errors() != 1 || rep.Warnings() != 1 {
		t.Errorf("counts %d/%d", rep.Errors(), rep.Warnings())
	}
}

func TestPrepareStopsAtStaticErrors(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("httpserver:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prep, rep, err := Prepare(config.CLIPaths{Home: home, CWD: home, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	if prep != nil || rep == nil || rep.Valid() {
		t.Fatalf("a file with static errors must not be probed: prep=%v report=%+v", prep, rep)
	}
}
