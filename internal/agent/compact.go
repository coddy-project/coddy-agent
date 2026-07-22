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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ErrNothingToCompact is returned when the history has no full user turn to
// fold away before the keep-recent boundary.
var ErrNothingToCompact = errors.New("nothing to compact")

// ErrCompactionDisabled is returned when compaction.enabled is false.
var ErrCompactionDisabled = errors.New("compaction is disabled (compaction.enabled)")

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
// inserts the summary row at that boundary. instructions optionally augments
// the summarization request (from the manual compact command arguments).
//
// When force is set (manual /compact), compaction always folds whatever exists:
// if the configured keep-recent window leaves nothing to summarize, it retries
// with progressively fewer kept turns (down to zero) so even a very short
// conversation compacts. Auto-compaction passes force=false and only runs at the
// normal boundary.
func (a *Agent) CompactSession(ctx context.Context, instructions string, force bool) (*CompactionResult, error) {
	if !a.cfg.Compaction.IsEnabled() {
		return nil, ErrCompactionDisabled
	}

	msgs := a.state.GetMessages()
	keep := a.cfg.Compaction.EffectiveKeepRecentTurns()
	splitIdx, ok := session.CompactionSplitIndex(msgs, keep)
	if !ok && force {
		for k := keep - 1; k >= 0 && !ok; k-- {
			splitIdx, ok = session.CompactionSplitIndex(msgs, k)
		}
	}
	if !ok {
		return nil, ErrNothingToCompact
	}
	head := session.MessagesForLLM(msgs[:splitIdx])

	provider, modelID, err := a.compactionProvider()
	if err != nil {
		return nil, fmt.Errorf("compaction model: %w", err)
	}

	resp, err := provider.Complete(ctx, buildCompactionRequest(head, instructions), nil)
	if err != nil {
		return nil, fmt.Errorf("compaction LLM call: %w", err)
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return nil, fmt.Errorf("compaction produced an empty summary")
	}

	a.state.InsertCompactionSummary(splitIdx, session.NewCompactionSummaryMessage(summary, modelID))

	return &CompactionResult{
		Summary:           summary,
		CompactedMessages: len(head),
		KeptMessages:      len(msgs) - splitIdx,
		Model:             modelID,
	}, nil
}

// CompactCommandName is the built-in slash command that triggers compaction.
const CompactCommandName = "compact"

// CompactCommandDescription is shown in slash-command catalogs.
const CompactCommandDescription = "Summarize older conversation history to free context; recent turns stay verbatim"

// parseCompactCommand reports whether the prompt text invokes the built-in
// /compact command and returns the trailing summarizer instructions.
func parseCompactCommand(text string) (instructions string, ok bool) {
	t := strings.TrimSpace(text)
	const cmd = "/" + CompactCommandName
	if t == cmd {
		return "", true
	}
	for _, sep := range []string{" ", "\t", "\n"} {
		if rest, found := strings.CutPrefix(t, cmd+sep); found {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// runCompactCommand executes the built-in /compact command for a prompt turn.
// Manual compaction is forced (folds whatever exists, even a short chat). The
// command text is persisted as a user message so it shows in the transcript; the
// outcome is streamed as one agent message chunk and stored as an assistant
// message. The generated summary is inserted as a compaction row, which the UI
// renders as its own foldout ("what is now in context").
func (a *Agent) runCompactCommand(ctx context.Context, instructions, rawCommand string) (string, error) {
	res, err := a.CompactSession(ctx, instructions, true)
	// Show the command in the transcript, regardless of the outcome.
	a.addUserCommandMessage(rawCommand)
	var text string
	switch {
	case errors.Is(err, ErrNothingToCompact):
		text = "Nothing to compact: there is no earlier conversation to summarize yet."
	case errors.Is(err, ErrCompactionDisabled):
		text = "Compaction is disabled in the configuration (compaction.enabled: false)."
	case err != nil:
		return string(acp.StopReasonRefused), err
	default:
		text = fmt.Sprintf("Context compacted: %d message(s) summarized, %d kept verbatim.", res.CompactedMessages, res.KeptMessages)
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
	return string(acp.StopReasonEndTurn), nil
}

// addUserCommandMessage persists the raw text of a built-in slash command
// (/compact, /plugin) as a user message so it appears in the transcript like any
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
// compaction.threshold_percent of the effective model's max_context_tokens.
// It is fail-open: any error (including nothing-to-compact right after a
// previous compaction) leaves the turn running uncompacted. Returns true when
// history was compacted and the outgoing message slice must be rebuilt.
func (a *Agent) maybeAutoCompact(ctx context.Context) bool {
	comp := &a.cfg.Compaction
	if !comp.IsEnabled() {
		return false
	}
	ent := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	if ent == nil || ent.MaxContextTokens <= 0 {
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
	if b.EstimatedTotal*100 < comp.EffectiveThresholdPercent()*ent.MaxContextTokens {
		return false
	}
	res, err := a.CompactSession(ctx, "", false)
	if err != nil {
		if !errors.Is(err, ErrNothingToCompact) {
			a.log.Warn("auto-compaction failed; continuing uncompacted", "error", err)
		}
		return false
	}
	a.log.Info("auto-compacted session context",
		"estimatedTokens", b.EstimatedTotal,
		"maxContextTokens", ent.MaxContextTokens,
		"thresholdPercent", comp.EffectiveThresholdPercent(),
		"compactedMessages", res.CompactedMessages,
		"keptMessages", res.KeptMessages)
	return true
}

// compactionProvider resolves the summarizer provider: compaction.model when
// set, otherwise the session's effective model.
func (a *Agent) compactionProvider() (llm.Provider, string, error) {
	modelID := strings.TrimSpace(a.cfg.Compaction.Model)
	if modelID == "" {
		modelID = a.state.EffectiveModelID(a.cfg)
	}
	if modelID == "" {
		return nil, "", fmt.Errorf("no model configured")
	}
	rm, err := a.cfg.ResolveLLM(modelID)
	if err != nil {
		return nil, "", err
	}
	mk := a.providerFactory
	if mk == nil {
		mk = llm.NewProvider
	}
	provider, err := mk(a.llmProviderInput(rm))
	if err != nil {
		return nil, "", err
	}
	return provider, modelID, nil
}

// buildCompactionRequest flattens the head of the conversation into a single
// summarization request. Tool calls and results are rendered as labeled lines
// so the summarizer sees what happened without replaying structured calls.
func buildCompactionRequest(head []llm.Message, instructions string) []llm.Message {
	var b strings.Builder
	b.WriteString("Summarize the following conversation transcript.\n\n<transcript>\n")
	for _, m := range head {
		if m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
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
	}
	b.WriteString("</transcript>")
	if s := strings.TrimSpace(instructions); s != "" {
		b.WriteString("\n\nAdditional instructions from the user for this summary:\n")
		b.WriteString(s)
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: compactionSystemPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}
}
