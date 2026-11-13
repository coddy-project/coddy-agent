package agent

// Folding a history out of the provider's prompt cache.
//
// A compaction used to be a request of its own: a summarizer system prompt and
// the whole head of the conversation flattened into one user message. That
// request shares no byte with what the session had been sending, so the
// provider read the head again at full price, and in a long session the folds
// cost more than the turns that triggered them - the measurement that led here
// put a fold at about seven ordinary steps, and the folds at roughly half of
// everything a session paid for without a cache hit.
//
// When the summarizer is the session's own model the head does not have to be
// rebuilt at all: it is the beginning of the request the turn has just sent.
// Coddy therefore asks with that request - the same system message, the same
// tool definitions, the same provider input, the same messages up to the
// compaction boundary - and appends the summarization instruction as the last,
// request-only message. Everything in front of that instruction is the prefix
// the provider has cached, so the fold reads it back instead of paying for it.
// The chain of earlier summary rows needs no section of its own either: those
// rows are already the first messages of the window, where the model reads them.
//
// The path is taken only when it is exactly the turn's own request. A
// summarizer on another model, a session that has sent nothing in this process,
// a head that is not the head of what the turn sends, a request that no longer
// fits the window, an answer that is a tool call rather than a summary: each of
// them falls back to the flattened transcript of compact_fold.go, which keeps
// its passes, its retries and its chain of fallback models.

import (
	"context"
	"reflect"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// cachedCompactionMarker opens the one message a cached-prefix summarization
// request adds to the conversation it is folding. Nothing else in the request
// carries it, which is how a test, and the model, tell the instruction from the
// conversation.
const cachedCompactionMarker = "Summarize the conversation above."

// cachedCompactionOutputRoom is the room left for the summary when the model
// entry names no output bound of its own.
const cachedCompactionOutputRoom = 4096

// noteTurnSend records what the request about to go out puts on the wire, so a
// compaction of this session can repeat its prefix. An acp model keeps its own
// context and is never compacted here, and a request that does not open with a
// system message is not one a summarizer could be asked with.
func (a *Agent) noteTurnSend(transport llmTransport, sendMessages []llm.Message, toolDefs []llm.ToolDefinition) {
	if transport.agent || len(sendMessages) == 0 || sendMessages[0].Role != llm.RoleSystem {
		return
	}
	st, ok := a.state.(interface{ SetTurnSend(session.TurnSend) })
	if !ok {
		return
	}
	st.SetTurnSend(session.TurnSend{
		Model:  transport.model,
		System: sendMessages[0],
		Tools:  toolDefs,
		Input:  transport.in,
	})
}

// turnSend is the recorded shape of this session's last request.
func (a *Agent) turnSend() (session.TurnSend, bool) {
	st, ok := a.state.(interface {
		TurnSend() (session.TurnSend, bool)
	})
	if !ok {
		return session.TurnSend{}, false
	}
	return st.TurnSend()
}

// cachedFoldSpec is one cached-prefix summarization: what the model reads of
// the session, where the compaction boundary falls in it, and what to make of
// what comes before that boundary.
type cachedFoldSpec struct {
	// Window is the window the model reads (session.MessagesForLLM) and Cut is
	// the boundary in it: everything before the cut is folded.
	Window []llm.Message
	Cut    int
	// Instructions are the operator's words for this summary and the session's
	// goal, as CompactSession composed them.
	Instructions string
	// EarlierSummaries says the window opens with the summary rows of earlier
	// compactions, which the new summary continues rather than retells.
	EarlierSummaries bool
	// Merge marks the call that retells the earliest rows of the chain as one.
	// What it reads is a run of summaries and nothing else, and Instructions
	// already says what to make of them.
	Merge bool
	// Reasoning is the level this compaction was asked to run at, empty for
	// the model's own. A level the operator named is a level the summary has
	// to be written at, and it is also something a provider keys its cache on,
	// so one that differs from what the turn ran at sends the fold down the
	// flattened path, where the chain builds a provider for it.
	Reasoning string
}

// cachedCompactionInstruction is the summarization brief as a message that
// follows the conversation instead of opening the request. It says the same
// things compactionSystemPrompt says, addressed to a model that has just read
// the conversation rather than a transcript handed to it.
func cachedCompactionInstruction(spec cachedFoldSpec) string {
	var b strings.Builder
	b.WriteString(cachedCompactionMarker)
	switch {
	case spec.Merge:
		b.WriteString(" The messages above are the summaries of earlier compactions of this session, oldest first, and nothing else.\n")
	default:
		b.WriteString(" The session is running out of context window, and your summary replaces the conversation above so the work can continue in a smaller one.\n\n")
		if spec.EarlierSummaries {
			b.WriteString("The messages at the top of the conversation are the summaries of earlier compactions. ")
			b.WriteString("They stay in the context as they are, and what you write is read after them: do not repeat what they say, ")
			b.WriteString("summarize the messages after them, and refer to them where those messages build on them.\n\n")
		}
		b.WriteString(`Write a dense summary. Preserve, in this order:
1. The user's goals, requirements, and constraints (including exact wording of still-relevant instructions).
2. Decisions made and their reasons; approaches that were rejected.
3. Current state of the work: what is done, what is in progress, what failed.
4. Exact file paths, function/type names, commands, and configuration values that matter for continuing.
5. Unresolved questions and concrete next steps.
`)
	}
	b.WriteString("\nAnswer with the summary itself: plain markdown, no preamble, no closing remarks, and no tool call. Do not invent facts the conversation does not carry.")
	if s := strings.TrimSpace(spec.Instructions); s != "" {
		b.WriteString("\n\nAdditional instructions for this summary:\n")
		b.WriteString(s)
	}
	return b.String()
}

// cachedCompactionRequest is the conversation up to the compaction boundary as
// the turn's own requests carry it, under the turn's system message and
// followed by the summarization instruction.
//
// The answer is nil when the head of the window is not the head of what the
// turn sends - a transform that reads past the cut, a history the pairing guard
// refuses - and the fold then flattens the head as it always did.
func (a *Agent) cachedCompactionRequest(send session.TurnSend, spec cachedFoldSpec) []llm.Message {
	if spec.Cut <= 0 || spec.Cut > len(spec.Window) {
		return nil
	}
	head, ok := a.sentHead(spec.Window, spec.Cut)
	if !ok || len(head) == 0 {
		return nil
	}
	req := make([]llm.Message, 0, len(head)+2)
	req = append(req, send.System)
	req = append(req, head...)
	req = append(req, llm.Message{
		Role:      llm.RoleUser,
		Content:   cachedCompactionInstruction(spec),
		Ephemeral: true,
	})
	if issues := session.ValidateToolPairing(req); len(issues) > 0 {
		return nil
	}
	return req
}

// sentHead is the first cut messages of the window as the send boundary makes
// of them: the UI-only rows dropped, the superseded results collapsed, the
// rules joined to the results that brought them and the pictures attached
// (react.go, runReActLoop). ok is false when one of those stages reads the
// messages past the cut, so that the head of the whole is not what the head
// alone comes to.
func (a *Agent) sentHead(visible []llm.Message, cut int) ([]llm.Message, bool) {
	// The rows the model reads back, folded pairs included. Both stages are
	// local to a message and its neighbour, but a fold across the cut would
	// still move it, so the head is measured against the whole.
	whole, cut, ok := headThrough(visible, cut, llmHistoryMessages)
	if !ok {
		return nil, false
	}
	// Eviction and the rules each rewrite a message where it stands, so the cut
	// does not move; the length is the contract and is checked rather than
	// trusted. Eviction has to run over the whole window: the working window of
	// the newest results it keeps is decided by what comes after the cut too,
	// and the head alone would keep results the turn's request had collapsed.
	for _, stage := range []func([]llm.Message) []llm.Message{a.prunedForLLM, withToolRules} {
		next := stage(whole)
		if len(next) != len(whole) {
			return nil, false
		}
		whole = next
	}
	// Pictures can take a message of their own, so the cut moves again.
	whole, cut, ok = headThrough(whole, cut, func(msgs []llm.Message) []llm.Message {
		return withToolImages(msgs, a.modelReadsImages(), a.loadToolImage)
	})
	if !ok {
		return nil, false
	}
	head := whole[:cut]
	// The send boundary repairs the outbound projection; a head that needs one
	// is a head whose calls the summarizer would read unanswered.
	repaired, _ := session.RepairMissingToolResults(head)
	if len(repaired) != len(head) {
		return nil, false
	}
	return head, true
}

// headThrough applies fn to a history and to its first cut messages and
// answers the transformed whole with the cut moved to where the transformed
// head ends. ok is false when the head of the whole is not what the head alone
// comes to, which is a transform that read the messages past the cut.
func headThrough(whole []llm.Message, cut int, fn func([]llm.Message) []llm.Message) ([]llm.Message, int, bool) {
	out := fn(whole)
	head := fn(whole[:cut])
	if len(head) > len(out) || !reflect.DeepEqual(out[:len(head)], head) {
		return nil, 0, false
	}
	return out, len(head), true
}

// cachedFold asks the summarizer with the conversation as the turn sent it and
// answers the summary it wrote. ok is false whenever this path does not apply
// or did not produce a summary, and the caller then folds the flattened
// transcript (foldCompactionHead), which owns the passes, the retries and the
// chain of fallback summarizers.
func (a *Agent) cachedFold(ctx context.Context, chain []compactionCandidate, spec cachedFoldSpec) (summary, modelID string, ok bool) {
	if len(chain) == 0 || ctx.Err() != nil {
		return "", "", false
	}
	send, held := a.turnSend()
	// Only the session's own model reads the conversation out of a cache it
	// wrote: a summarizer named by /compact or by compaction.model has none of
	// this session's prefix, and the first of the chain is what would answer.
	if !held || send.Model == "" || send.Model != chain[0].modelID || send.System.Content == "" {
		return "", "", false
	}
	// A reasoning level asked for by /compact, the tool or the HTTP route is
	// honoured by the chain's own provider, not by the turn's.
	if spec.Reasoning != "" && spec.Reasoning != send.Input.ReasoningEffort {
		return "", "", false
	}
	req := a.cachedCompactionRequest(send, spec)
	if req == nil {
		return "", "", false
	}
	if !a.cachedFoldFits(req) {
		a.log.Info("the conversation no longer fits one summarization request; folding the transcript in passes",
			"session", a.state.GetID(), "model", send.Model)
		return "", "", false
	}
	mk := a.providerFactory
	if mk == nil {
		mk = llm.NewProvider
	}
	// The turn's own input, so the model, the tools, the reasoning level and the
	// output bound this request carries are the ones the cached prefix was
	// written under.
	provider, err := mk(send.Input)
	if err != nil {
		a.log.Warn("could not build the session's provider for the summary; folding the transcript instead",
			"session", a.state.GetID(), "model", send.Model, "error", err)
		return "", "", false
	}
	resp, err := provider.Complete(ctx, req, send.Tools)
	if err != nil {
		a.log.Warn("the summary over the turn's own context failed; folding the transcript instead",
			"session", a.state.GetID(), "model", send.Model, "error", err)
		return "", "", false
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" || len(resp.ToolCalls) > 0 {
		a.log.Info("the summarizer answered the conversation with no summary; folding the transcript instead",
			"session", a.state.GetID(), "model", send.Model, "toolCalls", len(resp.ToolCalls), "empty", out == "")
		return "", "", false
	}
	return out, send.Model, true
}

// cachedFoldFits reports whether the request still fits the window with room
// for the summary. It fits by construction - it is what the provider has just
// accepted, less everything after the boundary - so this is the check for what
// construction does not cover: a model switched mid-session, a window a listing
// reported smaller after the fact. The estimate counts the messages and adds
// the overhead every request carries, which counts the system message twice:
// the error is on the side of folding in passes.
func (a *Agent) cachedFoldFits(req []llm.Message) bool {
	window, _ := a.contextWindow()
	if window <= 0 {
		return false
	}
	room := cachedCompactionOutputRoom
	if ent := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg)); ent != nil && ent.MaxTokens > 0 {
		room = ent.MaxTokens
	}
	return estimateMessagesTokens(req)+a.requestOverhead()+room <= window
}

// compactionOutcomeLog is the one line a fold writes about how it was made, so
// an operator reading the log can tell a fold served from the prompt cache from
// one that paid for the history.
func (a *Agent) compactionOutcomeLog(cached bool, modelID string, steps int) {
	if cached {
		a.log.Info("compaction summarized the conversation the turn had sent",
			"session", a.state.GetID(), "model", modelID)
		return
	}
	a.log.Info("compaction summarized a flattened transcript",
		"session", a.state.GetID(), "model", modelID, "passes", steps)
}
