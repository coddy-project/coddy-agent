// Package session manages per-session state for the agent.
package session

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// Mode is the current operating mode of a session.
type Mode string

const (
	ModeAgent Mode = "agent"
	ModePlan  Mode = "plan"
	ModeAsk   Mode = "ask"
)

// IsValidMode reports whether mode names a known session mode.
func IsValidMode(mode string) bool {
	switch Mode(mode) {
	case ModeAgent, ModePlan, ModeAsk:
		return true
	}
	return false
}

// State holds the complete state of a session.
type State struct {
	mu sync.RWMutex

	// ID is the unique session identifier.
	ID string

	// CWD is the session working directory.
	CWD string

	// Mode is the current operating mode.
	Mode Mode

	// SelectedModelID overrides agent.model for LLM calls when non-empty.
	// when non-empty. Empty means use config defaults for the current mode.
	SelectedModelID string

	// SelectedReasoning overrides the reasoning level for LLM calls when non-empty.
	// Resolved against the effective model's levels by EffectiveReasoning.
	SelectedReasoning string

	// HookContext is the context SessionStart hooks handed to the session;
	// every system prompt of the session carries it (see docs/features/hooks.md).
	HookContext string

	// Messages is the conversation history.
	Messages []llm.Message

	// msgRev counts every change to Messages and msgEditRev only those that
	// are not a plain append. Persistence reads the pair to tell "nothing
	// moved" from "the tail grew" without re-encoding the history to find out;
	// every write to Messages must bump them through the helpers below, under
	// the same lock that made the change.
	msgRev     uint64
	msgEditRev uint64

	// persistID tells this State apart from any other over the same bundle.
	// The counters above start at zero in every State, so a session that was
	// closed and reopened can reach a revision its predecessor already wrote;
	// without an identity a store would read that as "nothing moved" and skip
	// writing a history that is not on disk.
	persistOnce sync.Once
	persistID   uint64

	// UILog holds UI-only transcript lines (errors, etc.); excluded from LLM prompts.
	UILog []UILogEntry

	// hookNotices remembers which hooks-file notices this live session has
	// already recorded (see MarkHookNoticeShown); not persisted.
	hookNotices map[string]bool

	// configuredMCPClients come from config.yaml and are replaced on hot reload.
	configuredMCPClients []*mcp.Client
	// sessionMCPClients come from ACP session/new or session/load parameters.
	sessionMCPClients []*mcp.Client
	// mcpClosed marks the session as torn down so a concurrent settings reload
	// closes the servers it just dialed instead of attaching them to a dead session.
	mcpClosed bool
	// mcpReloadPending records a configured-MCP reload that arrived while a turn
	// held the turn lock. Swapping clients mid-turn would strand the tool
	// definitions the turn already sent to the model, so the reload is parked
	// here and drained when the turn releases the lock.
	mcpReloadPending bool

	// pendingReadyNotify holds session updates that must not reach the client
	// before the response carrying this session id is on the wire. Only
	// session/new reopening a persisted bundle parks work here: the client
	// learns the id from that response. A real session/load needs no deferral,
	// because the client supplied the id and ACP requires the replayed history
	// to arrive before the response.
	pendingReadyNotify func()

	// MCPFilterFactory builds a fresh per-turn MCP tool filter (set by the
	// Manager; may be nil = allow all). Re-reading config and .coddy/mcp.json
	// on every build lets enable/disable toggles apply to live sessions.
	MCPFilterFactory func() func(server, tool string) bool

	// Skills are the loaded slash skills.
	Skills []*skills.Skill

	// RulesCatalog is discovered project rules for the session CWD.
	RulesCatalog []*rules.Rule
	// ActiveAutoRules are sticky auto rules (alwaysApply true after first match).
	ActiveAutoRules []*rules.Rule
	// LastContextBreakdown is the latest per-category token estimate for the UI.
	LastContextBreakdown *ContextBreakdown
	// contextWindows reads the provider-reported context windows cached by
	// the manager that registered this session; nil for a state no manager
	// built. Set at construction and never changed (context_window.go).
	contextWindows providerContextWindows

	// Plan holds the current todo list entries.
	Plan []acp.PlanEntry

	// AgentMemory is optional session notes included in the system prompt template (.Memory).
	AgentMemory string

	// TitlePinned, when set, is written to session.json and overrides derived titles from the first user message.
	TitlePinned string

	// Tags are the session's labels, kept normalized (see NormalizeTags) so
	// every reader compares the same spelling.
	Tags []string

	// Archived takes the session out of the working list without removing the
	// bundle; ArchivedAt records the moment it was put aside.
	Archived   bool
	ArchivedAt string

	// Origin names the surface that started the session; see SessionMeta.Origin.
	Origin string

	// Pinned keeps the session at the top of every listing; PinnedAt records
	// the moment it was pinned, and PinnedRank the place the operator dragged
	// it to among the other pins (lower is higher up; 0 means never placed).
	Pinned     bool
	PinnedAt   string
	PinnedRank int

	// MemoryCopilotBlock is per-turn text from the memory copilot (not persisted to session.json).
	MemoryCopilotBlock string

	// pendingPlanContext is injected into the agent system prompt of the turn a
	// plan run started, and mirrored into the bundle (pending_plan_context.json)
	// so a turn continued after a restart still carries it.
	pendingPlanContext string

	// pendingImageParts are image attachments for the next user message (from inline_files in agent mode); not persisted.
	pendingImageParts []llm.ImagePart
	// surfaceSystemPrompt is the block the surface running the current turn
	// contributed to the system prompt; turn-scoped and never persisted.
	surfaceSystemPrompt string
	// turnWake is the background wake the current turn was started for, until
	// the agent takes it to mark the turn's first message; turn-scoped.
	turnWake *llm.BackgroundWake

	// SessionDir is the persisted session bundle directory (<sessionsRoot>/<id>/).
	SessionDir string

	// Scheduler job session: the session every run of one scheduler job is a
	// child of. It never runs a turn of its own - a read-only transcript for
	// every surface - and is written to session.json as schedulerRun and
	// schedulerJobId, which is what keeps it out of the working list.
	SchedulerRun   bool
	SchedulerJobID string

	// PermissionMode is the session-level override for tools.permission_mode.
	// Empty means use the config default. Values: "ask", "accept_edits", "bypass".
	// It lives in process memory only: a restart returns to the configuration.
	PermissionMode string

	// turn holds the settings that belong to turns rather than to the session
	// (settings_state.go): overrides armed by --once / --count=N, what the
	// running turn holds, what the last operator turn held. settingsMu guards
	// it alone; it is never taken together with mu.
	settingsMu sync.Mutex
	turn       turnSettings
	// settingsRev is the number of the last change of a setting a model
	// request reads (SettingsRevision).
	settingsRev atomic.Uint64
	// publishedSettings is the version of the last snapshot published.
	publishedSettings atomic.Uint64

	// subagent is set for a child session spawned by another session (see
	// subagent.go), a scheduled run included; nil for ordinary chats and for
	// the job session a scheduled run hangs under.
	subagent *SubagentMeta

	// sessionMCPDecls are the ACP client-supplied MCP declarations this session
	// dialed, kept so a child session can redial them: they exist nowhere in
	// the configuration, only on the wire that opened this session.
	sessionMCPDecls []config.MCPServerConfig

	// PermissionCommandGrants are session-scoped shell commands approved via "allow always" (same matching rules as tools.command_allowlist).
	PermissionCommandGrants []string
	// PermissionWriteGrants are keys "toolName|absolutePath" for filesystem tools approved via "allow always".
	PermissionWriteGrants []string
	// PermissionHTTPGrants are the http_request approvals given via an "always"
	// answer: a destination ("origin|..." or "url|...") and what a request to it
	// carried ("file|...", "proxy|...", "insecure|...", "output|..."). The keys
	// are built and matched by internal/permission.
	PermissionHTTPGrants []string

	// activitySeq increments when an agent turn finishes (persisted in session.json).
	// readActivitySeq is advanced when the user marks the session read (PATCH markActivityRead).
	activitySeq     uint64
	readActivitySeq uint64

	// persist is invoked after persisted fields change (set by Manager; may be nil).
	persist func()

	// cancel cancels the active prompt turn.
	cancel context.CancelFunc

	// userCancelledTurn is set when the user explicitly requested cancellation (via Stop or cross-process signal).
	// Cleared at the start of each new turn via SetCancel. Used to distinguish intentional stop from unexpected interruption.
	userCancelledTurn bool

	// queue holds the follow-ups written while the current turn runs, read by
	// the ReAct loop at its next step (turn_queue.go). queueOpen is the turn
	// boundary: a message is only ever accepted by the turn it belongs to.
	// Turn-scoped and never persisted - a queued message outliving the process
	// would be answered by a conversation that has moved on.
	queueMu   sync.Mutex
	queue     []QueuedMessage
	queueOpen bool
	// queueVersion counts the changes, so a client told about the queue down
	// two different connections can tell which answer is the newer one.
	queueVersion uint64
	// queueNotify is what the manager installed to announce a change; it runs
	// after every mutation, with queueMu released.
	queueNotify func()
	// queueMentions resolves the "@" references of a follow-up the turn reads
	// (SetQueuedMentionResolver); turn-scoped like the queue.
	queueMentions func([]acp.ContentBlock) []acp.ContentBlock

	// turnSender is where the current turn publishes its updates, kept so a
	// queue change made from outside the turn's goroutine reaches the clients
	// watching that turn. Turn-scoped, never persisted.
	turnSender acp.UpdateSender

	// progress is the running turn's clock and token count (turn_progress.go).
	// It has a lock of its own: the loop writes it while a call streams, and
	// nothing there should wait on a reader of the history.
	progressMu  sync.Mutex
	progress    TurnProgress
	progressSet bool
}

// GetID returns the session ID.
func (s *State) GetID() string {
	return s.ID
}

// GetCWD returns the session working directory.
func (s *State) GetCWD() string {
	return s.CWD
}

// SetCWD updates the session working directory (persisted in session.json).
func (s *State) SetCWD(dir string) {
	s.mu.Lock()
	s.CWD = dir
	s.mu.Unlock()
	s.touchPersist()
}

// setSessionDir records the bundle directory once it exists (child sessions
// register before their bundle is laid out).
func (s *State) setSessionDir(dir string) {
	s.mu.Lock()
	s.SessionDir = dir
	s.mu.Unlock()
}

// GetPersistedSessionDir returns the filesystem bundle dir if persistence is enabled.
func (s *State) GetPersistedSessionDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SessionDir
}

// SetSchedulerJobWithoutPersist marks this state as the session of a scheduler
// job. It does not persist by itself: the manager saves the state right after
// building it, and a load restores the mark from session.json.
func (s *State) SetSchedulerJobWithoutPersist(jobID string) {
	s.mu.Lock()
	s.SchedulerRun = true
	s.SchedulerJobID = strings.TrimSpace(jobID)
	s.mu.Unlock()
}

// IsSchedulerJob reports whether this session belongs to a scheduler job: the
// parent of that job's runs, never a chat.
func (s *State) IsSchedulerJob() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerRun
}

// GetSchedulerJobID returns the scheduler job this session belongs to, or ""
// for an ordinary session.
func (s *State) GetSchedulerJobID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerJobID
}

// IsReadOnlyTranscript reports whether no surface may start a turn on this
// session: a child spawned by another session (its one turn is its own task
// turn) and the session of a scheduler job (nothing ever talks to it).
func (s *State) IsReadOnlyTranscript() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subagent != nil || s.SchedulerRun
}

// GetSkills returns the loaded skills.
func (s *State) GetSkills() []*skills.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Skills
}

// GetMCPClients returns the connected MCP clients.
func (s *State) GetMCPClients() []*mcp.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clients := make([]*mcp.Client, 0, len(s.configuredMCPClients)+len(s.sessionMCPClients))
	clients = append(clients, s.configuredMCPClients...)
	clients = append(clients, s.sessionMCPClients...)
	return clients
}

func (s *State) addConfiguredMCPClient(client *mcp.Client) {
	if client == nil {
		return
	}
	s.mu.Lock()
	if s.mcpClosed {
		s.mu.Unlock()
		_ = client.Close()
		return
	}
	s.configuredMCPClients = append(s.configuredMCPClients, client)
	s.mu.Unlock()
}

// RememberSessionMCPDeclaration records a client-supplied MCP declaration so a
// child session spawned from this one can redial the same server.
func (s *State) RememberSessionMCPDeclaration(srv config.MCPServerConfig) {
	s.mu.Lock()
	s.sessionMCPDecls = append(s.sessionMCPDecls, srv)
	s.mu.Unlock()
}

// SessionMCPDeclarations returns a copy of the client-supplied MCP
// declarations this session dialed.
func (s *State) SessionMCPDeclarations() []config.MCPServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]config.MCPServerConfig, len(s.sessionMCPDecls))
	copy(out, s.sessionMCPDecls)
	return out
}

// SubagentMeta describes a child session spawned by another session: who
// spawned it, which pool task represents it, how deep it sits, and what role
// and tool set the runtime gave it.
type SubagentMeta struct {
	// Name is the subagent definition name.
	Name string
	// ParentSessionID is the session whose turn spawned this child; the pool
	// task representing the child lives under that session.
	ParentSessionID string
	// TaskID is the background task id of the run.
	TaskID string
	// Depth is the nesting level: 1 for a child of an ordinary session.
	Depth int
	// MaxTurns caps the child's ReAct rounds; 0 uses the configured default.
	MaxTurns int
	// Role is the definition body the child's system prompt carries. Not
	// persisted: a restored child is a read-only transcript.
	Role string
	// Tools is the effective tool set the child may call. Not persisted.
	Tools []string
	// Kind marks a child the runtime started on its own behalf (SubagentKindMemory);
	// empty for a spawn_agent child. The tool registration, the system flag
	// of the task and the manager's shortcuts key on it. Not persisted.
	Kind string
	// PromptTemplate, when set, is the system prompt template source a system
	// child renders instead of the mode template. Not persisted.
	PromptTemplate string
	// MaxTokens clamps the child's completion size; 0 keeps the model's own
	// bound. Not persisted.
	MaxTokens int
	// FallbackModels are the models[].model ids the child's provider moves to
	// when the one before them fails before answering. Not persisted.
	FallbackModels []string
	// Scheduler is set when the child is a run the scheduler started rather
	// than a delegate a model spawned: the job it belongs to, and how the run
	// was triggered. Persisted, so a transcript read from disk still says
	// which job it was.
	Scheduler *SchedulerRunMeta
}

// SchedulerRunMeta is the origin of a scheduled run.
type SchedulerRunMeta struct {
	// JobID is the scheduler job (the file basename under scheduler.dir).
	JobID string
	// Trigger is "cron" for a run the tick started, "manual" for one asked
	// for through the API or a tool.
	Trigger string
	// FireSlot is the UTC minute the cron fire was committed for; zero for a
	// manual run.
	FireSlot time.Time
}

// clone copies the origin so a caller never shares a pointer with the state.
func (m *SchedulerRunMeta) clone() *SchedulerRunMeta {
	if m == nil {
		return nil
	}
	out := *m
	return &out
}

// SubagentKindMemory is the Kind of the memory subagent, the child a user
// turn starts to recall and persist long-term memory.
const SubagentKindMemory = "memory"

// SetSubagentMeta marks the session as a child run. It does not persist by
// itself: the manager saves the state right after building it.
func (s *State) SetSubagentMeta(meta SubagentMeta) {
	meta.Tools = append([]string(nil), meta.Tools...)
	meta.FallbackModels = append([]string(nil), meta.FallbackModels...)
	meta.Scheduler = meta.Scheduler.clone()
	s.mu.Lock()
	s.subagent = &meta
	s.mu.Unlock()
}

// SetSubagentTools replaces the child's effective tool set. The manager calls
// it for a run whose set is decided only once its MCP clients are up, before
// the run's turn starts.
func (s *State) SetSubagentTools(tools []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subagent == nil {
		return
	}
	s.subagent.Tools = append([]string(nil), tools...)
}

// Subagent returns a copy of the child-run metadata, or nil for an ordinary
// session.
func (s *State) Subagent() *SubagentMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.subagent == nil {
		return nil
	}
	out := *s.subagent
	out.Tools = append([]string(nil), s.subagent.Tools...)
	out.FallbackModels = append([]string(nil), s.subagent.FallbackModels...)
	out.Scheduler = s.subagent.Scheduler.clone()
	return &out
}

// IsSubagentRun reports whether this session is a child spawned by another
// session, and therefore a read-only transcript for everyone but its own run.
func (s *State) IsSubagentRun() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subagent != nil
}

// AddSessionMCPClient attaches a client-supplied MCP connection to the session.
// Unlike the configured ones it survives a settings reload, because only the
// ACP client that opened the session can recreate it.
func (s *State) AddSessionMCPClient(client *mcp.Client) {
	if client == nil {
		return
	}
	s.mu.Lock()
	if s.mcpClosed {
		s.mu.Unlock()
		_ = client.Close()
		return
	}
	s.sessionMCPClients = append(s.sessionMCPClients, client)
	s.mu.Unlock()
}

// replaceConfiguredMCPClients atomically swaps hot-reloaded config clients and
// closes the previous processes without disturbing ACP session-provided clients.
// A session torn down while the new servers were still being dialed keeps none
// of them, so the reload cannot orphan subprocesses.
func (s *State) replaceConfiguredMCPClients(clients []*mcp.Client) {
	s.mu.Lock()
	if s.mcpClosed {
		s.mu.Unlock()
		for _, client := range clients {
			_ = client.Close()
		}
		return
	}
	previous := s.configuredMCPClients
	s.configuredMCPClients = append([]*mcp.Client(nil), clients...)
	s.mu.Unlock()
	for _, client := range previous {
		_ = client.Close()
	}
}

// markMCPReloadPending parks a configured-MCP reload for a session whose turn
// lock is currently held. A closed session drops it: there is nothing left to
// reload.
func (s *State) markMCPReloadPending() {
	s.mu.Lock()
	if !s.mcpClosed {
		s.mcpReloadPending = true
	}
	s.mu.Unlock()
}

// hasPendingMCPReload reports whether a parked reload is waiting, without
// clearing it. It lets the turn-lock release skip the lock dance on the common
// path where nothing is parked.
func (s *State) hasPendingMCPReload() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mcpReloadPending
}

// setPendingReadyNotify parks session updates until the response that first
// tells the client this session id has been written.
func (s *State) setPendingReadyNotify(notify func()) {
	s.mu.Lock()
	s.pendingReadyNotify = notify
	s.mu.Unlock()
}

// takePendingReadyNotify atomically clears and returns the parked updates, so
// they are published exactly once.
func (s *State) takePendingReadyNotify() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	notify := s.pendingReadyNotify
	s.pendingReadyNotify = nil
	return notify
}

// takeMCPReloadPending atomically clears the parked-reload flag and reports
// whether it was set, so exactly one of several racing drainers applies it.
func (s *State) takeMCPReloadPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.mcpReloadPending
	s.mcpReloadPending = false
	return pending
}

// GetMCPToolFilter builds the current MCP tool filter. Without a factory the
// filter allows everything (ACP-supplied servers, tests).
func (s *State) GetMCPToolFilter() func(server, tool string) bool {
	s.mu.RLock()
	factory := s.MCPFilterFactory
	s.mu.RUnlock()
	if factory == nil {
		return func(string, string) bool { return true }
	}
	return factory()
}

// SetPersistHook registers a callback after state that is written to disk changes.
func (s *State) SetPersistHook(fn func()) {
	s.mu.Lock()
	s.persist = fn
	s.mu.Unlock()
}

func (s *State) touchPersist() {
	s.mu.RLock()
	fn := s.persist
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

// SetMode updates the session mode (accepts string for interface compatibility).
func (s *State) SetMode(mode string) {
	s.mu.Lock()
	s.Mode = Mode(mode)
	s.mu.Unlock()
	s.bumpSettingsRevision()
	s.touchPersist()
}

// GetMode returns the current mode as a string.
func (s *State) GetMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(s.Mode)
}

// SetPermissionMode updates the session-level permission mode override.
func (s *State) SetPermissionMode(mode string) {
	s.mu.Lock()
	s.PermissionMode = mode
	s.mu.Unlock()
	s.bumpSettingsRevision()
	s.touchPersist()
}

// GetPermissionMode returns the session-level permission mode override (empty = use config default).
func (s *State) GetPermissionMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PermissionMode
}

// GetSelectedModelID returns the session model override, or empty if defaults apply.
func (s *State) GetSelectedModelID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SelectedModelID
}

// SetSelectedModelID sets the session model override (empty to use config defaults per mode).
func (s *State) SetSelectedModelID(id string) {
	s.mu.Lock()
	s.SelectedModelID = id
	s.mu.Unlock()
	s.bumpSettingsRevision()
	s.touchPersist()
}

// GetHookContext returns the context SessionStart hooks handed to the session.
func (s *State) GetHookContext() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HookContext
}

// SetHookContext replaces the SessionStart hook context and persists it.
func (s *State) SetHookContext(text string) {
	s.mu.Lock()
	s.HookContext = strings.TrimSpace(text)
	s.mu.Unlock()
	s.touchPersist()
}

// RestoreHookContextWithoutPersist sets the hook context from disk (session load).
func (s *State) RestoreHookContextWithoutPersist(text string) {
	s.mu.Lock()
	s.HookContext = strings.TrimSpace(text)
	s.mu.Unlock()
}

// GetSelectedReasoning returns the session reasoning override, or empty.
func (s *State) GetSelectedReasoning() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SelectedReasoning
}

// SetSelectedReasoning sets the session reasoning override (empty to use the model default).
func (s *State) SetSelectedReasoning(level string) {
	s.mu.Lock()
	s.SelectedReasoning = level
	s.mu.Unlock()
	s.bumpSettingsRevision()
	s.touchPersist()
}

// EffectiveReasoning returns the reasoning level for LLM calls for this session.
// Returns empty when the effective model has no reasoning support. The running
// turn's own level wins, then the session's selection when the model offers it
// ("off" included where the provider can turn thinking off), then the model's
// default level.
func (s *State) EffectiveReasoning(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	ent := cfg.FindModelEntry(s.EffectiveModelID(cfg))
	if ent == nil {
		return ""
	}
	choices := cfg.ReasoningChoicesFor(ent)
	if len(choices) == 0 {
		return ""
	}
	if turn := s.TurnSetting(SettingReasoning); turn != "" {
		if turn == config.ReasoningDefault {
			return cfg.DefaultReasoningLevelFor(ent)
		}
		if containsLevel(choices, turn) {
			return turn
		}
	}
	s.mu.RLock()
	sel := strings.TrimSpace(s.SelectedReasoning)
	s.mu.RUnlock()
	if containsLevel(choices, sel) {
		return sel
	}
	return cfg.DefaultReasoningLevelFor(ent)
}

// ContextWindow resolves the context window of the session's effective model:
// its max_context_tokens, then the window its provider's model listing
// reported to the manager that owns the session, then
// config.DefaultContextWindowTokens. It is the window GET /v1/models hands the
// web UI for the same model. tokens is 0 when the model is not configured.
func (s *State) ContextWindow(cfg *config.Config) (tokens int, source string) {
	if s == nil || cfg == nil {
		return 0, ""
	}
	return resolveContextWindow(cfg, s.EffectiveModelID(cfg), s.contextWindows)
}

// EffectiveModelID returns the model id used for LLM calls for this session.
func (s *State) EffectiveModelID(cfg *config.Config) string {
	// The running turn's own model wins while the configuration still knows it.
	if turn := s.TurnSetting(SettingModel); turn != "" && cfg != nil && cfg.FindModelEntry(turn) != nil {
		return turn
	}
	s.mu.RLock()
	sel := s.SelectedModelID
	s.mu.RUnlock()
	return ResolveModelID(cfg, sel)
}

// ResolveModelID is the model a session selecting this one runs on: the selection
// when the config knows it, the config's agent model when nothing is selected. A
// caller that names what a session will run before the session exists (the row of a
// scheduled run) asks here, so it names the same model the session then picks.
func ResolveModelID(cfg *config.Config, selected string) string {
	if sel := strings.TrimSpace(selected); sel != "" {
		return normalizeModelID(cfg, sel)
	}
	return normalizeModelID(cfg, strings.TrimSpace(cfg.Agent.Model))
}

func normalizeModelID(cfg *config.Config, id string) string {
	if id == "" {
		return ""
	}
	for i := range cfg.Models {
		if cfg.Models[i].Model == id {
			return id
		}
	}
	if len(cfg.Models) > 0 {
		return cfg.Models[0].Model
	}
	return id
}

// AddMessage appends a message to the conversation history.
func (s *State) AddMessage(msg llm.Message) {
	s.mu.Lock()
	s.Messages = append(s.Messages, msg)
	s.markMessagesAppended()
	s.mu.Unlock()
	s.touchPersist()
}

// markMessagesAppended records that messages were added at the end and nothing
// else moved. Callers hold s.mu.
func (s *State) markMessagesAppended() { s.msgRev++ }

// markMessagesEdited records a change that is not a plain append: an existing
// message was rewritten, or the history was replaced wholesale. Callers hold
// s.mu.
func (s *State) markMessagesEdited() { s.msgRev++; s.msgEditRev++ }

// statePersistIDs hands out the identity of each State that gets persisted.
var statePersistIDs atomic.Uint64

// MessagesForPersist returns the history together with the two revisions and
// this State's identity, read under one lock so a store cannot pair a history
// with revisions from either side of a concurrent change.
//
// The copy is deep where a message can still be changed underneath it: a
// PlanDocument is a pointer, and an in-place plan edit would otherwise rewrite
// content this snapshot is already encoding, pairing it with the revision from
// before the edit.
func (s *State) MessagesForPersist() (msgs []llm.Message, rev, editRev, id uint64) {
	s.persistOnce.Do(func() { s.persistID = statePersistIDs.Add(1) })
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs = make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	for i := range msgs {
		if pd := msgs[i].PlanDocument; pd != nil {
			snapshot := *pd
			msgs[i].PlanDocument = &snapshot
		}
	}
	return msgs, s.msgRev, s.msgEditRev, s.persistID
}

// GetMessages returns a copy of the message history. The copy is shallow: a
// message's PlanDocument is the same object the session holds, so a caller
// must treat what it gets back as read-only. Changing it would change the
// session's history without moving the revisions persistence reads, and the
// change would not reach disk. MessagesForPersist is the deep variant.
func (s *State) GetMessages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs
}

// MessagesRev is the revision of the message history: it moves on every append and
// every edit. A client that loaded the history at one revision holds everything a
// stream frame written before it described.
func (s *State) MessagesRev() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.msgRev
}

// MessagesWithRev returns a copy of the history with the revision it reflects, read
// under one lock so the two cannot come from either side of a change.
func (s *State) MessagesWithRev() ([]llm.Message, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs, s.msgRev
}

// GetAgentMemory returns session memory text for prompt templates.
func (s *State) GetAgentMemory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AgentMemory
}

// SetAgentMemory sets session notes included in rendered system prompts.
func (s *State) SetAgentMemory(text string) {
	s.mu.Lock()
	s.AgentMemory = text
	s.mu.Unlock()
	s.touchPersist()
}

// ConversationTitle returns the pinned title, or the one derived from the
// first user message (the value session.json records).
func (s *State) ConversationTitle() string {
	return persistedConversationTitle(s)
}

// GetTitlePinned returns the user-pinned session title shown in snapshots, if any.
func (s *State) GetTitlePinned() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TitlePinned
}

// SetTitlePinned sets the pinned title and persists session metadata when a store is attached.
func (s *State) SetTitlePinned(text string) {
	_ = s.ReplaceTitlePinned(text)
}

// ReplaceTitlePinned sets the pinned title and reports whether it moved. A
// caller that has to say what a write changed gets the answer from the write
// itself rather than reading the field first, which would be a different value
// by the time it wrote.
func (s *State) ReplaceTitlePinned(text string) bool {
	next := strings.TrimSpace(text)
	s.mu.Lock()
	if s.TitlePinned == next {
		s.mu.Unlock()
		return false
	}
	s.TitlePinned = next
	s.mu.Unlock()
	s.touchPersist()
	return true
}

// SetTitlePinnedIfUnset names the session only while it has no pinned title of
// its own, and answers the title it carries afterwards with whether this call
// wrote it. "Name it unless it has a name" is one step on purpose: the caller
// is the suggestion a describe call made seconds ago, racing whoever named the
// session in the meantime, and a read followed by a write is exactly the race
// it is trying to avoid.
func (s *State) SetTitlePinnedIfUnset(text string) (title string, written bool) {
	next := strings.TrimSpace(text)
	s.mu.Lock()
	if existing := strings.TrimSpace(s.TitlePinned); existing != "" {
		s.mu.Unlock()
		return existing, false
	}
	if s.TitlePinned == next {
		s.mu.Unlock()
		return next, false
	}
	s.TitlePinned = next
	s.mu.Unlock()
	s.touchPersist()
	return next, true
}

// SetTitlePinnedWithoutPersist restores pinned title from disk without writing.
func (s *State) SetTitlePinnedWithoutPersist(text string) {
	s.mu.Lock()
	s.TitlePinned = strings.TrimSpace(text)
	s.mu.Unlock()
}

// GetTags returns a copy of the session tags, so a caller cannot reach back
// into the state through the slice it was handed.
func (s *State) GetTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.Tags) == 0 {
		return nil
	}
	return append([]string(nil), s.Tags...)
}

// SetTags replaces the session tags and persists metadata when a store is
// attached. The values are normalized here, so nothing downstream has to
// wonder which spelling reached it. Writing the set it already has changes
// nothing and costs no write.
func (s *State) SetTags(tags []string) {
	_, _ = s.ReplaceTags(tags)
}

// ReplaceTags stores the whole set and answers what the session carries
// afterwards, with whether this call moved it.
func (s *State) ReplaceTags(tags []string) (stored []string, changed bool) {
	next := NormalizeTags(tags)
	s.mu.Lock()
	if slices.Equal(s.Tags, next) {
		s.mu.Unlock()
		return append([]string(nil), next...), false
	}
	s.Tags = next
	s.mu.Unlock()
	s.touchPersist()
	return append([]string(nil), next...), true
}

// UpdateTags adds and removes labels around the ones the session already
// carries, and answers the set it holds afterwards. The merge happens under the
// lock: "keep the rest" is the whole promise of an add, and a caller that reads
// the tags, merges and writes them back drops whatever another surface filed in
// between - which is the one thing this shape of call is for.
func (s *State) UpdateTags(add, remove []string) (stored []string, changed bool) {
	s.mu.Lock()
	next := MergeTags(s.Tags, add, remove)
	if slices.Equal(s.Tags, next) {
		s.mu.Unlock()
		return append([]string(nil), next...), false
	}
	s.Tags = next
	s.mu.Unlock()
	s.touchPersist()
	return append([]string(nil), next...), true
}

// SetTagsWithoutPersist restores tags from disk without writing.
func (s *State) SetTagsWithoutPersist(tags []string) {
	s.mu.Lock()
	s.Tags = NormalizeTags(tags)
	s.mu.Unlock()
}

// GetArchived reports whether the session was put aside.
func (s *State) GetArchived() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Archived
}

// GetArchivedAt returns when the session was archived, empty while it is not.
func (s *State) GetArchivedAt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ArchivedAt
}

// ArchiveState returns the flag and its stamp together. They are one fact, and
// a writer that reads them under two locks can be caught between the two halves
// of a change - persisting "archived with no stamp", or a stamp on a session
// that is no longer archived.
func (s *State) ArchiveState() (archived bool, at string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Archived, s.ArchivedAt
}

// SetArchived moves the session in or out of the archive and persists metadata
// when a store is attached. The stamp is taken on the way in and cleared on the
// way out; archiving a session that is already archived leaves the original
// stamp standing, because that is when it was put aside.
func (s *State) SetArchived(archived bool) {
	s.mu.Lock()
	if s.Archived == archived {
		// Already where it is being put: nothing to write, and in particular no
		// new stamp - when it was put aside is when it was put aside.
		s.mu.Unlock()
		return
	}
	if archived {
		s.Archived = true
		s.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		s.Archived, s.ArchivedAt = false, ""
	}
	s.mu.Unlock()
	s.touchPersist()
}

// SetArchivedWithoutPersist restores the archive flag and its stamp from disk
// without writing.
func (s *State) SetArchivedWithoutPersist(archived bool, at string) {
	s.mu.Lock()
	s.Archived = archived
	if archived {
		s.ArchivedAt = strings.TrimSpace(at)
	} else {
		s.ArchivedAt = ""
	}
	s.mu.Unlock()
}

// PinState returns the pin flag and its stamp together, for the same reason
// ArchiveState does: they are one fact and a writer must not catch half of it.
func (s *State) PinState() (pinned bool, at string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Pinned, s.PinnedAt
}

// PinPlacement returns the pin, its stamp and its hand-placed rank together:
// three halves of one fact, for the same reason ArchiveState returns two.
func (s *State) PinPlacement() (pinned bool, at string, rank int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Pinned, s.PinnedAt, s.PinnedRank
}

// SetPinnedRank records where among the pins the operator dragged this one.
// It means nothing for a session that is not pinned, so it is ignored there.
func (s *State) SetPinnedRank(rank int) {
	s.mu.Lock()
	if !s.Pinned || s.PinnedRank == rank {
		s.mu.Unlock()
		return
	}
	s.PinnedRank = rank
	s.mu.Unlock()
	s.touchPersist()
}

// SetPinned keeps the session at the top of every listing, or lets it back into
// the order. Pinning a pinned session changes nothing and costs no write, and
// in particular leaves the original stamp standing.
func (s *State) SetPinned(pinned bool) {
	s.mu.Lock()
	if s.Pinned == pinned {
		s.mu.Unlock()
		return
	}
	if pinned {
		s.Pinned = true
		s.PinnedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		// Unpinning forgets the placement too: pinning again is a new pin, and
		// a new pin goes where new pins go rather than to a seat it once had.
		s.Pinned, s.PinnedAt, s.PinnedRank = false, "", 0
	}
	s.mu.Unlock()
	s.touchPersist()
}

// SetPinnedWithoutPersist restores the pin, its stamp and its rank from disk.
func (s *State) SetPinnedWithoutPersist(pinned bool, at string, rank int) {
	s.mu.Lock()
	s.Pinned = pinned
	if pinned {
		s.PinnedAt, s.PinnedRank = strings.TrimSpace(at), rank
	} else {
		s.PinnedAt, s.PinnedRank = "", 0
	}
	s.mu.Unlock()
}

// GetOrigin returns the surface that started the session, empty for a session
// a person opened on this host.
func (s *State) GetOrigin() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Origin
}

// SetOrigin records the surface that started the session and persists metadata
// when a store is attached. It is written once, by the surface that created the
// session: a conversation does not change where it came from, and a later
// writer must not relabel somebody else's chat.
//
// An empty origin means "not recorded" rather than "opened on this host", which
// is also what every bundle stored before the field existed carries. That is why
// the guard here cannot be the whole protection: a caller stamps a session only
// when it is the one creating it (see the Telegram gateway's ensureSession),
// and this guard catches the repeat calls that follow.
func (s *State) SetOrigin(origin string) {
	s.mu.Lock()
	if strings.TrimSpace(s.Origin) != "" {
		s.mu.Unlock()
		return
	}
	s.Origin = strings.TrimSpace(origin)
	s.mu.Unlock()
	s.touchPersist()
}

// SetOriginWithoutPersist restores the origin from disk without writing.
func (s *State) SetOriginWithoutPersist(origin string) {
	s.mu.Lock()
	s.Origin = strings.TrimSpace(origin)
	s.mu.Unlock()
}

// GetMemoryCopilotBlock returns ephemeral recall text for the current user turn.
func (s *State) GetMemoryCopilotBlock() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MemoryCopilotBlock
}

// SetMemoryCopilotBlock sets recall text for this turn only (no disk persist).
func (s *State) SetMemoryCopilotBlock(text string) {
	s.mu.Lock()
	s.MemoryCopilotBlock = text
	s.mu.Unlock()
}

// ClearMemoryCopilotBlock clears recall text before a new user turn.
func (s *State) ClearMemoryCopilotBlock() {
	s.mu.Lock()
	s.MemoryCopilotBlock = ""
	s.mu.Unlock()
}

// SetPendingPlanContext sets design plan text for the agent turn about to
// start, and stores it in the session bundle beside the permission gate. That
// turn can stop on a permission prompt and be continued after the process has
// been restarted, and the continuation renders the same system prompt.
func (s *State) SetPendingPlanContext(text string) {
	s.mu.Lock()
	s.pendingPlanContext = strings.TrimSpace(text)
	text = s.pendingPlanContext
	dir := strings.TrimSpace(s.SessionDir)
	s.mu.Unlock()
	if dir == "" {
		return
	}
	if text == "" {
		_ = ClearPendingPlanContext(dir)
		return
	}
	_ = WritePendingPlanContext(dir, text)
}

// PendingPlanContext returns the hand-off of the turn in flight without
// consuming it. Reading it destructively is what used to lose it: the first
// system prompt of the turn took it, and everything rendered after a permission
// prompt - a rebuild after compaction, the continuation the user's answer
// starts - carried on without it. ClearPendingPlanContext releases it once the
// turn is really over.
func (s *State) PendingPlanContext() string {
	s.mu.RLock()
	out := s.pendingPlanContext
	dir := strings.TrimSpace(s.SessionDir)
	s.mu.RUnlock()
	if out != "" || dir == "" {
		return out
	}
	// Nothing in memory: this process did not start the turn. The bundle did.
	return ReadPendingPlanContext(dir)
}

// ClearPendingPlanContext releases the hand-off, in memory and in the bundle.
func (s *State) ClearPendingPlanContext() {
	s.mu.Lock()
	s.pendingPlanContext = ""
	dir := strings.TrimSpace(s.SessionDir)
	s.mu.Unlock()
	if dir != "" {
		_ = ClearPendingPlanContext(dir)
	}
}

// SetSurfaceSystemPrompt records the system prompt block the surface running
// the current turn contributed. It is turn-scoped state, held only while the
// turn lock is: nothing writes it to the bundle, so what a session keeps does
// not depend on where its last turn came from.
func (s *State) SetSurfaceSystemPrompt(block string) {
	s.mu.Lock()
	s.surfaceSystemPrompt = strings.TrimSpace(block)
	s.mu.Unlock()
}

// GetSurfaceSystemPrompt returns that block, or "" when the turn came from a
// surface that asks for nothing.
func (s *State) GetSurfaceSystemPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.surfaceSystemPrompt
}

// SetTurnWake records that the current turn was started by finished background
// tasks rather than by somebody typing. The agent takes it once, when it turns
// the prompt into the turn's first message (TakeTurnWake); the manager clears
// whatever is left when the turn is released. Nil clears it.
func (s *State) SetTurnWake(wake *llm.BackgroundWake) {
	s.mu.Lock()
	s.turnWake = wake
	s.mu.Unlock()
}

// TakeTurnWake returns the wake the current turn was started for and forgets
// it, so a continuation of the same admitted turn (a queued follow-up) is not
// marked a second time. Nil for a turn somebody typed.
func (s *State) TakeTurnWake() *llm.BackgroundWake {
	s.mu.Lock()
	defer s.mu.Unlock()
	wake := s.turnWake
	s.turnWake = nil
	return wake
}

// SetTurnSender records where the running turn publishes its session updates.
//
// Most updates are sent by the turn's own goroutine, which holds the sender
// already. A message queue change is the exception: it is made by whoever is
// watching - an HTTP request, a console keystroke - and still has to reach
// every client attached to that turn's stream. Turn-scoped, cleared when the
// turn releases, never persisted.
func (s *State) SetTurnSender(sender acp.UpdateSender) {
	s.mu.Lock()
	s.turnSender = sender
	s.mu.Unlock()
}

// TurnSender returns that sender, or nil when no turn is running.
func (s *State) TurnSender() acp.UpdateSender {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.turnSender
}

// SetPendingImageParts stores image parts to be attached to the next user message.
func (s *State) SetPendingImageParts(parts []llm.ImagePart) {
	s.mu.Lock()
	s.pendingImageParts = parts
	s.mu.Unlock()
}

// TakePendingImageParts returns and clears the pending image parts.
func (s *State) TakePendingImageParts() []llm.ImagePart {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.pendingImageParts
	s.pendingImageParts = nil
	return out
}

// AppendPlanDocument adds a UI transcript row for a design plan file.
func (s *State) AppendPlanDocument(doc plans.Document) {
	s.mu.Lock()
	updated := ""
	if !doc.UpdatedAt.IsZero() {
		updated = doc.UpdatedAt.UTC().Format(time.RFC3339)
	}
	path := ""
	if sd := strings.TrimSpace(s.SessionDir); sd != "" {
		if p, err := plans.FilePath(sd, doc.Slug); err == nil {
			path = p
		}
	}
	s.Messages = append(s.Messages, llm.Message{
		Role: llm.RoleAssistant,
		PlanDocument: &llm.PlanDocumentSnapshot{
			Slug:      doc.Slug,
			Name:      doc.Name,
			Overview:  doc.Overview,
			Content:   doc.Content,
			Body:      doc.Body,
			Path:      path,
			UpdatedAt: updated,
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	s.markMessagesAppended()
	s.mu.Unlock()
	s.touchPersist()
}

// PlanDocumentContentBySlug returns the last transcript snapshot content for slug, if any.
func (s *State) PlanDocumentContentBySlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.Messages) - 1; i >= 0; i-- {
		pd := s.Messages[i].PlanDocument
		if pd == nil || pd.Slug != slug {
			continue
		}
		return strings.TrimSpace(pd.Content)
	}
	return ""
}

// UpdatePlanDocumentFromWrite refreshes plan_document rows after a design plan file save.
func (s *State) UpdatePlanDocumentFromWrite(doc plans.Document) {
	slug := strings.TrimSpace(doc.Slug)
	if slug == "" {
		return
	}
	updated := ""
	if !doc.UpdatedAt.IsZero() {
		updated = doc.UpdatedAt.UTC().Format(time.RFC3339)
	}
	path := ""
	if sd := strings.TrimSpace(s.SessionDir); sd != "" {
		if p, err := plans.FilePath(sd, slug); err == nil {
			path = p
		}
	}
	s.mu.Lock()
	for i := range s.Messages {
		pd := s.Messages[i].PlanDocument
		if pd == nil || pd.Slug != slug {
			continue
		}
		s.Messages[i].PlanDocument.Name = doc.Name
		s.Messages[i].PlanDocument.Overview = doc.Overview
		s.Messages[i].PlanDocument.Content = doc.Content
		s.Messages[i].PlanDocument.Body = doc.Body
		if path != "" {
			s.Messages[i].PlanDocument.Path = path
		}
		if updated != "" {
			s.Messages[i].PlanDocument.UpdatedAt = updated
		}
		s.markMessagesEdited()
	}
	s.mu.Unlock()
	s.touchPersist()
}

// MarkPlanDocumentDiscarded flags transcript rows for slug as discarded (UI + plan-mode prompt).
func (s *State) MarkPlanDocumentDiscarded(slug string) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return
	}
	s.mu.Lock()
	for i := range s.Messages {
		pd := s.Messages[i].PlanDocument
		if pd == nil || pd.Slug != slug {
			continue
		}
		s.Messages[i].PlanDocument.Discarded = true
		s.markMessagesEdited()
	}
	s.mu.Unlock()
	s.touchPersist()
}

// DiscardedPlanSlugs returns unique slugs from discarded plan_document transcript rows.
func (s *State) DiscardedPlanSlugs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []string
	for _, m := range s.Messages {
		pd := m.PlanDocument
		if pd == nil || !pd.Discarded {
			continue
		}
		slug := strings.TrimSpace(pd.Slug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}
	return out
}

// GetPlan returns a copy of the current plan entries.
func (s *State) GetPlan() []acp.PlanEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]acp.PlanEntry, len(s.Plan))
	copy(result, s.Plan)
	return result
}

// SetPlan replaces the current plan entries.
func (s *State) SetPlan(entries []acp.PlanEntry) {
	s.mu.Lock()
	s.Plan = entries
	s.mu.Unlock()
	s.touchPersist()
}

// SetPlanWithoutPersist assigns the plan without touching disk (bootstrap from snapshot).
func (s *State) SetPlanWithoutPersist(entries []acp.PlanEntry) {
	s.mu.Lock()
	s.Plan = entries
	s.mu.Unlock()
}

// ReplaceMessagesWithoutPersist replaces conversation history without persisting (bootstrap).
//
// The plan documents are copied rather than adopted. Handed another State's
// messages, this would otherwise share those pointers with it, and an edit
// there would change this history without touching its revisions - which
// persistence reads as "nothing moved".
func (s *State) ReplaceMessagesWithoutPersist(msgs []llm.Message) {
	owned := make([]llm.Message, len(msgs))
	copy(owned, msgs)
	for i := range owned {
		if pd := owned[i].PlanDocument; pd != nil {
			snapshot := *pd
			owned[i].PlanDocument = &snapshot
		}
	}
	s.mu.Lock()
	s.Messages = owned
	s.markMessagesEdited()
	s.mu.Unlock()
}

// RestoreMetaWithoutPersist restores mode, model/reasoning and memory from disk
// (no persistence callback). The permission-mode override is never restored:
// it lasts as long as the process.
func (s *State) RestoreMetaWithoutPersist(mode Mode, selectedModelID, selectedReasoning, agentMemory string) {
	s.mu.Lock()
	s.Mode = mode
	s.SelectedModelID = selectedModelID
	s.SelectedReasoning = selectedReasoning
	s.AgentMemory = agentMemory
	s.mu.Unlock()
}

// ReplaceSkills replaces loaded skills without touching disk (used when rebuilding session).
func (s *State) ReplaceSkills(sk []*skills.Skill) {
	s.mu.Lock()
	s.Skills = sk
	s.mu.Unlock()
}

// GetRulesCatalog returns discovered rules.
func (s *State) GetRulesCatalog() []*rules.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RulesCatalog
}

// ReplaceRulesCatalog sets the rules catalog (session bootstrap).
func (s *State) ReplaceRulesCatalog(cat []*rules.Rule) {
	s.mu.Lock()
	s.RulesCatalog = cat
	s.ActiveAutoRules = nil
	s.mu.Unlock()
}

// GetActiveAutoRules returns sticky auto rules.
func (s *State) GetActiveAutoRules() []*rules.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveAutoRules
}

// SetActiveAutoRules updates sticky auto rules.
func (s *State) SetActiveAutoRules(r []*rules.Rule) {
	s.mu.Lock()
	s.ActiveAutoRules = r
	s.mu.Unlock()
}

// GetLastContextBreakdown returns the latest context breakdown for UI.
func (s *State) GetLastContextBreakdown() *ContextBreakdown {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.LastContextBreakdown == nil {
		return nil
	}
	cp := *s.LastContextBreakdown
	return &cp
}

// SetLastContextBreakdown stores the latest breakdown.
func (s *State) SetLastContextBreakdown(b *ContextBreakdown) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b == nil {
		s.LastContextBreakdown = nil
		return
	}
	cp := *b
	s.LastContextBreakdown = &cp
}

// SetCancel stores a cancel function for the active prompt turn and resets the user-cancelled flag.
func (s *State) SetCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
	s.userCancelledTurn = false
}

// SetUserCancelledTurn marks the current turn as explicitly cancelled by the user.
func (s *State) SetUserCancelledTurn() {
	s.mu.Lock()
	s.userCancelledTurn = true
	s.mu.Unlock()
}

// IsUserCancelledTurn reports whether the current turn was explicitly cancelled by the user.
func (s *State) IsUserCancelledTurn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userCancelledTurn
}

// Cancel cancels the active prompt turn if any.
func (s *State) Cancel() {
	s.mu.RLock()
	cancel := s.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// CloseAll closes all MCP clients. The session is left marked as closed so a
// settings reload racing this teardown does not reattach fresh servers.
func (s *State) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpClosed = true
	for _, c := range s.configuredMCPClients {
		_ = c.Close()
	}
	for _, c := range s.sessionMCPClients {
		_ = c.Close()
	}
	s.configuredMCPClients = nil
	s.sessionMCPClients = nil
}

// RestorePermissionGrantsWithoutPersist loads grants from disk snapshot (session/load).
func (s *State) RestorePermissionGrantsWithoutPersist(commands, writes, httpKeys []string) {
	s.mu.Lock()
	s.PermissionCommandGrants = append([]string(nil), commands...)
	s.PermissionWriteGrants = append([]string(nil), writes...)
	s.PermissionHTTPGrants = append([]string(nil), httpKeys...)
	s.mu.Unlock()
}

// RestoreActivityFromSnapshot restores activitySeq/readActivitySeq from disk (session/load).
func (s *State) RestoreActivityFromSnapshot(activitySeq, readActivitySeq uint64) {
	s.mu.Lock()
	s.activitySeq = activitySeq
	s.readActivitySeq = readActivitySeq
	s.mu.Unlock()
}

// GetActivitySeq returns the persisted activity generation counter.
func (s *State) GetActivitySeq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activitySeq
}

// GetReadActivitySeq returns the last read activity generation.
func (s *State) GetReadActivitySeq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readActivitySeq
}

// BumpActivitySeq increments the activity counter after a completed agent turn and persists.
func (s *State) BumpActivitySeq() {
	s.mu.Lock()
	s.activitySeq++
	s.mu.Unlock()
	s.touchPersist()
}

// MarkActivityReadSynced sets readActivitySeq to the current activitySeq in memory.
// Persist to disk via FileStore.PatchSessionMetaActivitySync (HTTP) so updatedAt is not bumped.
func (s *State) MarkActivityReadSynced() {
	s.mu.Lock()
	s.readActivitySeq = s.activitySeq
	s.mu.Unlock()
}

// GetPermissionCommandGrants returns a copy of session command grants.
func (s *State) GetPermissionCommandGrants() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.PermissionCommandGrants))
	copy(out, s.PermissionCommandGrants)
	return out
}

// GetPermissionWriteGrants returns a copy of session write grant keys.
func (s *State) GetPermissionWriteGrants() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.PermissionWriteGrants))
	copy(out, s.PermissionWriteGrants)
	return out
}

// GetPermissionHTTPGrants returns a copy of the session's http_request grant keys.
func (s *State) GetPermissionHTTPGrants() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.PermissionHTTPGrants))
	copy(out, s.PermissionHTTPGrants)
	return out
}

// AddHTTPGrantIfNew appends an http_request grant key if not already present.
func (s *State) AddHTTPGrantIfNew(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	for _, g := range s.PermissionHTTPGrants {
		if g == key {
			s.mu.Unlock()
			return
		}
	}
	s.PermissionHTTPGrants = append(s.PermissionHTTPGrants, key)
	s.mu.Unlock()
	s.touchPersist()
}

// AddCommandGrantIfNew appends a command pattern if not already matched by existing grants.
func (s *State) AddCommandGrantIfNew(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}
	s.mu.Lock()
	for _, g := range s.PermissionCommandGrants {
		if g == cmd {
			s.mu.Unlock()
			return
		}
	}
	s.PermissionCommandGrants = append(s.PermissionCommandGrants, cmd)
	s.mu.Unlock()
	s.touchPersist()
}

// AddWriteGrantIfNew appends a write grant key if not already present.
func (s *State) AddWriteGrantIfNew(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	for _, g := range s.PermissionWriteGrants {
		if g == key {
			s.mu.Unlock()
			return
		}
	}
	s.PermissionWriteGrants = append(s.PermissionWriteGrants, key)
	s.mu.Unlock()
	s.touchPersist()
}
