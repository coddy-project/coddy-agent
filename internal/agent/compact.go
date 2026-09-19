package agent

// Context compaction: summarize older conversation history with an LLM call
// and insert the summary into the transcript so later prompts replay only the
// summary plus the most recent turns (see session.MessagesForLLM).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ErrNothingToCompact is returned when the history has no full user turn to
// fold away before the keep-recent boundary.
var ErrNothingToCompact = errors.New("nothing to compact")

// ErrCompactionBlocked wraps the reason a PreCompact hook vetoed a compaction.
var ErrCompactionBlocked = errors.New("compaction blocked by hook")

// Triggers of the compaction hooks: what the matcher is compared with.
const (
	compactTriggerManual = "manual"
	compactTriggerAuto   = "auto"
)

// ErrCompactionDisabled is returned when compaction.enable is false.
var ErrCompactionDisabled = errors.New("compaction is disabled (compaction.enable)")

// ErrCompactionModel wraps why the summarizer a caller asked for by name cannot
// be used: nothing configured matches it, or more than one model does. A
// compaction asked to run on a named model never quietly runs on another.
var ErrCompactionModel = errors.New("compaction model")

// CompactOptions is one compaction request.
type CompactOptions struct {
	// Instructions optionally augments the summarization request (the words
	// after /compact, the tool's instructions argument).
	Instructions string
	// Model names the summarizer for this one compaction: a models[].model or a
	// substring naming exactly one (config.MatchModelID). Empty follows the
	// configuration. The configured chain stays behind it as the fallback.
	Model string
	// Force is a manual compaction: it folds whatever exists, down to keeping
	// no turn verbatim. The automatic trigger passes false.
	Force bool
	// FromTool marks a compaction the model asked for in the middle of its own
	// turn: the assistant message carrying that tool call is never folded.
	FromTool bool
}

// CompactionResult reports what a successful compaction did.
type CompactionResult struct {
	// Summary is the generated summary text (without the transcript preamble).
	Summary string
	// CompactedMessages is how many history messages were folded into the summary.
	CompactedMessages int
	// KeptMessages is how many messages after the summary stayed verbatim.
	KeptMessages int
	// Model is the models[].model that produced the summary.
	Model string
	// Steps is how many summarization calls the fold took: one while the
	// history fits a single request, more when it had to be folded in passes
	// (compact_fold.go).
	Steps int
}

// compactionSystemPrompt instructs the summarizer model.
const compactionSystemPrompt = `You are compacting the conversation history of a coding agent so the session can continue in a smaller context window.

Write a dense summary of the transcript you are given. Preserve, in this order:
1. The user's goals, requirements, and constraints (including exact wording of still-relevant instructions).
2. Decisions made and their reasons; approaches that were rejected.
3. Current state of the work: what is done, what is in progress, what failed.
4. Exact file paths, function/type names, commands, and configuration values that matter for continuing.
5. Unresolved questions and concrete next steps.

Output plain markdown, no preamble and no closing remarks. Do not invent facts that are not in the transcript.`

// CompactSession summarizes history older than the keep-recent boundary and
// inserts the summary row at that boundary.
//
// When the configured keep-recent window covers every user turn there is, it
// retries with progressively fewer kept turns. force (manual /compact) goes
// down to zero, so even a very short conversation compacts. Auto-compaction
// passes force=false and stops at one: the prompt being answered always stays
// verbatim, and with a single user turn there is nothing to fold. A session of
// a few long agent turns is what that fallback is for: without it, a window of
// keep_recent_turns turns would grow past the threshold and never compact.
func (a *Agent) CompactSession(ctx context.Context, opts CompactOptions) (*CompactionResult, error) {
	if !a.cfg.Compaction.IsEnabled() {
		return nil, ErrCompactionDisabled
	}
	instructions, force := opts.Instructions, opts.Force
	// A summarizer named for this call is resolved before anything runs: a name
	// that matches nothing is an answer to give, not a reason to fall through to
	// the configured chain.
	override := ""
	if strings.TrimSpace(opts.Model) != "" {
		id, err := a.cfg.MatchModelID(opts.Model)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCompactionModel, err)
		}
		override = id
	}
	// PreCompact hooks see the trigger and may veto: the manual command
	// reports the veto, an automatic compaction is skipped for this check.
	trigger := compactTriggerAuto
	if force {
		trigger = compactTriggerManual
	}
	mode := a.state.EffectiveMode()
	if reason, vetoed := a.runPreCompactHooks(ctx, mode, trigger, instructions); vetoed {
		return nil, fmt.Errorf("%w: %s", ErrCompactionBlocked, reason)
	}

	msgs := a.state.GetMessages()
	keep := a.cfg.Compaction.EffectiveKeepRecentTurns()
	// The floor holds for the configured value too, not only for the retries
	// below it: keep_recent_turns: 0 means "summarize everything", which is a
	// thing to ask of /compact and never of the automatic trigger - folding
	// the prompt being answered would send the model a turn with no prompt in
	// it.
	minKeep := 1
	if force {
		minKeep = 0
	}
	if keep < minKeep {
		keep = minKeep
	}
	splitIdx, ok := session.CompactionSplitIndex(msgs, keep)
	for k := keep - 1; !ok && k >= minKeep; k-- {
		splitIdx, ok = session.CompactionSplitIndex(msgs, k)
	}
	if !ok {
		return nil, ErrNothingToCompact
	}
	visible := session.MessagesForLLM(msgs)
	visibleStart := len(msgs) - len(visible)
	if opts.FromTool {
		// The call being executed sits in the last assistant message, and its
		// result is appended after this returns. Folding that message would
		// leave the result answering a call the provider never sees.
		if i := lastToolCallMessageIndex(msgs); i >= 0 && splitIdx > i {
			splitIdx = i
		}
		headStart := visibleStart
		if len(visible) > 0 && visible[0].CompactionSummary {
			headStart++
		}
		if splitIdx <= headStart {
			return nil, ErrNothingToCompact
		}
	}
	if splitIdx < visibleStart || splitIdx > len(msgs) {
		return nil, fmt.Errorf("invalid compaction boundary %d for visible window %d..%d", splitIdx, visibleStart, len(msgs))
	}
	// Analyze the full visible window before selecting the compacted head: pins
	// and writes in the kept tail can change whether an older result is useful or
	// stale, even though the tail itself is not sent to the summarizer.
	projected := a.prunedForLLM(visible)
	head := projected[:splitIdx-visibleStart]

	chain, err := a.compactionChain(override)
	if err != nil {
		return nil, fmt.Errorf("compaction model: %w", err)
	}

	// Lines the history already carried go before anything is measured: a
	// session fills its window by repeating itself, and the repeats are what
	// push a fold past the summarizer's window (compact_fold.go).
	if deduped, dropped := dedupeCompactionHead(head); dropped > 0 {
		a.log.Info("compaction dropped repeated lines from the history it is folding",
			"lines", dropped, "messages", len(head))
		head = deduped
	}

	// The fold is sized against the summarizer's own window, not the session's:
	// compaction.model may name a smaller or larger model than the turn runs on.
	// The first of the chain sets the size; a fallback below it reads the same
	// pass, which is why the share is a share rather than the whole window.
	window, _ := a.contextWindowFor(chain[0].modelID)
	budget := compactionInputBudget(window, session.EstimateTokens(instructions))

	row := a.newCompactionRow()
	summary, modelID, steps, err := a.foldCompactionHead(ctx, chain, head, instructions, budget, row.step)
	if err != nil {
		row.failed(err)
		return nil, err
	}

	a.state.InsertCompactionSummary(splitIdx, session.NewCompactionSummaryMessage(summary, modelID))
	a.refreshConversationContextUsage(true)
	a.runPostCompactHooks(ctx, mode, trigger, summary)

	res := &CompactionResult{
		Summary:           summary,
		CompactedMessages: len(head),
		KeptMessages:      len(msgs) - splitIdx,
		Model:             modelID,
		Steps:             steps,
	}
	row.done(compactionOutcomeText(res))
	return res, nil
}

// compactionOutcomeText is the one line every surface says about a finished
// compaction: the transcript row, the /compact answer and the compact_context
// tool result.
func compactionOutcomeText(res *CompactionResult) string {
	if res == nil {
		return ""
	}
	text := fmt.Sprintf("Context compacted: %d message(s) summarized, %d kept verbatim.",
		res.CompactedMessages, res.KeptMessages)
	if res.Model != "" {
		text += fmt.Sprintf(" Summarizer: %s.", res.Model)
	}
	if res.Steps > 1 {
		text += fmt.Sprintf(" The history did not fit one summarization request, so it was folded in %d passes.", res.Steps)
	}
	return text
}

// compactFromTool is the Env.CompactSession hook behind the compact_context
// tool. The model asking for a compaction is a manual one: it asked for the
// history it can see to be folded, so the fold goes as far as /compact does.
func (a *Agent) compactFromTool(ctx context.Context, req tooling.CompactRequest) (string, error) {
	res, err := a.CompactSession(ctx, CompactOptions{
		Instructions: req.Instructions,
		Model:        req.Model,
		Force:        true,
		FromTool:     true,
	})
	switch {
	case errors.Is(err, ErrNothingToCompact):
		return "Nothing to compact: there is no earlier conversation to summarize yet.", nil
	case errors.Is(err, ErrCompactionDisabled):
		return "", err
	case err != nil:
		return "", err
	}
	return compactionOutcomeText(res), nil
}

// CompactCommandName is the built-in slash command that triggers compaction.
const CompactCommandName = "compact"

// CompactCommandDescription is shown in slash-command catalogs.
const CompactCommandDescription = "Summarize older conversation history to free context; recent turns stay verbatim"

// compactUsage closes every reply that could not run the command as typed.
const compactUsage = "Usage: /compact [--model <id>] [instructions]. --model names the summarizer for this one " +
	"compaction: a configured models[].model, or a part of one that matches exactly one model."

// compactCommandArgs is the parsed form of one /compact invocation.
type compactCommandArgs struct {
	// Model is the value of --model, as typed.
	Model string
	// Instructions is everything after the options, verbatim.
	Instructions string
	// UnknownOptions are the leading --words the command does not know.
	UnknownOptions []string
	// ModelMissing reports --model with no value after it.
	ModelMissing bool
}

// parseCompactCommand reports whether the prompt text invokes the built-in
// /compact command. Options come first: --model <id> or --model=<id>. The
// first word that is not an option starts the summarizer instructions, which
// run to the end of the prompt untouched, so an instruction may mention an
// option without being read as one.
func parseCompactCommand(text string) (compactCommandArgs, bool) {
	t := strings.TrimSpace(text)
	const cmd = "/" + CompactCommandName
	if t == cmd {
		return compactCommandArgs{}, true
	}
	for _, sep := range []string{" ", "\t", "\n", "\r"} {
		rest, found := strings.CutPrefix(t, cmd+sep)
		if !found {
			continue
		}
		var args compactCommandArgs
		rest = strings.TrimSpace(rest)
		for strings.HasPrefix(rest, "--") {
			var word string
			word, rest = cutCompactWord(rest)
			switch {
			case word == "--model":
				if rest == "" || strings.HasPrefix(rest, "--") {
					args.ModelMissing = true
					continue
				}
				args.Model, rest = cutCompactWord(rest)
			case strings.HasPrefix(word, "--model="):
				args.Model = strings.TrimPrefix(word, "--model=")
				if args.Model == "" {
					args.ModelMissing = true
				}
			default:
				args.UnknownOptions = append(args.UnknownOptions, word)
			}
		}
		args.Instructions = rest
		return args, true
	}
	return compactCommandArgs{}, false
}

// cutCompactWord splits s at its first run of whitespace: the word before it
// and the text after it, trimmed.
func cutCompactWord(s string) (word, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// runCompactCommand executes the built-in /compact command for a prompt turn.
// Manual compaction is forced (folds whatever exists, even a short chat). The
// command text is persisted as a user message so it shows in the transcript; the
// outcome is streamed as one agent message chunk and stored as an assistant
// message. The generated summary is inserted as a compaction row, which the UI
// renders as its own foldout ("what is now in context").
func (a *Agent) runCompactCommand(ctx context.Context, args compactCommandArgs, rawCommand string) (string, error) {
	var res *CompactionResult
	var err error
	usageErr := compactArgsProblem(args)
	if usageErr == "" {
		res, err = a.CompactSession(ctx, CompactOptions{Instructions: args.Instructions, Model: args.Model, Force: true})
	}
	// Show the command in the transcript, regardless of the outcome.
	a.addUserCommandMessage(rawCommand)
	var text string
	switch {
	case usageErr != "":
		text = usageErr + " " + compactUsage
	case errors.Is(err, ErrCompactionModel):
		// A name that matches no model, or several: say which models there
		// are. Nothing was compacted and nothing failed, so the turn ends.
		text = "Nothing was compacted: " + strings.TrimPrefix(err.Error(), ErrCompactionModel.Error()+": ") + ". " + compactUsage
	case errors.Is(err, ErrNothingToCompact):
		text = "Nothing to compact: there is no earlier conversation to summarize yet."
	case errors.Is(err, ErrCompactionDisabled):
		text = "Compaction is disabled in the configuration (compaction.enable: false)."
	case err != nil:
		return string(acp.StopReasonRefused), err
	default:
		text = compactionOutcomeText(res)
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
	})
	a.state.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   text,
		Model:     a.state.EffectiveModelID(a.cfg),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	a.refreshConversationContextUsage(true)
	return string(acp.StopReasonEndTurn), nil
}

// compactArgsProblem is what is wrong with the options of a /compact as typed,
// or the empty string.
func compactArgsProblem(args compactCommandArgs) string {
	var problems []string
	if len(args.UnknownOptions) > 0 {
		problems = append(problems, "Unknown option: "+strings.Join(args.UnknownOptions, ", ")+".")
	}
	if args.ModelMissing {
		problems = append(problems, "--model needs a model id.")
	}
	return strings.Join(problems, " ")
}

// addUserCommandMessage persists the raw text of a built-in slash command
// (/compact, /plugin, /export) as a user message so it appears in the transcript like any
// other user input, instead of vanishing when the client reconciles with the
// server snapshot.
func (a *Agent) addUserCommandMessage(text string) {
	a.state.AddMessage(llm.Message{
		Role:      llm.RoleUser,
		Content:   text,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// maybeAutoCompact runs compaction when the estimated context usage reached
// compaction.threshold_percent of the session's context window (contextWindow:
// max_context_tokens, the provider's reported window, or the default - the
// window the web UI ring shows). It is fail-open: any error (including
// nothing-to-compact right after a previous compaction) leaves the turn
// running uncompacted. Returns true when history was compacted and the
// outgoing message slice must be rebuilt.
func (a *Agent) maybeAutoCompact(ctx context.Context) bool {
	comp := &a.cfg.Compaction
	if !comp.IsEnabled() {
		return false
	}
	window, source := a.contextWindow()
	if window <= 0 {
		return false
	}
	rs, ok := a.state.(rulesState)
	if !ok {
		return false
	}
	b := rs.GetLastContextBreakdown()
	if b == nil || b.EstimatedTotal <= 0 {
		return false
	}
	if b.EstimatedTotal*100 < comp.EffectiveThresholdPercent()*window {
		return false
	}
	res, err := a.CompactSession(ctx, CompactOptions{})
	if err != nil {
		switch {
		case errors.Is(err, ErrNothingToCompact):
			// Over the threshold with only the prompt being answered in the
			// window: said once per turn, not before every step of it.
			if !a.autoCompactSkipLogged {
				a.autoCompactSkipLogged = true
				a.log.Info("auto-compaction skipped: no earlier turn to fold, the prompt being answered stays verbatim",
					"estimatedTokens", b.EstimatedTotal,
					"contextWindow", window,
					"contextWindowSource", source,
					"thresholdPercent", comp.EffectiveThresholdPercent())
			}
		case errors.Is(err, ErrCompactionBlocked):
			a.log.Info("auto-compaction vetoed by a hook; continuing uncompacted", "error", err)
		default:
			a.log.Warn("auto-compaction failed; continuing uncompacted", "error", err)
		}
		return false
	}
	a.log.Info("auto-compacted session context",
		"estimatedTokens", b.EstimatedTotal,
		"contextWindow", window,
		"contextWindowSource", source,
		"thresholdPercent", comp.EffectiveThresholdPercent(),
		"compactedMessages", res.CompactedMessages,
		"keptMessages", res.KeptMessages)
	return true
}

// compactionCandidate is one summarizer of the chain a compaction may use.
type compactionCandidate struct {
	provider llm.Provider
	modelID  string
}

// compactionChain is the summarizers a compaction tries, in order: the model
// named for this one call (override, already resolved to a models[].model),
// compaction.model (or the session's model when it is unset), then
// compaction.fallback_models, then the session's own model as the last resort.
// A model that names nothing configured, or whose provider cannot be built, is
// left out rather than failing the chain - a compaction is what a session out
// of room has left, and one bad entry must not be the end of it (issue #247).
// The error is returned only when nothing in the chain resolves.
func (a *Agent) compactionChain(override string) ([]compactionCandidate, error) {
	sessionModel := a.state.EffectiveModelID(a.cfg)
	configured := strings.TrimSpace(a.cfg.Compaction.Model)
	if configured == "" {
		configured = sessionModel
	}
	wanted := []string{override, configured}
	for _, m := range a.cfg.Compaction.FallbackModels {
		wanted = append(wanted, strings.TrimSpace(m))
	}
	wanted = append(wanted, sessionModel)

	mk := a.providerFactory
	if mk == nil {
		mk = llm.NewProvider
	}
	var out []compactionCandidate
	seen := make(map[string]bool, len(wanted))
	var firstErr error
	for _, modelID := range wanted {
		if modelID == "" || seen[modelID] {
			continue
		}
		seen[modelID] = true
		rm, err := a.cfg.ResolveLLM(modelID)
		if err == nil {
			var provider llm.Provider
			provider, err = mk(a.llmProviderInput(rm))
			if err == nil {
				out = append(out, compactionCandidate{provider: provider, modelID: modelID})
				continue
			}
		}
		if firstErr == nil {
			firstErr = err
		}
		a.log.Warn("compaction summarizer unavailable; trying the next one", "model", modelID, "error", err)
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("no model configured")
	}
	return out, nil
}

// lastToolCallMessageIndex is the index of the last assistant message that
// carries tool calls, or -1.
func lastToolCallMessageIndex(msgs []llm.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			return i
		}
	}
	return -1
}

// renderCompactionMessage is one transcript entry as the summarizer reads it.
// Tool calls are rendered as labeled lines so it sees what happened without
// replaying structured calls. The empty string is an entry that carries
// nothing to summarize.
func renderCompactionMessage(m llm.Message) string {
	if m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(string(m.Role))
	if m.CompactionSummary {
		b.WriteString(" (earlier summary)")
	}
	b.WriteString(":\n")
	if strings.TrimSpace(m.Content) != "" {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	for _, tc := range m.ToolCalls {
		fmt.Fprintf(&b, "[tool call] %s %s\n", tc.Name, tc.InputJSON)
	}
	b.WriteString("\n")
	return b.String()
}

// renderCompactionTranscript is the whole run of messages the summarizer reads
// in one call.
func renderCompactionTranscript(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(renderCompactionMessage(m))
	}
	return b.String()
}

// buildCompactionRequest flattens the head of the conversation into a single
// summarization request.
func buildCompactionRequest(head []llm.Message, instructions string) []llm.Message {
	return compactionRequest("", renderCompactionTranscript(head), instructions)
}

// compactionRequest is one summarization call: the summary of everything
// folded so far (empty on a single-pass compaction and on the first pass of a
// multi-step one) followed by the next run of transcript. carry travels as
// part of the same user message so a provider that caches by prefix is not
// asked to keep a message that changes every pass.
func compactionRequest(carry, body, instructions string) []llm.Message {
	var b strings.Builder
	if strings.TrimSpace(carry) != "" {
		b.WriteString("This conversation is being summarized in several passes because it does not fit one request. ")
		b.WriteString("Below is the summary of everything before this point, then the next part of the transcript. ")
		b.WriteString("Answer with one summary that covers both, in the same format.\n\n<summary-so-far>\n")
		b.WriteString(carry)
		b.WriteString("\n</summary-so-far>\n\n<transcript>\n")
	} else {
		b.WriteString("Summarize the following conversation transcript.\n\n<transcript>\n")
	}
	b.WriteString(body)
	b.WriteString("</transcript>")
	if s := strings.TrimSpace(instructions); s != "" {
		b.WriteString("\n\nAdditional instructions from the user for this summary:\n")
		b.WriteString(s)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: prompts.WithIdentity(compactionSystemPrompt)},
		{Role: llm.RoleUser, Content: b.String()},
	}
}
