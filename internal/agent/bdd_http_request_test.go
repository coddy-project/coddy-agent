package agent

// Godog harness for features/http_request_tool.feature: the real Agent.Run
// with a fake provider that makes one http_request call per step, against a
// local httptest service that records what reached it. The permission sender
// records every prompt and answers with the option the scenario chose, so the
// scenarios assert the whole path - the gate, the grant the answer leaves in
// the session, the request on the wire and the answer the model reads.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// bddHTTPLogo is what the service answers for /logo.png: binary, eight bytes.
var bddHTTPLogo = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// bddHTTPSeen is one request as the service received it.
type bddHTTPSeen struct {
	method, uri string
	header      http.Header
	body        []byte
	fields      map[string]string
	files       map[string]bddHTTPFile
}

type bddHTTPFile struct {
	filename string
	content  string
}

// bddHTTPProvider requests one http_request call on its first turn and answers
// on the next.
type bddHTTPProvider struct {
	call  llm.ToolCall
	calls int
}

func (p *bddHTTPProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the http_request suite")
}

func (p *bddHTTPProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		tc := p.call
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: "done"})
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

// bddHTTPPermissionSender records every prompt and answers with one option.
type bddHTTPPermissionSender struct {
	answer   string
	requests []acp.PermissionRequestParams
}

func (s *bddHTTPPermissionSender) SendSessionUpdate(string, interface{}) error { return nil }

func (s *bddHTTPPermissionSender) RequestPermission(_ context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.requests = append(s.requests, p)
	return &acp.PermissionResult{Outcome: "selected", OptionID: s.answer}, nil
}

func (s *bddHTTPPermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

type httpRequestFeatureState struct {
	server    *httptest.Server
	workspace string
	cfg       *config.Config
	st        *session.State
	sender    *bddHTTPPermissionSender

	mu     sync.Mutex
	seen   []bddHTTPSeen
	answer string
	callN  int
	// browserOnly lists the paths the service serves only to a browser
	// User-Agent, the way some hosting fronts turn tools away.
	browserOnly map[string]bool
}

func (s *httpRequestFeatureState) reset() {
	s.close()
	s.seen = nil
	s.answer = ""
	s.callN = 0
	s.browserOnly = map[string]bool{}
}

func (s *httpRequestFeatureState) close() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	if s.workspace != "" {
		_ = os.RemoveAll(s.workspace)
		s.workspace = ""
	}
}

func (s *httpRequestFeatureState) expand(text string) string {
	if s.server != nil {
		text = strings.ReplaceAll(text, "{service}", s.server.URL)
	}
	return text
}

func (s *httpRequestFeatureState) record(r *http.Request) (bddHTTPSeen, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return bddHTTPSeen{}, err
	}
	seen := bddHTTPSeen{method: r.Method, uri: r.RequestURI, header: r.Header.Clone(), body: body}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		seen.fields = map[string]string{}
		seen.files = map[string]bddHTTPFile{}
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return bddHTTPSeen{}, err
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return bddHTTPSeen{}, err
			}
			if part.FileName() != "" {
				seen.files[part.FormName()] = bddHTTPFile{filename: part.FileName(), content: string(data)}
			} else {
				seen.fields[part.FormName()] = string(data)
			}
		}
	}
	return seen, nil
}

func (s *httpRequestFeatureState) localService() error {
	ws, err := os.MkdirTemp("", "coddy-bdd-http-*")
	if err != nil {
		return err
	}
	s.workspace = ws
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, err := s.record(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.seen = append(s.seen, seen)
		browserOnly := s.browserOnly[r.URL.Path]
		s.mu.Unlock()
		w.Header().Set("X-Service", "echo")
		switch {
		case browserOnly && !strings.HasPrefix(r.UserAgent(), "Mozilla/"):
			// What such a front answers a client it does not take for a browser:
			// its own HTML page under a status that says nothing useful.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = io.WriteString(w, "<html><body>Hosted site</body></html>")
		case browserOnly:
			w.Header().Set("Content-Type", "application/manifest+json")
			_, _ = io.WriteString(w, `{"name":"Match 3","start_url":"/"}`)
		case r.URL.Path == "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bddHTTPLogo)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/items/"):
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintf(w, "patched item %s", strings.TrimPrefix(r.URL.Path, "/items/"))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "ok")
		}
	}))
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	s.st = &session.State{ID: "sess_bdd_http", CWD: ws, Mode: session.ModeAgent}
	s.sender = &bddHTTPPermissionSender{answer: "allow"}
	return nil
}

func (s *httpRequestFeatureState) permissionMode(mode string) error {
	s.cfg.Tools.PermissionMode = mode
	return nil
}

func (s *httpRequestFeatureState) operatorAnswers(option string) error {
	s.sender.answer = option
	return nil
}

func (s *httpRequestFeatureState) workspaceFile(name, content string) error {
	path := filepath.Join(s.workspace, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *httpRequestFeatureState) operatorAllowlistedService() error {
	s.cfg.Tools.HTTPRequest.Allowlist = []string{s.server.URL}
	return nil
}

func (s *httpRequestFeatureState) serviceServesOnlyToABrowser(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browserOnly[path] = true
	return nil
}

// operatorConfiguresDefaultHeaders reads the map the way config.yaml holds it
// and runs it through the loader's own check of the tools section.
func (s *httpRequestFeatureState) operatorConfiguresDefaultHeaders(doc *godog.DocString) error {
	var headers map[string]string
	if err := yaml.Unmarshal([]byte(doc.Content), &headers); err != nil {
		return err
	}
	s.cfg.Tools.HTTPRequest.DefaultHeaders = headers
	return s.cfg.Tools.Validate()
}

func (s *httpRequestFeatureState) modelCalls(doc *godog.DocString) error {
	s.callN++
	call := llm.ToolCall{
		ID:        fmt.Sprintf("call_http_%d", s.callN),
		Name:      "http_request",
		InputJSON: s.expand(strings.TrimSpace(doc.Content)),
	}
	ag := NewAgent(s.cfg, s.st, s.sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return &bddHTTPProvider{call: call}, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "send it"}}); err != nil {
		return err
	}
	return nil
}

// lastAnswer is what the tool returned to the model for the latest call.
func (s *httpRequestFeatureState) lastAnswer() (string, error) {
	id := fmt.Sprintf("call_http_%d", s.callN)
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == id {
			return m.Content, nil
		}
	}
	return "", fmt.Errorf("no answer for tool call %s in the transcript", id)
}

func (s *httpRequestFeatureState) lastSeen() (bddHTTPSeen, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		return bddHTTPSeen{}, fmt.Errorf("the service received no request")
	}
	return s.seen[len(s.seen)-1], nil
}

func (s *httpRequestFeatureState) serviceReceived(want string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var got []string
	for _, r := range s.seen {
		line := r.method + " " + r.uri
		if line == want {
			return nil
		}
		got = append(got, line)
	}
	return fmt.Errorf("the service did not receive %q; it received %q", want, got)
}

func (s *httpRequestFeatureState) serviceReceivedHeader(line string) error {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return fmt.Errorf("header %q has no colon", line)
	}
	seen, err := s.lastSeen()
	if err != nil {
		return err
	}
	if got := seen.header.Get(strings.TrimSpace(name)); got != strings.TrimSpace(value) {
		return fmt.Errorf("header %s arrived as %q, want %q", name, got, strings.TrimSpace(value))
	}
	return nil
}

func (s *httpRequestFeatureState) serviceReceivedNoHeader(name string) error {
	seen, err := s.lastSeen()
	if err != nil {
		return err
	}
	if values, ok := seen.header[http.CanonicalHeaderKey(name)]; ok {
		return fmt.Errorf("header %s arrived as %q", name, values)
	}
	return nil
}

func (s *httpRequestFeatureState) serviceReceivedBody(want string) error {
	seen, err := s.lastSeen()
	if err != nil {
		return err
	}
	if string(seen.body) != want {
		return fmt.Errorf("body arrived as %q, want %q", seen.body, want)
	}
	return nil
}

func (s *httpRequestFeatureState) serviceReceivedField(name, want string) error {
	seen, err := s.lastSeen()
	if err != nil {
		return err
	}
	if got, ok := seen.fields[name]; !ok || got != want {
		return fmt.Errorf("form field %q arrived as %q (present %v), want %q", name, got, ok, want)
	}
	return nil
}

func (s *httpRequestFeatureState) serviceReceivedFile(field, filename, content string) error {
	seen, err := s.lastSeen()
	if err != nil {
		return err
	}
	got, ok := seen.files[field]
	if !ok {
		return fmt.Errorf("no file part %q arrived; fields %v", field, seen.fields)
	}
	if got.filename != filename || got.content != content {
		return fmt.Errorf("file part %q arrived as %q with %q, want %q with %q", field, got.filename, got.content, filename, content)
	}
	return nil
}

func (s *httpRequestFeatureState) toolAnsweredStatus(want string) error {
	answer, err := s.lastAnswer()
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(answer, "\n")
	if strings.TrimSpace(first) != want {
		return fmt.Errorf("the answer starts with %q, want %q\n%s", first, want, answer)
	}
	return nil
}

func (s *httpRequestFeatureState) toolAnswerContains(want string) error {
	answer, err := s.lastAnswer()
	if err != nil {
		return err
	}
	if !strings.Contains(answer, s.expand(want)) {
		return fmt.Errorf("the answer does not contain %q:\n%s", want, answer)
	}
	return nil
}

func (s *httpRequestFeatureState) workspaceFileHoldsServiceBytes(name string) error {
	data, err := os.ReadFile(filepath.Join(s.workspace, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, bddHTTPLogo) {
		return fmt.Errorf("%s holds %v, want %v", name, data, bddHTTPLogo)
	}
	return nil
}

func (s *httpRequestFeatureState) operatorAskedTimes(n int) error {
	if got := len(s.sender.requests); got != n {
		return fmt.Errorf("the operator was asked %d times, want %d", got, n)
	}
	return nil
}

func (s *httpRequestFeatureState) lastPrompt() (acp.PermissionRequestParams, error) {
	if len(s.sender.requests) == 0 {
		return acp.PermissionRequestParams{}, fmt.Errorf("the operator was never asked")
	}
	return s.sender.requests[len(s.sender.requests)-1], nil
}

func (s *httpRequestFeatureState) promptShows(want string) error {
	p, err := s.lastPrompt()
	if err != nil {
		return err
	}
	want = s.expand(want)
	var text strings.Builder
	for _, item := range p.ToolCall.Content {
		text.WriteString(item.Content.Text)
		text.WriteString("\n")
	}
	if !strings.Contains(text.String(), want) {
		return fmt.Errorf("the prompt does not show %q:\n%s", want, text.String())
	}
	return nil
}

func (s *httpRequestFeatureState) dialogOffers(want string) error {
	p, err := s.lastPrompt()
	if err != nil {
		return err
	}
	want = s.expand(want)
	var names []string
	for _, o := range p.Options {
		if o.Name == want {
			return nil
		}
		names = append(names, o.Name)
	}
	return fmt.Errorf("the dialog does not offer %q; it offers %q", want, names)
}

func initializeHTTPRequestScenario(sc *godog.ScenarioContext) {
	s := &httpRequestFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a local HTTP service the agent can reach$`, s.localService)
	sc.Step(`^the permission mode is "([^"]*)"$`, s.permissionMode)
	sc.Step(`^the operator answers permission prompts with "([^"]*)"$`, s.operatorAnswers)
	sc.Step(`^a workspace file "([^"]*)" containing "([^"]*)"$`, s.workspaceFile)
	sc.Step(`^the operator allowlisted the service in tools\.http_request\.allowlist$`, s.operatorAllowlistedService)
	sc.Step(`^the service serves "([^"]*)" only to a browser$`, s.serviceServesOnlyToABrowser)
	sc.Step(`^the operator configures tools\.http_request\.default_headers:$`, s.operatorConfiguresDefaultHeaders)
	sc.Step(`^the model calls http_request with:$`, s.modelCalls)
	sc.Step(`^the service received "([^"]*)"$`, s.serviceReceived)
	sc.Step(`^the service received the header "(.*)"$`, s.serviceReceivedHeader)
	sc.Step(`^the service received no header "([^"]*)"$`, s.serviceReceivedNoHeader)
	sc.Step(`^the service received the body "(.*)"$`, s.serviceReceivedBody)
	sc.Step(`^the service received the form field "([^"]*)" with "([^"]*)"$`, s.serviceReceivedField)
	sc.Step(`^the service received the file "([^"]*)" named "([^"]*)" with "([^"]*)"$`, s.serviceReceivedFile)
	sc.Step(`^the tool answered with the status line "([^"]*)"$`, s.toolAnsweredStatus)
	sc.Step(`^the tool answer contains "(.*)"$`, s.toolAnswerContains)
	sc.Step(`^the workspace file "([^"]*)" holds the bytes the service sent$`, s.workspaceFileHoldsServiceBytes)
	sc.Step(`^the operator was asked (\d+) times?$`, s.operatorAskedTimes)
	sc.Step(`^the permission prompt shows "(.*)"$`, s.promptShows)
	sc.Step(`^the permission dialog offers "(.*)"$`, s.dialogOffers)
}

func TestHTTPRequestToolFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "http-request-tool",
		ScenarioInitializer: initializeHTTPRequestScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/http_request_tool.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("http_request tool feature suite failed")
	}
}

// bddBrowserUA is the User-Agent the default-header tests configure.
const bddBrowserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// TestHTTPRequestDefaultHeadersStayOffTheModelProvider runs one turn against a
// model served over HTTP, the way a configured provider is reached, with
// default headers configured: the request the model asked for carries them,
// and not one request to the model does.
func TestHTTPRequestDefaultHeadersStayOffTheModelProvider(t *testing.T) {
	var serviceMu sync.Mutex
	var serviceHeaders []http.Header
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceMu.Lock()
		serviceHeaders = append(serviceHeaders, r.Header.Clone())
		serviceMu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(service.Close)

	model := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{{
		Tool:   &llmstub.ToolCall{Name: "http_request", Arguments: json.RawMessage(`{"url":"` + service.URL + `/items"}`)},
		Answer: "done",
	}}}
	var providerMu sync.Mutex
	var providerHeaders []http.Header
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerMu.Lock()
		providerHeaders = append(providerHeaders, r.Header.Clone())
		providerMu.Unlock()
		model.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(provider.Close)

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: provider.URL + "/v1", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxTokens: 100}},
		Agent:     config.Agent{Model: "stub/coddy-demo", MaxTurns: 4},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": bddBrowserUA, "X-Client": "coddy-lab"}
	st := &session.State{ID: "sess_http_defaults", CWD: t.TempDir(), Mode: session.ModeAgent}
	ag := NewAgent(cfg, st, &bddHTTPPermissionSender{answer: "allow"}, nil)
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "send it"}}); err != nil {
		t.Fatal(err)
	}

	serviceMu.Lock()
	defer serviceMu.Unlock()
	if len(serviceHeaders) != 1 {
		t.Fatalf("the service received %d requests, want the one the model asked for", len(serviceHeaders))
	}
	if got := serviceHeaders[0].Get("User-Agent"); got != bddBrowserUA {
		t.Errorf("http_request sent User-Agent %q, want the configured one", got)
	}
	if got := serviceHeaders[0].Get("X-Client"); got != "coddy-lab" {
		t.Errorf("http_request sent X-Client %q, want the configured one", got)
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	if len(providerHeaders) < 2 {
		t.Fatalf("the model was asked %d times, want the tool call and the answer after it", len(providerHeaders))
	}
	for i, h := range providerHeaders {
		if h.Get("User-Agent") == bddBrowserUA {
			t.Errorf("model request %d carried the configured User-Agent", i+1)
		}
		if v, ok := h["X-Client"]; ok {
			t.Errorf("model request %d carried X-Client %q", i+1, v)
		}
	}
}

// TestHTTPRequestEnvCopiesTheSection pins what httpRequestEnv takes from
// tools.http_request - the helper the start of a turn, the refresh after a
// config_commit and the environment of a resumed call all go through - and
// that it takes copies: a reload builds a new config rather than editing the
// one a call is reading. The resumed call's environment is checked through
// buildToolEnv itself.
func TestHTTPRequestEnvCopiesTheSection(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.HTTPRequest = config.ToolHTTPRequest{
		Allowlist:      []string{"api.github.com"},
		DefaultHeaders: map[string]string{"User-Agent": bddBrowserUA},
	}
	env := &tools.Env{}
	httpRequestEnv(env, cfg)
	if len(env.HTTPAllowlist) != 1 || env.HTTPAllowlist[0] != "api.github.com" {
		t.Errorf("allowlist = %v", env.HTTPAllowlist)
	}
	if env.HTTPDefaultHeaders["User-Agent"] != bddBrowserUA {
		t.Errorf("default headers = %v", env.HTTPDefaultHeaders)
	}
	cfg.Tools.HTTPRequest.Allowlist[0] = "changed"
	cfg.Tools.HTTPRequest.DefaultHeaders["User-Agent"] = "changed"
	if env.HTTPAllowlist[0] != "api.github.com" || env.HTTPDefaultHeaders["User-Agent"] != bddBrowserUA {
		t.Error("the environment shares the config's slices and maps instead of copying them")
	}

	st := &session.State{ID: "sess_http_resume", CWD: t.TempDir(), Mode: session.ModeAgent}
	resumed := NewAgent(cfg, st, &bddHTTPPermissionSender{answer: "allow"}, nil).buildToolEnv(string(session.ModeAgent), "")
	if resumed.HTTPDefaultHeaders["User-Agent"] != "changed" {
		t.Errorf("a resumed call's environment carries default headers %v", resumed.HTTPDefaultHeaders)
	}
}

// A prompt can wait across a restart, and the configuration can move while it
// waits. The answer was given for the request the prompt showed: when the
// headers the configuration adds have changed since, the resumed call asks
// again with the request it would send now; when nothing changed it just runs.
func TestResumedHTTPRequestAsksAgainWhenTheDefaultHeadersMoved(t *testing.T) {
	for _, c := range []struct {
		name          string
		moved, gone   bool
		wantPrompts   int
		wantUserAgent string
	}{
		{name: "unchanged", wantPrompts: 0, wantUserAgent: "shown/1"},
		{name: "moved", moved: true, wantPrompts: 1, wantUserAgent: "moved/2"},
		// The server resumes only a call whose prompt is on record; a record
		// gone by the time the call reads it was changed under the answer.
		{name: "record gone", gone: true, wantPrompts: 1, wantUserAgent: "shown/1"},
	} {
		moved := c.moved
		var mu sync.Mutex
		var agents []string
		service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			agents = append(agents, r.UserAgent())
			mu.Unlock()
			_, _ = io.WriteString(w, "ok")
		}))
		sd := t.TempDir()
		const callID = "call_http_resume"
		args := `{"url":"` + service.URL + `/items"}`
		cfg := &config.Config{
			Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
			Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
			Agent:     config.Agent{Model: "fake/model"},
		}
		cfg.Tools.PermissionMode = config.PermModeAsk
		cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": "shown/1"}
		st := &session.State{
			ID: "sess_resume_http", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: sd,
			Messages: []llm.Message{
				{Role: llm.RoleUser, Content: "fetch the items"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: callID, Name: "http_request", InputJSON: args}}},
			},
		}
		// The prompt as the turn showed it, persisted with the arguments it showed.
		shownEnv := &tools.Env{CWD: st.CWD}
		httpRequestEnv(shownEnv, cfg)
		shown := permission.HTTPRequestPromptBody(shownEnv, args)
		if err := session.WriteToolCallArgs(sd, callID, args); err != nil {
			t.Fatal(err)
		}
		if err := session.WritePendingPermission(sd, acp.PermissionRequestParams{
			SessionID: st.ID,
			ToolCall: acp.PermissionToolCall{
				ToolCallID: callID,
				Content:    []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: shown}}},
			},
		}, "http_request", args); err != nil {
			t.Fatal(err)
		}
		if moved {
			cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": "moved/2", "X-Client": "coddy-lab"}
		}
		if c.gone {
			if err := session.ClearPendingPermission(sd); err != nil {
				t.Fatal(err)
			}
		}

		sender := &bddHTTPPermissionSender{answer: "allow"}
		ag := NewAgent(cfg, st, sender, nil)
		ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return &resumePermissionProvider{t: t}, nil }
		if _, err := ag.ResumeAfterPermission(context.Background(), callID, &acp.PermissionResult{Outcome: "selected", OptionID: "allow"}); err != nil {
			t.Fatal(err)
		}
		service.Close()

		mu.Lock()
		sent := append([]string(nil), agents...)
		mu.Unlock()
		if len(sender.requests) != c.wantPrompts {
			t.Fatalf("%s: the resumed request was asked %d times, want %d", c.name, len(sender.requests), c.wantPrompts)
		}
		if len(sent) != 1 || sent[0] != c.wantUserAgent {
			t.Errorf("%s: the service received User-Agents %q, want %q", c.name, sent, c.wantUserAgent)
		}
		if !moved {
			continue
		}
		var text strings.Builder
		for _, item := range sender.requests[0].ToolCall.Content {
			text.WriteString(item.Content.Text)
		}
		if !strings.Contains(text.String(), "User-Agent: moved/2") || !strings.Contains(text.String(), "X-Client") {
			t.Errorf("the new prompt does not show the request as it goes out now:\n%s", text.String())
		}
	}
}
