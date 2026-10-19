package llm

// The acp provider: a local Agent Client Protocol agent (claude-agent-acp,
// codex-acp, opencode acp, coddy acp, ...) served as a model. The agent is a
// whole agent: it brings its own instructions and tools and runs them in the
// session's workspace; Coddy sees a model that answers with text and never
// calls a Coddy tool. Design: docs/plans/acp-provider.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	// acpDefaultModel keeps the agent's own model.
	acpDefaultModel = "default"
	// acpCloseTimeout bounds closing a session.
	acpCloseTimeout = 5 * time.Second
	// acpEmptyAnswer is the answer of a prompt that completed with no text.
	// Without it the loop would take the step for an empty answer and send
	// the agent the same prompt again.
	acpEmptyAnswer = "(the agent finished without a message)"
)

// Bounds of a prompt, variables so the tests can shorten them.
var (
	// acpCancelWait is how long a cancelled prompt waits for the agent to
	// confirm the cancellation before the prompt is abandoned.
	acpCancelWait = 10 * time.Second
	// acpAbandonWait is how long an abandoned prompt may still take to end
	// before its agent process is killed; the conversation stays locked
	// until then, so no retry runs next to it.
	acpAbandonWait = 20 * time.Second
	// acpSilence is how long a prompt may send nothing (time waiting on a
	// person not counted) before it is taken for stuck.
	acpSilence = 30 * time.Minute
)

type acpProvider struct {
	in ProviderInput
}

func newACPProvider(in ProviderInput) *acpProvider { return &acpProvider{in: in} }

// Complete runs the prompt and returns the whole answer.
func (p *acpProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return p.Stream(ctx, messages, tools, nil)
}

// Stream sends the agent what its session does not hold yet and streams the
// answer. tools is ignored: the agent has its own.
func (p *acpProvider) Stream(ctx context.Context, messages []Message, _ []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	conv := ConversationFrom(ctx)
	if err := p.admit(conv); err != nil {
		return nil, err
	}
	spec, err := resolveACPSpec(p.in, conv.SessionID, conv.CWD)
	if err != nil {
		return nil, err
	}
	if p.in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.in.Timeout)
		defer cancel()
	}
	sessionCWD := spec.sessionCWD
	if strings.TrimSpace(conv.CWD) != "" && strings.TrimSpace(p.in.ACP.CWD) == "" {
		sessionCWD = conv.CWD
	}
	if abs, err := filepath.Abs(sessionCWD); err == nil {
		sessionCWD = abs
	}
	history, ephemeral := acpHistory(messages)

	convKey := conv.SessionID + "\x00" + sessionCWD
	entry, release, c, err := p.lockConversation(ctx, spec, convKey)
	if err != nil {
		return nil, err
	}
	defer release()
	proc := entry.proc
	unlock := true
	defer func() {
		if unlock {
			c.lastUsed = time.Now()
			c.unlock()
		}
	}()

	// A prompt whose outcome is unknown and whose session is gone with it (an
	// abandoned prompt, a process that died after the agent acted) is still
	// never sent again, though no binding remembers it.
	lostKey := spec.name + "\x00" + convKey
	if c.sessionID == "" && acpAgents.lostUncertain(lostKey, history) {
		return nil, &ACPError{Kind: ACPErrorStalled, Agent: spec.label(), Message: "the agent's last prompt in this conversation ended without a clear outcome and may have run; it is not sent again. Send a new message to go on"}
	}

	plan, ok, err := c.planPrompt(history)
	if err != nil {
		return nil, err
	}
	if ok && !proc.hasSession(c.sessionID) {
		ok = false
	}
	sessionID := c.sessionID
	if ok {
		if err := p.applySelection(ctx, proc, sessionID, conv); err != nil {
			var acpErr *ACPError
			if !errors.As(err, &acpErr) || (acpErr.Kind != ACPErrorSelect && acpErr.Kind != ACPErrorRPC) {
				// A cancellation or a dead agent is not the session's fault.
				return nil, err
			}
			// A session that cannot be brought back to the wanted state is
			// replaced by a new one.
			proc.log.Debug("acp session left for a new one", "agent", spec.label(), "error", err)
			ok = false
		}
	}
	if !ok {
		if c.sessionID != "" {
			proc.closeSession(c.sessionID)
		}
		c.reset()
		sessionID, err = proc.newSession(ctx, sessionCWD)
		if err != nil {
			return nil, err
		}
		plan = acpPromptPlan{send: history, fresh: true}
		if err := p.applySelection(ctx, proc, sessionID, conv); err != nil {
			proc.closeSession(sessionID)
			return nil, err
		}
	}
	c.sessionID = sessionID

	blocks := acpPromptBlocks(plan, ephemeral, proc.init.AgentCapabilities.PromptCapabilities.Image)
	out := p.runPrompt(ctx, proc, sessionID, blocks, conv, onChunk)
	switch {
	case out.err == nil:
		c.commit(plan, true)
		acpAgents.clearUncertain(lostKey)
	case out.abandoned:
		// The agent did not confirm the cancellation: the session is given
		// up, and the conversation stays locked until the prompt ends or
		// its process is killed.
		proc.closeSession(sessionID)
		c.reset()
		acpAgents.markUncertain(lostKey, history)
		unlock = false
		go func() {
			defer func() {
				c.lastUsed = time.Now()
				c.unlock()
			}()
			select {
			case <-out.response:
			case <-proc.done:
			case <-time.After(acpAbandonWait):
				proc.log.Warn("acp agent did not end a cancelled prompt; stopping it", "agent", spec.label(), "provider", spec.name)
				acpAgents.kill(entry)
			}
		}()
	case out.updated:
		// The agent reported something before the prompt failed: it may
		// have acted on the message. It is recorded, never sent again.
		c.commit(plan, false)
		// The binding may go later (a sweep, an idle stop, a crash): the pool
		// remembers.
		acpAgents.markUncertain(lostKey, history)
	default:
		// Refused before the agent did anything: a new session starts next
		// time.
		proc.closeSession(sessionID)
		c.reset()
	}
	return out.resp, out.err
}

// lockConversation acquires the agent of spec and the conversation key of
// it. A call that waited for the conversation may find the agent gone - the
// watcher of an abandoned prompt killed it - and then takes the new one.
func (p *acpProvider) lockConversation(ctx context.Context, spec acpSpec, key string) (*acpPoolEntry, func(), *acpConversation, error) {
	for {
		entry, release, err := acpAgents.acquire(ctx, spec)
		if err != nil {
			return nil, nil, nil, err
		}
		c := entry.conversation(key)
		if err := c.lock(ctx); err != nil {
			release()
			return nil, nil, nil, err
		}
		entry.convMu.Lock()
		current := entry.conversations[key] == c
		entry.convMu.Unlock()
		if !current {
			// A sweep released the binding between the lookup and the lock.
			c.unlock()
			release()
			continue
		}
		if entry.proc.alive() {
			return entry, release, c, nil
		}
		c.unlock()
		release()
		select {
		case <-entry.proc.done:
		case <-ctx.Done():
			return nil, nil, nil, ctx.Err()
		}
	}
}

// admit refuses the calls an acp model does not serve.
func (p *acpProvider) admit(conv *Conversation) error {
	if p.in.ACP == nil {
		return &ACPError{Kind: ACPErrorConfig, Agent: p.in.Name, Message: "acp.command is not set"}
	}
	if conv == nil || conv.SessionID == "" {
		return &ACPError{Kind: ACPErrorUnsupported, Agent: p.in.Name, Message: "an acp model answers inside a session only; this call (compaction, a title, the goal check, a direct completion) needs a model of another type"}
	}
	if conv.Restricted {
		return &ACPError{Kind: ACPErrorUnsupported, Agent: p.in.Name, Message: "an acp model is not available on a restricted turn: its agent's own tools are not held by the restriction"}
	}
	if conv.Mode == "plan" || conv.Mode == "ask" {
		if p.in.ACP.Modes.For(conv.Mode) == "" {
			return &ACPError{Kind: ACPErrorConfig, Agent: p.in.Name, Message: fmt.Sprintf("acp.modes.%s of provider %q is not set: in %s mode the agent would run in its own default mode, which may write without asking. Set it to a read-only mode of the agent (claude-agent-acp: plan, codex-acp: read-only, opencode: plan, coddy: ask, zcode: plan)", conv.Mode, p.in.Name, conv.Mode)}
		}
	}
	return nil
}

// acpPromptOutcome is how one prompt ended.
type acpPromptOutcome struct {
	resp *Response
	err  error
	// updated: the agent reported anything for the prompt.
	updated bool
	// abandoned: the prompt did not end when cancelled; response yields its
	// answer if it ever comes.
	abandoned bool
	response  <-chan acpMessage
}

// runPrompt sends one session/prompt and turns what the agent reports into
// chunks until the prompt's response arrives.
func (p *acpProvider) runPrompt(ctx context.Context, proc *acpProcess, sessionID string, blocks []acpContentBlock, conv *Conversation, onChunk func(StreamChunk)) acpPromptOutcome {
	sink := newACPSink()
	detach := proc.attach(sessionID, sink)
	defer func() {
		detach()
		// A permission request queued behind the prompt's response is still
		// owed an answer: the protocol wants cancelled.
		for _, ev := range sink.drain() {
			if ev.permission != nil {
				proc.respond(ev.permissionID, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
			}
		}
	}()
	respCh, err := proc.start(ctx, "session/prompt", acpPromptParams{SessionID: sessionID, Prompt: blocks})
	if err != nil {
		// The line may have gone out before the write failed: what the agent
		// already reported for the prompt says whether it acted on it.
		updated := false
		for _, ev := range sink.drain() {
			if ev.actedOn() {
				updated = true
			}
			if ev.permission != nil {
				proc.respond(ev.permissionID, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
			}
		}
		return acpPromptOutcome{err: withUpdated(err, updated), updated: updated}
	}

	st := newACPStreamState(onChunk)
	perms := newACPPermissions(proc)
	defer perms.cancelAll()
	handle := func(events []acpEvent) {
		for _, ev := range events {
			if ev.actedOn() {
				st.updated = true
			}
			switch {
			case ev.update != nil:
				st.update(*ev.update)
			case ev.permission != nil:
				req := st.mergedPermission(*ev.permission)
				perms.handle(ctx, ev.permissionID, func(pctx context.Context) acpPermissionOutcome {
					return p.decidePermission(pctx, conv, req)
				})
			}
		}
	}
	fail := func(err error) acpPromptOutcome {
		return acpPromptOutcome{resp: st.partial(), err: withUpdated(err, st.updated), updated: st.updated}
	}

	silence := time.NewTimer(acpSilence)
	defer silence.Stop()
	quiet := func() {
		if !silence.Stop() {
			select {
			case <-silence.C:
			default:
			}
		}
		silence.Reset(acpSilence)
	}
	var cancelErr error
	for cancelErr == nil {
		select {
		case m, ok := <-respCh:
			// Everything the agent sent before its response is queued by now:
			// the reader routes in order.
			handle(sink.drain())
			if !ok {
				return fail(proc.exitError())
			}
			if m.Error != nil {
				return fail(proc.rpcError("session/prompt", m.Error))
			}
			var res acpPromptResult
			_ = json.Unmarshal(m.Result, &res)
			if res.StopReason == "cancelled" {
				if err := ctx.Err(); err != nil {
					return fail(err)
				}
				return fail(&ACPError{Kind: ACPErrorCancelled, Agent: proc.spec.label()})
			}
			return acpPromptOutcome{resp: st.finish(res), updated: st.updated}
		case <-sink.signal:
			handle(sink.drain())
			quiet()
		case <-perms.settled:
			// A person answered: the silence bound starts over.
			quiet()
		case <-proc.done:
			handle(sink.drain())
			select {
			case m, ok := <-respCh:
				// The agent answered and then exited: the answer stands.
				if ok {
					if m.Error != nil {
						return fail(proc.rpcError("session/prompt", m.Error))
					}
					var res acpPromptResult
					_ = json.Unmarshal(m.Result, &res)
					if res.StopReason == "cancelled" {
						return fail(&ACPError{Kind: ACPErrorCancelled, Agent: proc.spec.label()})
					}
					return acpPromptOutcome{resp: st.finish(res), updated: st.updated}
				}
			default:
			}
			return fail(proc.exitError())
		case <-silence.C:
			if perms.waiting() {
				quiet()
				continue
			}
			cancelErr = &ACPError{Kind: ACPErrorStalled, Agent: proc.spec.label(), Message: fmt.Sprintf("the agent sent nothing for %s", acpSilence)}
		case <-ctx.Done():
			cancelErr = ctx.Err()
		}
	}

	// The protocol's cancellation: session/cancel, every pending permission
	// request answered cancelled, then the agent's cancelled response,
	// waited for a bounded time.
	proc.notify("session/cancel", acpSessionIDParams{SessionID: sessionID})
	perms.cancelAll()
	deadline := time.NewTimer(acpCancelWait)
	defer deadline.Stop()
	// Late updates are not shown, the caller stopped reading, but what they
	// say about the prompt counts.
	settle := func() {
		for _, ev := range sink.drain() {
			if ev.actedOn() {
				st.updated = true
			}
			if ev.permission != nil {
				proc.respond(ev.permissionID, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
			}
		}
	}
	for {
		select {
		case <-respCh:
			settle()
			return fail(cancelErr)
		case <-sink.signal:
			settle()
		case <-proc.done:
			settle()
			return fail(cancelErr)
		case <-deadline.C:
			settle()
			out := fail(cancelErr)
			out.abandoned = true
			out.response = respCh
			return out
		}
	}
}

// acpToolState is what the agent reported about one of its tool calls.
type acpToolState struct {
	n        int
	title    string
	kind     string
	content  []json.RawMessage
	rawInput json.RawMessage
}

// acpStreamState gathers the answer of one prompt.
type acpStreamState struct {
	onChunk   func(StreamChunk)
	text      strings.Builder
	reasoning strings.Builder
	tools     map[string]*acpToolState
	updated   bool
	// textBroken: the agent did other work (a thought, a tool call) since
	// its last text, so its next text starts a paragraph of its own.
	textBroken bool
}

func newACPStreamState(onChunk func(StreamChunk)) *acpStreamState {
	return &acpStreamState{onChunk: onChunk, tools: map[string]*acpToolState{}}
}

func (s *acpStreamState) emit(c StreamChunk) {
	if s.onChunk != nil {
		s.onChunk(c)
	}
}

func (s *acpStreamState) addText(t string) {
	if t == "" {
		return
	}
	if s.textBroken && s.text.Len() > 0 {
		// An agent sends the parts of an answer it interrupted to think or
		// to run a tool as chunks of one message: without a break the last
		// sentence of one part runs into the first of the next.
		t = paragraphBreak(s.text.String(), t) + t
	}
	s.textBroken = false
	s.text.WriteString(t)
	s.emit(StreamChunk{TextDelta: t})
}

// paragraphBreak is what goes between before and next for next to start a
// paragraph of its own in Markdown: nothing when one of them already holds
// the blank line.
func paragraphBreak(before, next string) string {
	switch {
	case strings.HasSuffix(before, "\n\n"), strings.HasPrefix(next, "\n\n"):
		return ""
	case strings.HasSuffix(before, "\n") || strings.HasPrefix(next, "\n"):
		return "\n"
	}
	return "\n\n"
}

func (s *acpStreamState) addReasoning(t string) {
	if t == "" {
		return
	}
	s.textBroken = true
	s.reasoning.WriteString(t)
	s.emit(StreamChunk{ReasoningDelta: t})
}

// reasoningLine adds a paragraph of its own to the reasoning channel: the
// surfaces render reasoning as Markdown, where a single line break joins
// two lines into one.
func (s *acpStreamState) reasoningLine(line string) {
	prefix := ""
	switch r := s.reasoning.String(); {
	case r == "", strings.HasSuffix(r, "\n\n"):
	case strings.HasSuffix(r, "\n"):
		prefix = "\n"
	default:
		prefix = "\n\n"
	}
	s.addReasoning(prefix + line + "\n\n")
}

func (s *acpStreamState) update(u acpSessionUpdate) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if acpContentType(u.Content) == "reasoning" {
			// Coddy's own ACP server (coddy acp as the agent) streams its
			// reasoning this way.
			s.addReasoning(acpContentText(u.Content))
			return
		}
		s.addText(acpContentText(u.Content))
	case "agent_thought_chunk":
		s.addReasoning(acpContentText(u.Content))
	case "tool_call", "tool_call_update":
		s.tool(u)
	}
}

// tool keeps what the agent reports about one of its tool calls and shows
// it as a numbered line of the reasoning: the agent's own tool, never a tool
// call of Coddy's that a surface could take for one to run. The number keeps
// the loop guard from reading five calls of one title as a degenerating
// stream.
func (s *acpStreamState) tool(u acpSessionUpdate) {
	s.textBroken = true
	t := s.tools[u.ToolCallID]
	first := t == nil
	if first {
		t = &acpToolState{n: len(s.tools) + 1}
		s.tools[u.ToolCallID] = t
	}
	if v := strings.TrimSpace(u.Title); v != "" {
		t.title = v
	}
	if u.Kind != "" {
		t.kind = u.Kind
	}
	if items := u.toolContent(); len(items) > 0 {
		t.content = items
	}
	if len(u.RawInput) > 0 && string(u.RawInput) != "null" {
		t.rawInput = u.RawInput
	}
	label := t.title
	if label == "" {
		label = t.kind
	}
	if first {
		s.reasoningLine("[tool " + strconv.Itoa(t.n) + "] " + label)
	}
	if u.Status == "failed" {
		s.reasoningLine("[tool " + strconv.Itoa(t.n) + " failed] " + label)
	}
}

// mergedPermission fills a permission request with what earlier updates
// said about the same tool call: an agent may send the request with the
// tool call's id alone.
func (s *acpStreamState) mergedPermission(req acpPermissionRequest) acpPermissionRequest {
	t := s.tools[req.ToolCall.ToolCallID]
	if t == nil {
		return req
	}
	if strings.TrimSpace(req.ToolCall.Title) == "" {
		req.ToolCall.Title = t.title
	}
	if req.ToolCall.Kind == "" {
		req.ToolCall.Kind = t.kind
	}
	if len(req.ToolCall.Content) == 0 {
		req.ToolCall.Content = t.content
	}
	if len(req.ToolCall.RawInput) == 0 || string(req.ToolCall.RawInput) == "null" {
		req.ToolCall.RawInput = t.rawInput
	}
	return req
}

// acpContentText reads the text of a content block; other kinds become a
// short note.
func acpContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var c struct {
		Type string `json:"type"`
		Text string `json:"text"`
		URI  string `json:"uri"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return ""
	}
	switch c.Type {
	case "text", "", "reasoning":
		return c.Text
	case "resource_link":
		if c.Name != "" {
			return "[" + c.Name + "](" + c.URI + ")"
		}
		return c.URI
	case "image", "audio", "resource":
		return "[" + c.Type + "]"
	default:
		// A kind this client does not know says nothing it could show.
		return ""
	}
}

// acpContentType reads the type of a content block.
func acpContentType(raw json.RawMessage) string {
	var c struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Type
}

func (s *acpStreamState) partial() *Response {
	return &Response{Content: s.text.String(), Reasoning: s.reasoning.String(), StopReason: "end_turn"}
}

// acpStopNotes say why an agent stopped short of an ordinary end. Coddy's
// own stop reasons are not used for them: max_tokens would have the loop ask
// the agent to go on in words written for a model.
var acpStopNotes = map[string]string{
	"max_tokens":        "(the agent stopped: it reached its output limit)",
	"max_turn_requests": "(the agent stopped: it reached its limit of model requests for one prompt)",
	"refusal":           "(the agent refused to continue)",
}

func (s *acpStreamState) finish(res acpPromptResult) *Response {
	if note := acpStopNotes[res.StopReason]; note != "" {
		if s.text.Len() > 0 {
			note = "\n\n" + note
		}
		s.addText(note)
	}
	if strings.TrimSpace(s.text.String()) == "" {
		s.addText(acpEmptyAnswer)
	}
	r := s.partial()
	if u := res.Usage; u != nil {
		r.InputTokens = u.InputTokens
		r.OutputTokens = u.OutputTokens
		r.CachedInputTokens = u.CachedReadTokens
		r.ReasoningTokens = u.ThoughtTokens
	}
	return r
}

// applySelection brings the session to the model, mode and reasoning level
// the call asks for; what the call does not name goes back to what the
// session started with, so a choice of an earlier turn never sticks.
func (p *acpProvider) applySelection(ctx context.Context, proc *acpProcess, sessionID string, conv *Conversation) error {
	def := proc.defaultsOf(sessionID)
	model := strings.TrimSpace(p.in.Model)
	if model == "" || model == acpDefaultModel {
		model = def.model
	}
	if model != "" {
		if err := proc.selectModel(ctx, sessionID, model); err != nil {
			return err
		}
	}
	mode := p.in.ACP.Modes.For(conv.Mode)
	if mode == "" {
		mode = def.mode
	}
	if mode != "" {
		if err := proc.selectMode(ctx, sessionID, mode); err != nil {
			return err
		}
	}
	// The reasoning level is a preference, not a guarantee: a mode change
	// may move it on the agent's side, and some agents refuse a level a
	// mode does not take. A level this call does not ask for undoes only a
	// level an earlier call set; a refusal leaves the agent's own.
	thought := proc.offeredThought(sessionID, p.in.ReasoningEffort)
	if thought == "" {
		thought = proc.thoughtToUndo(sessionID, def.thought)
	}
	if thought != "" {
		if err := proc.selectThought(ctx, sessionID, thought); err != nil {
			var acpErr *ACPError
			if !errors.As(err, &acpErr) || (acpErr.Kind != ACPErrorSelect && acpErr.Kind != ACPErrorRPC) {
				return err
			}
			proc.log.Debug("acp agent kept its own reasoning level", "agent", proc.spec.label(), "level", thought, "error", err)
		}
	}
	return nil
}

// decidePermission answers the agent's request to run one of its tools.
func (p *acpProvider) decidePermission(ctx context.Context, conv *Conversation, req acpPermissionRequest) acpPermissionOutcome {
	if conv.Mode == "plan" || conv.Mode == "ask" {
		// Those modes promise no writes; the agent asking is a write. The
		// mode mapped in acp.modes is the guarantee, this the second layer.
		return acpAnswer(req.Options, "reject_once", "reject_always")
	}
	switch p.in.ACP.EffectivePermissions() {
	case config.ACPPermissionsAllow:
		return acpAnswer(req.Options, "allow_once", "allow_always")
	case config.ACPPermissionsDeny:
		return acpAnswer(req.Options, "reject_once", "reject_always")
	}
	switch {
	case conv.PermissionMode == config.PermModeBypass:
		return acpAnswer(req.Options, "allow_once", "allow_always")
	case conv.PermissionMode == config.PermModeAcceptEdits && req.ToolCall.Kind == "edit":
		return acpAnswer(req.Options, "allow_once", "allow_always")
	}
	if conv.AskPermission == nil {
		return acpAnswer(req.Options, "reject_once", "reject_always")
	}
	options, back := acpPromptOptions(req.Options)
	answer, err := conv.AskPermission(ctx, PermissionRequest{
		ToolCallID: req.ToolCall.ToolCallID,
		Title:      req.ToolCall.Title,
		Kind:       req.ToolCall.Kind,
		Body:       acpPermissionBody(req.ToolCall),
		Options:    options,
	})
	if ctx.Err() != nil {
		// The prompt was cancelled while the person decided.
		return acpPermissionOutcome{Outcome: "cancelled"}
	}
	if err == nil && !answer.Cancelled {
		if id, ok := back[answer.OptionID]; ok {
			return acpPermissionOutcome{Outcome: "selected", OptionID: id}
		}
	}
	// A refusal, a dismissed prompt or nobody to answer: the agent is told
	// no, not that the turn ended.
	return acpAnswer(req.Options, "reject_once", "reject_always")
}

// acpAnswer selects the agent's first option of the kinds, in order; with
// none of them offered the request is answered cancelled.
func acpAnswer(options []acpPermissionOption, kinds ...string) acpPermissionOutcome {
	for _, k := range kinds {
		for _, o := range options {
			if o.Kind == k {
				return acpPermissionOutcome{Outcome: "selected", OptionID: o.OptionID}
			}
		}
	}
	return acpPermissionOutcome{Outcome: "cancelled"}
}

// The option ids of Coddy's own permission prompts (internal/permission):
// every surface knows them, a surface's shortcut answers (bypass, a refusal
// nobody saw) use them, and the web UI draws the reject button by its id.
const (
	coddyOptionAllow       = "allow"
	coddyOptionAllowAlways = "allow_always"
	coddyOptionReject      = "reject"
)

// acpPromptOptions turns the agent's options into the options of Coddy's
// prompt - allow, allow always when the agent offers it, reject - and the
// map back to the agent's option ids.
func acpPromptOptions(agent []acpPermissionOption) ([]PermissionOption, map[string]string) {
	first := func(kinds ...string) (acpPermissionOption, bool) {
		for _, k := range kinds {
			for _, o := range agent {
				if o.Kind == k {
					return o, true
				}
			}
		}
		return acpPermissionOption{}, false
	}
	var out []PermissionOption
	back := map[string]string{}
	add := func(id, name, kind string, o acpPermissionOption) {
		label := strings.TrimSpace(o.Name)
		if label == "" {
			label = name
		}
		out = append(out, PermissionOption{ID: id, Name: label, Kind: kind})
		back[id] = o.OptionID
	}
	if o, ok := first("allow_once", "allow_always"); ok {
		// The kind the agent gave it: an agent offering only "always" makes
		// this allow a permanent one, and a surface drawing by kind says so.
		add(coddyOptionAllow, "Allow", o.Kind, o)
	}
	if o, ok := first("allow_always"); ok && back[coddyOptionAllow] != o.OptionID {
		add(coddyOptionAllowAlways, "Allow always", "allow_always", o)
	}
	if o, ok := first("reject_once", "reject_always"); ok {
		add(coddyOptionReject, "Reject", o.Kind, o)
	} else {
		// The person can always say no; the agent then hears cancelled.
		out = append(out, PermissionOption{ID: coddyOptionReject, Name: "Reject", Kind: "reject_once"})
	}
	return out, back
}

// acpPermissionBody renders a tool call's content for the permission prompt.
func acpPermissionBody(tc acpPermissionToolCall) string {
	var parts []string
	for _, raw := range tc.Content {
		var item struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			Path    string          `json:"path"`
			OldText *string         `json:"oldText"`
			NewText string          `json:"newText"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch item.Type {
		case "content":
			if t := acpContentText(item.Content); strings.TrimSpace(t) != "" {
				parts = append(parts, t)
			}
		case "diff":
			var b strings.Builder
			fmt.Fprintf(&b, "Edit %s\n", item.Path)
			if item.OldText != nil {
				for _, l := range strings.Split(*item.OldText, "\n") {
					b.WriteString("- " + l + "\n")
				}
			}
			for _, l := range strings.Split(item.NewText, "\n") {
				b.WriteString("+ " + l + "\n")
			}
			parts = append(parts, strings.TrimRight(b.String(), "\n"))
		}
	}
	if len(parts) == 0 && len(tc.RawInput) > 0 && string(tc.RawInput) != "null" {
		parts = append(parts, truncateForLog(string(tc.RawInput), 4000))
	}
	var paths []string
	for _, l := range tc.Locations {
		if l.Path != "" {
			paths = append(paths, l.Path)
		}
	}
	if len(paths) > 0 {
		parts = append(parts, "Paths: "+strings.Join(paths, ", "))
	}
	return strings.Join(parts, "\n\n")
}

// acpPermissions answers the permission requests of one prompt, each in a
// goroutine of its own so the stream keeps flowing while a person decides.
type acpPermissions struct {
	proc    *acpProcess
	mu      sync.Mutex
	pending map[string]acpPendingPermission
	closed  bool
	// settled is signalled when a request is answered.
	settled chan struct{}
}

type acpPendingPermission struct {
	id     json.RawMessage
	cancel context.CancelFunc
}

func newACPPermissions(proc *acpProcess) *acpPermissions {
	return &acpPermissions{proc: proc, pending: map[string]acpPendingPermission{}, settled: make(chan struct{}, 1)}
}

// waiting reports whether a request waits for its answer.
func (a *acpPermissions) waiting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending) > 0
}

func (a *acpPermissions) handle(ctx context.Context, id json.RawMessage, decide func(context.Context) acpPermissionOutcome) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		a.proc.respond(id, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	key := string(id)
	a.pending[key] = acpPendingPermission{id: id, cancel: cancel}
	a.mu.Unlock()
	go func() {
		outcome := decide(pctx)
		a.mu.Lock()
		_, still := a.pending[key]
		delete(a.pending, key)
		a.mu.Unlock()
		cancel()
		if still {
			// Not answered by cancelAll in the meantime.
			a.proc.respond(id, acpPermissionResult{Outcome: outcome})
		}
		select {
		case a.settled <- struct{}{}:
		default:
		}
	}()
}

// cancelAll answers every pending request cancelled, as the protocol asks of
// a client that cancels the prompt, and refuses any later one.
func (a *acpPermissions) cancelAll() {
	a.mu.Lock()
	a.closed = true
	pending := a.pending
	a.pending = map[string]acpPendingPermission{}
	a.mu.Unlock()
	for _, pp := range pending {
		pp.cancel()
		a.proc.respond(pp.id, acpPermissionResult{Outcome: acpPermissionOutcome{Outcome: "cancelled"}})
	}
}

// newSession opens an ACP session in cwd and keeps its state and defaults.
func (p *acpProcess) newSession(ctx context.Context, cwd string) (string, error) {
	var st acpSessionState
	if err := p.call(ctx, "session/new", acpNewSessionParams{CWD: cwd, MCPServers: []interface{}{}}, &st); err != nil {
		return "", err
	}
	if st.SessionID == "" {
		return "", &ACPError{Kind: ACPErrorRPC, Agent: p.spec.label(), Method: "session/new", Message: "no sessionId in the result"}
	}
	p.opened.Add(1)
	var def acpSessionDefaults
	if opt, ok := optionOf(st.ConfigOptions, "model"); ok {
		def.model = opt.current()
	} else if st.Models != nil {
		def.model = st.Models.CurrentModelID
	}
	if opt, ok := optionOf(st.ConfigOptions, "mode"); ok {
		def.mode = opt.current()
	} else if st.Modes != nil {
		def.mode = st.Modes.CurrentModeID
	}
	if opt, ok := optionOf(st.ConfigOptions, "thought_level"); ok {
		def.thought = opt.current()
	}
	p.mu.Lock()
	p.states[st.SessionID] = &st
	p.defaults[st.SessionID] = def
	p.mu.Unlock()
	return st.SessionID, nil
}

// hasSession reports whether this process opened the session and did not
// close it.
func (p *acpProcess) hasSession(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.states[id]
	return ok
}

func (p *acpProcess) defaultsOf(id string) acpSessionDefaults {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.defaults[id]
}

// closeSession forgets a session and, when the agent supports it, closes it
// in the background.
func (p *acpProcess) closeSession(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	delete(p.states, id)
	delete(p.defaults, id)
	delete(p.thoughtSet, id)
	p.mu.Unlock()
	if !p.init.AgentCapabilities.SessionCapabilities.canClose() || !p.alive() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), acpCloseTimeout)
		defer cancel()
		_ = p.call(ctx, "session/close", acpSessionIDParams{SessionID: id}, nil)
	}()
}

// stateOf returns a copy of what the agent last reported about a session.
func (p *acpProcess) stateOf(id string) acpSessionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[id]
	if st == nil {
		return acpSessionState{SessionID: id}
	}
	out := *st
	out.ConfigOptions = append([]acpConfigOption(nil), st.ConfigOptions...)
	if st.Modes != nil {
		m := *st.Modes
		out.Modes = &m
	}
	if st.Models != nil {
		m := *st.Models
		out.Models = &m
	}
	return out
}

func (p *acpProcess) storeOptions(id string, opts []acpConfigOption) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.states[id]; st != nil && len(opts) > 0 {
		st.ConfigOptions = opts
	}
}

// optionOf returns the first select config option of a category (or,
// failing that, with that id).
func optionOf(opts []acpConfigOption, category string) (acpConfigOption, bool) {
	if i := optionIndex(opts, category); i >= 0 {
		return opts[i], true
	}
	return acpConfigOption{}, false
}

// optionIndex is the position of the select option of category: the one the
// agent puts in that category, or else the one whose id is its name.
func optionIndex(opts []acpConfigOption, category string) int {
	for i, o := range opts {
		if o.Category == category && (o.Type == "" || o.Type == "select") {
			return i
		}
	}
	for i, o := range opts {
		if o.ID == category && (o.Type == "" || o.Type == "select") {
			return i
		}
	}
	return -1
}

func (p *acpProcess) setOption(ctx context.Context, sessionID, configID, value string) error {
	var res acpSetConfigOptionResult
	if err := p.call(ctx, "session/set_config_option", acpSetConfigOptionParams{SessionID: sessionID, ConfigID: configID, Value: value}, &res); err != nil {
		return err
	}
	p.storeOptions(sessionID, res.ConfigOptions)
	return nil
}

// selectModel selects the model through the agent's model config option,
// else through session/set_model.
func (p *acpProcess) selectModel(ctx context.Context, sessionID, model string) error {
	st := p.stateOf(sessionID)
	if opt, ok := optionOf(st.ConfigOptions, "model"); ok {
		if opt.current() == model {
			// Already there, whether or not the agent lists it.
			return nil
		}
		values := opt.values()
		for _, v := range values {
			if v.Value == model {
				if opt.current() == model {
					return nil
				}
				return p.setOption(ctx, sessionID, opt.ID, model)
			}
		}
		return p.notOffered("model", model, valueIDs(values))
	}
	if st.Models != nil {
		if st.Models.CurrentModelID == model {
			return nil
		}
		var ids []string
		for _, m := range st.Models.AvailableModels {
			ids = append(ids, m.ModelID)
			if m.ModelID == model {
				if st.Models.CurrentModelID == model {
					return nil
				}
				if err := p.call(ctx, "session/set_model", acpSetModelParams{SessionID: sessionID, ModelID: model}, nil); err != nil {
					return err
				}
				p.mu.Lock()
				if s := p.states[sessionID]; s != nil && s.Models != nil {
					s.Models.CurrentModelID = model
				}
				p.mu.Unlock()
				return nil
			}
		}
		return p.notOffered("model", model, ids)
	}
	return &ACPError{Kind: ACPErrorSelect, Agent: p.spec.label(), Message: fmt.Sprintf("the agent offers no model selection; use the model %q to keep its own", acpDefaultModel)}
}

// selectMode selects the mode through the agent's mode config option, else
// through session/set_mode.
func (p *acpProcess) selectMode(ctx context.Context, sessionID, mode string) error {
	st := p.stateOf(sessionID)
	if opt, ok := optionOf(st.ConfigOptions, "mode"); ok {
		if opt.current() == mode {
			return nil
		}
		values := opt.values()
		for _, v := range values {
			if v.Value == mode {
				if opt.current() == mode {
					return nil
				}
				return p.setOption(ctx, sessionID, opt.ID, mode)
			}
		}
		return p.notOffered("mode", mode, valueIDs(values))
	}
	if st.Modes != nil {
		if st.Modes.CurrentModeID == mode {
			return nil
		}
		var ids []string
		for _, m := range st.Modes.AvailableModes {
			ids = append(ids, m.ID)
			if m.ID == mode {
				if st.Modes.CurrentModeID == mode {
					return nil
				}
				if err := p.call(ctx, "session/set_mode", acpSetModeParams{SessionID: sessionID, ModeID: mode}, nil); err != nil {
					return err
				}
				p.mu.Lock()
				if s := p.states[sessionID]; s != nil && s.Modes != nil {
					s.Modes.CurrentModeID = mode
				}
				p.mu.Unlock()
				return nil
			}
		}
		return p.notOffered("mode", mode, ids)
	}
	return &ACPError{Kind: ACPErrorSelect, Agent: p.spec.label(), Message: fmt.Sprintf("the agent offers no modes, so acp.modes cannot select %q", mode)}
}

// offeredThought returns the agent's value for a reasoning level when its
// thought_level option offers it, else "".
func (p *acpProcess) offeredThought(sessionID, level string) string {
	level = strings.TrimSpace(level)
	if level == "" {
		return ""
	}
	opt, ok := optionOf(p.stateOf(sessionID).ConfigOptions, "thought_level")
	if !ok {
		return ""
	}
	for _, v := range opt.values() {
		if strings.EqualFold(v.Value, level) {
			return v.Value
		}
	}
	return ""
}

// selectThought selects a value of the agent's thought_level option.
func (p *acpProcess) selectThought(ctx context.Context, sessionID, value string) error {
	opt, ok := optionOf(p.stateOf(sessionID).ConfigOptions, "thought_level")
	if !ok || opt.current() == value {
		return nil
	}
	if err := p.setOption(ctx, sessionID, opt.ID, value); err != nil {
		return err
	}
	p.mu.Lock()
	p.thoughtSet[sessionID] = value
	p.mu.Unlock()
	return nil
}

// thoughtToUndo returns the session's starting reasoning level when a call
// before this one set another that still stands, else "".
func (p *acpProcess) thoughtToUndo(sessionID, initial string) string {
	p.mu.Lock()
	set := p.thoughtSet[sessionID]
	p.mu.Unlock()
	if set == "" || set == initial {
		return ""
	}
	opt, ok := optionOf(p.stateOf(sessionID).ConfigOptions, "thought_level")
	if !ok || opt.current() != set {
		// The agent moved it since: its own choice stands.
		return ""
	}
	return initial
}

func (p *acpProcess) notOffered(what, value string, offered []string) error {
	sort.Strings(offered)
	msg := fmt.Sprintf("the agent offers no %s %q", what, value)
	if len(offered) > 0 {
		msg += "; it offers: " + strings.Join(offered, ", ")
	}
	return &ACPError{Kind: ACPErrorSelect, Agent: p.spec.label(), Message: msg}
}

func valueIDs(values []acpConfigValue) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Value)
	}
	return out
}

// listACPModels lists what an acp row can serve: default (the agent's own
// model) and every model the agent offers in a new session.
func listACPModels(ctx context.Context, in ProviderInput) ([]ModelEntry, error) {
	spec, err := resolveACPSpec(in, "", "")
	if err != nil {
		return nil, err
	}
	entry, release, err := acpAgents.acquire(ctx, spec)
	if err != nil {
		return nil, err
	}
	defer release()
	proc := entry.proc
	cwd := spec.sessionCWD
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	sessionID, err := proc.newSession(ctx, cwd)
	if err != nil {
		return nil, err
	}
	defer proc.closeSession(sessionID)
	st := proc.stateOf(sessionID)
	out := []ModelEntry{{ID: acpDefaultModel, Name: "the agent's own model"}}
	seen := map[string]bool{acpDefaultModel: true}
	add := func(id, name string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, ModelEntry{ID: id, Name: name})
	}
	if opt, ok := optionOf(st.ConfigOptions, "model"); ok {
		for _, v := range opt.values() {
			add(v.Value, v.Name)
		}
	} else if st.Models != nil {
		for _, m := range st.Models.AvailableModels {
			add(m.ModelID, m.Name)
		}
	}
	return out, nil
}
