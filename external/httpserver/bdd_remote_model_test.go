//go:build http

package httpserver

// Godog harness for features/remote_model_provider.feature: a local Coddy
// reaching the models a remote Coddy shares, over the real coddy provider, the
// real shared-model routes and a scripted model behind the remote's one
// provider factory (bdd_remote_model_stand_test.go). The relay file of the same
// feature set is bdd_remote_model_relay_test.go.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// remoteModelRedPNG is a one-pixel PNG, the picture of the image scenario.
const remoteModelRedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

const localModelRef = "remote/coder"

// streamOutcome is what one call of the local coddy's provider returned.
type streamOutcome struct {
	resp   *llm.Response
	err    error
	chunks []llm.StreamChunk
}

func (o *streamOutcome) text() string {
	var b strings.Builder
	for _, c := range o.chunks {
		b.WriteString(c.TextDelta)
	}
	return b.String()
}

// busyTrace is what the wait of a call for a free slot reported.
type busyTrace struct {
	mu     sync.Mutex
	told   int
	sleeps []time.Duration
}

func (b *busyTrace) noteTold() {
	b.mu.Lock()
	b.told++
	b.mu.Unlock()
}

func (b *busyTrace) noteSleep(d time.Duration) {
	b.mu.Lock()
	b.sleeps = append(b.sleeps, d)
	b.mu.Unlock()
}

func (b *busyTrace) summary() (int, []time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.told, append([]time.Duration(nil), b.sleeps...)
}

// remoteModelState is one scenario: the remote, the local Coddy and what the
// steps have seen so far.
type remoteModelState struct {
	spec       remoteSpec
	remoteMuts []func(*config.Config)
	remote     *remoteStand

	// The local Coddy: where its provider points, the token it presents, and the
	// changes the steps make to its configuration before it is built.
	localBase, localToken string
	localMuts             []func(*config.Config)
	tool                  *weatherMCP
	local                 *localStand

	// A client that is not a Coddy.
	clientToken string
	http        httpResult
	listing     httpResult
	// localUsage is the local coddy's answer to the read of a usage.
	localUsage httpResult

	last     *streamOutcome
	prev     *streamOutcome
	offered  []llm.ToolDefinition
	sent     []llm.Message
	image    []byte
	turn     string
	busy     *busyTrace
	held     []*http.Response
	sixth    *http.Response
	cleanups []func()
}

func (s *remoteModelState) reset() {
	if s.remote != nil {
		s.remote.gate.releaseAll()
	}
	for _, r := range s.held {
		_ = r.Body.Close()
	}
	if s.sixth != nil {
		_ = s.sixth.Body.Close()
	}
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	if s.local != nil {
		s.local.close()
	}
	if s.tool != nil && s.local == nil {
		s.tool.srv.Close()
	}
	if s.remote != nil {
		s.remote.close()
	}
	*s = remoteModelState{}
}

// ---- building the two ends ----

func (s *remoteModelState) ensureRemote() error {
	if s.remote != nil {
		return nil
	}
	if s.spec.selector == "" {
		return fmt.Errorf("the scenario has no remote coddy yet")
	}
	r, err := newRemoteStand(s.spec, s.remoteMuts, true)
	if err != nil {
		return err
	}
	s.remote = r
	return nil
}

// mutateRemote changes the remote's configuration: before it is built the
// change goes into its first configuration, after it the running server is
// reloaded the way a saved file would reload it.
func (s *remoteModelState) mutateRemote(f func(*config.Config)) {
	if s.remote == nil {
		s.remoteMuts = append(s.remoteMuts, f)
		return
	}
	s.remote.reload(f)
}

func (s *remoteModelState) updateScript(f func(*remoteScript)) {
	f(&s.spec.script)
	if s.remote != nil {
		s.remote.mu.Lock()
		f(&s.remote.script)
		s.remote.mu.Unlock()
	}
}

func (s *remoteModelState) mutateLocal(f func(*config.Config)) {
	if s.local == nil {
		s.localMuts = append(s.localMuts, f)
		return
	}
	s.local.reload(f)
}

func (s *remoteModelState) ensureLocal() error {
	if s.local != nil {
		return nil
	}
	if err := s.ensureRemote(); err != nil {
		return err
	}
	base, token := s.localBase, s.localToken
	if base == "" {
		base = s.remote.url()
	}
	if token == "" {
		token = s.spec.sharedToken
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "remote", Type: "coddy", APIBase: base, APIKey: token, Proxy: "none"}},
		Agent:     config.Agent{Model: localModelRef},
	}
	for _, m := range s.localMuts {
		m(cfg)
	}
	if cfg.FindModelEntry(localModelRef) == nil {
		cfg.Models = append(cfg.Models, config.ModelEntry{Model: localModelRef, MaxTokens: 4096})
	}
	l, err := newLocalStand(cfg, s.tool)
	if err != nil {
		return err
	}
	s.local = l
	return nil
}

// ---- the remote's Given steps ----

type remoteClause struct {
	re    *regexp.Regexp
	apply func(*remoteSpec, []string) error
}

func clause(pattern string, apply func(*remoteSpec, []string) error) remoteClause {
	return remoteClause{re: regexp.MustCompile(pattern), apply: apply}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// remoteClauses are the phrases a Given step about the remote is made of, tried
// in this order at the start of what is left of the sentence.
var remoteClauses = []remoteClause{
	clause(`^(?:and\s+)?(?:with\s+)?an?\s+(\d+) token context`, func(sp *remoteSpec, m []string) error {
		sp.contextTokens = atoi(m[1])
		return nil
	}),
	clause(`^(?:and\s+)?(?:with\s+)?the reasoning levels "([^"]*)"`, func(sp *remoteSpec, m []string) error {
		lv := strings.Split(m[1], ",")
		sp.levels = &lv
		return nil
	}),
	clause(`^(?:and\s+)?that is multimodal`, func(sp *remoteSpec, _ []string) error {
		sp.multimodal = true
		return nil
	}),
	clause(`^(?:and\s+)?whose model calls "([^"]*)" with (\{.*?\}) and then answers "([^"]*)" once it sees the result`, func(sp *remoteSpec, m []string) error {
		sp.script.toolName, sp.script.toolArgs, sp.script.answerAfterTool = m[1], m[2], m[3]
		return nil
	}),
	clause(`^(?:and\s+)?whose model calls "([^"]*)" with (\{.*?\}) under the call id "([^"]*)"`, func(sp *remoteSpec, m []string) error {
		sp.script.toolName, sp.script.toolArgs, sp.script.toolID = m[1], m[2], m[3]
		return nil
	}),
	clause(`^(?:and\s+)?whose model answers "([^"]*)" and reports (\d+) input tokens, (\d+) output tokens and (\d+) cached tokens`, func(sp *remoteSpec, m []string) error {
		sp.script.answer = m[1]
		sp.script.usage, sp.script.hasUsage = [3]int{atoi(m[2]), atoi(m[3]), atoi(m[4])}, true
		return nil
	}),
	clause(`^(?:and\s+)?whose model answers "([^"]*)" once it sees a tool result`, func(sp *remoteSpec, m []string) error {
		sp.script.answerAfterTool = m[1]
		return nil
	}),
	clause(`^(?:and\s+)?whose model answers "([^"]*)"`, func(sp *remoteSpec, m []string) error {
		sp.script.answer = m[1]
		return nil
	}),
	clause(`^(?:and\s+)?whose model reasons "([^"]*)" with the signature "([^"]*)"`, func(sp *remoteSpec, m []string) error {
		sp.script.reasoning, sp.script.signature = m[1], m[2]
		return nil
	}),
	clause(`^(?:and\s+)?whose model holds every stream open until released`, func(sp *remoteSpec, _ []string) error {
		sp.script.holdFirst = -1
		return nil
	}),
	clause(`^(?:and\s+)?that allows reasoning off`, func(sp *remoteSpec, _ []string) error {
		sp.allowOff = true
		return nil
	}),
	clause(`^(?:and\s+)?whose provider reports (\d+) percent of its quota used`, func(sp *remoteSpec, m []string) error {
		sp.usagePercent = atoi(m[1])
		return nil
	}),
}

// parseRemoteTail reads the clauses of a sentence into spec, and refuses what no
// clause understands, so a step is never half-applied.
func parseRemoteTail(spec *remoteSpec, tail string) error {
	rest := strings.TrimSpace(tail)
next:
	for rest != "" {
		for _, c := range remoteClauses {
			if m := c.re.FindStringSubmatch(rest); m != nil {
				if err := c.apply(spec, m); err != nil {
					return err
				}
				rest = strings.TrimSpace(rest[len(m[0]):])
				continue next
			}
		}
		return fmt.Errorf("the remote's description has a part this harness does not understand: %q", rest)
	}
	return nil
}

func (s *remoteModelState) remoteSharing(selector, alias, tail string) error {
	s.spec = newRemoteSpec(selector, alias)
	return parseRemoteTail(&s.spec, tail)
}

func (s *remoteModelState) remoteWithAuth(auth remoteAuth) func(selector, alias string) error {
	return func(selector, alias string) error {
		s.spec = newRemoteSpec(selector, alias)
		s.spec.auth = auth
		return nil
	}
}

func (s *remoteModelState) remoteAlsoHas(selector string) error {
	s.mutateRemote(func(c *config.Config) {
		c.Models = append(c.Models, config.ModelEntry{Model: selector, MaxTokens: 1024, MaxContextTokens: 1000})
	})
	return nil
}

func (s *remoteModelState) remoteAllows(n int) error {
	s.mutateRemote(func(c *config.Config) { c.HTTPServer.SharedModels.MaxStreams = n })
	return nil
}

// remoteAllowsAndHolds is the busy remote of the waiting scenario: one slot, and
// one stream in it that the model keeps open until it is released.
func (s *remoteModelState) remoteAllowsAndHolds(n int) error {
	if err := s.remoteAllows(n); err != nil {
		return err
	}
	if err := s.ensureRemote(); err != nil {
		return err
	}
	s.updateScript(func(sc *remoteScript) { sc.holdFirst = 1 })
	resp, err := openStream(s.remote.url(), s.spec.sharedToken, s.spec.alias)
	if err != nil {
		return err
	}
	s.held = append(s.held, resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the stream to hold open was refused: %d", resp.StatusCode)
	}
	return remoteModelWaitUntil("the held stream to be running on the remote", func() bool { return s.remote.gate.runningCount() == 1 })
}

// ---- the local Coddy's Given steps ----

func (s *remoteModelState) localWithProvider(_ string) error { return nil }

func (s *remoteModelState) localWithBusyWait(_ string, seconds int) error {
	s.mutateLocal(func(c *config.Config) {
		for i := range c.Providers {
			if c.Providers[i].Name == "remote" {
				c.Providers[i].BusyWaitMS = seconds * 1000
			}
		}
	})
	return nil
}

func (s *remoteModelState) localWithTool(_, name, result string) error {
	s.tool = newWeatherMCP(name, result)
	return nil
}

func (s *remoteModelState) localHasReadTheListing() error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.local.mgr.AwaitContextWindows(ctx, s.local.mgr.Cfg(), []string{localModelRef}, 10*time.Second)
	if _, ok := s.local.mgr.ProviderModelEntry(s.local.mgr.Cfg(), "remote", "coder"); !ok {
		return fmt.Errorf("the local coddy has no listing of the remote after reading it")
	}
	return nil
}

func (s *remoteModelState) localRowHasLevels(ref, levels string) error {
	lv := strings.Split(levels, ",")
	s.mutateLocal(func(c *config.Config) {
		idx := -1
		for i := range c.Models {
			if c.Models[i].Model == ref {
				idx = i
			}
		}
		if idx < 0 {
			c.Models = append(c.Models, config.ModelEntry{Model: ref, MaxTokens: 4096})
			idx = len(c.Models) - 1
		}
		c.Models[idx].ReasoningLevels = &lv
	})
	return nil
}

func (s *remoteModelState) localAddsModel(ref string) error {
	s.mutateLocal(func(c *config.Config) {
		c.Models = append(c.Models, config.ModelEntry{Model: ref, MaxTokens: 4096})
	})
	return nil
}

// ---- the remote operator ----

func (s *remoteModelState) operatorPointsAt() error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	idx := s.remote.sharedRowIndex(s.spec.alias)
	if idx < 0 {
		return fmt.Errorf("no row of the remote is shared as %q", s.spec.alias)
	}
	s.mutateRemote(func(c *config.Config) { c.Models[idx].Model = "stub/another-model" })
	return nil
}

func (s *remoteModelState) operatorChangesContext(alias string, tokens int) error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	idx := s.remote.sharedRowIndex(alias)
	if idx < 0 {
		return fmt.Errorf("no row of the remote is shared as %q", alias)
	}
	s.mutateRemote(func(c *config.Config) { c.Models[idx].MaxContextTokens = tokens })
	return nil
}

// ---- a client that is not a Coddy ----

func (s *remoteModelState) listAs(token string) func() error {
	return func() error {
		if err := s.ensureRemote(); err != nil {
			return err
		}
		s.clientToken = token
		res, err := doHTTP(http.MethodGet, s.remote.url()+llm.CoddyModelsPath, token, nil)
		if err != nil {
			return err
		}
		s.listing, s.http = res, res
		return nil
	}
}

// clientLists is a client with the credential the remote hands out, when it
// hands out one: the token made for shared models, else its own token, and none
// for a remote that has no credential at all.
func (s *remoteModelState) clientLists() error {
	token := ""
	switch s.spec.auth {
	case authMainAndShared, authSharedOnly:
		token = s.spec.sharedToken
	case authMainOnly:
		token = s.spec.mainToken
	case authNone:
	}
	return s.listAs(token)()
}

func (s *remoteModelState) sharedClientLists() error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	return s.listAs(s.spec.sharedToken)()
}

func (s *remoteModelState) listingNames(want string) error {
	if s.listing.status != http.StatusOK {
		return fmt.Errorf("the listing was refused: %d %s", s.listing.status, s.listing.body)
	}
	var l llm.WireListing
	if err := json.Unmarshal(s.listing.body, &l); err != nil {
		return fmt.Errorf("the listing is not a listing: %v: %s", err, s.listing.body)
	}
	if len(l.Data) != 1 || l.Data[0].ID != want {
		return fmt.Errorf("the listing names %+v, want exactly %q", l.Data, want)
	}
	return nil
}

func (s *remoteModelState) listingNeverMentions(a, b, c string) error {
	for _, needle := range []string{a, b, c} {
		if strings.Contains(string(s.listing.body), needle) {
			return fmt.Errorf("the listing mentions %q: %s", needle, s.listing.body)
		}
	}
	return nil
}

func (s *remoteModelState) sharedClientAsksSessions() error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	s.clientToken = s.spec.sharedToken
	return s.askSessions()
}

func (s *remoteModelState) anonymousClientAsksSessions() error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	s.clientToken = ""
	return s.askSessions()
}

func (s *remoteModelState) askSessions() error {
	res, err := doHTTP(http.MethodGet, s.remote.url()+"/coddy/sessions", s.clientToken, nil)
	s.http = res
	return err
}

func (s *remoteModelState) sameClientAsksSessions() error { return s.askSessions() }

func (s *remoteModelState) sameClientPostsChat(path, selector string) error {
	raw, _ := json.Marshal(map[string]any{
		"model": selector, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "Hi"}},
	})
	res, err := doHTTP(http.MethodPost, s.remote.url()+path, s.clientToken, raw)
	s.http = res
	return err
}

func (s *remoteModelState) sameClientPostsSelector(path, selector string) error {
	raw, _ := json.Marshal(llm.WireRequest{
		Protocol: llm.CoddyProtocol, Model: selector,
		Messages: []llm.WireMessage{{Role: "user", Content: "Hi"}},
	})
	res, err := doHTTP(http.MethodPost, s.remote.url()+path, s.clientToken, raw)
	s.http = res
	return err
}

func (s *remoteModelState) refusedAsUnauthorized() error {
	if s.http.status != http.StatusUnauthorized {
		return fmt.Errorf("status %d, want 401: %s", s.http.status, s.http.body)
	}
	return nil
}

func (s *remoteModelState) remoteAnswers(status int) error {
	if s.http.status != status {
		return fmt.Errorf("status %d, want %d: %s", s.http.status, status, s.http.body)
	}
	return nil
}

func (s *remoteModelState) mainClientReachesSessions() error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	res, err := doHTTP(http.MethodGet, s.remote.url()+"/coddy/sessions", s.spec.mainToken, nil)
	if err != nil {
		return err
	}
	if res.status != http.StatusOK {
		return fmt.Errorf("the main token is refused on the sessions: %d %s", res.status, res.body)
	}
	return nil
}

func (s *remoteModelState) answerKind(status int, kind string) error {
	if s.http.status != status {
		return fmt.Errorf("status %d, want %d: %s", s.http.status, status, s.http.body)
	}
	var e llm.WireError
	if err := json.Unmarshal(s.http.body, &e); err != nil {
		return fmt.Errorf("not an error object: %s", s.http.body)
	}
	if e.Kind != kind {
		return fmt.Errorf("kind %q, want %q: %s", e.Kind, kind, s.http.body)
	}
	return nil
}

func (s *remoteModelState) answerKindAuth(kind string) error {
	return s.answerKind(http.StatusForbidden, kind)
}

func (s *remoteModelState) answerSaysNeedAuth() error {
	var e llm.WireError
	if err := json.Unmarshal(s.http.body, &e); err != nil {
		return fmt.Errorf("not an error object: %s", s.http.body)
	}
	if !strings.Contains(e.Message, "shared models need authentication") {
		return fmt.Errorf("the answer does not say that shared models need authentication: %q", e.Message)
	}
	return nil
}

// ---- the local Coddy streams ----

func stepCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// stream runs one call of the local coddy's provider and keeps what came out;
// the Then steps judge it.
func (s *remoteModelState) stream(model string, msgs []llm.Message, tools []llm.ToolDefinition, effort string, decorate func(context.Context) context.Context) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	ctx, cancel := stepCtx()
	defer cancel()
	prov, err := s.local.provider(ctx, model, effort)
	if err != nil {
		return err
	}
	if decorate != nil {
		ctx = decorate(ctx)
	}
	out := &streamOutcome{}
	resp, err := prov.Stream(ctx, msgs, tools, func(c llm.StreamChunk) { out.chunks = append(out.chunks, c) })
	out.resp, out.err = resp, err
	s.prev, s.last = s.last, out
	s.sent = msgs
	return nil
}

func (s *remoteModelState) streamed() (*streamOutcome, error) {
	if s.last == nil {
		return nil, fmt.Errorf("the local coddy has not streamed yet")
	}
	if s.last.err != nil {
		return nil, fmt.Errorf("the stream failed: %w", s.last.err)
	}
	if s.last.resp == nil {
		return nil, fmt.Errorf("the stream returned no response")
	}
	return s.last, nil
}

func (s *remoteModelState) streamsWithSystemAndMessage(model, system, message string) error {
	return s.stream(model, []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: message},
	}, nil, "", nil)
}

func (s *remoteModelState) streamsWithMessage(model, message string) error {
	return s.stream(model, []llm.Message{{Role: llm.RoleUser, Content: message}}, nil, "", nil)
}

func (s *remoteModelState) streamsAtLevel(model, level string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	// The level is one the local row offers, as the session would check it: its
	// own keys when it writes them, else the listing the local coddy has read.
	// The check goes through the resolver only, so a row whose levels the local
	// coddy cannot tell offers no level at all.
	ctx, cancel := stepCtx()
	defer cancel()
	s.local.mgr.AwaitContextWindows(ctx, s.local.mgr.Cfg(), []string{model}, session.ContextWindowWait)
	cfg := s.local.mgr.Cfg()
	ent := cfg.FindModelEntry(model)
	if ent == nil {
		return fmt.Errorf("the local coddy has no row %q", model)
	}
	choices := cfg.ReasoningChoicesFor(ent)
	offered := false
	for _, lv := range choices {
		offered = offered || lv == level
	}
	if !offered {
		return fmt.Errorf("the local row %q offers the reasoning levels %q, not %q", model, choices, level)
	}
	return s.streamMessage(model, "Hi", level)
}

func (s *remoteModelState) streamMessage(model, message, effort string) error {
	return s.stream(model, []llm.Message{{Role: llm.RoleUser, Content: message}}, nil, effort, nil)
}

func (s *remoteModelState) assemblesAnswer(want string) error {
	out, err := s.streamed()
	if err != nil {
		return err
	}
	if out.resp.Content != want {
		return fmt.Errorf("the final answer is %q, want %q", out.resp.Content, want)
	}
	if got := out.text(); got != want {
		return fmt.Errorf("the chunks assembled %q, want %q", got, want)
	}
	return nil
}

func (s *remoteModelState) lastReceived() ([]llm.Message, error) {
	msgs := s.remote.stub.lastMessages()
	if msgs == nil {
		return nil, fmt.Errorf("the remote model was never called")
	}
	return msgs, nil
}

func (s *remoteModelState) remoteReceivedSystemAndMessage(system, message string) error {
	msgs, err := s.lastReceived()
	if err != nil {
		return err
	}
	var gotSystem, gotUser bool
	for _, m := range msgs {
		gotSystem = gotSystem || (m.Role == llm.RoleSystem && m.Content == system)
		gotUser = gotUser || (m.Role == llm.RoleUser && m.Content == message)
	}
	if !gotSystem || !gotUser {
		return fmt.Errorf("the remote model received %+v, want the system prompt %q and the message %q", msgs, system, message)
	}
	return nil
}

func (s *remoteModelState) usageIs(in, out, cached int) error {
	o, err := s.streamed()
	if err != nil {
		return err
	}
	if o.resp.InputTokens != in || o.resp.OutputTokens != out || o.resp.CachedInputTokens != cached {
		return fmt.Errorf("usage %d/%d/%d, want %d/%d/%d", o.resp.InputTokens, o.resp.OutputTokens, o.resp.CachedInputTokens, in, out, cached)
	}
	return nil
}

func (s *remoteModelState) remoteHoldsNoSession() error { return s.remote.assertNoSession() }

func (s *remoteModelState) nothingMentions(needle string) error {
	if len(s.remote.capt.snapshot()) == 0 {
		return fmt.Errorf("the remote sent nothing, so there is nothing to check")
	}
	if s.remote.capt.mentions(needle) {
		return fmt.Errorf("something the remote sent mentions %q", needle)
	}
	return nil
}

// ---- tools ----

func weatherTool() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "get_weather",
		Description: "Current weather of a city",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name"},
			},
			"required": []any{"city"},
		},
	}
}

func (s *remoteModelState) streamsOfferingTool(model, tool string) error {
	def := weatherTool()
	def.Name = tool
	s.offered = []llm.ToolDefinition{def}
	return s.stream(model, []llm.Message{{Role: llm.RoleUser, Content: "What is the weather in Paris?"}}, s.offered, "", nil)
}

func canonicalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var back any
	if err := json.Unmarshal(raw, &back); err != nil {
		return "", err
	}
	out, err := json.Marshal(back)
	return string(out), err
}

func (s *remoteModelState) remoteOfferedTool(name string) error {
	s.remote.stub.mu.Lock()
	var tools []llm.ToolDefinition
	if n := len(s.remote.stub.tools); n > 0 {
		tools = s.remote.stub.tools[n-1]
	}
	s.remote.stub.mu.Unlock()
	for _, got := range tools {
		if got.Name != name {
			continue
		}
		for _, want := range s.offered {
			if want.Name != name {
				continue
			}
			a, err1 := canonicalJSON(got.InputSchema)
			b, err2 := canonicalJSON(want.InputSchema)
			if err1 != nil || err2 != nil || a != b || got.Description != want.Description {
				return fmt.Errorf("the remote model was offered %+v, the local coddy declared %+v", got, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the remote model was offered %+v, want the tool %q", tools, name)
}

func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func (s *remoteModelState) receivesToolCall(name, args, id string) error {
	o, err := s.streamed()
	if err != nil {
		return err
	}
	if len(o.resp.ToolCalls) != 1 {
		return fmt.Errorf("the final answer carries %d tool calls: %+v", len(o.resp.ToolCalls), o.resp.ToolCalls)
	}
	tc := o.resp.ToolCalls[0]
	if tc.Name != name || tc.ID != id || !sameJSON(tc.InputJSON, args) {
		return fmt.Errorf("tool call %+v, want %s %s under %s", tc, name, args, id)
	}
	seen := false
	for _, c := range o.chunks {
		if c.ToolCall != nil && c.ToolCall.ID == id {
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("no chunk announced the tool call %q", id)
	}
	return nil
}

func (s *remoteModelState) remoteExecutedNoTool() error {
	if n := s.remote.runnerCalls.Load(); n != 0 {
		return fmt.Errorf("the remote ran %d agent turn(s)", n)
	}
	return s.remote.assertNoSession()
}

func (s *remoteModelState) streamsWithToolResult(model, message, tool, args, id, result string) error {
	return s.stream(model, []llm.Message{
		{Role: llm.RoleUser, Content: message},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: tool, InputJSON: args}}},
		{Role: llm.RoleTool, ToolCallID: id, Content: result},
	}, []llm.ToolDefinition{weatherTool()}, "", nil)
}

func (s *remoteModelState) remoteReceivedToolResult(result, id string) error {
	msgs, err := s.lastReceived()
	if err != nil {
		return err
	}
	call := -1
	for i, m := range msgs {
		if m.Role == llm.RoleAssistant {
			for _, tc := range m.ToolCalls {
				if tc.ID == id {
					call = i
				}
			}
		}
		if m.Role == llm.RoleTool && m.ToolCallID == id && m.Content == result {
			if call < 0 || call > i {
				return fmt.Errorf("the tool result %q came before its assistant call: %+v", result, msgs)
			}
			return nil
		}
	}
	return fmt.Errorf("the remote model received %+v, want the result %q under %q", msgs, result, id)
}

// ---- a whole turn ----

func (s *remoteModelState) runsTurn(_, prompt string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	answer, err := s.local.runTurn(prompt)
	if err != nil {
		return err
	}
	s.turn = answer
	return nil
}

func (s *remoteModelState) localToolRanOnce(_, args string) error {
	if s.tool == nil {
		return fmt.Errorf("the scenario has no local tool")
	}
	calls := s.tool.recordedCalls()
	if len(calls) != 1 || !sameJSON(calls[0], args) {
		return fmt.Errorf("the local tool ran %d time(s) with %v, want once with %s", len(calls), calls, args)
	}
	return nil
}

func (s *remoteModelState) localAnswerIs(want string) error {
	if s.turn != want {
		return fmt.Errorf("the local coddy's answer is %q, want %q", s.turn, want)
	}
	return nil
}

func (s *remoteModelState) remoteReceivedLocalSystemPrompt() error {
	s.remote.stub.mu.Lock()
	calls := append([][]llm.Message(nil), s.remote.stub.msgs...)
	s.remote.stub.mu.Unlock()
	if len(calls) == 0 {
		return fmt.Errorf("the remote model was never called")
	}
	for i, msgs := range calls {
		found := false
		for _, m := range msgs {
			if m.Role == llm.RoleSystem && strings.Contains(m.Content, localMarker) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("call %d of the remote model carries no system prompt of the local harness (marker %q): %+v", i+1, localMarker, msgs)
		}
	}
	return nil
}

func (s *remoteModelState) remoteExecutedNoToolAndHoldsNoSession() error {
	return s.remoteExecutedNoTool()
}

// ---- reasoning ----

func (s *remoteModelState) receivesReasoning(want string) error {
	o, err := s.streamed()
	if err != nil {
		return err
	}
	if o.resp.Reasoning != want {
		return fmt.Errorf("reasoning %q, want %q", o.resp.Reasoning, want)
	}
	sig := o.resp.ReasoningSignature
	if sig == "" || sig == "sig-abc" {
		return fmt.Errorf("the signature came back %q, want a non-empty envelope", sig)
	}
	return nil
}

func (s *remoteModelState) streamsAgainWithAssistant(model string) error {
	o, err := s.streamed()
	if err != nil {
		return err
	}
	return s.stream(model, []llm.Message{
		{Role: llm.RoleUser, Content: "Hi"},
		{Role: llm.RoleAssistant, Content: o.resp.Content, Reasoning: o.resp.Reasoning, ReasoningSignature: o.resp.ReasoningSignature},
		{Role: llm.RoleUser, Content: "Go on."},
	}, nil, "", nil)
}

func (s *remoteModelState) remoteReceivedSignature(want string) error {
	msgs, err := s.lastReceived()
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && m.ReasoningSignature == want {
			return nil
		}
	}
	return fmt.Errorf("the remote model received %+v, want the signature %q unchanged", msgs, want)
}

func (s *remoteModelState) remoteDidNotReceiveSignature(raw string) error {
	msgs, err := s.lastReceived()
	if err != nil {
		return err
	}
	// The signature did leave the local coddy: it is the remote that kept it from
	// its provider.
	cs := s.remote.capt.completions()
	if len(cs) == 0 {
		return fmt.Errorf("the remote saw no completion request")
	}
	var sent llm.WireRequest
	if err := json.Unmarshal(cs[len(cs)-1].reqBody, &sent); err != nil {
		return fmt.Errorf("the last request is not a completion request: %v", err)
	}
	travelled := false
	for _, m := range sent.Messages {
		travelled = travelled || (m.Role == string(llm.RoleAssistant) && m.ReasoningSignature != "")
	}
	if !travelled {
		return fmt.Errorf("the local coddy sent no signature, so the remote had none to drop")
	}
	for _, m := range msgs {
		if m.ReasoningSignature != "" {
			return fmt.Errorf("the remote model received a signature (%q) of an earlier model, want none; raw %q must not arrive", m.ReasoningSignature, raw)
		}
	}
	return nil
}

func (s *remoteModelState) callSucceeds() error {
	_, err := s.streamed()
	return err
}

// ---- the revision of a row ----

func (s *remoteModelState) answersStaleRevision(code string) error {
	cs := s.remote.capt.completions()
	if len(cs) == 0 {
		return fmt.Errorf("the remote saw no completion request")
	}
	first := cs[0]
	var e llm.WireError
	if err := json.Unmarshal(first.body, &e); err != nil {
		return fmt.Errorf("the first answer is not an error object (%d): %s", first.status, first.body)
	}
	if first.status != http.StatusBadRequest || e.Code != code {
		return fmt.Errorf("the first answer is %d %+v, want 400 %q", first.status, e, code)
	}
	if first.providerCalls != 0 {
		return fmt.Errorf("the provider had been called %d time(s) when the remote refused the stale request", first.providerCalls)
	}
	return nil
}

func expectedRevisionOf(e remoteExchange) string {
	var req llm.WireRequest
	if json.Unmarshal(e.reqBody, &req) != nil || req.Options.ExpectedRevision == nil {
		return ""
	}
	return *req.Options.ExpectedRevision
}

func (s *remoteModelState) refreshesAndSendsAgain(alias string) error {
	cs := s.remote.capt.completions()
	if len(cs) != 2 {
		return fmt.Errorf("the remote saw %d completion requests, want the stale one and the one sent again", len(cs))
	}
	stale, fresh := expectedRevisionOf(cs[0]), expectedRevisionOf(cs[1])
	if stale == "" || fresh == "" || stale == fresh {
		return fmt.Errorf("the revisions sent were %q then %q, want an old one and a new one", stale, fresh)
	}
	res, err := doHTTP(http.MethodGet, s.remote.url()+llm.CoddyModelsPath, s.spec.sharedToken, nil)
	if err != nil {
		return err
	}
	var l llm.WireListing
	if err := json.Unmarshal(res.body, &l); err != nil {
		return err
	}
	for _, row := range l.Data {
		if row.ID == alias && row.Revision == fresh {
			if e, ok := s.local.mgr.ProviderModelEntry(s.local.mgr.Cfg(), "remote", alias); !ok || e.Revision != fresh {
				return fmt.Errorf("the local coddy's cache holds %q, want the refreshed %q", e.Revision, fresh)
			}
			return nil
		}
	}
	return fmt.Errorf("the request sent again carried the revision %q, which is not the one the remote lists now (%+v)", fresh, l.Data)
}

func (s *remoteModelState) providerCalledOnce() error {
	if n := s.remote.stub.callCount(); n != 1 {
		return fmt.Errorf("the provider was called %d times, want once", n)
	}
	return nil
}

func (s *remoteModelState) providerReceivedLevel(level string) error {
	b, ok := s.remote.lastBuild()
	if !ok {
		return fmt.Errorf("the remote built no provider")
	}
	if b.opts.ReasoningEffort != level {
		return fmt.Errorf("the provider was built with the reasoning level %q, want %q", b.opts.ReasoningEffort, level)
	}
	return nil
}

// ---- images ----

func (s *remoteModelState) streamsWithImage(model string) error {
	raw, err := base64.StdEncoding.DecodeString(remoteModelRedPNG)
	if err != nil {
		return err
	}
	s.image = raw
	return s.stream(model, []llm.Message{{
		Role:    llm.RoleUser,
		Content: "What is in this picture?",
		ImageParts: []llm.ImagePart{{
			DataURL: "data:image/png;base64," + remoteModelRedPNG, MIMEType: "image/png", Name: "red.png",
		}},
	}}, nil, "", nil)
}

func (s *remoteModelState) remoteReceivedImage() error {
	msgs, err := s.lastReceived()
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if m.Role != llm.RoleUser || len(m.ImageParts) == 0 {
			continue
		}
		if m.Content != "What is in this picture?" || len(m.ImageParts) != 1 {
			return fmt.Errorf("the user message is %+v, want the text and one image after it", m)
		}
		ip := m.ImageParts[0]
		header, payload, ok := strings.Cut(ip.DataURL, ";base64,")
		if !ok {
			return fmt.Errorf("the image is not a data URL: %.40s", ip.DataURL)
		}
		got, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || !bytes.Equal(got, s.image) {
			return fmt.Errorf("the image bytes differ (%d bytes, want %d): %v", len(got), len(s.image), err)
		}
		// The MIME type is the part's own field when the sender set it (the
		// provider call of the local coddy does) and the data URL's media type
		// when it did not (the HTTP intake builds a part from the URL alone).
		mime := ip.MIMEType
		if mime == "" {
			mime = strings.TrimPrefix(header, "data:")
		}
		if mime != "image/png" {
			return fmt.Errorf("the MIME type is %q, want image/png", mime)
		}
		return nil
	}
	return fmt.Errorf("the remote model received no image: %+v", msgs)
}

// ---- the local coddy's own routes ----

func (s *remoteModelState) modelsReportContext(ref string, tokens int) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	res, err := doHTTP(http.MethodGet, s.local.ts.URL+"/v1/models", "", nil)
	if err != nil {
		return err
	}
	var body struct {
		Data []struct {
			ID               string `json:"id"`
			MaxContextTokens int    `json:"max_context_tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return fmt.Errorf("GET /v1/models: %v: %s", err, res.body)
	}
	for _, m := range body.Data {
		if m.ID == ref {
			if m.MaxContextTokens != tokens {
				return fmt.Errorf("%s reports a context of %d tokens, want %d", ref, m.MaxContextTokens, tokens)
			}
			return nil
		}
	}
	return fmt.Errorf("GET /v1/models does not list %q: %s", ref, res.body)
}

func (s *remoteModelState) fetchesModels(provider string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	res, err := doHTTP(http.MethodGet, s.local.ts.URL+"/coddy/providers/"+provider+"/models", "", nil)
	s.listing = res
	return err
}

func (s *remoteModelState) offersExactly(want string) error {
	var body struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(s.listing.body, &body); err != nil {
		return fmt.Errorf("not a model list: %v: %s", err, s.listing.body)
	}
	if !body.OK {
		return fmt.Errorf("the local coddy could not fetch the models: %s", body.Error)
	}
	if len(body.Models) != 1 || body.Models[0].ID != want {
		return fmt.Errorf("the local coddy offers %+v, want exactly %q", body.Models, want)
	}
	return nil
}

// ---- the limit of five ----

func (s *remoteModelState) streamsRequested(n int, alias string) error {
	if err := s.ensureRemote(); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		resp, err := openStream(s.remote.url(), s.spec.sharedToken, alias)
		if err != nil {
			return err
		}
		s.held = append(s.held, resp)
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("stream %d was refused: %d %s", i+1, resp.StatusCode, raw)
		}
	}
	return nil
}

func (s *remoteModelState) allStreamsRunning(n int) error {
	key := sharedKeyFor(s.spec.sharedToken)
	return remoteModelWaitUntil(fmt.Sprintf("%d streams to run on the remote", n), func() bool {
		return s.remote.gate.runningCount() == n && s.remote.srv.sharedLimit.inUse(key) == n
	})
}

func (s *remoteModelState) sixthRequested(alias string) error {
	resp, err := openStream(s.remote.url(), s.spec.sharedToken, alias)
	if err != nil {
		return err
	}
	s.sixth = resp
	s.http = httpResult{status: resp.StatusCode, header: resp.Header}
	if resp.StatusCode != http.StatusOK {
		s.http.body, _ = io.ReadAll(resp.Body)
	}
	return nil
}

func (s *remoteModelState) answers429Busy(kind string) error {
	if err := s.answerKind(http.StatusTooManyRequests, kind); err != nil {
		return err
	}
	if got := s.http.header.Get("Retry-After"); got != "1" {
		return fmt.Errorf("Retry-After is %q, want 1", got)
	}
	return nil
}

func (s *remoteModelState) providerNotCalledForSixth() error {
	if n := s.remote.stub.callCount(); n != 5 {
		return fmt.Errorf("the provider was called %d times, want 5 (none for the sixth)", n)
	}
	return nil
}

func (s *remoteModelState) oneOfFiveEnds() error {
	if len(s.held) == 0 || !s.remote.gate.releaseOne() {
		return fmt.Errorf("no running stream to end")
	}
	if _, err := io.Copy(io.Discard, s.held[0].Body); err != nil {
		return err
	}
	_ = s.held[0].Body.Close()
	s.held = s.held[1:]
	key := sharedKeyFor(s.spec.sharedToken)
	return remoteModelWaitUntil("the slot of the ended stream to be free", func() bool { return s.remote.srv.sharedLimit.inUse(key) == len(s.held) })
}

func (s *remoteModelState) sixthServed() error {
	if s.sixth == nil || s.sixth.StatusCode != http.StatusOK {
		return fmt.Errorf("the sixth stream was not served: %+v", s.http)
	}
	if ct := s.sixth.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("the sixth answer is %q, want an event stream", ct)
	}
	return remoteModelWaitUntil("the sixth stream to run", func() bool { return s.remote.gate.runningCount() == 5 })
}

// ---- waiting for a free slot ----

func (s *remoteModelState) streamsUntilHeldEnds(model, message string) error {
	if err := s.ensureLocal(); err != nil {
		return err
	}
	trace := &busyTrace{}
	s.busy = trace
	key := sharedKeyFor(s.spec.sharedToken)
	err := s.stream(model, []llm.Message{{Role: llm.RoleUser, Content: message}}, nil, "", func(ctx context.Context) context.Context {
		// The remote told the local coddy it is busy: that is when the held stream
		// ends, and the sleep that follows takes no wall-clock time but still waits
		// until the slot is really free again.
		ctx = llm.WithBusyWaitNotify(ctx, func(llm.BusyWaitStatus) {
			trace.noteTold()
			s.remote.gate.releaseAll()
		})
		return llm.WithBusyWaitSleep(ctx, func(ctx context.Context, d time.Duration) error {
			trace.noteSleep(d)
			deadline := time.Now().Add(5 * time.Second)
			for s.remote.srv.sharedLimit.inUse(key) != 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("the held stream never freed its slot")
				}
				time.Sleep(time.Millisecond)
			}
			return nil
		})
	})
	return err
}

func (s *remoteModelState) providerCalledOnceForLocal() error {
	if s.busy == nil {
		return fmt.Errorf("the local coddy never waited")
	}
	told, sleeps := s.busy.summary()
	if told == 0 || len(sleeps) == 0 {
		return fmt.Errorf("the local coddy was told busy %d times and slept %d times, want it to have waited", told, len(sleeps))
	}
	for _, d := range sleeps {
		if d < time.Second {
			return fmt.Errorf("the local coddy slept %v, want at least the second the remote asked for", d)
		}
	}
	// One call is the held stream, the other the local coddy's: a refused request
	// never reached the provider.
	if n := s.remote.stub.callCount(); n != 2 {
		return fmt.Errorf("the provider was called %d times, want twice (the held stream and the local coddy's request once)", n)
	}
	return nil
}

// ---- step registration ----

func registerRemoteModelSteps(sc *godog.ScenarioContext, s *remoteModelState) {
	// The remote.
	sc.Step(`^a remote coddy sharing "([^"]*)" as "([^"]*)"(.*)$`, s.remoteSharing)
	sc.Step(`^a remote coddy with a main token, sharing "([^"]*)" as "([^"]*)", and a shared-model token$`, s.remoteWithAuth(authMainAndShared))
	sc.Step(`^a remote coddy with no main token and no web login, sharing "([^"]*)" as "([^"]*)", and a shared-model token$`, s.remoteWithAuth(authSharedOnly))
	sc.Step(`^a remote coddy with no token and no web login, sharing "([^"]*)" as "([^"]*)"$`, s.remoteWithAuth(authNone))
	sc.Step(`^the remote also has a model "([^"]*)" that it does not share$`, s.remoteAlsoHas)
	sc.Step(`^the remote allows (\d+) shared-model streams at once$`, s.remoteAllows)
	sc.Step(`^the remote allows (\d+) shared-model stream at once and one stream is held open$`, s.remoteAllowsAndHolds)
	sc.Step(`^the remote operator points "([^"]*)" at another model$`, func(string) error { return s.operatorPointsAt() })
	sc.Step(`^the remote operator changes the context window of "([^"]*)" to (\d+) tokens$`, s.operatorChangesContext)

	// The local coddy.
	sc.Step(`^a local coddy with a provider "([^"]*)" of type coddy pointing at the remote$`, s.localWithProvider)
	sc.Step(`^a local coddy with a provider "([^"]*)" of type coddy pointing at the remote with a busy wait of (\d+) seconds$`, s.localWithBusyWait)
	sc.Step(`^a local coddy with a provider "([^"]*)" and a local tool "([^"]*)" that returns "([^"]*)"$`, s.localWithTool)
	sc.Step(`^the local coddy has read the listing of the remote$`, s.localHasReadTheListing)
	sc.Step(`^the local row "([^"]*)" has the reasoning levels "([^"]*)"$`, s.localRowHasLevels)
	sc.Step(`^the local coddy adds the model "([^"]*)" without a context window$`, s.localAddsModel)
	sc.Step(`^GET /v1/models of the local coddy reports "([^"]*)" with a context of (\d+) tokens$`, s.modelsReportContext)
	sc.Step(`^the local coddy fetches the models of provider "([^"]*)"$`, s.fetchesModels)
	sc.Step(`^it offers exactly "([^"]*)"$`, s.offersExactly)

	// A client that is not a Coddy.
	sc.Step(`^a client lists the shared models of the remote$`, s.clientLists)
	sc.Step(`^a client holding the shared-model token lists the shared models of the remote$`, s.sharedClientLists)
	sc.Step(`^the listing names exactly "([^"]*)"$`, s.listingNames)
	sc.Step(`^the listing never mentions "([^"]*)", "([^"]*)" or "([^"]*)"$`, s.listingNeverMentions)
	sc.Step(`^the same client asks the remote for its sessions$`, s.sameClientAsksSessions)
	sc.Step(`^a client holding the shared-model token asks the remote for its sessions$`, s.sharedClientAsksSessions)
	sc.Step(`^a client holding no token asks the remote for its sessions$`, s.anonymousClientAsksSessions)
	sc.Step(`^the same client posts to "([^"]*)" for "([^"]*)"$`, s.sameClientPostsChat)
	sc.Step(`^the same client posts to "([^"]*)" for the selector "([^"]*)" instead of the alias$`, s.sameClientPostsSelector)
	sc.Step(`^the remote refuses it as unauthorized$`, s.refusedAsUnauthorized)
	sc.Step(`^the remote answers (\d+)$`, s.remoteAnswers)
	sc.Step(`^a client holding the main token still reaches the sessions of the remote$`, s.mainClientReachesSessions)
	sc.Step(`^the remote answers 403 with the kind "([^"]*)"$`, s.answerKindAuth)
	sc.Step(`^the answer says that shared models need authentication$`, s.answerSaysNeedAuth)

	// The local coddy streams.
	sc.Step(`^the local coddy streams "([^"]*)" with the system prompt "([^"]*)" and the message "([^"]*)"$`, s.streamsWithSystemAndMessage)
	sc.Step(`^the local coddy streams "([^"]*)" with the message "([^"]*)"$`, s.streamsWithMessage)
	sc.Step(`^the local coddy streams "([^"]*)" at the reasoning level "([^"]*)"$`, s.streamsAtLevel)
	sc.Step(`^the local coddy streams "([^"]*)" offering its own tool "([^"]*)"$`, s.streamsOfferingTool)
	sc.Step(`^the local coddy streams "([^"]*)" with the message "([^"]*)", the assistant call "([^"]*)" with (\{.*\}) under the id "([^"]*)" and the tool result "([^"]*)"$`, s.streamsWithToolResult)
	sc.Step(`^the local coddy streams "([^"]*)" again with that assistant message in its history$`, s.streamsAgainWithAssistant)
	sc.Step(`^the local coddy streams "([^"]*)" with a text part and an image part$`, s.streamsWithImage)
	sc.Step(`^the local coddy streams "([^"]*)" with the message "([^"]*)" and the held stream ends once the local coddy has been told busy$`, s.streamsUntilHeldEnds)
	sc.Step(`^the local coddy runs a turn on "([^"]*)" with the prompt "([^"]*)"$`, s.runsTurn)

	// What came out.
	sc.Step(`^the local coddy assembles the answer "([^"]*)"$`, s.assemblesAnswer)
	sc.Step(`^the remote model received the system prompt "([^"]*)" and the message "([^"]*)"$`, s.remoteReceivedSystemAndMessage)
	sc.Step(`^the usage the local coddy reads is (\d+) input tokens, (\d+) output tokens and (\d+) cached tokens$`, s.usageIs)
	sc.Step(`^the remote holds no session$`, s.remoteHoldsNoSession)
	sc.Step(`^nothing the remote sent mentions "([^"]*)"$`, s.nothingMentions)
	sc.Step(`^the remote model was offered the tool "([^"]*)" with the schema the local coddy declared$`, s.remoteOfferedTool)
	sc.Step(`^the local coddy receives the tool call "([^"]*)" with arguments (\{.*\}) under the id "([^"]*)"$`, s.receivesToolCall)
	sc.Step(`^the remote executed no tool$`, s.remoteExecutedNoTool)
	sc.Step(`^the remote model received the tool result "([^"]*)" under the call "([^"]*)", after that assistant call$`, s.remoteReceivedToolResult)
	sc.Step(`^the local tool "([^"]*)" ran once, on the local coddy, with (\{.*\})$`, s.localToolRanOnce)
	sc.Step(`^the local coddy's answer is "([^"]*)"$`, s.localAnswerIs)
	sc.Step(`^the remote model received the local coddy's system prompt$`, s.remoteReceivedLocalSystemPrompt)
	sc.Step(`^the remote executed no tool and holds no session$`, s.remoteExecutedNoToolAndHoldsNoSession)
	sc.Step(`^the local coddy receives the reasoning "([^"]*)" with a non-empty signature$`, s.receivesReasoning)
	sc.Step(`^the remote model received the reasoning signature "([^"]*)" unchanged$`, s.remoteReceivedSignature)
	sc.Step(`^the remote model did not receive the signature "([^"]*)"$`, s.remoteDidNotReceiveSignature)
	sc.Step(`^the call succeeds$`, s.callSucceeds)
	sc.Step(`^the remote answers "([^"]*)" before the provider is called$`, s.answersStaleRevision)
	sc.Step(`^the local coddy refreshes its view of "([^"]*)" and sends the request once more$`, s.refreshesAndSendsAgain)
	sc.Step(`^the provider was called once$`, s.providerCalledOnce)
	sc.Step(`^the remote model's provider received the reasoning level "([^"]*)"$`, s.providerReceivedLevel)
	sc.Step(`^the remote model received one image part after the text part, with the same MIME type and the same bytes$`, s.remoteReceivedImage)

	// The limit.
	sc.Step(`^(\d+) streams are requested from "([^"]*)"$`, s.streamsRequested)
	sc.Step(`^all (\d+) streams are running on the remote$`, s.allStreamsRunning)
	sc.Step(`^a sixth stream is requested from "([^"]*)"$`, s.sixthRequested)
	sc.Step(`^the remote answers 429 with the kind "([^"]*)" and a Retry-After of 1 second$`, s.answers429Busy)
	sc.Step(`^the provider was not called for the sixth$`, s.providerNotCalledForSixth)
	sc.Step(`^one of the 5 streams ends$`, s.oneOfFiveEnds)
	sc.Step(`^the sixth stream is served$`, s.sixthServed)
	sc.Step(`^the provider was called once for the local coddy's request$`, s.providerCalledOnceForLocal)

	registerRemoteModelPhase2Steps(sc, s)
}

// runRemoteModelSuite runs every scenario of one feature file; extra registers
// the steps only that file has.
func runRemoteModelSuite(t *testing.T, name, feature string, extra func(*godog.ScenarioContext, *remoteModelState)) {
	t.Helper()
	state := &remoteModelState{}
	suite := godog.TestSuite{
		Name: name,
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			registerRemoteModelSteps(sc, state)
			if extra != nil {
				extra(sc, state)
			}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				state.reset()
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{feature},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatalf("%s failed", name)
	}
}

func TestRemoteModelFeature(t *testing.T) {
	runRemoteModelSuite(t, "remote-model", "../../features/remote_model_provider.feature", nil)
}
