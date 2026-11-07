//go:build http

package httpserver

// Godog harness for features/direct_completion_without_session.feature: the
// real handlers and a real file store, with a provider that records what it was
// sent, so "nothing left" is read off the disk and the listing.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const directNoSessionAnswer = "direct-answer"

// directRecordingProvider answers every call with directNoSessionAnswer and
// keeps the messages of each one. A provider is shared by the calls of a
// server, so what it records is guarded.
type directRecordingProvider struct {
	mu    sync.Mutex
	calls [][]llm.Message
}

func (p *directRecordingProvider) record(msgs []llm.Message) *llm.Response {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, append([]llm.Message(nil), msgs...))
	return &llm.Response{Content: directNoSessionAnswer, StopReason: "end_turn", InputTokens: 3, OutputTokens: 1, UsageKnown: true}
}

func (p *directRecordingProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	return p.record(msgs), nil
}

func (p *directRecordingProvider) Stream(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	resp := p.record(msgs)
	onChunk(llm.StreamChunk{TextDelta: resp.Content})
	return resp, nil
}

func (p *directRecordingProvider) lastCall() []llm.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return nil
	}
	return p.calls[len(p.calls)-1]
}

type directNoSessionState struct {
	t        *testing.T
	provider *directRecordingProvider
	srv      *Server
	sessRoot string
	ts       *httptest.Server
	status   int
	sid      string
	body     string
}

func (s *directNoSessionState) startServer() error {
	s.provider = &directRecordingProvider{}
	_, srv, sessRoot := testHTTPServerPersist(s.t)
	srv.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) { return s.provider, nil }
	s.srv, s.sessRoot = srv, sessRoot
	s.ts = httptest.NewServer(srv.Handler())
	s.t.Cleanup(s.ts.Close)
	return nil
}

func (s *directNoSessionState) post(path, body, sid string) error {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if sid != "" {
		req.Header.Set("X-Coddy-Session-ID", sid)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	s.status, s.sid, s.body = res.StatusCode, res.Header.Get("X-Coddy-Session-ID"), string(raw)
	if s.status != http.StatusOK {
		return fmt.Errorf("POST %s: %d %s", path, s.status, s.body)
	}
	return nil
}

func directNoSessionBody(endpoint, stream string) string {
	if endpoint == "/v1/responses" {
		return fmt.Sprintf(`{"model":"openai/gpt-4o","input":"the question","stream":%s}`, stream)
	}
	return fmt.Sprintf(`{"model":"openai/gpt-4o","messages":[{"role":"user","content":"the question"}],"stream":%s}`, stream)
}

func (s *directNoSessionState) postWithoutHeader(endpoint, stream string) error {
	return s.post(endpoint, directNoSessionBody(endpoint, stream), "")
}

func (s *directNoSessionState) postWithSession(endpoint, sid string) error {
	return s.post(endpoint, directNoSessionBody(endpoint, "false"), sid)
}

func (s *directNoSessionState) postToolLoop() error {
	if err := s.post("/v1/chat/completions", directNoSessionBody("/v1/chat/completions", "false"), ""); err != nil {
		return err
	}
	return s.post("/v1/chat/completions", `{"model":"openai/gpt-4o","messages":[
		{"role":"user","content":"the question"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"18 degrees"}]}`, "")
}

func (s *directNoSessionState) postProfile() error {
	return s.post("/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"the question"}]}`, "")
}

func (s *directNoSessionState) gotAnswer() error {
	if !strings.Contains(s.body, directNoSessionAnswer) {
		return fmt.Errorf("answer %q does not carry the model's text", s.body)
	}
	return nil
}

func (s *directNoSessionState) namesNoSession() error {
	if s.sid != "" {
		return fmt.Errorf("the answer names session %q", s.sid)
	}
	return nil
}

func (s *directNoSessionState) storeEmpty() error {
	entries, err := os.ReadDir(s.sessRoot)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("the store holds %d entries, first %q", len(entries), entries[0].Name())
	}
	return nil
}

func (s *directNoSessionState) listingEmpty() error {
	// Print runs and empty sessions are asked for too, so a bundle hidden from
	// the default listing cannot pass as gone.
	res, err := http.Get(s.ts.URL + "/coddy/sessions?include_print=true&limit=50")
	if err != nil {
		return err
	}
	raw, err := ioReadAllClose(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("listing: %d %s", res.StatusCode, raw)
	}
	if n := len(gjson.GetBytes(raw, "sessions").Array()); n != 0 {
		return fmt.Errorf("the listing shows %d sessions: %s", n, raw)
	}
	return nil
}

func (s *directNoSessionState) secondRequestCarriedResult() error {
	last := s.provider.lastCall()
	if len(last) != 3 || len(last[1].ToolCalls) != 1 || last[2].Role != llm.RoleTool || last[2].Content != "18 degrees" {
		encoded, _ := json.Marshal(last)
		return fmt.Errorf("second request carried %s", encoded)
	}
	return nil
}

func (s *directNoSessionState) sessionStored(sid string) error {
	snap, err := (&session.FileStore{Root: s.sessRoot}).ReadSnapshot(sid)
	if err != nil {
		return fmt.Errorf("session %s: %w", sid, err)
	}
	if len(snap.Messages) != 2 || snap.Messages[0].Content != "the question" || snap.Messages[1].Content != directNoSessionAnswer {
		encoded, _ := json.Marshal(snap.Messages)
		return fmt.Errorf("session %s holds %s", sid, encoded)
	}
	return nil
}

func (s *directNoSessionState) namesStoredSession() error {
	if s.sid == "" {
		return fmt.Errorf("a profile turn named no session")
	}
	if _, err := os.Stat(filepath.Join(s.sessRoot, s.sid)); err != nil {
		return fmt.Errorf("session %s: %w", s.sid, err)
	}
	return nil
}

func TestDirectCompletionWithoutSession(t *testing.T) {
	suite := godog.TestSuite{
		Name: "direct-completion-without-session",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &directNoSessionState{t: t}
			sc.Given(`^a coddy server with a direct model$`, s.startServer)
			sc.When(`^a client posts the direct model to "([^"]*)" with stream (true|false) and no session header$`, s.postWithoutHeader)
			sc.When(`^a client posts the direct model to "([^"]*)" with session "([^"]*)"$`, s.postWithSession)
			sc.When(`^a client posts a tool call step and then its tool result with no session header$`, s.postToolLoop)
			sc.When(`^a client posts the agent profile with no session header$`, s.postProfile)
			sc.Then(`^the client gets the model's answer$`, s.gotAnswer)
			sc.Then(`^the answer names no session$`, s.namesNoSession)
			sc.Then(`^the session store is empty$`, s.storeEmpty)
			sc.Then(`^the session listing is empty$`, s.listingEmpty)
			sc.Then(`^the model received the call and its result in the second request$`, s.secondRequestCarriedResult)
			sc.Then(`^session "([^"]*)" is stored with the question and the answer$`, s.sessionStored)
			sc.Then(`^the answer names a stored session$`, s.namesStoredSession)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/direct_completion_without_session.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("direct completion without session feature suite failed")
	}
}
