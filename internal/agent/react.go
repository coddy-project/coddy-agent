// Package agent implements the ReAct (Reasoning + Acting) loop for a session turn.
// System prompts are rendered via internal/prompts (embedded templates or prompts.dir).
package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// SessionState is the interface Agent needs from a session.
// It is implemented by session.State without requiring a direct import.
type SessionState interface {
	GetID() string
	GetCWD() string
	GetMode() string
	SetMode(mode string)
	EffectiveModelID(cfg *config.Config) string
	EffectiveReasoning(cfg *config.Config) string
	AddMessage(msg llm.Message)
	GetMessages() []llm.Message
	InsertCompactionSummary(idx int, msg llm.Message)
	GetMCPClients() []*mcp.Client
	GetMCPToolFilter() func(server, tool string) bool
	GetSkills() []*skills.Skill
	GetAgentMemory() string
	GetMemoryCopilotBlock() string
	SetMemoryCopilotBlock(text string)
	ClearMemoryCopilotBlock()
	GetPlan() []acp.PlanEntry
	SetPlan([]acp.PlanEntry)
	GetPersistedSessionDir() string
	AppendPlanDocument(plans.Document)
	DiscardedPlanSlugs() []string
	TakePendingPlanContext() string
	TakePendingImageParts() []llm.ImagePart
	GetPermissionMode() string
	IsUserCancelledTurn() bool
}

// Agent runs the ReAct loop for a single session turn.
type Agent struct {
	cfg             *config.Config
	state           SessionState
	server          acp.UpdateSender
	log             *slog.Logger
	registry        *tools.Registry
	environment     platform.Environment
	providerFactory func(llm.ProviderInput) (llm.Provider, error)
	configReloader  func(context.Context) ([]string, error)
}

// NewAgent creates an Agent for a prompt turn.
func NewAgent(cfg *config.Config, state SessionState, server acp.UpdateSender, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	environment := platform.CurrentEnvironment()
	return &Agent{
		cfg:             cfg,
		state:           state,
		server:          server,
		log:             log,
		registry:        tools.NewRegistryForEnvironment(cfg, environment),
		environment:     environment,
		providerFactory: llm.NewProvider,
	}
}

// SetProviderFactory replaces the LLM provider factory used by subsequent turns.
func (a *Agent) SetProviderFactory(mk func(llm.ProviderInput) (llm.Provider, error)) {
	if a == nil || mk == nil {
		return
	}
	a.providerFactory = mk
}

// SetConfigReloader wires config_commit and config_rollback to the process/session runtime owner.
func (a *Agent) SetConfigReloader(reload func(context.Context) ([]string, error)) {
	if a == nil {
		return
	}
	a.configReloader = reload
}

// Run executes the ReAct loop and returns the stop reason.
func (a *Agent) Run(ctx context.Context, prompt []acp.ContentBlock) (string, error) {
	mode := a.state.GetMode()

	// Build the user message from prompt content blocks.
	a.state.ClearMemoryCopilotBlock()
	userText := contentBlocksToText(prompt)

	// The built-in /compact command compacts history instead of running the
	// ReAct loop. The command text is persisted (so it shows in the transcript
	// like any other message) by runCompactCommand itself.
	if instructions, ok := parseCompactCommand(userText); ok {
		return a.runCompactCommand(ctx, instructions, userText)
	}
	// The built-in /plugin command manages skill plugins and marketplaces
	// deterministically, without an LLM turn; the command text is persisted too.
	if args, ok := parsePluginCommand(userText); ok {
		return a.runPluginCommand(ctx, args, userText)
	}
	imageParts := a.state.TakePendingImageParts()
	messageContent := userText
	if note := filePathsNote(imageParts); note != "" {
		messageContent = userText + "\n\n" + note
	}
	a.state.AddMessage(llm.Message{
		Role:       llm.RoleUser,
		Content:    messageContent,
		ImageParts: imageParts,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	a.runMemoryBeforeTurn(ctx, userText)

	// Collect context files from the prompt for skill filtering.
	contextFiles := extractContextFiles(prompt)

	// Load skills applicable to this context.
	activeSkills := FilterSkillsForContext(a.state.GetSkills(), contextFiles)

	toolDefs := a.currentToolDefinitions(mode)

	// Get or create LLM provider.
	transport, err := a.getProvider(mode)
	if err != nil {
		return string(acp.StopReasonRefused), fmt.Errorf("no LLM configured: %w", err)
	}

	// Restore existing plan via session/update if one was set by coddy todo tools in a previous turn.
	if existing := a.state.GetPlan(); len(existing) > 0 {
		if err := a.sendPlan(a.state.GetID(), existing); err != nil {
			a.log.Warn("failed to restore plan", "error", err)
		}
	}

	// Build the full message list starting with system prompt (refreshed each ReAct turn).
	messages := a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))

	// buildSystemPrompt refreshed the context breakdown; compact before the
	// first LLM call when the estimate crossed the auto-compaction threshold.
	if a.maybeAutoCompact(ctx) {
		messages = a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))
	}

	maxTurns := a.cfg.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}

	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())

	toolEnv := &tools.Env{
		CWD:              a.state.GetCWD(),
		PermissionMode:   effectivePermMode(a.state, a.cfg),
		CommandAllowlist: a.cfg.Tools.CommandAllowlist,
		SessionID:        a.state.GetID(),
		SessionDir:       sd,
		ArchiveActiveMarkdown: func() error {
			if sd == "" {
				return nil
			}
			return session.ArchiveActiveTodo(sd)
		},
		WriteArchivedPlanMarkdown: func(md string) (string, error) {
			if sd == "" {
				return "", nil
			}
			return session.WritePlanArchivedMarkdown(sd, md)
		},
		Sender:  a.server,
		GetPlan: a.state.GetPlan,
		SetPlan: a.state.SetPlan,
		SetSessionMode: func(mode string) error {
			a.state.SetMode(strings.TrimSpace(mode))
			return nil
		},
		PersistPlanDocument: func(doc plans.Document) {
			a.state.AppendPlanDocument(doc)
		},
		SSHConnectTimeout: a.cfg.Tools.SSHConnectTimeout,
		LoadSkillBody:     a.loadSkillBody,
		ConfigPath:        a.cfg.Paths.ConfigPath,
		ConfigHome:        a.cfg.Paths.Home,
		ConfigCWD:         a.cfg.Paths.CWD,
		OutputLineLimits:  a.cfg.Tools.OutputLimits.AsMap(),
		Background:        a.backgroundPool(sd),
		BackgroundEnabled: a.cfg.Tools.Background.ResolvedEnabled(),
	}
	if a.configReloader != nil {
		toolEnv.ReloadConfig = func(ctx context.Context) ([]string, error) {
			warnings, err := a.configReloader(ctx)
			if err != nil {
				return warnings, err
			}
			next, err := config.LoadWithPaths(a.cfg.Paths)
			if err != nil {
				return warnings, err
			}
			a.cfg = next
			a.registry = tools.NewRegistryForEnvironment(next, a.environment)
			return warnings, nil
		}
	}
	toolEnv.SendDesignPlanUpdate = func(doc plans.Document) {
		tools.SendDesignPlanUpdate(toolEnv, doc)
	}

	return a.runReActLoop(ctx, mode, messages, toolDefs, transport, toolEnv, sd, userText, contextFiles, activeSkills, maxTurns)
}

// maxEmptyAssistantContinuations bounds how many times the ReAct loop re-prompts a model
// that ended a turn with no visible answer and no tool call (only reasoning, or nothing).
// It guards against dead-ending the conversation on a thinking-only bubble — seen with
// gpt-oss / harmony endpoints that leak a tool call into the reasoning channel — while
// preventing an unbounded empty-turn loop.
const maxEmptyAssistantContinuations = 2

// emptyAssistantContinuationNudge is injected into the LLM-facing message slice (never
// persisted to the transcript) to prompt the model to produce its answer or a tool call
// after an empty turn.
const emptyAssistantContinuationNudge = "Your previous message had no answer text and no tool call. Continue now: call the appropriate tool to act, or write your reply to the user."

// Loop-guard nudges are injected into the LLM-facing message slice only (never
// persisted to the transcript), the same way emptyAssistantContinuationNudge is.
// The repeated passage itself is stripped from the assistant message before the
// replay, so the nudge does not carry the loop straight back into the model.
const (
	streamLoopNudge = "Your previous response was cut off because it degenerated into repeating the same passage over and over. Do not continue that text. Decide what is actually left to do, then either call the appropriate tool or write a short, concrete reply to the user."

	reasoningLoopNudge = "Your previous turn was cut off because your reasoning kept repeating the same thought without reaching a conclusion. Stop deliberating and act: call the appropriate tool, or write your reply to the user now."

	toolLoopNudge = "You have requested the same tool call with identical arguments several times in a row, so it was not executed again. Repeating it will not produce a different result. Use what you already have: try a different tool or different arguments, or answer the user with the information you have."

	toolLoopSkippedResult = "not executed: the loop guard stopped this turn after repeated identical tool calls"
)

// loopAbortChannel names the streamed channel that degenerated into a loop.
type loopAbortChannel int

const (
	loopAbortNone loopAbortChannel = iota
	loopAbortText
	loopAbortReasoning
)

func (a *Agent) runReActLoop(
	ctx context.Context,
	mode string,
	messages []llm.Message,
	toolDefs []llm.ToolDefinition,
	transport llmTransport,
	toolEnv *tools.Env,
	sd, userText string,
	contextFiles []string,
	activeSkills []*skills.Skill,
	maxTurns int,
) (string, error) {
	var totalInputTokens, totalOutputTokens int
	var turnIndex int
	var lastStatsWrite time.Time
	var emptyContinuations int
	// Tracks whether any visible answer text was streamed to the user during this
	// turn, so an all-reasoning turn that never answers surfaces a notice instead
	// of dead-ending silently.
	var turnHadVisibleText bool

	// Runaway-loop protection. The tool detector spans the whole user turn (a model
	// can repeat the same call across ReAct rounds, not only inside one response);
	// the stream detectors are per LLM call and created below. loopNudges is the
	// shared budget: once it runs out, the next detected loop stops the turn.
	guardOn := a.cfg.Agent.LoopGuardEnabled()
	streamRepeatCycles := 0
	var toolRepeats *toolRepeatDetector
	loopNudgeBudget := 0
	if guardOn {
		streamRepeatCycles = a.cfg.Agent.EffectiveLoopStreamRepeatCycles()
		toolRepeats = newToolRepeatDetector(a.cfg.Agent.EffectiveLoopToolRepeatLimit())
		loopNudgeBudget = a.cfg.Agent.EffectiveLoopNudgeMax()
	}
	loopNudges := 0

	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return string(acp.StopReasonCancelled), nil
		}

		// System prompt is rebuilt every turn so conditional sections (e.g. todo checklist) match
		// state after coddy_todo_* tools in the same user turn.
		if len(messages) > 0 && messages[0].Role == llm.RoleSystem {
			messages[0].Content = a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles)
		}

		// Tool results can grow the context mid-turn; compact between LLM calls
		// when the refreshed estimate crossed the threshold. Run already checked
		// before the first call. Ephemeral continuation nudges are not part of
		// persisted state and are dropped by the rebuild (acceptable: the model
		// answered or called a tool since then).
		if turn > 0 && a.maybeAutoCompact(ctx) {
			messages = a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))
		}

		// Call LLM and stream response.
		var response *llm.Response
		var streamErr error
		var reasoningBuf strings.Builder

		reasonClockStart := time.Time{}
		reasonClockEnd := time.Time{}
		maybeMarkReasonEnd := func(now time.Time) {
			if reasonClockStart.IsZero() || !reasonClockEnd.IsZero() {
				return
			}
			if strings.TrimSpace(reasoningBuf.String()) == "" {
				return
			}
			reasonClockEnd = now
		}

		sessionID := a.state.GetID()

		// Cancel the stream if no tokens arrive within the configured window
		// (agent.llm_first_token_timeout_ms, default 90 s; the API hang guard). A model
		// configured with stream: false produces nothing until the whole completion is
		// ready, so the guard would cut every slow blocking answer: it is not armed for
		// that transport, and the turn context remains the bound. An explicit 0 disables
		// the guard for streaming too. firstTokenTimedOut
		// records that this timer, and not the user or the loop guard, did the
		// cancelling, which the error paths below cannot otherwise tell apart.
		firstTokenTimeout := a.cfg.Agent.EffectiveLLMFirstTokenTimeout()
		streamCtx, streamCancel := context.WithCancel(ctx)
		var firstTokenTimedOut atomic.Bool
		var firstTokenTimer *time.Timer
		if transport.streaming && firstTokenTimeout > 0 {
			firstTokenTimer = time.AfterFunc(firstTokenTimeout, func() {
				firstTokenTimedOut.Store(true)
				streamCancel()
			})
		}
		stopFirstTokenTimer := func() {
			if firstTokenTimer != nil {
				firstTokenTimer.Stop()
			}
		}

		// One detector per streamed channel: a degenerating thinking channel burns
		// exactly as many tokens as visible text while showing nothing in the
		// transcript. Tripping cancels the stream the same way the first-token timer
		// does; the branch after the call decides whether to nudge or stop.
		textLoop := newStreamRepeatDetector(streamRepeatCycles)
		reasonLoop := newStreamRepeatDetector(streamRepeatCycles)
		loopAbort := loopAbortNone

		emitReason := func(d string, now time.Time) {
			stopFirstTokenTimer()
			reasoningBuf.WriteString(d)
			// The clock measures wall time between the first reasoning delta and the
			// first answer text. A blocking response replays both back to back once
			// generation has already finished, so the only honest reading is none:
			// leaving the clock unset omits reasoning_duration_ms instead of
			// persisting a fabricated millisecond.
			if transport.streaming && reasonClockStart.IsZero() {
				reasonClockStart = now
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeReasoning, Text: d},
			})
		}
		emitText := func(delta string, now time.Time, markReasonEnd bool) {
			stopFirstTokenTimer()
			if markReasonEnd && strings.TrimSpace(delta) != "" {
				maybeMarkReasonEnd(now)
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: delta},
			})
		}

		// Prune superseded read/grep results from the projection sent to the model;
		// the working `messages` slice keeps full content (copy-on-write) so state,
		// the transcript, and later appends stay intact.
		sendMessages := a.prunedForLLM(messages)
		response, streamErr = transport.provider.Stream(streamCtx, sendMessages, toolDefs, func(chunk llm.StreamChunk) {
			if streamCtx.Err() != nil {
				return
			}
			now := time.Now()
			if chunk.ReasoningDelta != "" {
				emitReason(chunk.ReasoningDelta, now)
				if _, tripped := reasonLoop.Add(chunk.ReasoningDelta); tripped && loopAbort == loopAbortNone {
					loopAbort = loopAbortReasoning
					streamCancel()
					return
				}
			}
			if chunk.TextDelta != "" {
				if _, tripped := textLoop.Add(chunk.TextDelta); tripped && loopAbort == loopAbortNone {
					loopAbort = loopAbortText
					emitText(chunk.TextDelta, now, true)
					streamCancel()
					return
				}
			}
			if chunk.TextDelta != "" && strings.TrimSpace(chunk.TextDelta) != "" {
				emitText(chunk.TextDelta, now, true)
			} else if chunk.TextDelta != "" {
				emitText(chunk.TextDelta, now, false)
			}
			if chunk.ToolCall != nil && chunk.ToolCall.Name != "" {
				maybeMarkReasonEnd(now)
				if st := sessionStatePtr(a.state); st != nil {
					if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" && strings.TrimSpace(chunk.ToolCall.ID) != "" {
						_ = session.WriteToolCallMeta(sd, chunk.ToolCall.ID, session.ToolCallMeta{
							ToolCallID: strings.TrimSpace(chunk.ToolCall.ID),
							Name:       chunk.ToolCall.Name,
							Kind:       toolKind(chunk.ToolCall.Name),
							Status:     "pending",
						})
					}
				}
				_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: acp.UpdateTypeToolCall,
					ToolCallID:    chunk.ToolCall.ID,
					Title:         chunk.ToolCall.Name, // plain name, no "Calling: " prefix
					Kind:          toolKind(chunk.ToolCall.Name),
					Status:        "pending",
				})
				stopFirstTokenTimer()
			}
		})
		stopFirstTokenTimer()
		streamCancel()

		// The loop guard cancelled this stream: keep the useful part of the answer,
		// drop the repeated run so it is never replayed to the model, and either nudge
		// the model back on track or stop the turn with a notice. Checked before the
		// generic cancellation handling below, which cannot tell a guard abort from a
		// user Stop. A real cancellation racing the guard wins: the user asked to stop,
		// so the turn must not be re-prompted.
		if loopAbort != loopAbortNone && ctx.Err() == nil && !a.state.IsUserCancelledTurn() {
			a.persistLoopAbortedMessage(response, &reasoningBuf, reasonClockStart, reasonClockEnd, streamRepeatCycles)
			if loopNudges >= loopNudgeBudget {
				return string(acp.StopReasonRefused), loopAbortError(loopAbort)
			}
			loopNudges++
			messages = a.buildMessages(a.buildSystemPrompt(mode, activeSkills, toolDefs, userText, contextFiles))
			nudge := streamLoopNudge
			if loopAbort == loopAbortReasoning {
				nudge = reasoningLoopNudge
			}
			// LLM-facing only; never persisted to the transcript.
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: nudge})
			a.log.Warn("loop guard cut a degenerating response",
				"channel", loopAbortChannelName(loopAbort), "nudge", loopNudges)
			continue
		}

		// If the stream was cancelled by the first-token timer (no output produced, no user cancel),
		// surface a timeout error instead of a silent failure. The timer itself reports
		// that it fired, so a cancellation from anywhere else is never mislabelled.
		if firstTokenTimedOut.Load() && streamErr != nil && errors.Is(streamErr, context.Canceled) && !a.state.IsUserCancelledTurn() {
			hasAnyOutput := response != nil && (strings.TrimSpace(response.Content) != "" ||
				len(response.ToolCalls) > 0 || strings.TrimSpace(reasoningBuf.String()) != "")
			if !hasAnyOutput {
				return string(acp.StopReasonRefused), fmt.Errorf("model did not respond (no output within %v)", firstTokenTimeout)
			}
		}

		if streamErr != nil {
			// A mid-generation truncation keeps its partial answer like a user
			// stop: the user already watched the text stream in, so it must
			// survive in the transcript next to the honest error below.
			if (errors.Is(streamErr, context.Canceled) || llm.IsStreamTruncated(streamErr)) && response != nil {
				reasonTrim := strings.TrimSpace(reasoningBuf.String())
				hasText := strings.TrimSpace(response.Content) != ""
				hasTools := len(response.ToolCalls) > 0
				if hasText || hasTools || reasonTrim != "" {
					var reasoningMs int64
					if reasonTrim != "" && !reasonClockStart.IsZero() {
						end := reasonClockEnd
						if end.IsZero() {
							end = time.Now()
						}
						d := end.Sub(reasonClockStart)
						if d < 0 {
							d = 0
						}
						reasoningMs = d.Milliseconds()
					}
					reasonStore, reasonSig := reasoningForStorage(reasonTrim, reasoningBuf.String(), response)
					assistantMsg := llm.Message{
						Role:                llm.RoleAssistant,
						Content:             response.Content,
						Reasoning:           reasonStore,
						ReasoningSignature:  reasonSig,
						ToolCalls:           response.ToolCalls,
						ReasoningDurationMs: reasoningMs,
						Model:               a.state.EffectiveModelID(a.cfg),
						CreatedAt:           time.Now().UTC().Format(time.RFC3339),
					}
					a.state.AddMessage(assistantMsg)
					a.refreshConversationContextUsage(true)
				}
			}
			if errors.Is(streamErr, context.Canceled) {
				// If output was already streamed, treat as a clean user-stop regardless.
				hasAnyOutput := response != nil && (strings.TrimSpace(response.Content) != "" ||
					len(response.ToolCalls) > 0 || strings.TrimSpace(reasoningBuf.String()) != "")
				if hasAnyOutput || a.state.IsUserCancelledTurn() {
					return string(acp.StopReasonCancelled), nil
				}
				// Stream was interrupted before producing any output and the user did not stop it —
				// surface an error so the UI can show feedback instead of silently completing.
				return string(acp.StopReasonRefused), fmt.Errorf("generation was interrupted before producing a response")
			}
			if ctx.Err() != nil {
				// Context cancelled for non-context-Canceled stream error: still propagate the real error.
				return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w", streamErr)
			}
			return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w", streamErr)
		}

		// Accumulate and broadcast token usage after each LLM call.
		totalInputTokens += response.InputTokens
		totalOutputTokens += response.OutputTokens
		_ = a.server.SendSessionUpdate(sessionID, acp.TokenUsageUpdate{
			SessionUpdate: acp.UpdateTypeTokenUsage,
			InputTokens:   response.InputTokens,
			OutputTokens:  response.OutputTokens,
			TotalTokens:   totalInputTokens + totalOutputTokens,
		})

		if sd != "" {
			now := time.Now().UTC()
			if lastStatsWrite.IsZero() || now.Sub(lastStatsWrite) > 750*time.Millisecond {
				lastStatsWrite = now
				stats := session.SessionStats{
					Version:   1,
					UpdatedAt: now.Format(time.RFC3339),
					TokenUsageTotal: session.TokenUsageTotals{
						InputTokens:  totalInputTokens,
						OutputTokens: totalOutputTokens,
						TotalTokens:  totalInputTokens + totalOutputTokens,
					},
					TokenUsageByTurn: []session.TokenUsageTurn{{
						TurnIndex:    turnIndex,
						InputTokens:  response.InputTokens,
						OutputTokens: response.OutputTokens,
						TotalTokens:  totalInputTokens + totalOutputTokens,
						Timestamp:    now.Format(time.RFC3339),
					}},
				}
				if rs, ok := a.state.(rulesState); ok {
					if b := rs.GetLastContextBreakdown(); b != nil {
						cp := *b
						stats.ContextBreakdown = &cp
					}
				}
				_ = session.WriteSessionStats(sd, stats)
			}
		}
		turnIndex++

		reasonTrim := strings.TrimSpace(reasoningBuf.String())
		var reasoningMs int64
		if reasonTrim != "" && !reasonClockStart.IsZero() {
			end := reasonClockEnd
			if end.IsZero() {
				end = time.Now()
			}
			d := end.Sub(reasonClockStart)
			if d < 0 {
				d = 0
			}
			reasoningMs = d.Milliseconds()
		}

		// Append assistant message to history.
		reasonStore, reasonSig := reasoningForStorage(reasonTrim, reasoningBuf.String(), response)
		assistantMsg := llm.Message{
			Role:                llm.RoleAssistant,
			Content:             response.Content,
			Reasoning:           reasonStore,
			ReasoningSignature:  reasonSig,
			ToolCalls:           response.ToolCalls,
			ReasoningDurationMs: reasoningMs,
			Model:               a.state.EffectiveModelID(a.cfg),
			CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		}
		messages = append(messages, assistantMsg)
		a.state.AddMessage(assistantMsg)
		a.refreshConversationContextUsage(true)
		if strings.TrimSpace(response.Content) != "" {
			turnHadVisibleText = true
		}

		// If no tool calls, we're done — unless the model produced no visible answer at
		// all (empty content). Some models (notably gpt-oss / harmony endpoints) sometimes
		// end a turn with only internal reasoning — occasionally leaking a tool call into
		// the reasoning channel — emitting neither final content nor a tool_calls array.
		// Returning here would dead-end the conversation on a lone "thinking" bubble, so
		// re-prompt the model a bounded number of times before giving up.
		if len(response.ToolCalls) == 0 {
			if strings.TrimSpace(response.Content) == "" && emptyContinuations < maxEmptyAssistantContinuations {
				emptyContinuations++
				// LLM-facing only; never persisted to the transcript.
				messages = append(messages, llm.Message{
					Role:    llm.RoleUser,
					Content: emptyAssistantContinuationNudge,
				})
				continue
			}
			if response.StopReason == "max_tokens" {
				return string(acp.StopReasonMaxTokens), nil
			}
			// The turn produced no visible answer at all (only reasoning / empty
			// content) and the model never recovered after the continuation nudges.
			// Surface a clear notice (rendered as a system message with a Retry
			// control in the UI) instead of dead-ending silently on a thinking-only
			// turn — otherwise the user sees no assistant reply. Seen with gpt-oss /
			// harmony endpoints that route the tool call through the reasoning channel.
			if strings.TrimSpace(response.Content) == "" && !turnHadVisibleText {
				return string(acp.StopReasonRefused), fmt.Errorf("model produced no reply: only internal reasoning, with no answer text or tool call")
			}
			return string(acp.StopReasonEndTurn), nil
		}

		// Execute all tool calls.
		for i, tc := range response.ToolCalls {
			if ctx.Err() != nil {
				return string(acp.StopReasonCancelled), nil
			}

			// A model stuck on the identical call (same name, same canonical arguments)
			// would otherwise burn the whole max_turns budget without an answer. Skip
			// the execution and tell it so; every tool_call_id still gets a result,
			// because OpenAI-compatible endpoints reject the next request otherwise.
			if _, tripped := toolRepeats.Observe(tc.Name, tc.InputJSON); tripped {
				if loopNudges >= loopNudgeBudget {
					a.recordSkippedToolCalls(&messages, response.ToolCalls[i:], toolLoopSkippedResult)
					return string(acp.StopReasonRefused), fmt.Errorf(
						"stopped: the model kept requesting the same %s call with identical arguments", tc.Name)
				}
				loopNudges++
				// The counter deliberately keeps running: clearing it here (as Roo does,
				// where the trip is a blocking question to the user) would let the model
				// execute the same call limit-1 more times per nudge. A genuinely
				// different call resets the counter on its own.
				a.log.Warn("loop guard blocked a repeated tool call", "tool", tc.Name, "nudge", loopNudges)
				a.recordSkippedToolCalls(&messages, response.ToolCalls[i:i+1], toolLoopNudge)
				continue
			}

			result, execErr := a.executeToolCall(ctx, tc, toolEnv, mode, a.state.GetID(), false)

			var toolResultMsg llm.Message
			if execErr != nil {
				toolResultMsg = llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf("error: %v", execErr),
					ToolCallID: tc.ID,
				}
			} else {
				toolResultMsg = llm.Message{
					Role:       llm.RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				}
			}

			messages = append(messages, toolResultMsg)
			a.state.AddMessage(toolResultMsg)
			a.refreshConversationContextUsage(true)
		}
		if toolEnv.ConfigReloaded {
			activeSkills = FilterSkillsForContext(a.state.GetSkills(), contextFiles)
			toolDefs = a.currentToolDefinitions(mode)
			toolEnv.PermissionMode = effectivePermMode(a.state, a.cfg)
			toolEnv.CommandAllowlist = append([]string(nil), a.cfg.Tools.CommandAllowlist...)
			toolEnv.SSHConnectTimeout = a.cfg.Tools.SSHConnectTimeout
			toolEnv.OutputLineLimits = a.cfg.Tools.OutputLimits.AsMap()
			toolEnv.Background = a.backgroundPool(sd)
			toolEnv.BackgroundEnabled = a.cfg.Tools.Background.ResolvedEnabled()
			toolEnv.ConfigReloaded = false
		}
		// The model made progress (executed tool calls), so reset the empty-turn
		// counter. The give-up notice is for CONSECUTIVE stalls (no answer and no
		// tool call), not for a slow multi-step task that keeps acting between
		// reasoning-only thoughts — otherwise a model that alternates thinking and
		// tool calls (gpt-oss / harmony) is abandoned mid-task.
		emptyContinuations = 0
	}

	return string(acp.StopReasonMaxTurns), nil
}

// persistLoopAbortedMessage stores the partial assistant message from a stream the
// loop guard cut, with the repeated run removed from both the answer text and the
// reasoning. Trimming here is what keeps the loop out of the context: buildMessages
// replays the transcript from session state, so a looped passage left in place would
// be fed straight back to the model on the nudge call (and to the compaction
// summarizer, and to every later turn) and would immediately re-seed the loop.
func (a *Agent) persistLoopAbortedMessage(
	response *llm.Response,
	reasoningBuf *strings.Builder,
	reasonClockStart, reasonClockEnd time.Time,
	minCycles int,
) {
	content := ""
	var toolCalls []llm.ToolCall
	if response != nil {
		content = response.Content
		toolCalls = response.ToolCalls
	}
	content, _ = trimRepeatedTail(content, minCycles)

	// Trim the raw buffer before trimming whitespace: dropping a trailing space
	// first would truncate the last cycle and misalign the repeat detection.
	reasonRaw := reasoningBuf.String()
	reasonRaw, reasonCut := trimRepeatedTail(reasonRaw, minCycles)
	reasonTrim := strings.TrimSpace(reasonRaw)

	var reasonStore, reasonSig string
	if reasonCut {
		// The Anthropic signature only validates against the exact reasoning text,
		// so a trimmed block must be replayed unsigned.
		reasonStore, reasonSig = reasonTrim, ""
	} else {
		reasonStore, reasonSig = reasoningForStorage(reasonTrim, reasonRaw, response)
	}

	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasonStore) == "" && len(toolCalls) == 0 {
		return
	}

	var reasoningMs int64
	if reasonTrim != "" && !reasonClockStart.IsZero() {
		end := reasonClockEnd
		if end.IsZero() {
			end = time.Now()
		}
		if d := end.Sub(reasonClockStart); d > 0 {
			reasoningMs = d.Milliseconds()
		}
	}

	a.state.AddMessage(llm.Message{
		Role:                llm.RoleAssistant,
		Content:             content,
		Reasoning:           reasonStore,
		ReasoningSignature:  reasonSig,
		ToolCalls:           toolCalls,
		ReasoningDurationMs: reasoningMs,
		Model:               a.state.EffectiveModelID(a.cfg),
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
	})
	a.refreshConversationContextUsage(true)
}

// recordSkippedToolCalls answers tool calls the loop guard refused to execute.
// Every tool_call_id an assistant message announced must get a result, otherwise
// OpenAI-compatible endpoints reject the next request in the conversation.
func (a *Agent) recordSkippedToolCalls(messages *[]llm.Message, calls []llm.ToolCall, reason string) {
	for _, tc := range calls {
		msg := llm.Message{
			Role:       llm.RoleTool,
			Content:    reason,
			ToolCallID: tc.ID,
		}
		*messages = append(*messages, msg)
		a.state.AddMessage(msg)
		_ = a.server.SendSessionUpdate(a.state.GetID(), acp.ToolCallStatusUpdate{
			SessionUpdate: acp.UpdateTypeToolCallUpdate,
			ToolCallID:    tc.ID,
			Status:        "cancelled",
			Content: []acp.ToolCallResultItem{
				{Type: "content", Content: acp.ContentBlock{Type: "text", Text: reason}},
			},
		})
	}
	a.refreshConversationContextUsage(true)
}

// loopAbortChannelName labels the streamed channel that looped, for logs.
func loopAbortChannelName(c loopAbortChannel) string {
	if c == loopAbortReasoning {
		return "reasoning"
	}
	return "text"
}

// loopAbortError is the notice surfaced when a turn keeps looping after every
// nudge. The session manager records it as a UI log entry with a Retry control.
func loopAbortError(c loopAbortChannel) error {
	if c == loopAbortReasoning {
		return fmt.Errorf("stopped: the model kept repeating the same reasoning without reaching an answer")
	}
	return fmt.Errorf("stopped: the model kept repeating the same output instead of finishing the task")
}

// executeToolCall runs a single tool call and reports updates to the client.
func (a *Agent) executeToolCall(ctx context.Context, tc llm.ToolCall, env *tools.Env, mode, sessionID string, skipPermission bool) (string, error) {
	env.ToolCallID = strings.TrimSpace(tc.ID)
	defer func() { env.ToolCallID = "" }()

	// Touching a directory pulls its nested AGENTS.md into the prompt. Done up
	// front so it holds regardless of the outcome below (permission denial,
	// tool error), and so both callers — the ReAct loop and the resume-after-
	// permission path — are covered without threading state through.
	a.activateScopedRulesForToolCall(tc.Name, tc.InputJSON, env.CWD)

	sessionDir := ""
	if st := sessionStatePtr(a.state); st != nil {
		sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	}

	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		_ = session.MarkToolCallStarted(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), "in_progress")
		_ = session.WriteToolCallArgs(sessionDir, tc.ID, tc.InputJSON)
	}

	// Mark as in_progress, include raw InputJSON so connected clients can show args.
	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        "in_progress",
		Content: []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: tc.InputJSON}},
		},
	})

	// Check if tool requires permission.
	tool, ok := a.registry.Get(tc.Name)
	requiresPerm := ok && tool.RequiresPermission

	var sessCmdGrants, sessWriteGrants []string
	if st := sessionStatePtr(a.state); st != nil {
		sessCmdGrants = st.GetPermissionCommandGrants()
		sessWriteGrants = st.GetPermissionWriteGrants()
	}

	if tc.Name == "run_command" {
		switch env.PermissionMode {
		case config.PermModeBypass:
			requiresPerm = false
		case config.PermModeAcceptEdits:
			cmd := permission.ExtractRunCommand(tc.InputJSON)
			if permission.CommandAllowedWithSession(env, sessCmdGrants, cmd) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		default: // ask
			cmd := permission.ExtractRunCommand(tc.InputJSON)
			if permission.CommandAllowedWithSession(env, sessCmdGrants, cmd) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		}
	} else if configWriteTool(tc.Name) {
		// Committing or rolling back the agent's own configuration can start
		// new MCP processes and change the permission policy itself, so
		// accept_edits does NOT auto-approve it the way it approves project
		// file writes. Only the explicit bypass mode skips the prompt.
		requiresPerm = env.PermissionMode != config.PermModeBypass
	} else if filesystemWriteTool(tc.Name) {
		switch env.PermissionMode {
		case config.PermModeBypass, config.PermModeAcceptEdits:
			keys := permission.WriteGrantKeys(tc.Name, tc.InputJSON, env.CWD)
			if permission.AllWriteKeysGranted(sessWriteGrants, keys) {
				requiresPerm = false
			} else {
				requiresPerm = false // auto-approve
			}
		default: // ask
			keys := permission.WriteGrantKeys(tc.Name, tc.InputJSON, env.CWD)
			if permission.AllWriteKeysGranted(sessWriteGrants, keys) {
				requiresPerm = false
			} else {
				requiresPerm = true
			}
		}
	}

	if requiresPerm && !skipPermission {
		promptBody := permission.PromptBody(tc.Name, tc.InputJSON)
		if tc.Name == "config_commit" {
			// The commit call itself carries no arguments, so the dialog must
			// show the staged commands it would apply (secrets redacted) -
			// otherwise the operator confirms blindly.
			if pending := tools.PendingConfigSummary(env); len(pending) > 0 {
				promptBody += "\n\nStaged config commands to be committed:\n" + strings.Join(pending, "\n")
			}
		}
		if tc.Name == "config_rollback" {
			promptBody += "\n\nRestores the pre-commit snapshot (config.yaml.prev) over the active configuration; " +
				"changes committed after that snapshot leave the active file."
		}
		permResult, err := a.server.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: sessionID,
			ToolCall: acp.PermissionToolCall{
				ToolCallID: tc.ID,
				Title:      fmt.Sprintf("Run: %s", tc.Name),
				Kind:       toolKind(tc.Name),
				Status:     "pending",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: "text", Text: promptBody}},
				},
			},
			Options: permission.Options(tc.Name, tc.InputJSON),
		})

		if err != nil || permResult == nil || permResult.Outcome == "cancelled" || permResult.OptionID == "reject" {
			_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    tc.ID,
				Status:        "cancelled",
			})
			return "permission denied by user", nil
		}
		if st := sessionStatePtr(a.state); st != nil {
			permission.RecordAllowAlways(st, tc.Name, tc.InputJSON, env.CWD, permResult)
		}
	}

	// Execute the tool.
	var result string
	var execErr error

	// Check if it's an MCP tool (name contains __).
	if idx := strings.Index(tc.Name, "__"); idx >= 0 {
		serverName := tc.Name[:idx]
		toolName := tc.Name[idx+2:]
		result, execErr = a.callMCPTool(ctx, serverName, toolName, tc.InputJSON)
		// MCP calls bypass the registry, so apply the output ceiling here (the
		// "default" knob, since MCP tool names are not individually configured).
		if execErr == nil {
			result = tools.ApplyOutputLimit(result, tc.Name, env)
		} else {
			execErr = tools.ApplyOutputLimitError(execErr, tc.Name, env)
		}
	} else {
		result, execErr = a.registry.Execute(ctx, tc.Name, tc.InputJSON, env)
	}

	status := "completed"
	if execErr != nil {
		status = "failed"
	}

	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		finalText := result
		if execErr != nil {
			finalText = fmt.Sprintf("error: %v", execErr)
		}
		_ = session.WriteToolCallResult(sessionDir, tc.ID, finalText)
		_ = session.MarkToolCallFinished(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), status)
	}

	payload := result
	if execErr != nil {
		payload = fmt.Sprintf("error: %v", execErr)
	}
	var content []acp.ToolCallResultItem
	var previewMeta map[string]interface{}
	if strings.TrimSpace(payload) != "" {
		display, meta := session.PreviewToolResultForSessionUpdate(tc.Name, payload)
		previewMeta = meta
		content = []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: display}},
		}
	}

	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        status,
		Content:       content,
		Meta:          previewMeta,
	})

	return result, execErr
}

// mcpToolDefinitions converts the tools of connected MCP clients into LLM
// tool definitions, hiding entries the filter disallows. Shared by the main
// prompt path and the permission-resume path.
func mcpToolDefinitions(clients []*mcp.Client, allowed func(server, tool string) bool) []llm.ToolDefinition {
	var defs []llm.ToolDefinition
	for _, client := range clients {
		for _, t := range client.Tools() {
			if !allowed(client.Name(), t.Name) {
				continue
			}
			defs = append(defs, t.ToLLMToolDefinition(client.Name()))
		}
	}
	return defs
}

// callMCPTool routes a tool call to the appropriate MCP client. Disabled
// tools are rejected here too so stale history cannot invoke them.
func (a *Agent) callMCPTool(ctx context.Context, serverName, toolName, argsJSON string) (string, error) {
	if allowed := a.state.GetMCPToolFilter(); !allowed(serverName, toolName) {
		return "", fmt.Errorf("MCP tool %s__%s is disabled", serverName, toolName)
	}
	for _, client := range a.state.GetMCPClients() {
		if client.Name() == serverName {
			return client.CallTool(ctx, toolName, argsJSON)
		}
	}
	return "", fmt.Errorf("MCP server not found: %s", serverName)
}

func (a *Agent) currentToolDefinitions(mode string) []llm.ToolDefinition {
	toolSet := ToolSetForMode(mode)
	available := a.registry.AllToolDefinitions()
	if a.configReloader == nil {
		// Without a runtime reloader the staged config flow cannot commit, so
		// the whole editing family is hidden; config_get stays read-only.
		filtered := available[:0]
		for _, definition := range available {
			switch definition.Name {
			case "config_set", "config_changes", "config_commit", "config_revert", "config_rollback":
			default:
				filtered = append(filtered, definition)
			}
		}
		available = filtered
	}
	defs := FilterToolDefinitions(available, toolSet)
	if toolSet.Unrestricted() || mode == "plan" {
		defs = append(defs, mcpToolDefinitions(a.state.GetMCPClients(), a.state.GetMCPToolFilter())...)
	}
	return defs
}

// buildMessages constructs the message slice to send to the LLM.
// The most recent user message is augmented with bodies of any explicitly invoked (/name) skills
// so the LLM sees the full skill instructions immediately before the user's request.
// The stored history content is never modified — only the slice sent to the LLM differs.
func (a *Agent) buildMessages(systemPrompt string) []llm.Message {
	// After a compaction only the last summary and the messages after it are
	// replayed to the LLM; earlier history stays in the transcript for the UI.
	// Read/grep result eviction is applied at the provider.Stream send boundary
	// (see runReActLoop), not here, so the working message slice keeps full
	// content while the projection sent to the model is pruned.
	history := session.MessagesForLLM(a.state.GetMessages())
	allSkills := a.state.GetSkills()
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})

	// Find the index of the most recent user message to augment it.
	lastUserIdx := -1
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleUser {
			lastUserIdx = i
			break
		}
	}

	for i, m := range history {
		if !isLLMHistoryMessage(m) {
			continue
		}
		if i == lastUserIdx && len(allSkills) > 0 {
			if aug := augmentUserMessageWithInvokedSkills(m.Content, allSkills); aug != m.Content {
				m.Content = aug
			}
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// augmentUserMessageWithInvokedSkills prepends full skill bodies for any /name commands found
// in userText. The original text is preserved at the end so the LLM sees both the skill context
// and the user's exact request. Returns userText unchanged when no skills are invoked.
func augmentUserMessageWithInvokedSkills(userText string, allSkills []*skills.Skill) string {
	names := skills.ParseInvokedCommandNames(userText)
	if len(names) == 0 {
		return userText
	}
	idx := skills.SkillBySlashName(allSkills)
	var prefix strings.Builder
	for _, n := range names {
		sk, ok := idx[n]
		if !ok {
			continue
		}
		body := strings.TrimSpace(sk.Content)
		if body == "" {
			continue
		}
		prefix.WriteString("## Invoked skill: /")
		prefix.WriteString(n)
		prefix.WriteString("\n\n")
		prefix.WriteString(body)
		prefix.WriteString("\n\n---\n\n")
	}
	if prefix.Len() == 0 {
		return userText
	}
	return prefix.String() + userText
}

func isLLMHistoryMessage(m llm.Message) bool {
	if m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Reasoning) == "" {
		return false
	}
	return true
}

// sendPlan sends the plan update to the client.
func (a *Agent) sendPlan(sessionID string, entries []acp.PlanEntry) error {
	return a.server.SendSessionUpdate(sessionID, acp.PlanUpdate{
		SessionUpdate: acp.UpdateTypePlan,
		Entries:       entries,
	})
}

// reasoningForStorage picks the reasoning text and signature to persist on an assistant message.
// When the provider signs the reasoning (Anthropic extended thinking), the exact unmodified text
// must be stored so the signature validates on replay; otherwise the trimmed text is used for display.
func reasoningForStorage(trimmed, exact string, response *llm.Response) (text, signature string) {
	if response != nil && response.ReasoningSignature != "" {
		return exact, response.ReasoningSignature
	}
	return trimmed, ""
}

// llmTransport pairs a provider with the transport it was built for. The ReAct
// loop needs the distinction: a provider for a model with stream: false answers
// in one piece after the whole completion is generated, so guards that expect
// chunks to arrive progressively do not apply to it.
type llmTransport struct {
	provider  llm.Provider
	streaming bool
}

// getProvider creates the LLM provider for the given mode.
func (a *Agent) getProvider(mode string) (llmTransport, error) {
	modelID := a.state.EffectiveModelID(a.cfg)
	if modelID == "" {
		return llmTransport{}, fmt.Errorf("no model configured")
	}

	rm, err := a.cfg.ResolveLLM(modelID)
	if err != nil {
		return llmTransport{}, err
	}

	mk := a.providerFactory
	if mk == nil {
		mk = llm.NewProvider
	}
	in := a.llmProviderInput(rm)
	in.ReasoningEffort = a.state.EffectiveReasoning(a.cfg)
	provider, err := mk(in)
	if err != nil {
		return llmTransport{}, err
	}
	return llmTransport{provider: provider, streaming: rm.Stream}, nil
}

func (a *Agent) llmProviderInput(rm *config.ResolvedLLM) llm.ProviderInput {
	return llm.WithAgentResilience(llm.ProviderInput{
		Type:          rm.ProviderType,
		Model:         rm.Model,
		APIKey:        rm.APIKey,
		BaseURL:       rm.BaseURL,
		ProxyURL:      rm.ProxyURL,
		AuthPath:      rm.AuthPath,
		MaxTokens:     rm.MaxTokens,
		Temperature:   rm.Temperature,
		DisableStream: !rm.Stream,
		Timeout:       time.Duration(rm.TimeoutMS) * time.Millisecond,
	}, a.cfg.Agent.EffectiveLLMRetryMax(), a.cfg.Agent.LLMRetryBaseMS, a.cfg.Agent.LLMMinIntervalMS)
}

// contentBlocksToText converts ACP content blocks to a plain text string.
// Hydrated attachments become **<coddy_attachment path="..." name="...">…</coddy_attachment>**
// with file body inside CDATA so the SPA can strip tags for display while the model retains full context.
func contentBlocksToText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource":
			if b.Resource != nil {
				parts = append(parts, resourceBlockToXMLAttachment(b.Resource))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func xmlEscapedAttr(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func wrapXMLCDATA(body string) string {
	// Split CDATA if the payload contains the terminator sequence.
	escaped := strings.ReplaceAll(body, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[" + escaped + "]]>"
}

func resourceBlockToXMLAttachment(res *acp.Resource) string {
	pathRaw := strings.TrimSpace(res.URI)
	pathRaw = strings.TrimPrefix(pathRaw, "file://")
	pathFwd := filepath.ToSlash(pathRaw)
	name := filepath.Base(pathFwd)
	if name == "." || name == "/" {
		name = pathFwd
	}
	var b strings.Builder
	b.WriteString(`<coddy_attachment path="`)
	b.WriteString(xmlEscapedAttr(pathFwd))
	b.WriteString(`" name="`)
	b.WriteString(xmlEscapedAttr(name))
	b.WriteString(`">`)
	b.WriteByte('\n')
	b.WriteString(wrapXMLCDATA(res.Text))
	b.WriteString("\n</coddy_attachment>")
	return b.String()
}

// extractContextFiles returns file paths referenced in content blocks.
func extractContextFiles(blocks []acp.ContentBlock) []string {
	var files []string
	for _, b := range blocks {
		if b.Type == "resource" && b.Resource != nil {
			uri := b.Resource.URI
			if strings.HasPrefix(uri, "file://") {
				files = append(files, fileURIPath(uri))
			}
		}
	}
	return files
}

// fileURIPath turns a file:// URI into a filesystem path. On Windows the
// authority-less form is file:///C:/proj/x.go, whose leading slash must go —
// "/C:/proj/x.go" matches no rule scope and no glob. A POSIX path that merely
// contains a colon (/a:b) keeps its slash: the drive form requires a separator
// after the colon, or nothing at all.
func fileURIPath(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) >= 3 && p[0] == '/' && isASCIILetter(p[1]) && p[2] == ':' &&
		(len(p) == 3 || p[3] == '/' || p[3] == '\\') {
		p = p[1:]
	}
	return p
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// toolKind maps a tool name to an ACP tool call kind.
func toolKind(name string) string {
	switch name {
	case "read", "keep_result", "glob", "grep", "websearch", "webfetch", "config_get", "config_changes":
		return "read"
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv", "config_commit", "config_rollback":
		return "write"
	case "run_command":
		return "run_command"
	default:
		return "other"
	}
}

func filesystemWriteTool(name string) bool {
	switch name {
	case "write", "edit", "apply_patch", "mkdir", "rmdir", "touch", "rm", "mv":
		return true
	default:
		return false
	}
}

// configWriteTool names the tools that write the agent's own configuration.
// They get a stricter permission policy than project file writes: accept_edits
// never auto-approves them (see executeToolCall).
func configWriteTool(name string) bool {
	switch name {
	case "config_commit", "config_rollback":
		return true
	default:
		return false
	}
}

// effectivePermMode returns the session-level permission mode override, falling back to the config default.
func effectivePermMode(state SessionState, cfg *config.Config) string {
	if m := state.GetPermissionMode(); m != "" {
		return m
	}
	return cfg.Tools.ResolvedPermMode()
}

// extractCommand parses the "command" field from run_command JSON args.
func extractCommand(argsJSON string) string {
	return permission.ExtractRunCommand(argsJSON)
}

func sessionStatePtr(s SessionState) *session.State {
	st, ok := s.(*session.State)
	if !ok {
		return nil
	}
	return st
}

// filePathsNote builds an XML annotation listing the on-disk paths where
// uploaded files were saved.  Returns an empty string when no part has a
// FilePath set (e.g. sessions without a persistent directory).
// The tag is stripped from the user-visible bubble by the SPA's
// stripCoddyAttachmentsForUserDisplay function.
func filePathsNote(parts []llm.ImagePart) string {
	var lines []string
	for _, p := range parts {
		if p.FilePath == "" {
			continue
		}
		line := "- " + p.FilePath
		if p.Name != "" && p.Name != filepath.Base(p.FilePath) {
			line += " (" + p.Name + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n")
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString("</coddy_session_assets>")
	return b.String()
}
