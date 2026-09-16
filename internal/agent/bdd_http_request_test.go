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

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
}

func (s *httpRequestFeatureState) reset() {
	s.close()
	s.seen = nil
	s.answer = ""
	s.callN = 0
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
		s.mu.Unlock()
		w.Header().Set("X-Service", "echo")
		switch {
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
	sc.Step(`^the model calls http_request with:$`, s.modelCalls)
	sc.Step(`^the service received "([^"]*)"$`, s.serviceReceived)
	sc.Step(`^the service received the header "(.*)"$`, s.serviceReceivedHeader)
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
