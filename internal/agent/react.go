// Package agent implements the ReAct (Reasoning + Acting) loop for a session turn.
// System prompts are rendered via internal/prompts (embedded templates or prompts.dir).
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
	toolweb "github.com/EvilFreelancer/coddy-agent/internal/tools/web"
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
	PendingPlanContext() string
	ClearPendingPlanContext()
	TakePendingImageParts() []llm.ImagePart
	GetPermissionMode() string
	// The settings the running turn works with (session/settings_state.go):
	// its own when a --once / --count override, a skill or the model's
	// switch_model set one, the session's otherwise. SettingsRevision moves
	// whenever one a model request reads changes.
	EffectiveMode() string
	EffectivePermissionMode() string
	SettingsRevision() uint64
	SetTurnSetting(setting, value string)
	TurnSetting(setting string) string
	// How the session_describe tool reaches the session's own filing
	// (session_filing.go). The writers report what they moved and do their own
	// merging, so the tool never has to read a filing it is about to write.
	ConversationTitle() string
	GetTags() []string
	ReplaceTitlePinned(title string) bool
	ReplaceTags(tags []string) (stored []string, changed bool)
	UpdateTags(add, remove []string) (stored []string, changed bool)
	IsUserCancelledTurn() bool
	// TakeQueuedMessages drains the follow-ups written while this turn runs
	// (session/turn_queue.go). The loop reads them between its own steps.
	TakeQueuedMessages() []session.QueuedMessage
	// QueuedMessages is what is still waiting after that drain: a message may
	// have been written while the batch was being read in.
	QueuedMessages() []session.QueuedMessage
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

	// subagentRuntime owns child sessions; nil when this surface cannot spawn.
	subagentRuntime SubagentRuntime
	// detachedPermissions answers a child's prompt after the parent turn that
	// spawned it has ended; nil on surfaces with nowhere to put one.
	detachedPermissions DetachedPermissionBroker
	// subagent is set when this session is itself a child run (see subagent.go).
	subagent *session.SubagentMeta
	// progress is the running turn's clock and token count (turn_progress.go).
	progress *turnProgress
	// limitWaitHeartbeat overrides how often a waiting turn re-sends its
	// countdown (tests); zero means limitWaitHeartbeat.
	limitWaitHeartbeat time.Duration
	// limitLedger is the user turn's account of time spent on usage
	// limits (limit_wait.go); Run starts a fresh one.
	limitLedger *limitWaitLedger
	// currentToolCallID is the tool call being executed, so a spawn can link
	// its task to the transcript row.
	currentToolCallID string

	// hooks is the operator hook runner of the current turn, built on first
	// use from the definition files (hooks.go). hookStopReason carries a
	// continue:false answered by a hook to the loop, which ends the turn.
	hooks          *hooks.Runner
	hooksMu        sync.Mutex
	hooksLoaded    bool
	hookStopReason string
	// turnHookContext is what UserPromptSubmit hooks handed over for this
	// turn's system prompt (hooks.go).
	turnHookContext string
	// autoCompactSkipLogged records that this turn already logged an
	// automatic compaction with nothing to fold (compact.go).
	autoCompactSkipLogged bool
	// clock is the wall clock the turn context block reads; nil means
	// time.Now. Tests that assert on a rendered timestamp set it.
	clock func() time.Time
	// memoryRun is the memory subagent this turn started, or nil
	// (memory_run.go). The Agent lives for one turn, so it needs no reset.
	memoryRun *memoryTurnRun
}

// NewAgent creates an Agent for a prompt turn.
func NewAgent(cfg *config.Config, state SessionState, server acp.UpdateSender, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	environment := platform.CurrentEnvironment()
	a := &Agent{
		cfg:    cfg,
		state:  state,
		server: server,
		// Tagged here rather than at every call site, so logger.levels can
		// name "agent" whichever entrypoint built the loop.
		log:             logger.Component(log, logger.ComponentAgent),
		registry:        tools.NewRegistryForEnvironment(cfg, environment),
		environment:     environment,
		providerFactory: llm.NewProvider,
	}
	if st := sessionStatePtr(state); st != nil {
		a.subagent = st.Subagent()
	}
	// The system memory child gets its tools here, in its own registry;
	// nothing else ever sees them.
	a.registerMemoryChildTools()
	return a
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
	mode := a.state.EffectiveMode()
	// A new user turn starts its account of time spent on usage limits
	// (limit_wait.go); the built-ins below never touch it.
	a.limitLedger = &limitWaitLedger{}
	// Hook definitions are re-read for every turn.
	a.resetHooks()
	a.hookStopReason = ""
	a.turnHookContext = ""

	// Build the user message from prompt content blocks.
	a.state.ClearMemoryCopilotBlock()
	userText := contentBlocksToText(prompt)

	// The built-in /compact, /plugin, and /export commands are operator input:
	// they run deterministically, outside the tool set and the permission
	// gate. A child's prompt is written by the parent model, so for a subagent
	// the same text is an ordinary task and never reaches the built-ins. They
	// read what the operator typed, never the attachments that came with it:
	// data piped into a one-shot run is not a /export target or /plugin
	// arguments.
	if a.subagent == nil {
		typed := typedText(prompt)
		// The built-in /compact command compacts history instead of running the
		// ReAct loop. The command text is persisted (so it shows in the transcript
		// like any other message) by runCompactCommand itself.
		if instructions, ok := parseCompactCommand(typed); ok {
			return a.runCompactCommand(ctx, instructions, userText)
		}
		// The built-in /plugin command manages skill plugins and marketplaces
		// deterministically, without an LLM turn; the command text is persisted too.
		if args, ok := parsePluginCommand(typed); ok {
			return a.runPluginCommand(ctx, args, userText)
		}
		// The built-in /export command writes the transcript to a file in the
		// workspace; the command text is persisted after the export is built.
		if args, ok := parseExportCommand(typed); ok {
			return a.runExportCommand(ctx, args, userText)
		}
	}
	// The bodies of the skills the prompt invokes as /name, and the rules its
	// mentioned paths activate, ride in this message (mentions.go), never in
	// the system prompt and never added per request.
	for _, inv := range invokedSkills(typedText(prompt), a.state.GetSkills()) {
		a.applySkillSettings(ctx, inv.name, inv.skill)
	}
	if extra := invokedSkillBlocks(typedText(prompt), a.state.GetSkills()); len(extra) > 0 {
		prompt = append(append([]acp.ContentBlock(nil), prompt...), extra...)
		userText = contentBlocksToText(prompt)
	}
	if withRules := a.attachActivatedRules(prompt); len(withRules) != len(prompt) {
		prompt = withRules
		userText = contentBlocksToText(prompt)
	}
	// UserPromptSubmit hooks see the prompt before it becomes a message: a
	// rejected prompt is never added, and the turn ends with the reason. The
	// attachments that came with it are dropped too, so they do not leak into
	// the next prompt.
	if reason, rejected := a.runUserPromptHooks(ctx, mode, userText); rejected {
		a.state.TakePendingImageParts()
		return string(acp.StopReasonRefused), fmt.Errorf("prompt rejected by hook: %s", reason)
	}
	imageParts := a.state.TakePendingImageParts()
	messageContent := userText
	if note := filePathsNote(imageParts); note != "" {
		messageContent = userText + "\n\n" + note
	}
	// A turn finished background tasks started opens with the wake. The pool
	// marks the tasks that woke the agent first, so a surface that reads its
	// task list again on the wake finds the mark; the clients hear the wake
	// before the message is persisted, like every frame a message describes;
	// and the message keeps the marker, so no surface shows the instruction
	// as something the operator typed, after a reload either.
	wake := a.takeTurnWake()
	if wake != nil {
		a.markWokeTasks(wake)
		_ = a.server.SendSessionUpdate(a.state.GetID(), session.BackgroundWakeUpdate(wake))
	}
	a.state.AddMessage(llm.Message{
		Role:           llm.RoleUser,
		Content:        messageContent,
		ImageParts:     imageParts,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		BackgroundWake: wake,
	})
	a.setHookTurn(session.CountUserTurns(a.state.GetMessages()))
	// The turn's clock is announced before anything slow happens - the memory
	// run below, the first model call - so a surface counts from the start.
	a.beginTurnProgress()
	defer a.endTurnProgress()
	a.runMemoryBeforeTurn(ctx, userText, mode)
	// A report that lands after this turn returned is history in the Tasks
	// drawer, never the next turn's context.
	defer a.finishMemoryTurn()

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

	// Build the full message list starting with the system prompt. It is
	// rendered once here and then frozen for the whole turn so the provider's
	// prefix cache keeps the conversation behind it (buildSystemPromptParts).
	sys := a.buildSystemPromptParts(mode, activeSkills, toolDefs, contextFiles)
	messages := a.buildMessages(sys.Content)
	// The hand-off belongs to this turn and to its continuation after a
	// permission prompt, and to nothing after that.
	defer a.releasePlanContext()

	// buildSystemPromptParts refreshed the context breakdown; compact before the
	// first LLM call when the estimate crossed the auto-compaction threshold.
	if a.maybeAutoCompact(ctx) {
		sys = a.buildSystemPromptParts(mode, activeSkills, toolDefs, contextFiles)
		messages = a.buildMessages(sys.Content)
	}

	maxTurns := a.cfg.Agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 30
	}
	if a.subagent != nil {
		maxTurns = a.cfg.Subagents.EffectiveMaxTurns(a.cfg.Agent.MaxTurns)
		if a.subagent.MaxTurns > 0 {
			maxTurns = a.subagent.MaxTurns
		}
	}

	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())

	toolEnv := &tools.Env{
		CWD:              a.state.GetCWD(),
		PermissionMode:   effectivePermMode(a.state, a.cfg),
		CommandAllowlist: a.cfg.Tools.CommandAllowlist,
		HTTPAllowlist:    a.cfg.Tools.HTTPRequest.Allowlist,
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
		CompactSession: a.compactFromTool,
		FileSession: func(upd tooling.SessionFilingUpdate) (tooling.SessionFilingResult, error) {
			return applySessionFiling(a.state, upd)
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
		WebSearch:         webSearchSettings(a.cfg),
	}
	// The model's own model switch; a subagent runs on what its parent chose.
	if a.subagent == nil && a.settings() != nil {
		toolEnv.SwitchModel = a.switchModel
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
	a.applySubagentEnv(toolEnv, mode)

	return a.runReActLoop(ctx, mode, sys, messages, toolDefs, transport, toolEnv, sd, userText, contextFiles, activeSkills, maxTurns)
}

// releasePlanContext hands back the design plan hand-off once the turn that ran
// the plan is really over.
//
// A turn stopped on a permission prompt is not over: the user answers it later,
// possibly in another process, and ResumeAfterPermission renders this turn's
// system prompt again. So the gate left in the bundle is what decides - while
// one is held, the hand-off stays where the continuation can find it.
//
// A process that dies mid-turn with no gate held leaves the record behind, and
// the next turn of that session carries the plan text once more before
// releasing it. That is the right way round: the plan was not finished, and
// one extra turn of context costs less than dropping it.
func (a *Agent) releasePlanContext() {
	if sd := strings.TrimSpace(a.state.GetPersistedSessionDir()); sd != "" && session.PendingPermissionHeld(sd) {
		return
	}
	a.state.ClearPendingPlanContext()
}

// maxEmptyAssistantContinuations bounds how many times the ReAct loop re-prompts a model
// that ended a turn with no visible answer and no tool call (only reasoning, or nothing).
// It guards against dead-ending the conversation on a thinking-only bubble — seen with
// gpt-oss / harmony endpoints that leak a tool call into the reasoning channel — while
// preventing an unbounded empty-turn loop.
const maxEmptyAssistantContinuations = 2

// maxEmptyAssistantReissues bounds how many times an empty turn is answered by
// replaying the identical request before the model is talked to in words. One
// model name at a proxy is usually a group of interchangeable deployments, and
// a member that returns reasoning with neither text nor a tool call fails that
// way per attempt: the replay lands on another member and comes back with the
// tool call the first one lost. Only after that is the model itself nudged,
// which is the recovery that helps when the model, not the lane, is at fault.
const maxEmptyAssistantReissues = 1

// maxFirstTokenRetries bounds how many times a streamed call the first-token
// guard cut with nothing produced is re-issued before the turn gives up. Same
// reasoning as maxEmptyAssistantReissues, and safe by construction: the cut
// call emitted no chunk to the client and appended no message, so the replay
// cannot show or store anything twice.
const maxFirstTokenRetries = 1

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

	// permissionDeniedByUser is what a tool call gets when the gate was
	// answered with a refusal. It is matched verbatim elsewhere (the
	// context-eviction pass reads it as "this write never happened"), so it
	// stays a constant rather than a literal repeated per call site.
	permissionDeniedByUser = "permission denied by user"

	// permissionNotGrantedPrefix opens the refusals nobody actually answered -
	// a detached subagent's prompt that reached no client, say. The model must
	// not read those as a user saying no, and the eviction pass must still
	// treat the write as not done, so both share this prefix.
	permissionNotGrantedPrefix = "permission not granted: "
)

// permissionDeniedResult renders a refused gate for the model, naming the
// reason when the refusal came from something other than a user's answer.
func permissionDeniedResult(res *acp.PermissionResult) string {
	if res != nil {
		if reason := strings.TrimSpace(res.Reason); reason != "" {
			return permissionNotGrantedPrefix + reason
		}
	}
	return permissionDeniedByUser
}

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
	sys *systemPromptBuild,
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
	// Replays of a request the lane, not the model, failed to answer. Both are
	// reset alongside emptyContinuations once the model makes progress.
	var emptyReissues int
	var firstTokenRetries int
	// Tracks whether any visible answer text was streamed to the user during this
	// turn, so an all-reasoning turn that never answers surfaces a notice instead
	// of dead-ending silently.
	var turnHadVisibleText bool

	// Run opened the turn's progress already; a loop entered another way (a
	// resumed permission) opens its own.
	a.beginTurnProgress()
	defer a.endTurnProgress()

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

	// Stop hooks may send the agent back to work; stopBlocks counts those
	// continuations against hooks.stop_loop_limit and stopHookActive tells
	// the hook that it already did so in this turn.
	stopHookActive := false
	stopBlocks := 0

	// The turn's account of time spent on usage limits (limit_wait.go).
	limitWait := a.limitLedgerFor()

	// What the transport was built for, to notice a change between requests.
	transportRev := a.state.SettingsRevision()
	transportKey := a.transportKey()

	for turn := 0; turn < maxTurns; turn++ {
		if ctx.Err() != nil {
			return string(acp.StopReasonCancelled), nil
		}

		// A follow-up the operator wrote while the previous step ran is read
		// here, before the request that answers that step's tool results is
		// built: that is what puts the correction inside the work instead of
		// after it. It is appended before the rebuild below, so a compaction
		// that replays the transcript carries it too.
		a.readQueuedMessages(&messages)

		// A model or a reasoning level changed since the transport was built -
		// by the operator, a --once override, the model's own switch_model -
		// takes effect from this request, never inside a stream.
		if rev := a.state.SettingsRevision(); rev != transportRev {
			transportRev = rev
			if key := a.transportKey(); key != transportKey {
				next, err := a.getProvider(mode)
				if err != nil {
					a.log.Warn("settings changed mid-turn but the new model is unavailable; keeping the current one", "error", err)
				} else {
					a.log.Info("model settings changed mid-turn", "from", transportKey, "to", key)
					transport, transportKey = next, key
				}
			}
		}

		// The system message stays exactly as the turn rendered it, so the
		// provider's cached copy of everything behind it survives this step.
		// What moved since - the wall clock, the todo checklist after a
		// coddy_todo_* call, the rules a filesystem tool activated - travels in
		// the turn context block appended after the history at the send
		// boundary below (turn_context.go).
		//
		// The exception is a template under prompts.dir that prints those facts
		// itself: its own conditionals have to keep matching the state, so it is
		// re-rendered here as every template was before, and carries no block.
		if sys.Volatile && len(messages) > 0 && messages[0].Role == llm.RoleSystem {
			sys = a.buildSystemPromptParts(mode, activeSkills, toolDefs, contextFiles)
			messages[0].Content = sys.Content
		}
		turnCtx := a.buildTurnContext(sys)

		// Tool results can grow the context mid-turn; compact between LLM calls
		// when the refreshed estimate crossed the threshold. Run already checked
		// before the first call. Ephemeral continuation nudges are not part of
		// persisted state and are dropped by the rebuild (acceptable: the model
		// answered or called a tool since then).
		a.refreshContextBreakdown(sys, turnCtx)
		if turn > 0 && a.maybeAutoCompact(ctx) {
			sys = a.buildSystemPromptParts(mode, activeSkills, toolDefs, contextFiles)
			messages = a.buildMessages(sys.Content)
			turnCtx = a.buildTurnContext(sys)
			// The rebuilt estimate above was taken without the block; the call
			// below sends it, so the accounting has to see it too.
			a.refreshContextBreakdown(sys, turnCtx)
		}

		// Call LLM and stream response.
		var response *llm.Response
		var streamErr error
		var reasoningBuf strings.Builder

		reasonClockStart := time.Time{}
		reasonClockEnd := time.Time{}
		// streamedAny records that a chunk of any kind reached the client
		// from this call: a limit reported after that is never waited for
		// and re-issued, whatever the provider's own error carries.
		streamedAny := false
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
			streamedAny = true
			reasoningBuf.WriteString(d)
			a.progress.streamed(d)
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
			streamedAny = true
			if markReasonEnd && strings.TrimSpace(delta) != "" {
				maybeMarkReasonEnd(now)
			}
			a.progress.streamed(delta)
			_ = a.server.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: delta},
			})
		}

		// Prune superseded read/grep results from the projection sent to the model;
		// the working `messages` slice keeps full content (copy-on-write) so state,
		// the transcript, and later appends stay intact.
		sendMessages := withTurnContext(a.prunedForLLM(messages), turnCtx)
		// The call's own clock: when it went out, when the first chunk came
		// back and how many followed. It names the silence in the errors
		// below and is the debug-level account of every call.
		callStart := time.Now()
		var firstChunkAt time.Time
		var chunkCount int
		a.progress.beginCall()
		response, streamErr = transport.provider.Stream(streamCtx, sendMessages, toolDefs, func(chunk llm.StreamChunk) {
			if streamCtx.Err() != nil {
				return
			}
			now := time.Now()
			chunkCount++
			if firstChunkAt.IsZero() {
				firstChunkAt = now
			}
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
			// A call the model has only named yet announces the same pending row:
			// the arguments can take seconds to stream, and without the row the
			// transcript stands still with nothing but a Stop button. The row is
			// keyed by the call id, so the complete call updates it in place.
			// A call's arguments are output like any text, and often most of
			// it (a write carries the whole file). They are not streamed as
			// deltas, so they enter the count when the call is complete.
			if chunk.ToolCall != nil {
				a.progress.streamed(chunk.ToolCall.Name + chunk.ToolCall.InputJSON)
			}
			announce := chunk.ToolCall
			if announce == nil {
				announce = chunk.ToolCallNamed
			}
			if announce != nil && announce.Name != "" {
				streamedAny = true
				maybeMarkReasonEnd(now)
				if st := sessionStatePtr(a.state); st != nil {
					if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" && strings.TrimSpace(announce.ID) != "" {
						_ = session.WriteToolCallMeta(sd, announce.ID, session.ToolCallMeta{
							ToolCallID: strings.TrimSpace(announce.ID),
							Name:       announce.Name,
							Kind:       toolKind(announce.Name),
							Status:     "pending",
						})
					}
				}
				_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: acp.UpdateTypeToolCall,
					ToolCallID:    announce.ID,
					Title:         announce.Name, // plain name, no "Calling: " prefix
					Kind:          toolKind(announce.Name),
					Status:        "pending",
				})
				stopFirstTokenTimer()
			}
		})
		stopFirstTokenTimer()
		streamCancel()
		a.logLLMCall(callStart, firstChunkAt, chunkCount, response, streamErr)

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
			messages = a.buildMessages(sys.Content)
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
				// Nothing was emitted and nothing was appended, so the identical
				// request can go out again: behind one model name there is often a
				// group of deployments, and the silent one is not the whole lane.
				// The iteration is repeated, not counted, like the wait on a limit.
				if firstTokenRetries < maxFirstTokenRetries {
					firstTokenRetries++
					a.log.Warn("no first token from the model; re-issuing the same request",
						"timeout", firstTokenTimeout, "attempt", firstTokenRetries)
					turn--
					continue
				}
				return string(acp.StopReasonRefused), fmt.Errorf("model did not respond (no output within %v)", firstTokenTimeout)
			}
		}

		if streamErr != nil {
			// The provider named the moment its limit lifts and the operator
			// asked the turn to wait for it: the countdown reaches the client,
			// then the same call runs again (nothing was persisted for the
			// failed one, so nothing repeats). A cancel during the wait ends
			// the turn as a stop. The iteration is repeated, not counted.
			if reset, ok := a.limitResetToWaitFor(streamErr, response, reasoningBuf.String(), streamedAny); ok {
				waitStart := time.Now()
				err := a.waitForLimitReset(ctx, sessionID, reset)
				limitWait.Charge(time.Since(waitStart))
				if err != nil {
					if a.state.IsUserCancelledTurn() {
						return string(acp.StopReasonCancelled), nil
					}
					// A shutdown or a deadline, not the user: the turn ends with
					// the limit it was waiting on and says what cut the wait.
					return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w (the wait for the reset was interrupted: %v)", reset, err)
				}
				turn--
				continue
			}
			// A mid-generation truncation, or a stall the idle guard cut,
			// keeps its partial answer like a user stop: the user already
			// watched the text stream in, so it must survive in the
			// transcript next to the honest error below.
			if (errors.Is(streamErr, context.Canceled) || llm.IsStreamTruncated(streamErr) || llm.IsStreamStalled(streamErr)) && response != nil {
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
				// Stream was interrupted before producing any output and the user did not stop it
				// (a signal, a shutdown): surface an error so the UI can show feedback instead of
				// silently completing, and say how long the model had been silent, because a
				// process killed after a long empty wait is a different story from one interrupted
				// at once.
				return string(acp.StopReasonRefused), fmt.Errorf("generation was interrupted before producing a response (the model had been silent for %s)", humanDuration(time.Since(callStart)))
			}
			if ctx.Err() != nil {
				// Context cancelled for non-context-Canceled stream error: still propagate the real error.
				return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w", streamErr)
			}
			return string(acp.StopReasonRefused), fmt.Errorf("LLM error: %w", streamErr)
		}

		// What the provider served from its prompt cache. A long conversation
		// only stays affordable while this is most of the input, which is what
		// the frozen system prompt and the trailing turn context block are for
		// (turn_context.go); a provider that reports nothing leaves it at zero.
		a.log.Debug("llm call usage",
			"input_tokens", response.InputTokens,
			"cached_input_tokens", response.CachedInputTokens,
			"output_tokens", response.OutputTokens)

		// Accumulate and broadcast token usage after each LLM call.
		totalInputTokens += response.InputTokens
		totalOutputTokens += response.OutputTokens
		_ = a.server.SendSessionUpdate(sessionID, acp.TokenUsageUpdate{
			SessionUpdate: acp.UpdateTypeTokenUsage,
			InputTokens:   response.InputTokens,
			OutputTokens:  response.OutputTokens,
			TotalTokens:   totalInputTokens + totalOutputTokens,
		})
		a.progress.finishCall(response.OutputTokens)

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
			// First recovery is the plain replay: drop the empty turn from the
			// LLM-facing slice so the request going out is byte for byte the one
			// that failed, and let the proxy hand it to another deployment. The
			// transcript keeps that turn, because the user watched its reasoning
			// stream in. Words come next, once a replay has not helped.
			if strings.TrimSpace(response.Content) == "" && emptyReissues < maxEmptyAssistantReissues &&
				len(messages) > 0 && messages[len(messages)-1].Role == llm.RoleAssistant {
				emptyReissues++
				messages = messages[:len(messages)-1]
				a.log.Warn("model answered with no text and no tool call; re-issuing the same request",
					"attempt", emptyReissues)
				continue
			}
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
			// A Stop hook may send the agent back to work with a follow-up that
			// is submitted as the next user message (persisted, so the transcript
			// explains the continuation), bounded by hooks.stop_loop_limit.
			if followUp, again := a.runStopHooks(ctx, mode, response.Content, stopHookActive); again {
				limit := a.cfg.Hooks.EffectiveStopLoopLimit()
				if stopBlocks >= limit {
					a.log.Warn("stop hook loop limit reached; ending the turn", "limit", limit)
					return string(acp.StopReasonEndTurn), nil
				}
				// The follow-up needs an iteration to be read in; on the last one
				// it would only leave a dangling user message behind.
				if turn+1 >= maxTurns {
					a.log.Warn("stop hook follow-up dropped: the turn cap is reached", "max_turns", maxTurns)
					return string(acp.StopReasonEndTurn), nil
				}
				stopBlocks++
				stopHookActive = true
				follow := llm.Message{
					Role:      llm.RoleUser,
					Content:   stopHookPrefix + followUp,
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				}
				messages = append(messages, follow)
				a.state.AddMessage(follow)
				a.refreshConversationContextUsage(true)
				continue
			}

			// The turn is about to end with an answer, and the Stop hooks have
			// had their say. Anything the operator queued while that answer was
			// being written is read now, so it is answered by this turn rather
			// than waiting for the next prompt. One iteration is needed to read
			// it in; on the last one it would only leave a dangling user
			// message behind, and the manager's boundary drain takes it.
			if turn+1 < maxTurns && a.readQueuedMessages(&messages) {
				continue
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

			// A hook answered continue: false. The remaining calls of the batch
			// still get a result, because OpenAI-compatible endpoints reject the
			// next request otherwise; then the turn ends with the hook's reason.
			if reason := a.hookStopReason; reason != "" {
				a.hookStopReason = ""
				a.recordSkippedToolCalls(&messages, response.ToolCalls[i+1:], "not executed: a hook stopped the turn")
				return string(acp.StopReasonRefused), fmt.Errorf("stopped by hook: %s", reason)
			}
		}
		// The model folded its own history: the transcript the loop replays is
		// shorter now, so the outgoing slice is rebuilt from it before the next
		// call, the way an automatic compaction between steps rebuilds it. Done
		// after the whole batch, so a tool result already appended is picked up
		// from the transcript rather than dropped.
		if toolEnv.ContextCompacted {
			toolEnv.ContextCompacted = false
			messages = a.buildMessages(sys.Content)
			turnCtx = a.buildTurnContext(sys)
			a.refreshContextBreakdown(sys, turnCtx)
		}
		if toolEnv.ConfigReloaded {
			activeSkills = FilterSkillsForContext(a.state.GetSkills(), contextFiles)
			toolDefs = a.currentToolDefinitions(mode)
			toolEnv.PermissionMode = effectivePermMode(a.state, a.cfg)
			toolEnv.CommandAllowlist = append([]string(nil), a.cfg.Tools.CommandAllowlist...)
			toolEnv.HTTPAllowlist = append([]string(nil), a.cfg.Tools.HTTPRequest.Allowlist...)
			toolEnv.SSHConnectTimeout = a.cfg.Tools.SSHConnectTimeout
			toolEnv.OutputLineLimits = a.cfg.Tools.OutputLimits.AsMap()
			toolEnv.Background = a.backgroundPool(sd)
			toolEnv.BackgroundEnabled = a.cfg.Tools.Background.ResolvedEnabled()
			toolEnv.WebSearch = webSearchSettings(a.cfg)
			toolEnv.ConfigReloaded = false
		}
		// The model made progress (executed tool calls), so reset the empty-turn
		// counter. The give-up notice is for CONSECUTIVE stalls (no answer and no
		// tool call), not for a slow multi-step task that keeps acting between
		// reasoning-only thoughts — otherwise a model that alternates thinking and
		// tool calls (gpt-oss / harmony) is abandoned mid-task. The replay budgets
		// follow the same rule: a lane that answered once earns a fresh one.
		emptyContinuations = 0
		emptyReissues = 0
		firstTokenRetries = 0
	}

	return string(acp.StopReasonMaxTurns), nil
}

// logLLMCall is the debug-level account of one model call: how long it took,
// how long the first chunk took to arrive, how many chunks followed, and how
// it ended. It is what -log-level debug shows for a call that hangs or is
// cut, where the transcript alone shows nothing at all.
func (a *Agent) logLLMCall(callStart, firstChunkAt time.Time, chunks int, response *llm.Response, err error) {
	if a.log == nil || !a.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	attrs := []any{
		"duration", humanDuration(time.Since(callStart)),
		"chunks", chunks,
	}
	if firstChunkAt.IsZero() {
		attrs = append(attrs, "first_chunk", "none")
	} else {
		attrs = append(attrs, "first_chunk", humanDuration(firstChunkAt.Sub(callStart)))
	}
	if response != nil {
		attrs = append(attrs, "stop_reason", response.StopReason,
			"content_bytes", len(response.Content), "tool_calls", len(response.ToolCalls))
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	a.log.Debug("llm call finished", attrs...)
}

// humanDuration rounds a duration for a message a person reads: whole
// seconds once it is a second or more, milliseconds below that.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Second {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Millisecond).String()
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
	// The permission mode is read on every call, not once per turn: the
	// operator may have switched the session to bypass from the previous
	// call's dialog, and the rest of the batch runs under that.
	env.PermissionMode = effectivePermMode(a.state, a.cfg)
	env.ToolCallID = strings.TrimSpace(tc.ID)
	a.currentToolCallID = env.ToolCallID
	defer func() {
		env.ToolCallID = ""
		a.currentToolCallID = ""
	}()

	// Touching a directory pulls its nested AGENTS.md into the prompt. Done up
	// front so it holds regardless of the outcome below (permission denial,
	// tool error), and so both callers — the ReAct loop and the resume-after-
	// permission path — are covered without threading state through.
	a.activateScopedRulesForToolCall(tc.Name, tc.InputJSON, env.CWD)

	sessionDir := ""
	if st := sessionStatePtr(a.state); st != nil {
		sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	}

	// The persisted arguments are what a permission answered later resumes
	// on, so a write that fails is remembered and cancels the call before
	// any prompt instead of leaving a resume nothing trustworthy to run.
	var argsPersistErr error
	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		_ = session.MarkToolCallStarted(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), "in_progress")
		argsPersistErr = session.WriteToolCallArgs(sessionDir, tc.ID, tc.InputJSON)
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

	// A child may only call what its effective tool set admits: the
	// advertised definitions are filtered the same way, so this catches a
	// hallucinated or replayed call, MCP tools included, before anything runs
	// or asks for permission. The refusal goes through the same bookkeeping
	// as every other outcome, so the child's transcript records it as failed.
	if a.subagent != nil && !a.subagentAllows(tc.Name) {
		reason := fmt.Sprintf("tool %s is not available to this subagent", tc.Name)
		a.finishToolCall(sessionDir, sessionID, tc, reason, nil, "failed")
		return "", fmt.Errorf("%s", reason)
	}

	// A restricted mode filters tool definitions before the LLM sees them, but a
	// call replayed from history can still name a hidden tool; refuse it here so
	// the mode boundary holds at execution time too.
	if refusal, refused := toolCallRefusedByMode(mode, tc.Name); refused {
		a.finishToolCall(sessionDir, sessionID, tc, refusal, nil, "cancelled")
		return refusal, nil
	}

	// Operator hooks see the call before the permission gate, whatever the
	// permission mode: a hook can deny it, approve it past the prompt, force
	// the prompt, rewrite its arguments or add context to its result. They run
	// on the permission resume path as well (the history holds the model's
	// original arguments, so a rewrite must be applied again to what runs);
	// there allow and ask are moot, because the user already answered.
	var hookRes preToolUseOutcome
	{
		original := tc.InputJSON
		var ran bool
		hookRes, ran = a.runPreToolUseHooks(ctx, &tc, mode)
		if ran && hookRes.blocked {
			result := "blocked by hook: " + hookRes.reason
			a.finishToolCall(sessionDir, sessionID, tc, result, nil, "cancelled")
			return result, nil
		}
		// The turn was cancelled while a hook was running: the hooks answered
		// nothing, and the call must not run on the strength of that silence.
		if ran && ctx.Err() != nil {
			result := "cancelled before the tool ran"
			a.finishToolCall(sessionDir, sessionID, tc, result, nil, "cancelled")
			return result, nil
		}
		if ran && skipPermission && !sameToolArgs(tc.InputJSON, original) {
			// The user approved the arguments the prompt showed; a hook that
			// changes them again on the resume is not covered by that answer.
			result := "cancelled: a hook changed the approved arguments after the approval; run the call again"
			a.finishToolCall(sessionDir, sessionID, tc, result, nil, "cancelled")
			return result, nil
		}
		if ran && !sameToolArgs(tc.InputJSON, original) {
			// The rewritten arguments are what runs and what the operator must
			// see on the tool call card; the model's own message keeps the
			// original call, as it must for the transcript to replay. A
			// permission answered later resumes on this persisted value, so a
			// write that fails cancels the call before any prompt instead of
			// letting the resume fall back to arguments nobody saw.
			if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
				argsPersistErr = session.WriteToolCallArgs(sessionDir, tc.ID, tc.InputJSON)
			}
			_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    tc.ID,
				Status:        "in_progress",
				Content: []acp.ToolCallResultItem{
					{Type: "content", Content: acp.ContentBlock{Type: "text", Text: tc.InputJSON}},
				},
			})
		}
	}

	// Check if tool requires permission.
	tool, ok := a.registry.Get(tc.Name)
	requiresPerm := ok && tool.RequiresPermission

	var sessCmdGrants, sessWriteGrants, sessHTTPGrants []string
	if st := sessionStatePtr(a.state); st != nil {
		sessCmdGrants = st.GetPermissionCommandGrants()
		sessWriteGrants = st.GetPermissionWriteGrants()
		sessHTTPGrants = st.GetPermissionHTTPGrants()
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
	} else if tc.Name == toolweb.ToolHTTPRequest {
		// The gate decides on where the request goes and on what it carries
		// beyond that - files, a proxy, an unchecked certificate, a file it
		// writes - so an approved origin never approves an upload by itself.
		requiresPerm = !permission.HTTPRequestAllowedWithSession(env, sessHTTPGrants, tc.InputJSON)
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

	// A hook's allow skips the prompt; its ask forces one even in a mode that
	// would auto-approve.
	if hookRes.allow {
		requiresPerm = false
	}
	if hookRes.ask {
		requiresPerm = true
	}

	if requiresPerm && !skipPermission {
		promptBody := permission.PromptBody(tc.Name, tc.InputJSON)
		if tc.Name == toolweb.ToolHTTPRequest {
			// Raw arguments would bury the address and the files in JSON;
			// the prompt shows the request as it would go out.
			promptBody = permission.HTTPRequestPromptBody(tc.InputJSON, env.CWD)
		}
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
		if argsPersistErr != nil {
			result := "cancelled: the arguments could not be persisted before the permission prompt: " + argsPersistErr.Error()
			a.finishToolCall(sessionDir, sessionID, tc, result, nil, "cancelled")
			return result, nil
		}
		// Notification hooks learn that a prompt is about to wait for the
		// operator (a chat ping, a desktop notification); they cannot answer it.
		a.runNotificationHooks(ctx, mode, hookNotificationPermissionPrompt, tc, promptBody)
		// The mode this prompt is asked under: the session's, or ask when a
		// hook forced the prompt - a sender must not wave that one through.
		askedUnder := env.PermissionMode
		if hookRes.ask {
			askedUnder = config.PermModeAsk
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
			Options: permission.OptionsFor(tc.Name, tc.InputJSON, permission.OptionContext{
				Mode:          env.PermissionMode,
				SessionSwitch: a.subagent == nil && !hookRes.ask,
			}),
			SessionPermissionMode: askedUnder,
		})

		if err != nil || !permission.Approved(permResult) {
			_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
				SessionUpdate: acp.UpdateTypeToolCallUpdate,
				ToolCallID:    tc.ID,
				Status:        "cancelled",
			})
			return permissionDeniedResult(permResult), nil
		}
		if st := sessionStatePtr(a.state); st != nil {
			permission.RecordAllowAlways(st, tc.Name, tc.InputJSON, env.CWD, permResult)
		}
		a.switchPermissionModeFromDialog(ctx, env, permResult)
	}

	// Execute the tool.
	var result string
	var execErr error
	started := time.Now()

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

	// PostToolUse (or PostToolUseFailure) feedback and the PreToolUse
	// context travel with the result, so the model reads them next to the
	// output they refer to.
	if feedback := a.runPostToolUseHooks(ctx, tc, result, execErr, time.Since(started), mode); feedback != "" {
		if execErr != nil {
			execErr = fmt.Errorf("%w\n\n%s", execErr, feedback)
		} else {
			result = joinHookText(result, feedback)
		}
	}
	if len(hookRes.context) > 0 {
		text := hookContextText(hookRes.context)
		if execErr != nil {
			execErr = fmt.Errorf("%w\n\n%s", execErr, text)
		} else {
			result = joinHookText(result, text)
		}
	}

	status := "completed"
	if execErr != nil {
		status = "failed"
	}
	a.finishToolCall(sessionDir, sessionID, tc, result, execErr, status)
	return result, execErr
}

// finishToolCall persists the outcome of one tool call and publishes the final
// tool_call_update: the normal completed/failed path and the mode refusal
// (status cancelled, result carrying the refusal text) share it so the
// transcript, the tool_calls store, and the preview stay consistent.
func (a *Agent) finishToolCall(sessionDir, sessionID string, tc llm.ToolCall, result string, execErr error, status string) {
	var todoPlanSnapshot []acp.PlanEntry
	if status == "completed" {
		todoPlanSnapshot = todoPlanSnapshotAfterToolCall(tc.Name, a.state, execErr)
	}

	if sessionDir != "" && strings.TrimSpace(tc.ID) != "" {
		finalText := result
		if execErr != nil {
			finalText = fmt.Sprintf("error: %v", execErr)
		}
		_ = session.WriteToolCallResult(sessionDir, tc.ID, finalText)
		_ = session.MarkToolCallFinished(sessionDir, tc.ID, tc.Name, toolKind(tc.Name), status)
		if len(todoPlanSnapshot) > 0 {
			_ = session.WriteToolCallPlanSnapshot(sessionDir, tc.ID, todoPlanSnapshot)
		}
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
	if len(todoPlanSnapshot) > 0 {
		if previewMeta == nil {
			previewMeta = map[string]interface{}{}
		}
		coddyMeta, _ := previewMeta["coddy"].(map[string]interface{})
		if coddyMeta == nil {
			coddyMeta = map[string]interface{}{}
			previewMeta["coddy"] = coddyMeta
		}
		coddyMeta["todoPlan"] = todoPlanSnapshot
	}

	_ = a.server.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    tc.ID,
		Status:        status,
		Content:       content,
		Meta:          previewMeta,
	})
}

func todoPlanSnapshotAfterToolCall(toolName string, state SessionState, execErr error) []acp.PlanEntry {
	if execErr != nil || state == nil {
		return nil
	}
	switch toolName {
	case todo.ToolNameItemUpdate, todo.ToolNamePlanReplace:
		entries := state.GetPlan()
		if len(entries) == 0 {
			return nil
		}
		return append([]acp.PlanEntry(nil), entries...)
	default:
		return nil
	}
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
	if a.subagent != nil {
		// An empty effective set means no tools at all, not "unrestricted" as
		// the nil ToolSet would read; the spawn refuses such a set up front,
		// this keeps a replayed or restored child honest too.
		if len(a.subagent.Tools) == 0 {
			return nil
		}
		defs = FilterToolDefinitions(defs, ToolSet(a.subagent.Tools))
	} else if !a.canSpawnInMode(mode) {
		// spawn_agent is registered whenever the feature is on; a surface with no
		// runtime (a scheduled run), a session at the depth limit or an ask-mode
		// turn must not advertise it.
		filtered := make([]llm.ToolDefinition, 0, len(defs))
		for _, d := range defs {
			if d.Name != tools.ToolSpawnAgent {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
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
	// content while the projection sent to the model is pruned. A message is
	// replayed exactly as it was stored: what a /skill or an @mention brought
	// was written into it when it was sent (mentions.go), so no request
	// rewrites an earlier message and the provider's cached prefix holds.
	history := session.MessagesForLLM(a.state.GetMessages())
	msgs := make([]llm.Message, 0, len(history)+1)
	msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: systemPrompt})
	for _, m := range history {
		if !isLLMHistoryMessage(m) {
			continue
		}
		msgs = append(msgs, m)
	}
	return msgs
}

// invokedSkillBlocks returns an attachment carrying the body of every skill
// the typed text invokes as /name. It rides in the message that invoked it,
// written once, so the next turn replays the same bytes rather than a message
// that lost the body it was sent with.
func invokedSkillBlocks(text string, allSkills []*skills.Skill) []acp.ContentBlock {
	var out []acp.ContentBlock
	for _, inv := range invokedSkills(text, allSkills) {
		n, sk := inv.name, inv.skill
		body := strings.TrimSpace(sk.Content)
		if body == "" {
			continue
		}
		out = append(out, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: &acp.Resource{
			URI:      "skill:" + n,
			MimeType: "text/markdown; charset=utf-8",
			Text:     body,
			Mention:  &acp.ResourceMention{Kind: mention.KindSkill, Name: n},
		}})
	}
	return out
}

// invokedSkill is one skill a prompt invokes, under the name it was invoked by.
type invokedSkill struct {
	name  string
	skill *skills.Skill
}

// invokedSkills resolves the /name tokens of the typed text to skills, in the
// order they appear.
func invokedSkills(text string, allSkills []*skills.Skill) []invokedSkill {
	if len(allSkills) == 0 {
		return nil
	}
	names := skills.ParseInvokedCommandNames(text)
	if len(names) == 0 {
		return nil
	}
	idx := skills.SkillBySlashName(allSkills)
	var out []invokedSkill
	for _, n := range names {
		if sk, ok := idx[n]; ok {
			out = append(out, invokedSkill{name: n, skill: sk})
		}
	}
	return out
}

// applySkillSettings runs the rest of the turn on the model and reasoning
// level a skill's frontmatter names, whether the operator invoked the skill
// or the model loaded it. A setting the turn already holds - the operator's
// --once, the model's own switch - is not overridden, and a value the
// configuration cannot honour is logged and skipped: a skill never fails the
// turn it helps.
func (a *Agent) applySkillSettings(ctx context.Context, name string, sk *skills.Skill) {
	if sk == nil || a.subagent != nil || (sk.Model == "" && sk.Reasoning == "") {
		return
	}
	ap := a.settings()
	if ap == nil {
		return
	}
	ch := session.SettingsChange{Source: "skill:" + name}
	if m := sk.Model; m != "" && a.state.TurnSetting(session.SettingModel) == "" {
		ch.Model = &m
	}
	if r := sk.Reasoning; r != "" && a.state.TurnSetting(session.SettingReasoning) == "" {
		ch.Reasoning = &r
	}
	if ch.Empty() {
		return
	}
	if _, err := ap.ApplyTurnSettings(ctx, a.state.GetID(), ch); err != nil {
		a.log.Warn("skill frontmatter settings skipped", "skill", name, "error", err)
	}
}

// typedText is what the user wrote: the text blocks of a prompt, without the
// attachments resolved for it, so a "/name" inside an attached file invokes
// nothing.
func typedText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == acp.ContentTypeText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
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
	in := a.childProviderInput(a.turnProviderInput(rm))
	in.ReasoningEffort = a.state.EffectiveReasoning(a.cfg)
	provider, err := mk(in)
	if err != nil {
		return llmTransport{}, err
	}
	provider = a.withChildFallbacks(provider, modelID, mk)
	return llmTransport{provider: provider, streaming: rm.Stream}, nil
}

// settingsApplier is the manager's setter for session settings. The agent
// reaches it through its subagent runtime, which is the manager on every
// surface; without one (a bare agent in a test) the state is written directly.
type settingsApplier interface {
	ApplySessionSettings(ctx context.Context, sessionID string, ch session.SettingsChange) (acp.SessionSettings, error)
	ApplyTurnSettings(ctx context.Context, sessionID string, ch session.SettingsChange) (acp.SessionSettings, error)
}

// settings returns the manager's setter, or nil when the agent runs without one.
func (a *Agent) settings() settingsApplier {
	if ap, ok := a.subagentRuntime.(settingsApplier); ok {
		return ap
	}
	return nil
}

// switchModel backs the switch_model tool: the model's own choice of model
// and reasoning level, for the rest of the turn or for the session. It goes
// through the manager's setter like the operator's command, so it is checked
// against the configuration, logged and shown on every surface; the loop
// builds the new transport before its next request.
func (a *Agent) switchModel(ctx context.Context, req tooling.ModelSwitch) (string, error) {
	ap := a.settings()
	if ap == nil {
		return "", fmt.Errorf("switch_model is not available in this session")
	}
	ch := session.SettingsChange{Source: "model"}
	if req.Model != "" {
		ch.Model = &req.Model
	}
	if req.Reasoning != "" {
		ch.Reasoning = &req.Reasoning
	}
	scope := "for the rest of this turn"
	var err error
	if req.Session {
		scope = "for the rest of the session"
		_, err = ap.ApplySessionSettings(ctx, a.state.GetID(), ch)
	} else {
		_, err = ap.ApplyTurnSettings(ctx, a.state.GetID(), ch)
	}
	if err != nil {
		return "", err
	}
	model := a.state.EffectiveModelID(a.cfg)
	reasoning := a.state.EffectiveReasoning(a.cfg)
	if reasoning == "" {
		reasoning = "none offered"
	}
	return fmt.Sprintf("Switched %s: model %s, reasoning %s. It applies from your next request.", scope, model, reasoning), nil
}

// switchPermissionModeFromDialog applies a permission answer that also
// switches the session's permission mode ("bypass permissions for this
// session", "allow edits for this session", #292). The change goes through
// the manager's setter, so every surface shows it and the log records it,
// and the tool environment follows at once: the rest of this turn runs under
// the new mode.
func (a *Agent) switchPermissionModeFromDialog(ctx context.Context, env *tools.Env, res *acp.PermissionResult) {
	mode := permission.SessionModeOption(res)
	if mode == "" || a.subagent != nil {
		return
	}
	if ap := a.settings(); ap != nil {
		if _, err := ap.ApplySessionSettings(ctx, a.state.GetID(), session.SettingsChange{PermissionMode: &mode, Source: "permission_dialog"}); err != nil {
			a.log.Warn("permission dialog: the session's permission mode could not be switched", "mode", mode, "error", err)
			return
		}
	} else if st := sessionStatePtr(a.state); st != nil {
		st.SetPermissionMode(mode)
		st.ClearTurnOverride(session.SettingPermissionMode)
		a.log.Info("permission mode switched from the permission dialog", "session", a.state.GetID(), "mode", mode)
	}
	if env != nil {
		env.PermissionMode = effectivePermMode(a.state, a.cfg)
	}
}

// transportKey names what a model request is built for: the model and the
// reasoning level. The transport is rebuilt only when it changes.
func (a *Agent) transportKey() string {
	return a.state.EffectiveModelID(a.cfg) + "|" + a.state.EffectiveReasoning(a.cfg)
}

// childProviderInput applies what a system child's spec says about its
// model calls: the completion cap of memory.copilot_max_tokens, clamped the
// way the copilot pass clamped it.
func (a *Agent) childProviderInput(in llm.ProviderInput) llm.ProviderInput {
	if a.subagent == nil || a.subagent.MaxTokens <= 0 {
		return in
	}
	if in.MaxTokens <= 0 || in.MaxTokens > a.subagent.MaxTokens {
		in.MaxTokens = a.subagent.MaxTokens
	}
	return in
}

// withChildFallbacks wraps a child's provider in the fallback chain its spec
// names (memory.fallback_models, then the session's model): a call that
// fails before producing any output moves to the next model, a stream that
// broke after output does not. An entry that resolves to nothing is skipped
// and logged; an ordinary session, or a child without fallbacks, gets its
// provider back untouched.
func (a *Agent) withChildFallbacks(primary llm.Provider, modelID string, mk func(llm.ProviderInput) (llm.Provider, error)) llm.Provider {
	if a.subagent == nil || len(a.subagent.FallbackModels) == 0 {
		return primary
	}
	candidates := []llm.FallbackCandidate{{Provider: primary, Model: modelID}}
	seen := map[string]bool{modelID: true}
	for _, ref := range a.subagent.FallbackModels {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		rm, err := a.cfg.ResolveLLM(ref)
		if err != nil {
			a.log.Warn("fallback model unavailable; skipped", "model", ref, "error", err)
			continue
		}
		in := a.childProviderInput(a.turnProviderInput(rm))
		in.ReasoningEffort = a.state.EffectiveReasoning(a.cfg)
		provider, err := mk(in)
		if err != nil {
			a.log.Warn("fallback model unavailable; skipped", "model", ref, "error", err)
			continue
		}
		candidates = append(candidates, llm.FallbackCandidate{Provider: provider, Model: ref})
	}
	return llm.NewFallbackChain(candidates, func(from, to string, err error) {
		a.log.Warn("model failed before answering; falling back to the next one", "model", from, "next", to, "error", err)
	})
}

// turnProviderInput is llmProviderInput plus the bounds of a user turn: the
// first-token timer as the call's own budget, and with the wait on, the
// wait's maximum as the turn's budget and the turn's ledger. Helpers
// (compaction, the memory copilot) use llmProviderInput alone and never
// see the option.
func (a *Agent) turnProviderInput(rm *config.ResolvedLLM) llm.ProviderInput {
	in := a.llmProviderInput(rm)
	// The first-token timer cuts a streamed call that stays silent, retry
	// waits included: a server-requested pause the timer would cut anyway
	// is reported as a quota reset instead of being slept through in vain.
	if rm.Stream {
		if timeout := a.cfg.Agent.EffectiveLLMFirstTokenTimeout(); timeout > 0 {
			in.CallBudget = timeout
		}
	}
	// With the wait on, its maximum bounds every sleep the turn spends on a
	// limit, the wrapper's retries included: a pause beyond it comes back
	// as a quota reset and ends the turn at once instead of being slept
	// through by the retries first, and an explicit zero means no sleep on
	// a limit anywhere. The wrapper's sleeps count against the turn's
	// total, calls that succeed afterwards included.
	if a.cfg.Agent.WaitForLimitReset {
		in.RetryBudget, in.RetryBudgetSet = a.cfg.Agent.EffectiveWaitForLimitResetMax(), true
		in.LimitLedger = a.limitLedgerFor()
	}
	return in
}

func (a *Agent) llmProviderInput(rm *config.ResolvedLLM) llm.ProviderInput {
	in := llm.ProviderInput{
		Name:          rm.ProviderName,
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
	}
	// The stall guard (agent.llm_stream_idle_timeout_ms) watches the gaps
	// between the bytes of a streamed answer; a blocking answer arrives in
	// one piece and has no gaps to watch.
	if rm.Stream {
		in.StreamIdleTimeout = a.cfg.Agent.EffectiveLLMStreamIdleTimeout()
	}
	return llm.WithAgentResilience(in, a.cfg.Agent.EffectiveLLMRetryMax(), a.cfg.Agent.LLMRetryBaseMS, a.cfg.Agent.LLMMinIntervalMS)
}

// contentBlocksToText converts ACP content blocks to a plain text string.
// Attachments - a mentioned file, folder, session, rule or subagent, or a
// resource an editor sent - become <coddy_attachment ...> elements with the
// body in CDATA (resourceAttachmentXML), so the SPA and the console can
// collapse them for display while the model retains full context.
func contentBlocksToText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case acp.ContentTypeText:
			parts = append(parts, b.Text)
		case acp.ContentTypeResource:
			if b.Resource != nil {
				parts = append(parts, resourceAttachmentXML(b.Resource))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// wrapXMLCDATA wraps body in CDATA, split where the body itself holds the
// terminator sequence.
func wrapXMLCDATA(body string) string {
	escaped := strings.ReplaceAll(body, "]]>", "]]]]><![CDATA[>")
	return "<![CDATA[" + escaped + "]]>"
}

// extractContextFiles returns the local files and folders the prompt's
// attachments read: a file:// resource an editor sent, and every file or
// folder a mention resolved to. Path-scoped rules activate on them.
func extractContextFiles(blocks []acp.ContentBlock) []string {
	var files []string
	for _, b := range blocks {
		if b.Type != acp.ContentTypeResource || b.Resource == nil {
			continue
		}
		if m := b.Resource.Mention; m != nil && m.Path != "" && (m.Kind == mention.KindFile || m.Kind == mention.KindDirectory) {
			files = append(files, m.Path)
			continue
		}
		if uri := b.Resource.URI; strings.HasPrefix(uri, "file://") {
			base, _, _ := session.SplitLineRangeURI(uri)
			files = append(files, fileURIPath(base))
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
	case "read", "keep_result", "glob", "grep", "websearch", "webfetch", "config_get", "config_changes", "coddy_docs_search", "coddy_docs_read":
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
	if m := state.EffectivePermissionMode(); m != "" {
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
