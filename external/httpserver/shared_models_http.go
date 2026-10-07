//go:build http

package httpserver

// The routes a Coddy shares its models[] rows through (docs/plans/remote-model-provider.md):
//
//	GET  /coddy/llm/models        the shared rows, under their aliases
//	POST /coddy/llm/completions   one stateless model call, answered as an event stream
//
// The harness stays on the calling Coddy; this side only runs the provider the
// row is configured with. Nothing here resolves a session, runs a hook, reads a
// rule or writes to disk, and no turn lock is taken.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	sharedModelsPattern      = "GET /coddy/llm/models"
	sharedUsagePattern       = "GET /coddy/llm/models/{alias}/usage"
	sharedCompletionsPattern = "POST /coddy/llm/completions"
)

// isSharedLLMPattern reports the routes a shared-model token opens, and nothing
// else: the listing, the usage of one alias (reserved for the second phase) and
// the completions.
func isSharedLLMPattern(pattern string) bool {
	switch pattern {
	case sharedModelsPattern, sharedUsagePattern, sharedCompletionsPattern:
		return true
	}
	return false
}

// registerSharedModelRoutes mounts the routes. They are always registered: the
// handler decides by the live policy of the request, so a hot reload takes
// effect without a restart.
func (s *Server) registerSharedModelRoutes() {
	s.mux.HandleFunc(sharedModelsPattern, s.llmModelsGet)
	s.mux.HandleFunc(sharedUsagePattern, s.llmUsageGet)
	s.mux.HandleFunc(sharedCompletionsPattern, s.llmCompletionsPost)
}

// sharedTimings are the three timers of a call, the defaults unless a test set
// shorter ones.
func (s *Server) sharedTimings() (body, write, heartbeat time.Duration) {
	body, write, heartbeat = sharedBodyDeadline, sharedWriteDeadline, sharedHeartbeat
	if s.sharedBodyP > 0 {
		body = s.sharedBodyP
	}
	if s.sharedWriteW > 0 {
		write = s.sharedWriteW
	}
	if s.sharedHB > 0 {
		heartbeat = s.sharedHB
	}
	return body, write, heartbeat
}

func (s *Server) sharedClockNow() sharedClock {
	if s.sharedClk != nil {
		return s.sharedClk
	}
	return realSharedClock{}
}

// writeSharedAuthRefusal is the answer of a node that has no credential of any
// class and has not been told allow_insecure: it offers no shared model to
// anybody. Once a credential exists the gate's own 401 answers instead.
func writeSharedAuthRefusal(w http.ResponseWriter) {
	writeSharedError(w, http.StatusForbidden, llm.WireError{
		Kind: llm.WireKindAuth,
		Message: "shared models need authentication: this remote has no credential configured, so it offers its shared models to nobody; " +
			"its operator sets httpserver.shared_models.tokens (or httpserver.auth_token), or httpserver.allow_insecure for an API that is open on purpose",
	})
}

// sharedRow is the listing row of a shared model: everything a client can
// observe about it, under the alias, with a revision that changes when any of
// it, or the model the alias points at, changes. The revision is a keyed hash,
// so the upstream model id cannot be recovered from it.
func (s *Server) sharedRow(cfg *config.Config, ent *config.ModelEntry) llm.WireModelRow {
	var levels []string
	for _, lv := range cfg.ReasoningLevelsFor(ent) {
		if lv != config.ReasoningOff {
			levels = append(levels, lv)
		}
	}
	row := llm.WireModelRow{
		ID:                ent.SharedAlias(),
		MaxContextTokens:  s.contextWindowFor(cfg, ent.Model),
		Multimodal:        ent.Multimodal,
		ReasoningLevels:   levels,
		ReasoningDefault:  cfg.DefaultReasoningLevelFor(ent),
		AllowReasoningOff: cfg.ReasoningOffOffered(ent),
	}
	row.Revision = llm.Revision(s.sharedModelsKey(),
		"row", row.ID,
		strconv.Itoa(row.MaxContextTokens),
		strconv.FormatBool(row.Multimodal),
		strings.Join(row.ReasoningLevels, ","),
		row.ReasoningDefault,
		strconv.FormatBool(row.AllowReasoningOff),
		"model", ent.Model,
	)
	return row
}

// sharedModelTag is the tag of the model the alias points at, which the
// reasoning signature envelope carries.
func (s *Server) sharedModelTag(ent *config.ModelEntry) string {
	return llm.ModelTag(s.sharedModelsKey(), ent.Model)
}

// awaitSharedWindows waits a bounded moment for the context windows a listing
// reads from the remote's own provider, the same wait GET /v1/models makes, so
// the listing and the revision check of a call see one number.
func (s *Server) awaitSharedWindows(ctx context.Context, cfg *config.Config, entries []*config.ModelEntry) {
	if s.mgr == nil || len(entries) == 0 {
		return
	}
	refs := make([]string, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, e.Model)
	}
	s.mgr.AwaitContextWindows(ctx, cfg, refs, session.ContextWindowWait)
}

// llmModelsGet answers GET /coddy/llm/models: the shared rows only, under their
// aliases. There is no stream field (the wire is always a stream) and no
// upstream model id or provider name anywhere.
func (s *Server) llmModelsGet(w http.ResponseWriter, r *http.Request) {
	pol := s.authSnapshot(r)
	if pol.anonymousSharedRefused() {
		writeSharedAuthRefusal(w)
		return
	}
	cfg := pol.cfg
	entries := cfg.SharedModelEntries()
	s.awaitSharedWindows(r.Context(), cfg, entries)
	rows := make([]llm.WireModelRow, 0, len(entries))
	for _, ent := range entries {
		rows = append(rows, s.sharedRow(cfg, ent))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(llm.WireListing{Protocol: llm.CoddyProtocol, Data: rows})
}

// llmUsageGet reserves GET /coddy/llm/models/{alias}/usage, which the second
// phase fills with a projection of the account usage. Until then every alias,
// known or not, is a 404 that names nothing.
func (s *Server) llmUsageGet(w http.ResponseWriter, r *http.Request) {
	pol := s.authSnapshot(r)
	if pol.anonymousSharedRefused() {
		writeSharedAuthRefusal(w)
		return
	}
	writeSharedError(w, http.StatusNotFound, sharedInvalid("not_found", "this remote does not report the usage of a shared model"))
}

// sharedCallerKey is the limiter key of a request: the credential that passed
// the gate, never what the client merely sent. That is the digest of the bearer
// when the snapshot accepts it (any token class), else of the session cookie of
// a signed-in browser that the sessions store knows, else the one anonymous key.
// A node that is open on purpose has no credential to tell callers apart by, so
// a different bearer or a made-up cookie on each request buys no budget of its
// own.
func (s *Server) sharedCallerKey(r *http.Request, pol *authPolicy) string {
	if pol == nil || !pol.enabled {
		return sharedAnonymousKey
	}
	if t := bearerToken(r); acceptBearer(pol.tokens, t) || acceptBearer(pol.sharedTokens, t) {
		return sharedKeyFor(t)
	}
	if _, ok := s.sessionFromRequest(r, pol.login); ok {
		if c, err := r.Cookie(sessionCookieName(r)); err == nil && c.Value != "" {
			return sharedKeyFor("cookie:" + c.Value)
		}
	}
	if _, ok := s.telegramSessionFromRequest(r); ok {
		if c, err := r.Cookie(tgSessionCookieName(r)); err == nil && c.Value != "" {
			return sharedKeyFor("cookie:" + c.Value)
		}
	}
	return sharedAnonymousKey
}

// sharedCallLog is what the log line of a call records: the alias, the kind of
// the outcome, the status, the duration and a request id. Never content, and a
// raw upstream text only at debug level.
type sharedCallLog struct {
	alias   string
	kind    string
	status  int
	cause   string
	emitted int
}

// newSharedRequestID is a short random id to find a call in the log by.
func newSharedRequestID() string {
	return strings.ToLower(rand.Text()[:12])
}

// llmCompletionsPost answers POST /coddy/llm/completions: one stateless model
// call, always an event stream. A request that is refused before the stream
// starts gets a JSON error object instead, and no refusal reads more of the
// body than it must.
func (s *Server) llmCompletionsPost(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	reqID := newSharedRequestID()
	w.Header().Set("X-Coddy-Request-ID", reqID)
	call := sharedCallLog{alias: "-"}
	defer func() { s.logSharedCall(&call, reqID, time.Since(started)) }()
	refuse := func(status int, e llm.WireError) {
		call.kind, call.status = e.Kind, status
		writeSharedError(w, status, e)
	}
	// The body deadline P runs from here: whatever refuses the request before
	// the body is read leaves the deadline armed, so the server's own attempt to
	// drain an unread body before it answers (net/http does that for a small one)
	// ends at P at the latest and never pins the connection. A successful read
	// clears it. The connection is not told to close instead: over the swarm
	// tunnel, "Connection: close" would end the whole tunnel.
	deadlineSet := s.armSharedBodyDeadline(w)

	// The policy is the gate's snapshot of this request and the configuration the
	// one it was taken from: the rest of the call never asks the live
	// configuration anything.
	pol := s.authSnapshot(r)
	if pol.anonymousSharedRefused() {
		call.kind, call.status = llm.WireKindAuth, http.StatusForbidden
		writeSharedAuthRefusal(w)
		return
	}
	cfg := pol.cfg
	if cfg == nil {
		refuse(http.StatusInternalServerError, sharedSetupFailure())
		return
	}

	// The slot is taken right after authentication and before the body is read,
	// so a busy remote answers without reading up to 32 MiB. Every exit below
	// frees it through the one guarded release.
	limit := cfg.HTTPServer.EffectiveSharedMaxStreams()
	slot, ok := s.sharedLimit.acquire(s.sharedCallerKey(r, pol), limit)
	if !ok {
		refuse(http.StatusTooManyRequests, llm.WireError{
			Kind:    llm.WireKindBusy,
			Message: "the remote has no free stream slot for this credential: " + strconv.Itoa(limit) + " calls are running",
		})
		return
	}
	defer slot.release()

	if r.ContentLength > llm.CoddyMaxRequestBytes {
		refuse(http.StatusRequestEntityTooLarge, sharedTooLarge())
		return
	}
	body, rerr := s.readSharedBody(w, r, deadlineSet)
	if rerr != nil {
		switch {
		case errors.As(rerr, new(*http.MaxBytesError)):
			refuse(http.StatusRequestEntityTooLarge, sharedTooLarge())
		case isSharedTimeout(rerr):
			bodyP, _, _ := s.sharedTimings()
			refuse(http.StatusRequestTimeout, sharedInvalid("body_timeout", "the request body did not arrive within "+bodyP.String()))
		default:
			refuse(http.StatusBadRequest, sharedInvalid("bad_body", "the request body could not be read"))
		}
		return
	}
	// A request that is small on the wire can still decode into gigabytes: an
	// empty object is three bytes with its comma and a message of the wire about
	// a hundred and thirty, so the number of objects and arrays is bounded before
	// the body is held or decoded.
	if jsonOpenersExceed(body, sharedMaxJSONElements) {
		refuse(http.StatusRequestEntityTooLarge, sharedTooManyElements())
		return
	}
	slot.hold(body)
	var req llm.WireRequest
	err := json.Unmarshal(body, &req)
	slot.drop()
	if err != nil {
		refuse(http.StatusBadRequest, sharedInvalid("bad_json", "the request is not valid JSON of the shared-model protocol"))
		return
	}

	if req.Protocol != llm.CoddyProtocol {
		refuse(http.StatusBadRequest, sharedInvalid(llm.WireCodeProtocolMismatch,
			fmt.Sprintf("this remote speaks shared-model protocol %d and the request speaks %d: update the older Coddy", llm.CoddyProtocol, req.Protocol)))
		return
	}
	ent := cfg.FindSharedModel(req.Model)
	if ent == nil {
		// The alias is the only name that finds a row: a provider/model selector,
		// a private model and a missing one answer alike, and the answer repeats
		// nothing the client sent.
		refuse(http.StatusNotFound, sharedInvalid("unknown_model", "no shared model is offered under that name"))
		return
	}
	alias := ent.SharedAlias()
	call.alias = alias

	// A client that reads an older row than the remote has is answered before
	// anything is built: no upstream request is made for it.
	if exp := req.Options.ExpectedRevision; exp != nil && *exp != "" {
		s.awaitSharedWindows(r.Context(), cfg, []*config.ModelEntry{ent})
		if current := s.sharedRow(cfg, ent).Revision; current != *exp {
			e := sharedInvalid(llm.WireCodeStaleRevision, "the model changed on the remote since this client read its listing: read the listing again")
			e.Revision = current
			refuse(http.StatusBadRequest, e)
			return
		}
	}

	opts, werr := sharedCallOptions(cfg, ent, req.Options)
	if werr != nil {
		refuse(http.StatusBadRequest, *werr)
		return
	}
	msgs, werr := sharedMessages(req.Messages, s.sharedModelTag(ent))
	if werr != nil {
		refuse(http.StatusBadRequest, *werr)
		return
	}
	tools := llm.WireToolsToLLM(req.Tools)

	// The factory of the direct path, so the provider's proxy, its resilient
	// wrapper and its credential are the remote's own.
	provider, err := s.sharedProvider(cfg, ent.Model, opts)
	if err != nil {
		s.log.Error("shared model call: the provider could not be built", "alias", alias, "request_id", reqID, "error", err)
		refuse(http.StatusBadGateway, sharedSetupFailure())
		return
	}
	s.runSharedCall(w, r, cfg, ent, provider, msgs, tools, &call, reqID)
}

// sharedSetupFailure is the answer when the remote cannot set a model up: its
// own configuration is at fault, which the log says and the client is not told.
func sharedSetupFailure() llm.WireError {
	return llm.WireError{
		Kind: llm.WireKindUpstream, Cause: llm.WireCauseStatus,
		Message: "the remote could not set up this model; its operator finds the reason in the remote's log",
	}
}

// sharedTooLarge is the answer to a request above the body limit.
func sharedTooLarge() llm.WireError {
	return sharedInvalid("request_too_large", "the request is larger than the 32 MiB a shared model takes; the history has to be shorter")
}

// sharedTooManyElements is the answer to a request that opens more objects and
// arrays than a history needs.
func sharedTooManyElements() llm.WireError {
	return sharedInvalid("request_too_large", "the request holds too many elements; the history has to be shorter")
}

// logSharedCall writes the one line a call leaves in the log.
func (s *Server) logSharedCall(call *sharedCallLog, reqID string, took time.Duration) {
	attrs := []any{
		"alias", call.alias, "kind", call.kind, "status", call.status,
		"duration_ms", took.Milliseconds(), "request_id", reqID,
	}
	if call.cause != "" {
		attrs = append(attrs, "cause", call.cause)
	}
	if call.emitted > 0 {
		attrs = append(attrs, "emitted", call.emitted)
	}
	// A busy refusal is flow control, not a failure: a client waiting for a slot
	// is refused about once a second.
	if call.kind == sharedOutcomeOK || call.kind == llm.WireKindBusy {
		s.log.Info("shared model call", attrs...)
		return
	}
	s.log.Warn("shared model call did not complete", attrs...)
}

// sharedOutcomeOK is the kind a call that ended with its final frame logs.
const sharedOutcomeOK = "ok"

// sharedOutcomeGone and sharedOutcomeWrite are what a call logs when nobody was
// left to tell: the client disconnected, or a write failed or ran out of time.
const (
	sharedOutcomeGone  = "client_gone"
	sharedOutcomeWrite = "write_failed"
)

// armSharedBodyDeadline starts the body deadline P on the connection's own read
// deadline, and reports whether the writer could set one.
func (s *Server) armSharedBodyDeadline(w http.ResponseWriter) bool {
	bodyP, _, _ := s.sharedTimings()
	return http.NewResponseController(w).SetReadDeadline(time.Now().Add(bodyP)) == nil
}

// readSharedBody reads the request body under the size limit and the body
// deadline P, which armSharedBodyDeadline started on the connection or, for a
// writer that cannot set one, a timer that closes the body when the time is up.
// A read that succeeded clears the deadline; one that failed leaves it where it
// stands.
func (s *Server) readSharedBody(w http.ResponseWriter, r *http.Request, deadlineSet bool) ([]byte, error) {
	var timedOut atomic.Bool
	if !deadlineSet {
		bodyP, _, _ := s.sharedTimings()
		t := time.AfterFunc(bodyP, func() {
			timedOut.Store(true)
			_ = r.Body.Close()
		})
		defer t.Stop()
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, llm.CoddyMaxRequestBytes))
	switch {
	case err != nil && timedOut.Load():
		err = os.ErrDeadlineExceeded
	case err == nil && deadlineSet:
		_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	}
	return body, err
}

// sharedMaxJSONElements is the number of objects and arrays (every `{` and `[`
// outside a string) one request body may open. A real history stays orders of
// magnitude below it: a message with a tool call opens about five, a tool
// definition with a nested schema about ten.
const sharedMaxJSONElements = 1 << 19

// jsonOpenersExceed reports whether body opens more than limit objects and arrays,
// counting in one pass over the bytes and stopping at the first one over. A
// brace or a bracket inside a string, an escaped quote included, is text and
// does not count. The body is not checked for being JSON: the decoder does
// that, and only for a body that passed this.
func jsonOpenersExceed(body []byte, limit int) bool {
	opened := 0
	inString, escaped := false, false
	for _, c := range body {
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			opened++
			if opened > limit {
				return true
			}
		}
	}
	return false
}

// isSharedTimeout reports a read that ran out of time.
func isSharedTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// sharedMaxRetryBudgetMS is the longest retry budget a call takes: the eight
// hours that bound a whole call as well.
const sharedMaxRetryBudgetMS = int64(8 * time.Hour / time.Millisecond)

// sharedCallOptions turns the options the client set into the options of the
// provider call, applying the row's own rules; the retry budget the client still
// has travels in them. An option the client left out stays as the row is configured: an
// omitted reasoning_effort is the row's reasoning_default, max_tokens is the
// smaller of the client's value and the row's own, and a temperature the client
// never set never reaches the provider's validation. The refusal names the
// alias, never the selector.
func sharedCallOptions(cfg *config.Config, ent *config.ModelEntry, wo llm.WireOptions) (llm.RequestOptions, *llm.WireError) {
	alias := ent.SharedAlias()
	fail := func(format string, args ...any) (llm.RequestOptions, *llm.WireError) {
		e := sharedInvalid("invalid_option", fmt.Sprintf(format, args...))
		return llm.RequestOptions{}, &e
	}
	providerType := ""
	if prov := cfg.FindProvider(ent.ProviderName()); prov != nil {
		providerType = prov.Type
	}

	var opts llm.RequestOptions
	if wo.MaxTokens != nil {
		v := *wo.MaxTokens
		if v < 1 {
			return fail("shared model %q: max_tokens must be a positive integer", alias)
		}
		if ceiling := ent.MaxTokens; ceiling > 0 && v > ceiling {
			v = ceiling
		}
		opts.MaxTokens = &v
	}
	if wo.Temperature != nil {
		v := *wo.Temperature
		opts.Temperature = &v
	}
	level := ""
	if wo.ReasoningEffort != nil {
		level = strings.TrimSpace(*wo.ReasoningEffort)
	}
	switch {
	case level == "":
		opts.ReasoningEffort = cfg.DefaultReasoningLevelFor(ent)
	case reasoningLevelOffered(cfg, ent, level):
		opts.ReasoningEffort = level
	case level == config.ReasoningOff && len(cfg.ReasoningLevelsFor(ent)) > 0:
		return fail("shared model %q does not allow the reasoning level off", alias)
	default:
		offered := cfg.ReasoningChoicesFor(ent)
		if len(offered) == 0 {
			return fail("shared model %q offers no reasoning levels, so reasoning_effort %q cannot apply", alias, level)
		}
		return fail("reasoning_effort %q is not offered by shared model %q (offered: %s)", level, alias, strings.Join(offered, ", "))
	}
	if err := opts.Validate(providerType); err != nil {
		// The provider's own text names its type and its backend, which the
		// answer of a remote never does.
		return fail("shared model %q cannot take an option of this request; leave max_tokens, temperature and reasoning_effort out to use the model's own", alias)
	}

	if wo.RetryBudgetMS != nil {
		// The client's number is clamped before it becomes a duration: past about
		// 9.2e12 ms the product would wrap.
		d := time.Duration(min(max(*wo.RetryBudgetMS, 0), sharedMaxRetryBudgetMS)) * time.Millisecond
		opts.RetryBudget = &d
	}
	return opts, nil
}

// sharedMessages converts the wire history into what the provider sees, and
// opens the signature envelopes for the model the alias points at now: a
// signature of another model, or one that never had an envelope, is dropped and
// the call goes on without it, never an error. An image part is passed as it is
// whether or not the row lists multimodal: the provider's own refusal comes back
// as an invalid error.
func sharedMessages(wire []llm.WireMessage, modelTag string) ([]llm.Message, *llm.WireError) {
	if len(wire) == 0 {
		e := sharedInvalid("no_messages", "messages are required")
		return nil, &e
	}
	for i := range wire {
		switch llm.Role(wire[i].Role) {
		case llm.RoleSystem, llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
		default:
			e := sharedInvalid("bad_role", fmt.Sprintf("messages[%d] has an unknown role", i))
			return nil, &e
		}
	}
	msgs := llm.WireMessagesToLLM(wire)
	llm.OpenMessageSignatures(msgs, modelTag)
	return msgs, nil
}

// sharedProvider builds the provider of one call through the factory the direct
// completion path uses, the caller's retry budget carried in the options.
func (s *Server) sharedProvider(cfg *config.Config, selector string, opts llm.RequestOptions) (llm.Provider, error) {
	mk := s.makeLLMFromYAML
	if mk == nil {
		mk = defaultMakeLLMFromYAML
	}
	return mk(cfg, selector, opts)
}

// runSharedCall runs the provider and tells the client what happened as an event
// stream: the headers and a first heartbeat, then chunk frames, then exactly one
// terminal frame. The slot stays taken by the caller's defer until this returns.
func (s *Server) runSharedCall(w http.ResponseWriter, r *http.Request, cfg *config.Config, ent *config.ModelEntry, provider llm.Provider, msgs []llm.Message, tools []llm.ToolDefinition, call *sharedCallLog, reqID string) {
	// What the guards read of the row: whether it streams and the type of its
	// provider. cfg.ResolveLLM would also run the provider's credential helper,
	// which the provider's own build has just done once for this call.
	var rm *config.ResolvedLLM
	if prov := cfg.FindProvider(ent.ProviderName()); prov != nil {
		rm = &config.ResolvedLLM{ProviderName: prov.Name, ProviderType: prov.Type, Stream: ent.EffectiveStream()}
	}
	blocking := rm != nil && !rm.Stream

	// The call's context: cancelled by the client's disconnect, by a failed write
	// and by the stall guard.
	callCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	timedOut := func() bool { return false }
	if blocking {
		// A blocking row is not stall-guarded: its answer arrives in one piece. It
		// is bounded as a whole, so a hung one releases its slot.
		var cancelBound context.CancelFunc
		callCtx, cancelBound = context.WithTimeout(callCtx, cfg.HTTPServer.EffectiveSharedMaxCall())
		defer cancelBound()
		bounded := callCtx
		timedOut = func() bool { return errors.Is(bounded.Err(), context.DeadlineExceeded) }
	}

	_, writeW, heartbeat := s.sharedTimings()
	clk := s.sharedClockNow()
	stream := newSharedStream(w, clk, writeW, cancel)
	rc := http.NewResponseController(w)
	defer func() {
		// The next request on a kept-alive connection must not inherit a deadline
		// that has passed.
		_ = rc.SetWriteDeadline(time.Time{})
		_ = rc.SetReadDeadline(time.Time{})
	}()

	// Headers and the first heartbeat go out before the provider is called.
	if err := stream.start(); err != nil {
		call.kind, call.status = sharedOutcomeWrite, 0
		return
	}
	stopHeartbeat := stream.runHeartbeat(r.Context(), heartbeat)
	defer stopHeartbeat()

	// The remote owns model progress: S is armed now, before the first byte, and
	// every chunk re-arms it. A blocking row has none.
	var stallAfter time.Duration
	if rm != nil {
		stallAfter = streamIdleTimeout(cfg, rm)
	}
	guard := startSharedStallGuard(clk, stallAfter, cancel)
	defer guard.stop()
	resp, err := provider.Stream(callCtx, msgs, tools, func(c llm.StreamChunk) {
		guard.progress()
		_ = stream.chunk(c)
	})
	guard.stop()
	call.emitted = stream.emittedChunks()

	switch {
	case r.Context().Err() != nil:
		call.kind = sharedOutcomeGone
		return
	case stream.writeFailure() != nil:
		call.kind = sharedOutcomeWrite
		return
	}

	if err == nil && resp != nil {
		final := llm.WireFinalFromResponse(llm.SealResponseSignature(resp, s.sharedModelTag(ent)))
		if ferr := stream.finish(final); ferr != nil {
			call.kind = sharedOutcomeWrite
			return
		}
		call.kind, call.status = sharedOutcomeOK, http.StatusOK
		return
	}
	if err == nil {
		err = errors.New("the provider returned neither a response nor an error")
	}
	wire := classifySharedFailure(err, guard.stalled(), timedOut(), call.emitted)
	// What the provider said stays in the log, at debug level: it can quote the
	// request, and it carries the provider's name and address.
	s.log.Debug("shared model call: the provider failed", "alias", ent.SharedAlias(), "request_id", reqID, "error", err.Error())
	_ = stream.finish(wire)
	call.kind, call.status, call.cause = wire.Kind, wire.Status, wire.Cause
}
