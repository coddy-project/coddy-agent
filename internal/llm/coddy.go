package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The coddy provider: a model that a remote `coddy serve` shares under an
// alias. The whole harness stays local - system prompt, rules, tool
// definitions, history and the tool-call loop - and each model turn is one
// stateless streamed POST to the remote, which runs the provider the row is
// configured with and answers with frames (coddy_wire.go). The remote is
// reached directly or through a swarm relay mount; api_base is its origin or
// the mount (https://relay/swarm/nodes/<node>), and the type appends
// /coddy/llm/....
//
// Complete is Stream with the chunks dropped: the wire has no blocking form,
// and models[].stream: false of the local row changes nothing on it.

const (
	// coddyBodyDeadline is the remote's own bound on reading a request body (P).
	coddyBodyDeadline = 30 * time.Second
	// coddyRequestBound is the client's bound on the exchange before the
	// remote's headers (R = P + 15 s), counted from the start of the upload: a
	// timer of the provider that cancels the request, since Go's
	// ResponseHeaderTimeout starts only after the body is written.
	coddyRequestBound = coddyBodyDeadline + 15*time.Second
	// coddyAnswerLimit bounds how much of a refused request's answer is read.
	coddyAnswerLimit = 64 << 10
)

type coddyProvider struct {
	model    string
	endpoint string
	apiKey   string
	hc       *http.Client

	maxTokens      int
	temperature    float64
	tempSet        bool
	effort         string
	retryBudget    time.Duration
	retryBudgetSet bool
	ledger         LimitLedger

	busyWait         time.Duration
	expectedRevision string
	refresh          func(ctx context.Context) (*ModelEntry, error)

	requestBound time.Duration
	// answerBound bounds the read of an answer that is not the stream.
	answerBound time.Duration
	clock       coddyClock
	jitter      func() float64

	mu sync.Mutex
	// view is the row as the remote last described it to this provider, set
	// after a stale_revision and used by the calls that follow.
	view *ModelEntry
}

func newCoddyProvider(p ProviderInput, hc *http.Client) (*coddyProvider, error) {
	endpoint, err := coddyEndpoint(p.BaseURL, CoddyCompletionsPath)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		return nil, fmt.Errorf("coddy provider: an HTTP client is required; build it with providerHTTPClient")
	}
	return &coddyProvider{
		model:            p.Model,
		endpoint:         endpoint,
		apiKey:           p.APIKey,
		hc:               hc,
		maxTokens:        p.MaxTokens,
		temperature:      p.Temperature,
		tempSet:          p.TemperatureSet,
		effort:           p.ReasoningEffort,
		retryBudget:      p.RetryBudget,
		retryBudgetSet:   p.RetryBudgetSet,
		ledger:           p.LimitLedger,
		busyWait:         p.BusyWait,
		expectedRevision: p.ExpectedRevision,
		refresh:          p.RefreshCapabilities,
		requestBound:     coddyRequestBound,
		answerBound:      coddyBodyDeadline,
		clock:            realCoddyClock(),
		jitter:           randomJitter,
	}, nil
}

// coddyEndpoint joins the route to the provider's api_base, which may be the
// remote's origin or a relay mount, with or without a trailing slash.
func coddyEndpoint(base, route string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("coddy provider needs api_base: the address of the remote coddy serve or of a swarm relay mount (https://relay/swarm/nodes/<node>)")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		shown := "the value given"
		if err == nil {
			shown = u.Redacted()
		}
		return "", fmt.Errorf("coddy provider: api_base must be an http(s) address (the remote coddy serve or a swarm relay mount), got %s", shown)
	}
	u.Path = strings.TrimRight(u.Path, "/") + route
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/") + route
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Complete is Stream with the chunks dropped.
func (p *coddyProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

// Stream sends one call and reads its frames. A call answered stale_revision
// refreshes the provider's view of the row and is sent once more; a second
// stale_revision ends it.
func (p *coddyProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	st := busyWaitStateFrom(ctx)
	if st == nil {
		// Used without the outer scope: this call gets a wait of its own.
		st = &busyWaitState{}
	}
	refreshed := false
	for {
		resp, err := p.attempt(ctx, st, p.buildRequest(messages, tools), onChunk)
		if err == nil || refreshed || !isStaleRevision(err) {
			return resp, err
		}
		if p.refresh == nil {
			return resp, err
		}
		entry, rerr := p.refresh(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("the remote's view of this model is stale and refreshing it failed: %w", rerr)
		}
		if entry == nil {
			// The alias is no longer listed: nothing to rebuild the request from.
			return resp, err
		}
		p.setView(entry)
		refreshed = true
	}
}

func isStaleRevision(err error) bool {
	return CoddyErrorKind(err) == WireKindInvalid && CoddyErrorCode(err) == WireCodeStaleRevision
}

func (p *coddyProvider) setView(e *ModelEntry) {
	cp := *e
	cp.ReasoningLevels = append([]string(nil), e.ReasoningLevels...)
	p.mu.Lock()
	p.view = &cp
	p.mu.Unlock()
}

func (p *coddyProvider) currentView() *ModelEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.view
}

// buildRequest projects the call onto the wire. Only the options the client
// set explicitly are sent. A request rebuilt from a refreshed view keeps only
// what the provider owns: a reasoning level the row no longer lists falls back
// by the rule the session applies, and image parts go when the row is no
// longer multimodal; the history is not re-projected.
func (p *coddyProvider) buildRequest(messages []Message, tools []ToolDefinition) WireRequest {
	effort := p.effort
	revision := p.expectedRevision
	if view := p.currentView(); view != nil {
		effort = fallbackReasoningEffort(effort, view)
		revision = view.Revision
		if !view.Multimodal {
			messages = withoutImageParts(messages)
		}
	}
	var opts WireOptions
	if p.maxTokens > 0 {
		v := p.maxTokens
		opts.MaxTokens = &v
	}
	// Like a local provider: a configured temperature stays off next to a
	// reasoning level, one the caller asked for is kept, zero included.
	if p.tempSet || (p.temperature > 0 && effort == "") {
		v := p.temperature
		opts.Temperature = &v
	}
	if effort != "" {
		v := effort
		opts.ReasoningEffort = &v
	}
	if p.retryBudgetSet {
		left := p.retryBudget
		if p.ledger != nil {
			left -= p.ledger.Spent()
		}
		ms := max(left, 0).Milliseconds()
		opts.RetryBudgetMS = &ms
	}
	if revision != "" {
		v := revision
		opts.ExpectedRevision = &v
	}
	return WireRequest{
		Protocol: CoddyProtocol,
		Model:    p.model,
		Messages: WireMessagesFromLLM(messages),
		Tools:    WireToolsFromLLM(tools),
		Options:  opts,
	}
}

// fallbackReasoningEffort is the level a session ends up with for a row: the
// level it asked for while the row still offers it ("off" included where the
// row allows it), else the row's default level, else none. It mirrors
// State.EffectiveReasoning applied to a listing entry.
func fallbackReasoningEffort(effort string, row *ModelEntry) string {
	var choices []string
	for _, lv := range row.ReasoningLevels {
		if lv != "off" {
			choices = append(choices, lv)
		}
	}
	if len(choices) == 0 {
		return ""
	}
	if row.AllowReasoningOff {
		choices = append(choices, "off")
	}
	for _, c := range choices {
		if c == effort && effort != "" {
			return effort
		}
	}
	for _, lv := range row.ReasoningLevels {
		if lv == row.ReasoningDefault && lv != "" {
			return lv
		}
	}
	return ""
}

// FallbackReasoningEffort is fallbackReasoningEffort for the agent: it builds a
// request of a coddy row from one listing record, level and revision together,
// and needs the rule the provider applies to a refreshed view.
func FallbackReasoningEffort(effort string, row *ModelEntry) string {
	return fallbackReasoningEffort(effort, row)
}

// withoutImageParts returns the messages with every attached part removed, as
// the HTTP intake does for a model that takes none; the caller's slice is left
// as it was.
func withoutImageParts(messages []Message) []Message {
	var out []Message
	for i, m := range messages {
		if len(m.ImageParts) == 0 {
			continue
		}
		if out == nil {
			out = append([]Message(nil), messages...)
		}
		out[i].ImageParts = nil
	}
	if out == nil {
		return messages
	}
	return out
}

// attempt is one exchange: the request until the remote admits it (waiting
// out busy), then the stream.
func (p *coddyProvider) attempt(ctx context.Context, st *busyWaitState, req WireRequest, onChunk func(StreamChunk)) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("coddy request: %w", err)
	}
	if len(body) > CoddyMaxRequestBytes {
		return nil, &coddyAPIError{kind: WireKindInvalid, status: http.StatusRequestEntityTooLarge,
			message: "the request is larger than the 32 MiB a shared model takes; the history has to be shorter"}
	}
	resp, err := p.admit(ctx, st, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return p.readFrames(resp.Body, onChunk)
}

// admit sends the request until the remote takes it. A busy answer is waited
// out under the call's one deadline; any other refusal is the answer. When the
// remote takes a call that had to wait, the countdown that was shown is ended
// with one more report, Admitted.
func (p *coddyProvider) admit(ctx context.Context, st *busyWaitState, body []byte) (*http.Response, error) {
	st.begin(p.clock.now(), p.busyWait)
	waited := false
	for {
		resp, err := p.post(ctx, body)
		if err != nil {
			return nil, err
		}
		st.noteRequest()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if !isEventStreamType(resp.Header.Get("Content-Type")) {
				return nil, p.unsupportedAnswer(ctx, resp)
			}
			now := p.clock.now()
			st.noteAdmitted(now)
			if notify := busyWaitNotifyFrom(ctx); waited && notify != nil {
				requests, spent, remaining := st.progress(now)
				notify(BusyWaitStatus{Budget: p.busyWait, Requests: requests, Waited: spent, Remaining: max(remaining, 0), Admitted: true})
			}
			return resp, nil
		}
		answer, rerr := p.readAnswer(ctx, resp, coddyAnswerLimit)
		_ = resp.Body.Close()
		if rerr != nil {
			return nil, rerr
		}
		cerr := p.finalizeAnswer(classifyCoddyAnswer(resp.StatusCode, resp.Header, answer))
		var api *coddyAPIError
		if !errors.As(cerr, &api) || api.kind != WireKindBusy {
			return nil, cerr
		}

		now := p.clock.now()
		index, requests, spent, remaining := st.noteBusy(now)
		if p.busyWait <= 0 || remaining <= 0 {
			return nil, &coddyBusyError{waited: spent, budget: p.busyWait, requests: requests, code: api.code}
		}
		waited = true
		sleep := min(busySleep(index, api.retryAfter, p.jitter()), remaining)
		if sleep <= 0 {
			// Not reachable while busySleep keeps its floor; a sleep that is
			// not a pause would spin on the remote until the budget ran out.
			sleep = remaining
		}
		if notify := busyWaitNotifyFrom(ctx); notify != nil {
			notify(BusyWaitStatus{Budget: p.busyWait, Requests: requests, Waited: spent, Remaining: remaining, RetryIn: sleep})
		}
		sleeper := p.clock.sleep
		if fn := busyWaitSleepFrom(ctx); fn != nil {
			sleeper = fn
		}
		if err := sleeper(ctx, sleep); err != nil {
			return nil, err
		}
	}
}

// finalizeAnswer turns a quota answer into the typed reset the agent's
// wait_for_limit_reset reads.
func (p *coddyProvider) finalizeAnswer(err error) error {
	var api *coddyAPIError
	if errors.As(err, &api) && api.kind == WireKindQuota {
		return quotaResetFromWire(api, p.clock.now())
	}
	return err
}

func isEventStreamType(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// readAnswer reads at most limit bytes of an answer that is not the stream: a
// refusal, an answer of a hop, a success that is no event stream. After the
// headers the request bound R is finished with and the client's byte guard
// watches event streams only, so a hop that sends a part of the body and stalls
// would hold the call until the context ends. The read has a bound of its own
// (answerBound): when it fires the answer is the same bound error a remote that
// never answered gets, and Stop is the context's error, never the bound's. Any
// other read error is returned too; nothing is classified from a body that was
// cut.
func (p *coddyProvider) readAnswer(ctx context.Context, resp *http.Response, limit int64) ([]byte, error) {
	bound := p.answerBound
	if bound <= 0 {
		bound = coddyBodyDeadline
	}
	var settled atomic.Bool
	timer := time.AfterFunc(bound, func() {
		if settled.CompareAndSwap(false, true) {
			_ = resp.Body.Close()
		}
	})
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if !settled.CompareAndSwap(false, true) {
		// The bound fired first and closed the body under the read.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("coddy request: %w", &coddyBoundError{after: bound})
	}
	timer.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("coddy request: reading the remote's answer (HTTP %d): %w", resp.StatusCode, err)
	}
	return body, nil
}

// unsupportedAnswer is a success that is not the stream of the wire: a remote
// built without the shared-model routes answers its web page, or a gateway
// answers for it. It is the same clear error as a listing that is not one, and
// is not retried.
func (p *coddyProvider) unsupportedAnswer(ctx context.Context, resp *http.Response) error {
	head, err := p.readAnswer(ctx, resp, 1024)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	what := "content type " + cleanRemoteText(resp.Header.Get("Content-Type"))
	switch {
	case looksLikeHTML(head):
		what = "a web page"
	case strings.TrimSpace(resp.Header.Get("Content-Type")) == "":
		what = "no content type"
	}
	return &coddyAPIError{kind: CoddyKindUnsupported, status: resp.StatusCode,
		message: "the remote does not offer shared models: it answered the completion with " + what + " instead of an event stream (is api_base a remote coddy serve with shared models, or a swarm relay mount?)"}
}

// post sends one request under the request bound R: a timer of the provider
// that cancels the exchange when the remote has not produced its headers in
// time, a stalled upload included. Once the headers are here the timer is
// finished with and the stream is bounded only by its own guards.
func (p *coddyProvider) post(ctx context.Context, body []byte) (*http.Response, error) {
	bound := p.requestBound
	if bound <= 0 {
		bound = coddyRequestBound
	}
	reqCtx, cancel := context.WithCancel(ctx)
	var settled atomic.Bool
	timer := time.AfterFunc(bound, func() {
		if settled.CompareAndSwap(false, true) {
			cancel()
		}
	})
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		timer.Stop()
		cancel()
		return nil, fmt.Errorf("coddy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// Liveness is counted in bytes, heartbeats included: an HTTP-level gzip on
	// top would only make the transport buffer what has to arrive as it is
	// written.
	req.Header.Set("Accept-Encoding", "identity")
	// A refused call must not upload the history: the remote decides before it
	// reads the body, and Go's transport holds the body back until it says so
	// (ExpectContinueTimeout of the shared transport). Where a hop strips the
	// header, a retry costs a re-upload.
	req.Header.Set("Expect", "100-continue")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.hc.Do(req)
	if !settled.CompareAndSwap(false, true) {
		// The bound fired first.
		if resp != nil {
			_ = resp.Body.Close()
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("coddy request: %w", &coddyBoundError{after: bound})
	}
	timer.Stop()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("coddy request: %w", redactURLError(err))
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// coddyPartial is what the chunks of a stream built, kept for the failure
// that cuts it: all text and reasoning, and the tool calls whose tool_call
// chunk arrived. Calls that were only named or streamed are dropped, their
// arguments may be cut mid-JSON.
type coddyPartial struct {
	text      strings.Builder
	reasoning strings.Builder
	calls     []ToolCall
	input     int
	output    int
}

func (a *coddyPartial) add(c StreamChunk) {
	a.text.WriteString(c.TextDelta)
	a.reasoning.WriteString(c.ReasoningDelta)
	if c.ToolCall != nil {
		a.calls = append(a.calls, *c.ToolCall)
	}
	if c.InputTokens > 0 {
		a.input = c.InputTokens
	}
	if c.OutputTokens > 0 {
		a.output = c.OutputTokens
	}
}

// response is the partial answer, or nil when the chunks built nothing.
func (a *coddyPartial) response() *Response {
	if strings.TrimSpace(a.text.String()) == "" && strings.TrimSpace(a.reasoning.String()) == "" && len(a.calls) == 0 {
		return nil
	}
	return &Response{
		Content:      a.text.String(),
		Reasoning:    a.reasoning.String(),
		ToolCalls:    append([]ToolCall(nil), a.calls...),
		InputTokens:  a.input,
		OutputTokens: a.output,
	}
}

// readFrames reads the stream: chunks are progress, and exactly one terminal
// event is consumed - the final frame is the result, an error frame the
// failure - anything after it is discarded. EOF without one is a truncated
// stream and a transport failure.
func (p *coddyProvider) readFrames(body io.Reader, onChunk func(StreamChunk)) (*Response, error) {
	scanner := newSSEScanner(&frameLimitReader{r: body, limit: CoddyMaxFrameBytes})
	var partial coddyPartial
	// emitted is the client's own count of the chunks it passed on, never the
	// frame's field. With no callback nothing reached a caller (Complete), and
	// a failure midway is as safe to repeat as one before it.
	emitted := 0

	// fail returns the partial answer next to the error, as every provider
	// does for a stream cut after output.
	fail := func(err error) (*Response, error) { return partial.response(), err }

	for scanner.Next() {
		payload := bytes.TrimSpace(scanner.Frame().data)
		if len(payload) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &head); err != nil {
			return fail(undecodableFrame(payload, err, emitted > 0))
		}
		switch head.Type {
		case WireTypeChunk:
			var c WireChunk
			if err := json.Unmarshal(payload, &c); err != nil {
				return fail(undecodableFrame(payload, err, emitted > 0))
			}
			chunk := c.ToStreamChunk()
			partial.add(chunk)
			if onChunk != nil {
				emitted++
				onChunk(chunk)
			}
		case WireTypeFinal:
			var f WireFinal
			if err := json.Unmarshal(payload, &f); err != nil {
				return fail(undecodableFrame(payload, err, emitted > 0))
			}
			return f.ToResponse(), nil
		case WireTypeError:
			var e WireError
			if err := json.Unmarshal(payload, &e); err != nil {
				return fail(undecodableFrame(payload, err, emitted > 0))
			}
			return fail(p.finalizeAnswer(coddyAPIErrorFromWire(e, nil, emitted > 0)))
		}
		// A frame of another type is not this version's business.
	}

	switch err := scanner.Err(); {
	case errors.Is(err, errCoddyFrameTooLarge):
		return fail(&coddyAPIError{kind: CoddyKindProtocol,
			message: "the remote sent an event larger than 16 MiB; the call was dropped"})
	case err != nil:
		// A reset, an unexpected EOF, an http2 stream error, the stall guard
		// of the HTTP client, Stop: the emitted flag keeps the wrapper from
		// repeating a call whose chunks a caller already saw.
		return fail(fmt.Errorf("coddy stream: %w", &streamTransportError{cause: err, emitted: emitted > 0}))
	}
	return fail(fmt.Errorf("coddy stream: %w", &streamTruncatedError{emitted: emitted > 0}))
}

// undecodableFrame is a frame that is not what the wire says: a corrupt stream
// and final, unless the JSON merely stops short, which is a cut like any other.
func undecodableFrame(payload []byte, cause error, emitted bool) error {
	und := &streamUndecodableError{snippet: streamErrorSnippet(payload), cause: cause}
	if trunc := streamDecodeTruncation(und, emitted, len(payload)); trunc != nil {
		return fmt.Errorf("coddy stream: %w", trunc)
	}
	return fmt.Errorf("coddy stream: %w", und)
}

var errCoddyFrameTooLarge = errors.New("an event of the stream is larger than the limit")

// frameLimitReader bounds the size of one SSE event as it is read: the bytes
// since the last blank line may not pass limit, however many lines they are
// split into, so a remote cannot grow the client's memory without end.
type frameLimitReader struct {
	r       io.Reader
	limit   int
	size    int
	lineLen int
	err     error
}

func (l *frameLimitReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.r.Read(p)
	for i := 0; i < n; i++ {
		switch p[i] {
		case '\n':
			if l.lineLen == 0 {
				l.size = 0
			} else {
				l.size++
			}
			l.lineLen = 0
		case '\r':
			l.size++
		default:
			l.lineLen++
			l.size++
		}
		if l.size > l.limit {
			l.err = errCoddyFrameTooLarge
			return i, l.err
		}
	}
	return n, err
}
