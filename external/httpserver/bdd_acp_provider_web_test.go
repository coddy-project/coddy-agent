//go:build http

package httpserver

// Godog harness for features/acp_provider_web.feature: the HTTP API of coddy
// serve with the real session manager, agent loop and providers, acp rows
// running the scripted ACP agent of internal/llm/acptest (this test binary
// started in its place) and a scripted OpenAI-compatible model (llmstub) for
// the chat that starts a subagent.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// acpChat is one streamed chat turn as the web UI reads it.
type acpChat struct {
	sid       string
	question  string
	text      string
	reasoning string
	body      string
	// asked is the toolCall title of every permission event.
	asked []string
}

type acpWebState struct {
	t       *testing.T
	root    string
	cwd     string
	rows    []config.ProviderConfig
	models  []config.ModelEntry
	logs    map[string]string
	rules   []llmstub.Rule
	stub    *httptest.Server
	srv     *Server
	ts      *httptest.Server
	chat    *acpChat
	chats   []*acpChat
	prevEnv *string
}

func (s *acpWebState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-acp-web-*")
	if err != nil {
		return err
	}
	*s = acpWebState{t: s.t, root: root, cwd: filepath.Join(root, "work"), logs: map[string]string{}}
	if prev, had := os.LookupEnv(llm.ACPDepthEnv); had {
		s.prevEnv = &prev
	}
	_ = os.Unsetenv(llm.ACPDepthEnv)
	return os.MkdirAll(s.cwd, 0o755)
}

func (s *acpWebState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.stub != nil {
		s.stub.Close()
		s.stub = nil
	}
	llm.StopACPAgents()
	if s.prevEnv != nil {
		_ = os.Setenv(llm.ACPDepthEnv, *s.prevEnv)
		s.prevEnv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// row adds an acp row named name running script.
func (s *acpWebState) row(name string, script acptest.Script, models ...string) {
	script.Log = filepath.Join(s.root, name+".log")
	s.logs[name] = script.Log
	cmd, args := acptest.Command(s.t, script)
	s.rows = append(s.rows, config.ProviderConfig{Name: name, Type: "acp", ACP: &config.ACPProviderConfig{
		Command: cmd, Args: args, Modes: config.ACPModes{Plan: "read", Ask: "read"},
	}})
	for _, m := range models {
		s.models = append(s.models, config.ModelEntry{Model: m, MaxContextTokens: 128000})
	}
}

var webEchoTurn = acptest.Turn{Thoughts: []string{"looking"}, Tools: []acptest.Tool{{ID: "t1", Title: "Read README.md"}}, Echo: true}

func (s *acpWebState) twoRows() error {
	s.row("alpha", acptest.Script{Models: []string{"small", "large"}, Modes: []string{"default", "read"}, Close: true, Turns: []acptest.Turn{webEchoTurn}}, "alpha/default", "alpha/large")
	s.row("beta", acptest.Script{Modes: []string{"default", "read"}, Close: true, Turns: []acptest.Turn{webEchoTurn}}, "beta/default")
	return nil
}

func (s *acpWebState) twoRowsWithBarrier(n int) error {
	turn := acptest.Turn{Echo: true, Barrier: n}
	s.row("alpha", acptest.Script{Modes: []string{"default", "read"}, Close: true, Turns: []acptest.Turn{turn}}, "alpha/default")
	s.row("beta", acptest.Script{Modes: []string{"default", "read"}, Close: true, Turns: []acptest.Turn{turn}}, "beta/default")
	return nil
}

func (s *acpWebState) rowThatAsks(name, title string) error {
	s.row(name, acptest.Script{Modes: []string{"default", "read"}, Close: true, Turns: []acptest.Turn{{
		Permission: &acptest.Permission{Title: title, Kind: "edit", Content: "the change", Options: []acptest.Option{
			{ID: "ok", Name: "Allow", Kind: "allow_once"}, {ID: "no", Name: "Reject", Kind: "reject_once"},
		}},
		Echo: true,
	}}}, name+"/default")
	return nil
}

func (s *acpWebState) handsToSubagent(question, model string) error {
	args, _ := json.Marshal(map[string]string{"agent": "general", "prompt": "child task", "description": "child on the agent", "model": model})
	s.rules = append(s.rules, llmstub.Rule{Match: question, Tool: &llmstub.ToolCall{Name: "spawn_agent", Arguments: args}, Answer: "The subagent reported back."})
	return nil
}

// server starts the HTTP surface on the rows, the scripted chat model first.
func (s *acpWebState) server() error {
	if s.ts != nil {
		return nil
	}
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: s.rules}
	s.stub = httptest.NewServer(stub.Handler())
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: append([]config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: s.stub.URL + "/v1", APIKey: "sk-stub"}}, s.rows...),
		Models:    append([]config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}}, s.models...),
		Agent:     config.Agent{Model: s.models[0].Model},
	}
	cfg.Tools.PermissionMode = config.PermModeAsk
	log := slog.New(slog.DiscardHandler)
	var mgr *session.Manager
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(cfg, st, snd, log)
		loop.SetSubagentRuntime(mgr)
		return loop.Run(ctx, prompt)
	}
	mgr = session.NewManager(cfg, noopSender{}, runner, log, s.cwd, &session.FileStore{Root: filepath.Join(s.root, "sessions")})
	s.srv = New(cfg, mgr, log, s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

// ask streams one chat turn on model, calling onPermission with each
// permission event's payload while the stream is open.
func (s *acpWebState) ask(model, question string, onPermission func(sid string, payload string) error) (*acpChat, error) {
	if err := s.server(); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"model": "agent", "stream": true, "input": question, "metadata": map[string]any{"model": model}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("POST /v1/responses: %d %s", res.StatusCode, raw)
	}
	chat := &acpChat{sid: res.Header.Get("X-Coddy-Session-ID"), question: question}
	var all strings.Builder
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		all.WriteString(line + "\n")
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
			continue
		}
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if line == "" {
				event = ""
			}
			continue
		}
		if payload == "[DONE]" {
			break
		}
		switch event {
		case "permission":
			chat.asked = append(chat.asked, gjson.Get(payload, "toolCall.title").String())
			if onPermission != nil {
				if err := onPermission(chat.sid, payload); err != nil {
					return nil, err
				}
			}
		case "":
			chat.text += gjson.Get(payload, "choices.0.delta.content").String()
			chat.reasoning += gjson.Get(payload, "choices.0.delta.reasoning_content").String()
		}
	}
	chat.body = all.String()
	if !strings.Contains(chat.body, "data: [DONE]") {
		return nil, fmt.Errorf("the stream did not end: %s", chat.body)
	}
	return chat, nil
}

func (s *acpWebState) chatAsks(model, question string) error {
	chat, err := s.ask(model, question, nil)
	s.chat = chat
	return err
}

func (s *acpWebState) chatAsksAndAllows(model, question string) error {
	chat, err := s.ask(model, question, func(sid, payload string) error {
		body, _ := json.Marshal(map[string]string{"toolCallId": gjson.Get(payload, "toolCall.toolCallId").String(), "optionId": "allow"})
		req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/sessions/"+sid+"/permission", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		if res.StatusCode/100 != 2 {
			return fmt.Errorf("POST permission: %d", res.StatusCode)
		}
		return nil
	})
	s.chat = chat
	return err
}

func (s *acpWebState) chatsAtOnce(n int, modelA string, m int, modelB string) error {
	if err := s.server(); err != nil {
		return err
	}
	type result struct {
		chat *acpChat
		err  error
	}
	var questions [][2]string
	for _, spec := range []struct {
		model string
		n     int
	}{{modelA, n}, {modelB, m}} {
		for i := 1; i <= spec.n; i++ {
			questions = append(questions, [2]string{spec.model, fmt.Sprintf("question %d for %s", i, spec.model)})
		}
	}
	results := make(chan result, len(questions))
	for _, q := range questions {
		go func(model, question string) {
			chat, err := s.ask(model, question, nil)
			results <- result{chat, err}
		}(q[0], q[1])
	}
	for range questions {
		r := <-results
		if r.err != nil {
			return r.err
		}
		s.chats = append(s.chats, r.chat)
	}
	return nil
}

func (s *acpWebState) modelListOffers(list string) error {
	if err := s.server(); err != nil {
		return err
	}
	res, err := http.Get(s.ts.URL + "/v1/models")
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	for _, want := range strings.Split(list, ",") {
		want = strings.TrimSpace(want)
		if !gjson.GetBytes(raw, `data.#(id=="`+want+`")`).Exists() {
			return fmt.Errorf("GET /v1/models lacks %q: %s", want, raw)
		}
	}
	return nil
}

func (s *acpWebState) modelsRouteLists(list, row string) error {
	res, err := http.Post(s.ts.URL+"/coddy/providers/models", "application/json", strings.NewReader(`{"name":"`+row+`"}`))
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var ids []string
	for _, m := range gjson.GetBytes(raw, "models.#.id").Array() {
		ids = append(ids, m.String())
	}
	var want []string
	for _, v := range strings.Split(list, ",") {
		want = append(want, strings.TrimSpace(v))
	}
	if strings.Join(ids, ", ") != strings.Join(want, ", ") {
		return fmt.Errorf("models of %s = %v, want %v (%s)", row, ids, want, raw)
	}
	return nil
}

func (s *acpWebState) streamCarries(want string) error {
	if !strings.HasPrefix(s.chat.text, want) {
		return fmt.Errorf("answer = %q, want it to open with %q", s.chat.text, want)
	}
	return nil
}

func (s *acpWebState) streamReasoningNames(title string) error {
	if !strings.Contains(s.chat.reasoning, "] "+title) {
		return fmt.Errorf("reasoning = %q", s.chat.reasoning)
	}
	return nil
}

func (s *acpWebState) storedReasoningNames(title string) error {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/sessions/"+s.chat.sid+"/messages", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.chat.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	last := gjson.GetBytes(raw, "messages.#(role==\"assistant\")#|@reverse|0")
	if !strings.Contains(last.Get("reasoning").String(), "] "+title) {
		return fmt.Errorf("stored answer = %s", last.Raw)
	}
	return nil
}

func (s *acpWebState) streamAsked(title string) error {
	if len(s.chat.asked) != 1 || s.chat.asked[0] != title {
		return fmt.Errorf("permission events = %q, want one about %q", s.chat.asked, title)
	}
	return nil
}

func (s *acpWebState) answerHeardAllow() error {
	if !strings.Contains(s.chat.text, "permission: ok") {
		return fmt.Errorf("answer = %q", s.chat.text)
	}
	return nil
}

func (s *acpWebState) everyChatOwnAnswer() error {
	if len(s.chats) == 0 {
		return fmt.Errorf("no chats")
	}
	for _, c := range s.chats {
		if !strings.HasPrefix(c.text, "echo: "+c.question) {
			return fmt.Errorf("chat asked %q answered %q", c.question, c.text)
		}
		if !strings.Contains(c.text, "barrier: ") || strings.Contains(c.text, "barrier: timeout") {
			return fmt.Errorf("chat asked %q did not meet the others: %q", c.question, c.text)
		}
	}
	return nil
}

func (s *acpWebState) subagentReportHolds(want string) error {
	if !strings.Contains(s.chat.body, want) {
		return fmt.Errorf("the stream holds no report with %q: %s", want, s.chat.body)
	}
	if !strings.Contains(s.chat.text, "The subagent reported back.") {
		return fmt.Errorf("the chat's own answer = %q", s.chat.text)
	}
	return nil
}

func TestACPProviderWebFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "acp_provider_web",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &acpWebState{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, s.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a server whose acp rows "alpha" and "beta" run scripted agents$`, s.twoRows)
			sc.Step(`^a server whose acp rows "alpha" and "beta" run agents that answer only (\d+) prompts at once$`, s.twoRowsWithBarrier)
			sc.Step(`^a server whose acp row "([^"]*)" runs an agent that asks to "([^"]*)"$`, s.rowThatAsks)
			sc.Step(`^the chat's own model hands "([^"]*)" to a subagent on "([^"]*)"$`, s.handsToSubagent)
			sc.Step(`^a chat on "([^"]*)" asks "([^"]*)"$`, s.chatAsks)
			sc.Step(`^a chat on "([^"]*)" asks "([^"]*)" and the person allows the request$`, s.chatAsksAndAllows)
			sc.Step(`^(\d+) chats on "([^"]*)" and (\d+) on "([^"]*)" ask at once$`, s.chatsAtOnce)
			sc.Step(`^the model list offers "([^"]*)"$`, s.modelListOffers)
			sc.Step(`^the models route lists "([^"]*)" for the row "([^"]*)"$`, s.modelsRouteLists)
			sc.Step(`^the stream carries the answer "([^"]*)"$`, s.streamCarries)
			sc.Step(`^the stream's reasoning names the agent's tool "([^"]*)"$`, s.streamReasoningNames)
			sc.Step(`^the stored answer keeps the agent's tool "([^"]*)" in its reasoning$`, s.storedReasoningNames)
			sc.Step(`^the stream asked about "([^"]*)"$`, s.streamAsked)
			sc.Step(`^the chat's answer says the agent heard its allow option$`, s.answerHeardAllow)
			sc.Step(`^every chat streams the answer to its own question$`, s.everyChatOwnAnswer)
			sc.Step(`^the subagent's report holds "([^"]*)"$`, s.subagentReportHolds)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/acp_provider_web.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("acp_provider_web feature failed")
	}
}
