//go:build http

package httpserver

// Godog harness for features/remote_model_provider.feature: a model a remote
// Coddy shares (models[].shared_as) serves another Coddy's harness through a
// provider of type coddy. The stand is in bdd_remote_model_provider_stand_test.go.
//
// "The local coddy streams" is one model call of the local harness: the
// provider the local agent builds for its session's row, sent the scenario's
// own messages and tools. "The local coddy runs a turn" is the whole agent loop
// over POST /v1/responses, its tool served by a local MCP server. A client of
// the remote is a plain HTTP client holding the shared-model token unless the
// step names another credential.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// rmpRedSquare is a PNG of one red pixel.
const rmpRedSquare = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// rmpTurnSession is the session of the turn the local Coddy runs over HTTP.
const rmpTurnSession = "sess_rmp_turn"

// rmpHeldStream is a stream the scripted provider holds open, known by the
// message it was asked for.
type rmpHeldStream struct {
	res     *http.Response
	message string
}

type rmpState struct {
	root   string
	logs   *rmpLogBuffer
	remote *rmpRemote
	local  *rmpLocal

	// What a client of the remote got last: an answer read whole, or the
	// headers of a stream that is still open.
	status int
	header http.Header
	body   []byte
	// held are the streams the scripted provider holds open, oldest first, and
	// served counts every stream the remote took.
	held   []rmpHeldStream
	served int

	// The last stream of the local Coddy.
	mu       sync.Mutex
	history  []llm.Message
	resp     *llm.Response
	chunks   []llm.StreamChunk
	declared []llm.ToolDefinition
	// staleAt is the exchange the remote answered stale_revision in.
	staleAt int
	// localRow is the local row a step wrote a key in.
	localRow string
	// usage is the local Coddy's reading of the usage of a model.
	usage map[string]any
}

func (s *rmpState) reset() error {
	root, err := os.MkdirTemp("", "coddy-bdd-rmp-*")
	if err != nil {
		return err
	}
	logs := &rmpLogBuffer{}
	*s = rmpState{root: root, logs: logs, remote: newRMPRemote(root, logs), staleAt: -1}
	return nil
}

func (s *rmpState) close() {
	for _, h := range s.held {
		_ = h.res.Body.Close()
	}
	s.held = nil
	if s.local != nil {
		s.local.close()
	}
	if s.remote != nil {
		s.remote.close()
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
}

func rmpContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), rmpWait)
}

// rmpEventually polls cond until it holds or the stand's wait runs out.
func rmpEventually(what string, cond func() (bool, string)) error {
	deadline := time.Now().Add(rmpWait)
	for {
		ok, last := cond()
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: still %s after %s", what, last, rmpWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func rmpSplitList(list string) []any {
	var out []any
	for _, v := range strings.Split(list, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func rmpJSONEqual(a, b any) (bool, string, string) {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	var na, nb any
	_ = json.Unmarshal(ra, &na)
	_ = json.Unmarshal(rb, &nb)
	ca, _ := json.Marshal(na)
	cb, _ := json.Marshal(nb)
	return bytes.Equal(ca, cb), string(ca), string(cb)
}

func rmpJSONText(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	return v
}

// ---- a client of the remote ----

// clientToken is what "a client" of the remote holds: the shared-model token
// when the remote has one, else nothing.
func (s *rmpState) clientToken() string {
	if s.remote.hasToken("shared") {
		return rmpSharedToken
	}
	return ""
}

func (s *rmpState) send(method, path, token string, body []byte) (*http.Response, error) {
	if err := s.remote.start(); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.remote.ts.URL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

// call sends a request to the remote and reads its whole answer.
func (s *rmpState) call(method, path, token string, body []byte) error {
	res, err := s.send(method, path, token, body)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status, s.header = res.StatusCode, res.Header
	s.body, err = io.ReadAll(res.Body)
	return err
}

func rmpWireBody(model, text string) []byte {
	raw, _ := json.Marshal(llm.WireRequest{
		Protocol: llm.CoddyProtocol,
		Model:    model,
		Messages: []llm.WireMessage{{Role: string(llm.RoleUser), Content: text}},
	})
	return raw
}

// requestStream asks the remote for one completion of alias. A stream the
// provider holds stays open in held; any other answer is read whole.
func (s *rmpState) requestStream(alias, text string) error {
	res, err := s.send(http.MethodPost, "/coddy/llm/completions", rmpSharedToken, rmpWireBody(alias, text))
	if err != nil {
		return err
	}
	s.status, s.header = res.StatusCode, res.Header
	streaming := res.StatusCode == http.StatusOK && strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream")
	if streaming {
		s.served++
	}
	if streaming && (s.remote.upstream.holdAll || text == rmpHoldMarker) {
		s.held = append(s.held, rmpHeldStream{res: res, message: text})
		s.body = nil
		return nil
	}
	defer func() { _ = res.Body.Close() }()
	s.body, err = io.ReadAll(res.Body)
	return err
}

// answerError decodes the error object of the last answer.
func (s *rmpState) answerError() (llm.WireError, error) {
	var e llm.WireError
	if err := json.Unmarshal(s.body, &e); err != nil {
		return e, fmt.Errorf("the answer is not an error object (status %d): %s", s.status, s.body)
	}
	return e, nil
}

func (s *rmpState) expectStatus(want int) error {
	if s.status != want {
		return errStatus(fmt.Sprintf("want status %d", want), s.status, string(s.body))
	}
	return nil
}

// ---- the local Coddy's stream ----

// localStream sends msgs and tools through the provider the local agent built
// for the session's model, which must be sel.
func (s *rmpState) localStream(sel string, msgs []llm.Message, tools []llm.ToolDefinition, notify func(llm.BusyWaitStatus)) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	if want := s.local.rows[0]["model"]; want != sel {
		return fmt.Errorf("the local session runs %v, the step streams %s", want, sel)
	}
	ctx, cancel := rmpContext()
	defer cancel()
	p, err := s.local.rowProvider(ctx)
	if err != nil {
		return err
	}
	if notify != nil {
		ctx = llm.WithBusyWaitNotify(ctx, notify)
	}
	var chunks []llm.StreamChunk
	var mu sync.Mutex
	resp, err := p.Stream(ctx, msgs, tools, func(c llm.StreamChunk) {
		mu.Lock()
		chunks = append(chunks, c)
		mu.Unlock()
	})
	s.mu.Lock()
	s.history, s.resp, s.chunks = msgs, resp, chunks
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("the local coddy's stream of %s failed: %w", sel, err)
	}
	if resp == nil {
		return fmt.Errorf("the local coddy's stream of %s returned no answer", sel)
	}
	return nil
}

func (s *rmpState) setLocalLevel(level string) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	ctx, cancel := rmpContext()
	defer cancel()
	s.local.readListing(ctx)
	if _, err := s.local.session(ctx); err != nil {
		return err
	}
	_, err := s.local.mgr.ApplySessionSettings(ctx, s.local.sid, session.SettingsChange{Reasoning: &level, Source: "web"})
	return err
}

// localModel reads GET /v1/models of the local Coddy and returns the row of sel.
func (s *rmpState) localModel(sel string) (map[string]any, error) {
	if err := s.local.start(s.remote); err != nil {
		return nil, err
	}
	res, err := http.Get(s.local.ts.URL + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, errStatus("GET /v1/models", res.StatusCode, string(raw))
	}
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, row := range doc.Data {
		if row["id"] == sel {
			return row, nil
		}
	}
	return nil, fmt.Errorf("GET /v1/models lists no %q: %s", sel, raw)
}

// ---- given: the remote ----

func (s *rmpState) remoteContextLevels(sel, alias string, tokens int, levels string) error {
	row := s.remote.share(sel, alias)
	row["max_context_tokens"] = tokens
	rmpSetLevels(row, rmpSplitList(levels))
	return nil
}

func (s *rmpState) remotePrivateModel(sel string) error {
	s.remote.row(sel)
	return nil
}

func (s *rmpState) remoteMultimodal(sel, alias string) error {
	s.remote.share(sel, alias)["multimodal"] = true
	return nil
}

func (s *rmpState) remoteAnswersWithUsage(sel, alias, text string, in, out, cached int) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpAnswer(text, in, out, cached))
	return nil
}

func (s *rmpState) remoteCallsTool(sel, alias, tool, args, id string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpCallTool(tool, args, id))
	return nil
}

func (s *rmpState) remoteAnswersAfterResult(sel, alias, text string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpAnswerAfterResult(text))
	return nil
}

func (s *rmpState) remoteCallsThenAnswers(sel, alias, tool, args, text string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpCallThenAnswer(tool, args, "call_"+tool+"_1", text))
	return nil
}

func (s *rmpState) remoteReasons(sel, alias, text, sig string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpReason(text, sig))
	return nil
}

func (s *rmpState) remoteContextAnswers(sel, alias string, tokens int, text string) error {
	s.remote.share(sel, alias)["max_context_tokens"] = tokens
	s.remote.upstream.setScript(rmpAnswer(text, 0, 0, 0))
	return nil
}

func (s *rmpState) remoteLevelsAnswers(sel, alias, levels, text string) error {
	rmpSetLevels(s.remote.share(sel, alias), rmpSplitList(levels))
	s.remote.upstream.setScript(rmpAnswer(text, 0, 0, 0))
	return nil
}

func (s *rmpState) remoteContext(sel, alias string, tokens int) error {
	s.remote.share(sel, alias)["max_context_tokens"] = tokens
	return nil
}

func (s *rmpState) remoteLevels(sel, alias, levels string) error {
	rmpSetLevels(s.remote.share(sel, alias), rmpSplitList(levels))
	return nil
}

func (s *rmpState) remoteAllowsOff(sel, alias string) error {
	row := s.remote.share(sel, alias)
	rmpSetLevels(row, []any{"low", "high"})
	row["allow_reasoning_off"] = true
	return nil
}

func (s *rmpState) remoteMultimodalAnswers(sel, alias, text string) error {
	s.remote.share(sel, alias)["multimodal"] = true
	s.remote.upstream.setScript(rmpAnswer(text, 0, 0, 0))
	return nil
}

func (s *rmpState) remoteReportsUsage(sel, alias string, percent int) error {
	s.remote.share(sel, alias)
	s.remote.reportUsage(percent)
	return nil
}

func (s *rmpState) remoteWithMainAndShared(sel, alias string) error {
	s.remote.setTokens(true, true)
	s.remote.share(sel, alias)
	return nil
}

func (s *rmpState) remoteSharedOnly(sel, alias string) error {
	s.remote.setTokens(false, true)
	s.remote.share(sel, alias)
	return nil
}

func (s *rmpState) remoteNoAuth(sel, alias string) error {
	s.remote.setTokens(false, false)
	s.remote.share(sel, alias)
	return nil
}

func (s *rmpState) remoteHoldsEveryStream(sel, alias string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.holdAll = true
	return nil
}

func (s *rmpState) remoteAnswers(sel, alias, text string) error {
	s.remote.share(sel, alias)
	s.remote.upstream.setScript(rmpAnswer(text, 0, 0, 0))
	return nil
}

func (s *rmpState) remoteAllowsStreams(n int) error {
	if s.remote.started() {
		return fmt.Errorf("the remote is already running")
	}
	s.remote.sharedModels()["max_streams"] = n
	return nil
}

func (s *rmpState) remoteAllowsAndHolds(n int) error {
	if err := s.remoteAllowsStreams(n); err != nil {
		return err
	}
	if err := s.requestStream("coder", rmpHoldMarker); err != nil {
		return err
	}
	if len(s.held) != 1 {
		return errStatus("the held stream was not served", s.status, string(s.body))
	}
	return s.allRunning(1)
}

func (s *rmpState) remoteAdmitsRate(perMinute, burst int) error {
	if s.remote.started() {
		return fmt.Errorf("the remote is already running")
	}
	sm := s.remote.sharedModels()
	sm["rate_per_minute"] = perMinute
	sm["rate_burst"] = burst
	return nil
}

func (s *rmpState) remoteFreezesClock() error {
	if s.remote.started() {
		return fmt.Errorf("the remote is already running")
	}
	s.remote.frozen = true
	return nil
}

// ---- given: the local Coddy ----

func (s *rmpState) localWithProvider(name string) error {
	s.local = newRMPLocal(s.root, name, s.logs)
	return nil
}

func (s *rmpState) localWithBusyWait(name string, seconds int) error {
	s.local = newRMPLocal(s.root, name, s.logs)
	s.local.busyWaitMS = seconds * 1000
	return nil
}

func (s *rmpState) localWithNoModel(name string) error {
	s.local = newRMPLocal(s.root, name, s.logs)
	s.local.noRows = true
	return nil
}

// localAddsInSettings saves the settings document the way the web form does:
// the document GET /coddy/config served, with one more models[] row that names
// nothing but its model, sent back whole with PUT /coddy/config.
func (s *rmpState) localAddsInSettings(sel string) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	res, err := http.Get(s.local.ts.URL + "/coddy/config")
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errStatus("GET /coddy/config", res.StatusCode, string(raw))
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	models, _ := doc["models"].([]any)
	doc["models"] = append(models, map[string]any{"model": sel})
	body, _ := json.Marshal(doc)
	req, err := http.NewRequest(http.MethodPut, s.local.ts.URL+"/coddy/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	answer, _ := io.ReadAll(put.Body)
	_ = put.Body.Close()
	if put.StatusCode != http.StatusOK {
		return errStatus("PUT /coddy/config", put.StatusCode, string(answer))
	}
	return nil
}

func (s *rmpState) localRestarts() error { return s.local.restart() }

func (s *rmpState) localWithTool(name, tool, result string) error {
	if tool != "get_weather" {
		return fmt.Errorf("the stand's local MCP server offers get_weather, not %q", tool)
	}
	s.local = newRMPLocal(s.root, name, s.logs)
	s.local.weather = newRMPWeatherMCP(result)
	return nil
}

func (s *rmpState) localAddsModel(sel string) error {
	s.local.row(sel)
	return nil
}

func (s *rmpState) localAddsMultimodal(sel string) error {
	s.local.row(sel)["multimodal"] = true
	return nil
}

func (s *rmpState) localRowSetsContext(sel string, tokens int) error {
	s.local.row(sel)["max_context_tokens"] = tokens
	s.localRow = sel
	return nil
}

func (s *rmpState) localRowLevels(sel, levels string) error {
	s.local.row(sel)["reasoning_levels"] = rmpSplitList(levels)
	return nil
}

func (s *rmpState) localReadsListing() error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	ctx, cancel := rmpContext()
	defer cancel()
	s.local.readListing(ctx)
	if _, ok := s.local.mgr.ProviderModelEntry(s.local.mgr.Cfg(), s.local.providerName, "coder"); !ok {
		return fmt.Errorf("the local coddy holds no listing record of coder after reading the listing")
	}
	return nil
}

// ---- when ----

func (s *rmpState) clientListsShared() error {
	return s.call(http.MethodGet, "/coddy/llm/models", s.clientToken(), nil)
}

func (s *rmpState) sharedClientListsShared() error {
	return s.call(http.MethodGet, "/coddy/llm/models", rmpSharedToken, nil)
}

func (s *rmpState) sharedClientSessions() error {
	return s.call(http.MethodGet, "/coddy/sessions", rmpSharedToken, nil)
}

func (s *rmpState) noTokenSessions() error {
	return s.call(http.MethodGet, "/coddy/sessions", "", nil)
}

func (s *rmpState) sharedClientPosts(path, model string) error {
	var body []byte
	if path == "/coddy/llm/completions" {
		body = rmpWireBody(model, "Hi")
	} else {
		body, _ = json.Marshal(map[string]any{
			"model":    model,
			"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
		})
	}
	return s.call(http.MethodPost, path, rmpSharedToken, body)
}

func (s *rmpState) localStreamsSystem(sel, system, msg string) error {
	return s.localStream(sel, []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: msg},
	}, nil, nil)
}

func (s *rmpState) localStreamsMessage(sel, msg string) error {
	return s.localStream(sel, []llm.Message{{Role: llm.RoleUser, Content: msg}}, nil, nil)
}

func (s *rmpState) localStreamsOfferingTool(sel, tool string) error {
	s.declared = []llm.ToolDefinition{{Name: tool, Description: "The weather in a city.", InputSchema: rmpWeatherSchema()}}
	return s.localStream(sel, []llm.Message{{Role: llm.RoleUser, Content: "Weather in Paris?"}}, s.declared, nil)
}

func (s *rmpState) localStreamsToolResult(sel, msg, tool, args, id, result string) error {
	s.declared = []llm.ToolDefinition{{Name: tool, Description: "The weather in a city.", InputSchema: rmpWeatherSchema()}}
	return s.localStream(sel, []llm.Message{
		{Role: llm.RoleUser, Content: msg},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: tool, InputJSON: args}}},
		{Role: llm.RoleTool, ToolCallID: id, Content: result},
	}, s.declared, nil)
}

// localStreamsAgain sends the last stream's history with its answer and one
// more user message.
func (s *rmpState) localStreamsAgain(sel string) error {
	s.mu.Lock()
	prev, resp := append([]llm.Message(nil), s.history...), s.resp
	s.mu.Unlock()
	if resp == nil {
		return fmt.Errorf("there is no earlier answer to carry")
	}
	msgs := append(prev, llm.Message{
		Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls,
		Reasoning: resp.Reasoning, ReasoningSignature: resp.ReasoningSignature,
	}, llm.Message{Role: llm.RoleUser, Content: "Go on."})
	return s.localStream(sel, msgs, nil, nil)
}

func (s *rmpState) localStreamsAtLevel(sel, level string) error {
	if err := s.setLocalLevel(level); err != nil {
		return err
	}
	return s.localStream(sel, []llm.Message{{Role: llm.RoleUser, Content: "Hi"}}, nil, nil)
}

func (s *rmpState) localStreamsImage(sel string) error {
	return s.localStream(sel, []llm.Message{{
		Role: llm.RoleUser, Content: "What is in the picture?",
		ImageParts: []llm.ImagePart{{DataURL: rmpRedSquare, Name: "square.png"}},
	}}, nil, nil)
}

// localStreamsReleasingHeld streams msg and lets the held stream end the first
// time the local provider is told the remote is busy.
func (s *rmpState) localStreamsReleasingHeld(sel, msg string) error {
	var once sync.Once
	toldBusy := false
	err := s.localStream(sel, []llm.Message{{Role: llm.RoleUser, Content: msg}}, nil, func(st llm.BusyWaitStatus) {
		if st.Admitted {
			return
		}
		once.Do(func() {
			toldBusy = true
			s.remote.upstream.release(rmpHoldMarker)
		})
	})
	if err != nil {
		return err
	}
	if !toldBusy {
		return fmt.Errorf("the local coddy was never told the remote is busy")
	}
	return nil
}

func (s *rmpState) localAttachesImage(sel string) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"model": sel, "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "What is in the picture?"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": rmpRedSquare}},
		}}},
	})
	res, err := http.Post(s.local.ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return errStatus("POST /v1/chat/completions", res.StatusCode, string(raw))
	}
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	for _, m := range call.Messages {
		if m.Role == llm.RoleUser && len(m.ImageParts) == 1 && m.ImageParts[0].DataURL == rmpRedSquare {
			return nil
		}
	}
	return fmt.Errorf("the remote's provider received no image from the local coddy: %+v", call.Messages)
}

func (s *rmpState) localRunsTurn(sel, prompt string) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	if want := s.local.rows[0]["model"]; want != sel {
		return fmt.Errorf("the local coddy runs %v, the step runs %s", want, sel)
	}
	body, _ := json.Marshal(map[string]any{"model": "agent", "input": prompt, "stream": true})
	req, err := http.NewRequest(http.MethodPost, s.local.ts.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", rmpTurnSession)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return errStatus("POST /v1/responses", res.StatusCode, string(raw))
	}
	if !strings.Contains(string(raw), "data: [DONE]") {
		return fmt.Errorf("the turn's stream did not end: %s", raw)
	}
	return nil
}

func (s *rmpState) localRefreshesListing() error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	ctx, cancel := rmpContext()
	defer cancel()
	s.local.readListing(ctx)
	_, err := s.local.mgr.RefreshProviderModelEntry(ctx, s.local.mgr.Cfg(), s.local.providerName, "coder", "", session.ContextWindowWait)
	return err
}

func (s *rmpState) localReadsUsage(sel string) error {
	if err := s.local.start(s.remote); err != nil {
		return err
	}
	provider, alias, _ := strings.Cut(sel, "/")
	res, err := http.Get(s.local.ts.URL + "/coddy/providers/" + provider + "/usage?model=" + alias)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return errStatus("GET the local usage", res.StatusCode, string(raw))
	}
	s.usage = nil
	if err := json.Unmarshal(raw, &s.usage); err != nil {
		return err
	}
	if s.usage["ok"] != true {
		return fmt.Errorf("the local usage read did not succeed: %s", raw)
	}
	return nil
}

func (s *rmpState) remoteChangesContext(alias string, tokens int) error {
	return s.remote.reconfigure(func() error {
		row, err := s.remote.sharedRow(alias)
		if err != nil {
			return err
		}
		row["max_context_tokens"] = tokens
		return nil
	})
}

func (s *rmpState) remoteNarrows(alias, level string) error {
	return s.remote.reconfigure(func() error {
		row, err := s.remote.sharedRow(alias)
		if err != nil {
			return err
		}
		rmpSetLevels(row, []any{level})
		return nil
	})
}

// remotePointsAtAnother moves the alias to another model of the same provider.
func (s *rmpState) remotePointsAtAnother(alias string) error {
	return s.remote.reconfigure(func() error {
		row, err := s.remote.sharedRow(alias)
		if err != nil {
			return err
		}
		delete(row, "shared_as")
		next := s.remote.share("stub/qwen3-next", alias)
		for k, v := range row {
			if k != "model" {
				next[k] = v
			}
		}
		return nil
	})
}

func (s *rmpState) streamsRequested(n int, alias string) error {
	for i := 0; i < n; i++ {
		if err := s.requestStream(alias, "Stream "+strconv.Itoa(i+1)); err != nil {
			return err
		}
		if err := s.servedStream(); err != nil {
			return fmt.Errorf("stream %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *rmpState) oneStreamRequested(alias string) error {
	return s.requestStream(alias, "One more stream")
}

func (s *rmpState) oneStreamEnds(n int) error {
	if len(s.held) == 0 {
		return fmt.Errorf("no stream is held open")
	}
	h := s.held[0]
	s.held = s.held[1:]
	s.remote.upstream.release(h.message)
	raw, err := io.ReadAll(h.res.Body)
	_ = h.res.Body.Close()
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), `"type":"final"`) {
		return fmt.Errorf("the stream that ended has no final frame: %s", raw)
	}
	return s.allRunning(n - 1)
}

// ---- then ----

func (s *rmpState) listingNamesExactly(alias string) error {
	if err := s.expectStatus(http.StatusOK); err != nil {
		return err
	}
	var doc llm.WireListing
	if err := json.Unmarshal(s.body, &doc); err != nil {
		return err
	}
	var ids []string
	for _, row := range doc.Data {
		ids = append(ids, row.ID)
	}
	if len(ids) != 1 || ids[0] != alias {
		return fmt.Errorf("the listing names %v, want exactly %q", ids, alias)
	}
	return nil
}

func (s *rmpState) listingNeverMentions(a, b, c string) error {
	for _, word := range []string{a, b, c} {
		if strings.Contains(string(s.body), word) {
			return fmt.Errorf("the listing mentions %q: %s", word, s.body)
		}
	}
	return nil
}

func (s *rmpState) v1ModelsContext(sel string, tokens int) error {
	row, err := s.localModel(sel)
	if err != nil {
		return err
	}
	if got, _ := row["max_context_tokens"].(float64); int(got) != tokens {
		return fmt.Errorf("GET /v1/models reports %s with a context of %v tokens, want %d", sel, row["max_context_tokens"], tokens)
	}
	return nil
}

func (s *rmpState) v1ModelsLevels(sel, levels string) error {
	row, err := s.localModel(sel)
	if err != nil {
		return err
	}
	var got []string
	list, _ := row["reasoning_levels"].([]any)
	for _, v := range list {
		got = append(got, fmt.Sprint(v))
	}
	if strings.Join(got, ", ") != levels {
		return fmt.Errorf("GET /v1/models reports %s with the reasoning levels %v, want %s", sel, got, levels)
	}
	return nil
}

func (s *rmpState) v1ModelsMultimodal(sel string) error {
	row, err := s.localModel(sel)
	if err != nil {
		return err
	}
	if row["multimodal"] != true {
		return fmt.Errorf("GET /v1/models does not report %s as multimodal: %v", sel, row)
	}
	return nil
}

func (s *rmpState) assembles(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || s.resp.Content != text {
		return fmt.Errorf("the local coddy assembled %+v, want %q", s.resp, text)
	}
	return nil
}

func (s *rmpState) remoteReceivedSystemAndMessage(system, msg string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	if len(call.Messages) == 0 || call.Messages[0].Role != llm.RoleSystem || call.Messages[0].Content != system {
		return fmt.Errorf("the remote's provider did not receive the system prompt %q first: %+v", system, call.Messages)
	}
	if got := rmpLastUserText(call.Messages); got != msg {
		return fmt.Errorf("the remote's provider received the message %q, want %q", got, msg)
	}
	return nil
}

func (s *rmpState) usageIs(in, out, cached int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.resp
	if r == nil || r.InputTokens != in || r.OutputTokens != out || r.CachedInputTokens != cached {
		return fmt.Errorf("the local coddy read %+v, want %d input, %d output and %d cached tokens", r, in, out, cached)
	}
	return nil
}

func (s *rmpState) remoteHoldsNoSession() error {
	n, err := s.remote.sessionCount()
	if err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("the remote holds %d sessions", n)
	}
	if calls := s.remote.runnerCalls.Load(); calls != 0 {
		return fmt.Errorf("the remote ran %d turns", calls)
	}
	return nil
}

func (s *rmpState) nothingSentMentions(word string) error {
	for _, ex := range s.remote.rec.snapshot() {
		var headers strings.Builder
		_ = ex.Header.Write(&headers)
		if strings.Contains(string(ex.Response), word) || strings.Contains(headers.String(), word) {
			return fmt.Errorf("the remote's answer to %s %s mentions %q: %s", ex.Method, ex.Path, word, ex.Response)
		}
	}
	return nil
}

func (s *rmpState) remoteOfferedSchema(tool string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	for _, t := range call.Tools {
		if t.Name != tool {
			continue
		}
		if ok, got, want := rmpJSONEqual(t.InputSchema, rmpWeatherSchema()); !ok {
			return fmt.Errorf("the remote's provider was offered %s with the schema %s, the local coddy declared %s", tool, got, want)
		}
		return nil
	}
	return fmt.Errorf("the remote's provider was not offered %q: %+v", tool, call.Tools)
}

func (s *rmpState) localReceivesToolCall(tool, args, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || len(s.resp.ToolCalls) != 1 {
		return fmt.Errorf("the local coddy received %+v, want one tool call", s.resp)
	}
	tc := s.resp.ToolCalls[0]
	if tc.Name != tool || tc.ID != id {
		return fmt.Errorf("the local coddy received the call %q under %q, want %q under %q", tc.Name, tc.ID, tool, id)
	}
	if ok, got, want := rmpJSONEqual(rmpJSONText(tc.InputJSON), rmpJSONText(args)); !ok {
		return fmt.Errorf("the local coddy received the arguments %s, want %s", got, want)
	}
	return nil
}

// remoteExecutedNoTool holds when the remote ran no turn and made one provider
// call per completion it served, so no tool loop ran there.
func (s *rmpState) remoteExecutedNoTool() error {
	if calls := s.remote.runnerCalls.Load(); calls != 0 {
		return fmt.Errorf("the remote ran %d turns", calls)
	}
	for _, ex := range s.remote.rec.snapshot() {
		if ex.Path == "/coddy/llm/completions" && ex.CallsAtEnd-ex.CallsAtStart > 1 {
			return fmt.Errorf("one completion made %d provider calls on the remote", ex.CallsAtEnd-ex.CallsAtStart)
		}
	}
	return nil
}

func (s *rmpState) remoteReceivedToolResult(result, id string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	for i, m := range call.Messages {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 || m.ToolCalls[0].ID != id {
			continue
		}
		if i+1 < len(call.Messages) {
			next := call.Messages[i+1]
			if next.Role == llm.RoleTool && next.ToolCallID == id && next.Content == result {
				return nil
			}
		}
	}
	return fmt.Errorf("the remote's provider did not receive the result %q of %s after the call: %+v", result, id, call.Messages)
}

func (s *rmpState) localToolRanOnce(tool, args string) error {
	calls := s.local.weather.snapshot()
	if len(calls) != 1 {
		return fmt.Errorf("the local tool ran %d times: %v", len(calls), calls)
	}
	name, got, _ := strings.Cut(calls[0], " ")
	if name != tool {
		return fmt.Errorf("the local MCP server ran %q, want %q", name, tool)
	}
	if ok, g, w := rmpJSONEqual(rmpJSONText(got), rmpJSONText(args)); !ok {
		return fmt.Errorf("the local tool ran with %s, want %s", g, w)
	}
	return nil
}

func (s *rmpState) localAnswerIs(text string) error {
	st := s.local.mgr.SessionByID(rmpTurnSession)
	if st == nil {
		return fmt.Errorf("the local coddy has no session %s", rmpTurnSession)
	}
	msgs := st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			if strings.TrimSpace(msgs[i].Content) != text {
				return fmt.Errorf("the local coddy answered %q, want %q", msgs[i].Content, text)
			}
			return nil
		}
	}
	return fmt.Errorf("the local session holds no answer")
}

// remoteReceivedLocalSystemPrompt checks the turn's request: the system message
// the local agent built, recognised by the workspace's AGENTS.md in it.
func (s *rmpState) remoteReceivedLocalSystemPrompt() error {
	for _, call := range s.remote.upstream.snapshot() {
		if len(call.Tools) == 0 {
			continue
		}
		if len(call.Messages) == 0 || call.Messages[0].Role != llm.RoleSystem {
			return fmt.Errorf("the turn's request does not start with a system message: %+v", call.Messages)
		}
		if !strings.Contains(call.Messages[0].Content, rmpWorkspaceMarker) {
			return fmt.Errorf("the system message the remote received is not the local coddy's: %.400s", call.Messages[0].Content)
		}
		return nil
	}
	return fmt.Errorf("the remote's provider never received the turn's request")
}

func (s *rmpState) remoteNoToolNoSession() error {
	if err := s.remoteExecutedNoTool(); err != nil {
		return err
	}
	return s.remoteHoldsNoSession()
}

func (s *rmpState) localReceivesReasoning(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil || s.resp.Reasoning != text {
		return fmt.Errorf("the local coddy received the reasoning %+v, want %q", s.resp, text)
	}
	if s.resp.ReasoningSignature == "" {
		return fmt.Errorf("the reasoning came without a signature")
	}
	return nil
}

func (s *rmpState) remoteReceivedSignature(sig string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	for _, m := range call.Messages {
		if m.Role == llm.RoleAssistant && m.ReasoningSignature == sig {
			return nil
		}
	}
	return fmt.Errorf("the remote's provider did not receive the signature %q: %+v", sig, call.Messages)
}

func (s *rmpState) remoteDidNotReceiveSignature(sig string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	sawAssistant := false
	for _, m := range call.Messages {
		if m.Role == llm.RoleAssistant {
			sawAssistant = true
		}
		if m.ReasoningSignature == sig {
			return fmt.Errorf("the remote's provider received the signature %q of the earlier model", sig)
		}
	}
	if !sawAssistant {
		return fmt.Errorf("the request carried no earlier assistant message: %+v", call.Messages)
	}
	return nil
}

func (s *rmpState) callSucceeds() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resp == nil {
		return fmt.Errorf("the call returned no answer")
	}
	return nil
}

func rmpWireCode(raw []byte) string {
	var e llm.WireError
	_ = json.Unmarshal(raw, &e)
	return e.Code
}

func (s *rmpState) remoteAnswersBeforeProvider(code string) error {
	for i, ex := range s.remote.rec.snapshot() {
		if ex.Method != http.MethodPost || ex.Path != "/coddy/llm/completions" || ex.Status != http.StatusBadRequest {
			continue
		}
		if rmpWireCode(ex.Response) != code {
			continue
		}
		if ex.CallsAtEnd != ex.CallsAtStart {
			return fmt.Errorf("the remote called its provider for the request it answered %s", code)
		}
		s.staleAt = i
		return nil
	}
	return fmt.Errorf("the remote never answered %q: %s", code, s.exchangeSummary())
}

func (s *rmpState) exchangeSummary() string {
	var b strings.Builder
	for i, ex := range s.remote.rec.snapshot() {
		fmt.Fprintf(&b, "\n  %d %s %s -> %d %.200s", i, ex.Method, ex.Path, ex.Status, ex.Response)
	}
	return b.String()
}

func (s *rmpState) localRefreshesAndResends(string) error {
	if s.staleAt < 0 {
		return fmt.Errorf("no stale answer to follow")
	}
	read, resent := false, false
	for _, ex := range s.remote.rec.snapshot()[s.staleAt+1:] {
		switch {
		case ex.Method == http.MethodGet && ex.Path == "/coddy/llm/models" && ex.Status == http.StatusOK:
			read = true
		case read && ex.Method == http.MethodPost && ex.Path == "/coddy/llm/completions" && ex.Status == http.StatusOK:
			resent = true
		}
	}
	if !read || !resent {
		return fmt.Errorf("after the stale answer the local coddy read the listing: %v, sent the request again: %v%s", read, resent, s.exchangeSummary())
	}
	return nil
}

func (s *rmpState) providerCalledOnce() error {
	if n := s.remote.upstream.callCount(); n != 1 {
		return fmt.Errorf("the remote's provider was called %d times", n)
	}
	return nil
}

func (s *rmpState) fallsBackTo(level, received string) error {
	var sent string
	for _, ex := range s.remote.rec.snapshot() {
		if ex.Method == http.MethodPost && ex.Path == "/coddy/llm/completions" && ex.Status == http.StatusOK {
			var req llm.WireRequest
			if err := json.Unmarshal(ex.Request, &req); err != nil {
				return err
			}
			sent = ""
			if req.Options.ReasoningEffort != nil {
				sent = *req.Options.ReasoningEffort
			}
		}
	}
	if sent != level {
		return fmt.Errorf("the local coddy sent the level %q, want %q", sent, level)
	}
	return s.providerReceivedLevel(received)
}

func (s *rmpState) localConfigSays(tokens int) error {
	row, err := s.local.configRow(s.localRow)
	if err != nil {
		return err
	}
	if got, _ := row["max_context_tokens"].(int); got != tokens {
		return fmt.Errorf("the local configuration file says max_context_tokens %v, want %d", row["max_context_tokens"], tokens)
	}
	return nil
}

func (s *rmpState) providerReceivedLevel(level string) error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	if call.Options.ReasoningEffort != level {
		return fmt.Errorf("the remote's provider received the reasoning level %q, want %q", call.Options.ReasoningEffort, level)
	}
	return nil
}

func (s *rmpState) remoteReceivedImage() error {
	call, err := s.remote.upstream.last()
	if err != nil {
		return err
	}
	for _, m := range call.Messages {
		if m.Role != llm.RoleUser {
			continue
		}
		if m.Content != "What is in the picture?" {
			return fmt.Errorf("the text part arrived as %q", m.Content)
		}
		if len(m.ImageParts) != 1 {
			return fmt.Errorf("the remote's provider received %d image parts", len(m.ImageParts))
		}
		if got := m.ImageParts[0].DataURL; got != rmpRedSquare {
			return fmt.Errorf("the image arrived as %.80s, want %.80s", got, rmpRedSquare)
		}
		return nil
	}
	return fmt.Errorf("the remote's provider received no user message: %+v", call.Messages)
}

// reportsPercent reads the session window of the local Coddy's reading: a
// fresh reading of the alias, whose number came from the remote's account,
// read there once.
func (s *rmpState) reportsPercent(percent int) error {
	usage, _ := s.usage["usage"].(map[string]any)
	if usage["model"] != "coder" || usage["stale"] == true {
		return fmt.Errorf("the reading is %v, want a fresh one of the alias coder", usage)
	}
	windows, _ := usage["windows"].([]any)
	for _, w := range windows {
		win, _ := w.(map[string]any)
		if win["id"] != "session" {
			continue
		}
		got, _ := win["usedPercent"].(float64)
		if math.Abs(got-float64(percent)) > 0.05 {
			return fmt.Errorf("the session window reports %v percent used, want %d", win["usedPercent"], percent)
		}
		if reads := s.remote.usageReads.Load(); reads != 1 {
			return fmt.Errorf("the remote read its account %d times, want once", reads)
		}
		return nil
	}
	return fmt.Errorf("the local usage reading has no session window: %v", s.usage)
}

// usageDocument is the remote's answer to the usage read of the alias.
func (s *rmpState) usageDocument() ([]byte, error) {
	for _, ex := range s.remote.rec.snapshot() {
		if ex.Method == http.MethodGet && strings.HasPrefix(ex.Path, "/coddy/llm/models/") && strings.HasSuffix(ex.Path, "/usage") {
			return ex.Response, nil
		}
	}
	return nil, fmt.Errorf("the remote was never asked for the usage of a shared model")
}

func (s *rmpState) remoteAccountWide() error {
	raw, err := s.usageDocument()
	if err != nil {
		return err
	}
	var doc llm.WireUsage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if !doc.AccountWide {
		return fmt.Errorf("the remote's usage document is not marked account-wide: %s", raw)
	}
	return nil
}

func (s *rmpState) documentNeverMentions() error {
	raw, err := s.usageDocument()
	if err != nil {
		return err
	}
	text := strings.ToLower(string(raw))
	for _, word := range []string{"qwen3-secret", "stub", rmpUsageKeyName, "neuraldeep", "1229", "2000.73", "rub", "wallet", "balance"} {
		if strings.Contains(text, strings.ToLower(word)) {
			return fmt.Errorf("the usage document mentions %q: %s", word, raw)
		}
	}
	return nil
}

func (s *rmpState) refusedUnauthorized() error { return s.expectStatus(http.StatusUnauthorized) }

func (s *rmpState) remoteAnswersStatus(status int) error { return s.expectStatus(status) }

func (s *rmpState) mainReachesSessions() error {
	if err := s.call(http.MethodGet, "/coddy/sessions", rmpMainToken, nil); err != nil {
		return err
	}
	return s.expectStatus(http.StatusOK)
}

func (s *rmpState) remoteAnswersKind(status int, kind string) error {
	if err := s.expectStatus(status); err != nil {
		return err
	}
	e, err := s.answerError()
	if err != nil {
		return err
	}
	if e.Kind != kind {
		return fmt.Errorf("the answer's kind is %q, want %q: %s", e.Kind, kind, s.body)
	}
	return nil
}

func (s *rmpState) answerNeedsAuth() error {
	e, err := s.answerError()
	if err != nil {
		return err
	}
	if !strings.Contains(e.Message, "shared models need authentication") {
		return fmt.Errorf("the answer does not say shared models need authentication: %q", e.Message)
	}
	return nil
}

func (s *rmpState) allRunning(n int) error {
	return rmpEventually(fmt.Sprintf("%d streams running", n), func() (bool, string) {
		got := int(s.remote.upstream.running.Load())
		return got == n, fmt.Sprintf("%d running", got)
	})
}

func (s *rmpState) remoteAnswersBusy(status int, kind string, retry int) error {
	if err := s.remoteAnswersKind(status, kind); err != nil {
		return err
	}
	if got := s.header.Get("Retry-After"); got != strconv.Itoa(retry) {
		return fmt.Errorf("Retry-After is %q, want %d", got, retry)
	}
	return nil
}

func (s *rmpState) remoteAnswersBusyCode(status int, kind, code string, retry int) error {
	if err := s.remoteAnswersBusy(status, kind, retry); err != nil {
		return err
	}
	if e, _ := s.answerError(); e.Code != code {
		return fmt.Errorf("the answer's code is %q, want %q", e.Code, code)
	}
	return nil
}

func (s *rmpState) providerNotCalledForRefused() error {
	if n := s.remote.upstream.callCount(); n != s.served {
		return fmt.Errorf("the remote's provider was called %d times for %d streams it took", n, s.served)
	}
	return nil
}

func (s *rmpState) servedStream() error {
	if s.status != http.StatusOK || !strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") {
		return errStatus("the stream was not served", s.status, string(s.body))
	}
	return nil
}

func (s *rmpState) providerCalledOnceFor(msg string) error {
	n := 0
	for _, call := range s.remote.upstream.snapshot() {
		if rmpLastUserText(call.Messages) == msg {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("the remote's provider was called %d times for %q", n, msg)
	}
	return nil
}

func (s *rmpState) remoteCounted(n int, outcome string) error {
	return rmpEventually(fmt.Sprintf("%d %q calls counted", n, outcome), func() (bool, string) {
		if err := s.call(http.MethodGet, "/coddy/shared-models/stats", rmpMainToken, nil); err != nil {
			return false, err.Error()
		}
		var doc sharedStatsDoc
		if err := json.Unmarshal(s.body, &doc); err != nil {
			return false, string(s.body)
		}
		got := int64(0)
		for _, row := range doc.Rows {
			if row.Outcome == outcome {
				got += row.Calls
			}
		}
		return got == int64(n), string(s.body)
	})
}

func initRemoteModelProviderScenario(sc *godog.ScenarioContext) {
	s := &rmpState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, sn *godog.Scenario, err error) (context.Context, error) {
		if err != nil && s.logs != nil {
			fmt.Fprintf(os.Stderr, "--- log of the failed scenario %q:\n%s", sn.Name, s.logs.String())
		}
		s.close()
		return ctx, nil
	})

	const q = `"([^"]*)"`
	const js = `(\{.*\})`

	// The remote.
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` with a (\d+) token context and the reasoning levels `+q+`$`, s.remoteContextLevels)
	sc.Step(`^the remote also has a model `+q+` that it does not share$`, s.remotePrivateModel)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` that is multimodal$`, s.remoteMultimodal)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model answers `+q+` and reports (\d+) input tokens, (\d+) output tokens and (\d+) cached tokens$`, s.remoteAnswersWithUsage)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model calls `+q+` with `+js+` under the call id `+q+`$`, s.remoteCallsTool)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model answers `+q+` once it sees a tool result$`, s.remoteAnswersAfterResult)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model calls `+q+` with `+js+` and then answers `+q+` once it sees the result$`, s.remoteCallsThenAnswers)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model reasons `+q+` with the signature `+q+`$`, s.remoteReasons)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` with a (\d+) token context whose model answers `+q+`$`, s.remoteContextAnswers)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` with the reasoning levels `+q+` whose model answers `+q+`$`, s.remoteLevelsAnswers)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` with a (\d+) token context$`, s.remoteContext)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` with the reasoning levels `+q+`$`, s.remoteLevels)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` that allows reasoning off$`, s.remoteAllowsOff)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` that is multimodal and whose model answers `+q+`$`, s.remoteMultimodalAnswers)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose provider reports (\d+) percent of its quota used$`, s.remoteReportsUsage)
	sc.Step(`^a remote coddy with a main token, sharing `+q+` as `+q+`, and a shared-model token$`, s.remoteWithMainAndShared)
	sc.Step(`^a remote coddy with no main token and no web login, sharing `+q+` as `+q+`, and a shared-model token$`, s.remoteSharedOnly)
	sc.Step(`^a remote coddy with no token and no web login, sharing `+q+` as `+q+`$`, s.remoteNoAuth)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model holds every stream open until released$`, s.remoteHoldsEveryStream)
	sc.Step(`^a remote coddy sharing `+q+` as `+q+` whose model answers `+q+`$`, s.remoteAnswers)
	sc.Step(`^the remote allows (\d+) shared-model streams at once$`, s.remoteAllowsStreams)
	sc.Step(`^the remote allows (\d+) shared-model streams? at once and one stream is held open$`, s.remoteAllowsAndHolds)
	sc.Step(`^the remote admits (\d+) shared-model calls a minute with a burst of (\d+)$`, s.remoteAdmitsRate)
	sc.Step(`^the remote freezes its rate-window clock$`, s.remoteFreezesClock)

	// The local Coddy.
	sc.Step(`^a local coddy with a provider `+q+` of type coddy pointing at the remote$`, s.localWithProvider)
	sc.Step(`^a local coddy with a provider `+q+` of type coddy pointing at the remote with a busy wait of (\d+) seconds$`, s.localWithBusyWait)
	sc.Step(`^a local coddy with a provider `+q+` and a local tool `+q+` that returns `+q+`$`, s.localWithTool)
	sc.Step(`^a local coddy with a provider `+q+` of type coddy pointing at the remote and no model of it yet$`, s.localWithNoModel)
	sc.Step(`^the local operator adds the model `+q+` in Settings$`, s.localAddsInSettings)
	sc.Step(`^the local coddy restarts$`, s.localRestarts)
	sc.Step(`^the local coddy adds the model `+q+` without a context window$`, s.localAddsModel)
	sc.Step(`^the local coddy adds the model `+q+` without reasoning levels$`, s.localAddsModel)
	sc.Step(`^the local coddy adds the model `+q+` declaring it multimodal$`, s.localAddsMultimodal)
	sc.Step(`^the local row `+q+` sets max_context_tokens (\d+)$`, s.localRowSetsContext)
	sc.Step(`^the local row `+q+` has the reasoning levels `+q+`$`, s.localRowLevels)
	sc.Step(`^the local coddy has read the listing of the remote$`, s.localReadsListing)

	// What happens.
	sc.Step(`^a client lists the shared models of the remote$`, s.clientListsShared)
	sc.Step(`^a client holding the shared-model token lists the shared models of the remote$`, s.sharedClientListsShared)
	sc.Step(`^the same client asks the remote for its sessions$`, s.sharedClientSessions)
	sc.Step(`^a client holding the shared-model token asks the remote for its sessions$`, s.sharedClientSessions)
	sc.Step(`^a client holding no token asks the remote for its sessions$`, s.noTokenSessions)
	sc.Step(`^the same client posts to `+q+` for `+q+`$`, s.sharedClientPosts)
	sc.Step(`^the same client posts to `+q+` for the selector `+q+` instead of the alias$`, s.sharedClientPosts)
	sc.Step(`^the local coddy streams `+q+` with the system prompt `+q+` and the message `+q+`$`, s.localStreamsSystem)
	sc.Step(`^the local coddy streams `+q+` with the message `+q+`$`, s.localStreamsMessage)
	sc.Step(`^the local coddy streams `+q+` offering its own tool `+q+`$`, s.localStreamsOfferingTool)
	sc.Step(`^the local coddy streams `+q+` with the message `+q+`, the assistant call `+q+` with `+js+` under the id `+q+` and the tool result `+q+`$`, s.localStreamsToolResult)
	sc.Step(`^the local coddy streams `+q+` again with that assistant message in its history$`, s.localStreamsAgain)
	sc.Step(`^the local coddy streams `+q+` at the reasoning level `+q+`$`, s.localStreamsAtLevel)
	sc.Step(`^the local coddy streams `+q+` with a text part and an image part$`, s.localStreamsImage)
	sc.Step(`^the local coddy streams `+q+` with the message `+q+` and the held stream ends once the local coddy has been told busy$`, s.localStreamsReleasingHeld)
	sc.Step(`^the local coddy lets a user attach an image to a message sent to `+q+`$`, s.localAttachesImage)
	sc.Step(`^the local coddy runs a turn on `+q+` with the prompt `+q+`$`, s.localRunsTurn)
	sc.Step(`^the local coddy refreshes its view of the remote listing$`, s.localRefreshesListing)
	sc.Step(`^the local coddy reads the usage of the model `+q+`$`, s.localReadsUsage)
	sc.Step(`^the remote operator changes the context window of `+q+` to (\d+) tokens$`, s.remoteChangesContext)
	sc.Step(`^the remote operator narrows `+q+` to the reasoning level `+q+`$`, s.remoteNarrows)
	sc.Step(`^the remote operator points `+q+` at another model$`, s.remotePointsAtAnother)
	sc.Step(`^(\d+) streams are requested from `+q+`$`, s.streamsRequested)
	sc.Step(`^a sixth stream is requested from `+q+`$`, s.oneStreamRequested)
	sc.Step(`^a further stream is requested from `+q+`$`, s.oneStreamRequested)
	sc.Step(`^one of the (\d+) streams ends$`, s.oneStreamEnds)

	// What is observed.
	sc.Step(`^the listing names exactly `+q+`$`, s.listingNamesExactly)
	sc.Step(`^the listing never mentions `+q+`, `+q+` or `+q+`$`, s.listingNeverMentions)
	sc.Step(`^GET /v1/models of the local coddy reports `+q+` with a context of (\d+) tokens$`, s.v1ModelsContext)
	sc.Step(`^GET /v1/models of the local coddy reports `+q+` with the reasoning levels `+q+`$`, s.v1ModelsLevels)
	sc.Step(`^GET /v1/models of the local coddy reports `+q+` as multimodal$`, s.v1ModelsMultimodal)
	sc.Step(`^the local coddy assembles the answer `+q+`$`, s.assembles)
	sc.Step(`^the remote model received the system prompt `+q+` and the message `+q+`$`, s.remoteReceivedSystemAndMessage)
	sc.Step(`^the usage the local coddy reads is (\d+) input tokens, (\d+) output tokens and (\d+) cached tokens$`, s.usageIs)
	sc.Step(`^the remote holds no session$`, s.remoteHoldsNoSession)
	sc.Step(`^nothing the remote sent mentions `+q+`$`, s.nothingSentMentions)
	sc.Step(`^the remote model was offered the tool `+q+` with the schema the local coddy declared$`, s.remoteOfferedSchema)
	sc.Step(`^the local coddy receives the tool call `+q+` with arguments `+js+` under the id `+q+`$`, s.localReceivesToolCall)
	sc.Step(`^the remote executed no tool$`, s.remoteExecutedNoTool)
	sc.Step(`^the remote model received the tool result `+q+` under the call `+q+`, after that assistant call$`, s.remoteReceivedToolResult)
	sc.Step(`^the local tool `+q+` ran once, on the local coddy, with `+js+`$`, s.localToolRanOnce)
	sc.Step(`^the local coddy's answer is `+q+`$`, s.localAnswerIs)
	sc.Step(`^the remote model received the local coddy's system prompt$`, s.remoteReceivedLocalSystemPrompt)
	sc.Step(`^the remote executed no tool and holds no session$`, s.remoteNoToolNoSession)
	sc.Step(`^the local coddy receives the reasoning `+q+` with a non-empty signature$`, s.localReceivesReasoning)
	sc.Step(`^the remote model received the reasoning signature `+q+` unchanged$`, s.remoteReceivedSignature)
	sc.Step(`^the remote model did not receive the signature `+q+`$`, s.remoteDidNotReceiveSignature)
	sc.Step(`^the call succeeds$`, s.callSucceeds)
	sc.Step(`^the remote answers `+q+` before the provider is called$`, s.remoteAnswersBeforeProvider)
	sc.Step(`^the local coddy refreshes its view of `+q+` and sends the request once more$`, s.localRefreshesAndResends)
	sc.Step(`^the provider was called once$`, s.providerCalledOnce)
	sc.Step(`^the local coddy falls back to the level `+q+` and the provider received `+q+`$`, s.fallsBackTo)
	sc.Step(`^the local configuration file still says (\d+)$`, s.localConfigSays)
	sc.Step(`^the remote model's provider received the reasoning level `+q+`$`, s.providerReceivedLevel)
	sc.Step(`^the remote model received one image part after the text part, with the same MIME type and the same bytes$`, s.remoteReceivedImage)
	sc.Step(`^it reports (\d+) percent of the quota used$`, s.reportsPercent)
	sc.Step(`^the remote marks the reading as account-wide$`, s.remoteAccountWide)
	sc.Step(`^the document never mentions the upstream model id, the provider name, the key name or any amount of money$`, s.documentNeverMentions)
	sc.Step(`^the remote refuses it as unauthorized$`, s.refusedUnauthorized)
	sc.Step(`^the remote answers (\d+)$`, s.remoteAnswersStatus)
	sc.Step(`^a client holding the main token still reaches the sessions of the remote$`, s.mainReachesSessions)
	sc.Step(`^the remote answers (\d+) with the kind `+q+`$`, s.remoteAnswersKind)
	sc.Step(`^the answer says that shared models need authentication$`, s.answerNeedsAuth)
	sc.Step(`^all (\d+) streams are running on the remote$`, s.allRunning)
	sc.Step(`^the remote answers (\d+) with the kind `+q+` and a Retry-After of (\d+) seconds?$`, s.remoteAnswersBusy)
	sc.Step(`^the remote answers (\d+) with the kind `+q+`, the code `+q+` and a Retry-After of (\d+) seconds?$`, s.remoteAnswersBusyCode)
	sc.Step(`^the provider was not called for the sixth$`, s.providerNotCalledForRefused)
	sc.Step(`^the provider was not called for the further stream$`, s.providerNotCalledForRefused)
	sc.Step(`^the sixth stream is served$`, s.servedStream)
	sc.Step(`^the provider was called once for the local coddy's request$`, func() error { return s.providerCalledOnceFor("Hi") })
	sc.Step(`^the remote counted (\d+) `+q+` calls?$`, s.remoteCounted)
}

func TestRemoteModelProviderFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "remote-model-provider",
		ScenarioInitializer: initRemoteModelProviderScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/remote_model_provider.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("remote model provider feature suite failed")
	}
}
