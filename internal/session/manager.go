package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// AgentRunner is a function that runs the ReAct loop for a prompt turn.
// It is provided at Manager construction time to avoid circular imports.
// sender is used for session updates and permission prompts (ACP server or HTTP bridge).
type AgentRunner func(ctx context.Context, state *State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error)

// Manager handles all active sessions and implements acp.Handler.
type Manager struct {
	cfgAt      atomic.Pointer[config.Config]
	server     acp.UpdateSender
	skillsLoad *skills.Loader
	runner     AgentRunner
	log        *slog.Logger
	// defaultCWD is used when session/new passes an empty cwd (from CLI default or os.Getwd).
	defaultCWD string
	store      *FileStore

	// preferredNewSessionID, when non-empty before session/new is handled, selects the id for the next new session (--session-id).
	preferredNewSessionID string

	sessions map[string]*State
	mu       sync.RWMutex

	// stubTurnMu guards in-process turns when flock is unavailable or SessionDir is empty.
	stubTurnMu sync.Map // sessionID -> *sync.Mutex

	// activeTurns counts prompt turns in flight in THIS process, keyed by session id.
	// See turn_active.go for why it is a count rather than a set.
	activeTurnMu sync.Mutex
	activeTurns  map[string]int

	// turnObservers receive the started/ended edges of activeTurns (see turn_events.go).
	turnObserverMu  sync.Mutex
	turnObservers   map[int]func(TurnEvent)
	turnObserverSeq int
}

// NewManager creates a session manager. defaultCWD is the fallback filesystem root when the
// ACP client omits cwd; may be empty if every session supplies a non-empty cwd.
// store may be nil to disable persistence.
func NewManager(cfg *config.Config, server acp.UpdateSender, runner AgentRunner, log *slog.Logger, defaultCWD string, store *FileStore) *Manager {
	skillsDirs := make([]string, len(cfg.Skills.Dirs))
	copy(skillsDirs, cfg.Skills.Dirs)

	m := &Manager{
		server:     server,
		runner:     runner,
		skillsLoad: skills.NewLoader(skillsDirs),
		log:        log,
		defaultCWD: defaultCWD,
		store:      store,
		sessions:   make(map[string]*State),
	}
	m.cfgAt.Store(cfg)
	return m
}

// Cfg returns the current configuration (same pointer as used by the session manager).
func (m *Manager) Cfg() *config.Config {
	return m.activeCfg()
}

// activeCfg returns the current process configuration (never nil after NewManager).
func (m *Manager) activeCfg() *config.Config {
	return m.cfgAt.Load()
}

// mcpReloadTimeout bounds the MCP handshakes triggered by a settings save so a
// hung server cannot block the request that replaced the configuration. The
// stdio subprocess itself outlives this context (see newStdioTransport).
const mcpReloadTimeout = 30 * time.Second

// ReplaceConfig swaps the live configuration, rebuilds the skills loader, and
// applies configured MCP server changes to sessions that are already active.
func (m *Manager) ReplaceConfig(next *config.Config) {
	if next == nil {
		return
	}
	previous := m.activeCfg()
	skillsDirs := make([]string, len(next.Skills.Dirs))
	copy(skillsDirs, next.Skills.Dirs)
	m.skillsLoad = skills.NewLoader(skillsDirs)
	m.cfgAt.Store(next)
	if previous != nil && reflect.DeepEqual(previous.MCPServers, next.MCPServers) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpReloadTimeout)
	defer cancel()
	m.reloadConfiguredMCPServers(ctx)
}

// SetPreferredSessionID pins the identifier used for the next session/new invocation (typically from --session-id).
func (m *Manager) SetPreferredSessionID(id string) {
	m.preferredNewSessionID = strings.TrimSpace(id)
}

// SetServer injects the update sender (used when server and manager are constructed together).
func (m *Manager) SetServer(server acp.UpdateSender) {
	m.server = server
}

func (m *Manager) makePersist(st *State) func() {
	return func() {
		if m.store == nil || st == nil || strings.TrimSpace(st.SessionDir) == "" {
			return
		}
		if err := m.store.Save(st); err != nil {
			m.log.Warn("persist session", "id", st.ID, "error", err)
		}
	}
}

func (m *Manager) sessionResultModes(st *State) *acp.ModeState {
	return &acp.ModeState{
		CurrentModeID: string(st.Mode),
		AvailableModes: []acp.SessionMode{
			{ID: "agent", Name: "Agent", Description: "Execute tasks with full tool access"},
			{ID: "plan", Name: "Plan", Description: "Plan and design without code execution"},
		},
	}
}

// ---- acp.Handler implementation ----

func (m *Manager) HandleInitialize(_ context.Context, params acp.InitializeParams) (*acp.InitializeResult, error) {
	m.log.Info("initialize", "client", params.ClientInfo, "protocolVersion", params.ProtocolVersion, "agentVersion", version.Get())

	caps := acp.AgentCapabilities{
		LoadSession: m.store != nil,
		PromptCapabilities: &acp.PromptCapabilities{
			EmbeddedContext: true,
		},
		MCPCapabilities: &acp.MCPCapabilities{
			HTTP: true,
			SSE:  true,
		},
	}
	if m.store != nil {
		caps.SessionCapabilities = &acp.SessionCaps{}
	}

	return &acp.InitializeResult{
		ProtocolVersion:   acp.ProtocolVersion,
		AgentCapabilities: caps,
		AgentInfo: acp.ImplementationInfo{
			Name:    acp.AgentName,
			Title:   acp.AgentTitle,
			Version: version.Get(),
		},
		AuthMethods: []string{},
	}, nil
}

func (m *Manager) HandleSessionNew(ctx context.Context, params acp.SessionNewParams) (*acp.SessionNewResult, error) {
	preferredConsumed := ""
	if strings.TrimSpace(m.preferredNewSessionID) != "" {
		preferredConsumed = strings.TrimSpace(m.preferredNewSessionID)
		m.preferredNewSessionID = ""
	}

	var id string
	if preferredConsumed != "" {
		if err := ValidateFolderSessionID(preferredConsumed); err != nil {
			return nil, fmt.Errorf("session/new: %w", err)
		}
		id = preferredConsumed
	} else {
		id = newSessionID()
	}

	m.mu.RLock()
	_, occupied := m.sessions[id]
	m.mu.RUnlock()
	if occupied {
		return nil, fmt.Errorf("session/new: session id already active: %s", id)
	}

	// CLI --session-id with an existing snapshot is treated as reopening disk state.
	if m.store != nil && preferredConsumed != "" {
		if _, err := m.store.ReadSnapshot(id); err == nil {
			loadResult, err := m.loadSessionFromDisk(ctx, acp.SessionLoadParams{
				SessionID:  id,
				CWD:        params.CWD,
				MCPServers: params.MCPServers,
			}, true)
			if err != nil {
				return nil, fmt.Errorf("session/new: reopen persisted session %s: %w", id, err)
			}
			_ = loadResult
			st := m.getSession(id)
			return &acp.SessionNewResult{
				SessionID:     id,
				ConfigOptions: BuildACPConfigOptions(m.activeCfg(), st),
				Modes:         m.sessionResultModes(st),
			}, nil
		}
	}

	cwd, err := EffectiveSessionCWD(params.CWD, m.defaultCWD)
	if err != nil {
		return nil, fmt.Errorf("session/new: %w", err)
	}

	var sessionDir string
	if m.store != nil {
		sessionDir, err = m.store.EnsureLayout(id)
		if err != nil {
			return nil, fmt.Errorf("session/new: layout: %w", err)
		}
	}

	state, err := m.buildFreshState(ctx, id, cwd, sessionDir, params.MCPServers)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.sessions[id] = state
	m.mu.Unlock()

	if m.store != nil {
		if err := m.store.Save(state); err != nil {
			m.log.Warn("initial session save", "error", err)
		}
	}

	m.log.Info("session created", "id", id, "cwd", cwd, "mode", state.Mode)

	return &acp.SessionNewResult{
		SessionID:     id,
		ConfigOptions: BuildACPConfigOptions(m.activeCfg(), state),
		Modes:         m.sessionResultModes(state),
	}, nil
}

func (m *Manager) buildFreshState(ctx context.Context, id, cwd, sessionDir string, mcpServers []acp.MCPServer) (*State, error) {
	loadedSkills, err := m.skillsLoad.LoadAll(cwd, m.activeCfg().Paths.Home, m.activeCfg().Skills.ManagedDir(m.activeCfg().Paths.Home))
	if err != nil {
		m.log.Warn("failed to load skills", "error", err)
	}

	state := &State{
		ID:         id,
		CWD:        cwd,
		Mode:       ModeAgent,
		Skills:     loadedSkills,
		SessionDir: sessionDir,
	}
	state.ReplaceRulesCatalog(DiscoverRules(m.activeCfg(), cwd))

	state.SetPersistHook(m.makePersist(state))

	m.connectConfiguredMCPServers(ctx, state)

	for _, srv := range mcpServers {
		cfgSrv := acpMCPServerToConfig(srv)
		client, err := m.connectMCPServer(ctx, state, cfgSrv)
		if err != nil {
			m.log.Warn("failed to connect client MCP server", "server", srv.Name, "error", err)
			continue
		}
		state.AddSessionMCPClient(client)
	}

	return state, nil
}

// loadSessionFromDisk restores a persisted bundle. deferPublish parks the
// replayed transcript (and the plan and context usage that go with it) on the
// state instead of writing it immediately: session/new reopening a bundle must
// not emit updates for a session id the client only learns from the response it
// has not received yet. HandleSessionReady publishes them afterwards.
func (m *Manager) loadSessionFromDisk(ctx context.Context, params acp.SessionLoadParams, deferPublish bool) (*acp.SessionLoadResult, error) {
	if m.store == nil {
		return nil, fmt.Errorf("session/load: persistence is disabled")
	}
	if err := ValidateFolderSessionID(params.SessionID); err != nil {
		return nil, fmt.Errorf("session/load: %w", err)
	}

	snap, err := m.store.ReadSnapshot(params.SessionID)
	if err != nil {
		return nil, err
	}

	fallback := snap.Meta.CWD
	if strings.TrimSpace(fallback) == "" {
		fallback = m.defaultCWD
	}

	cwd, err := EffectiveSessionCWD(params.CWD, fallback)
	if err != nil {
		return nil, fmt.Errorf("session/load cwd: %w", err)
	}

	m.mu.Lock()
	if prev, ok := m.sessions[params.SessionID]; ok {
		prev.CloseAll()
		delete(m.sessions, params.SessionID)
	}
	m.mu.Unlock()

	st := &State{
		ID:         params.SessionID,
		CWD:        cwd,
		SessionDir: snap.Dir,
	}

	mode := Mode(snap.Meta.Mode)
	if mode != ModeAgent && mode != ModePlan {
		mode = ModeAgent
	}
	st.RestoreMetaWithoutPersist(mode, snap.Meta.SelectedModelID, snap.Meta.SelectedReasoning, snap.Meta.AgentMemory, snap.Meta.PermissionMode)
	st.SetTitlePinnedWithoutPersist(snap.Meta.TitlePinned)
	st.ReplaceMessagesWithoutPersist(snap.Messages)
	st.SetPlanWithoutPersist(snap.Plan)
	st.RestorePermissionGrantsWithoutPersist(snap.PermissionCommands, snap.PermissionWriteKeys)
	st.RestoreUILogWithoutPersist(snap.UILog)
	st.RestoreActivityFromSnapshot(snap.Meta.ActivitySeq, snap.Meta.ReadActivitySeq)
	restoreContextBreakdown(st)

	loadedSkills, err := m.skillsLoad.LoadAll(cwd, m.activeCfg().Paths.Home, m.activeCfg().Skills.ManagedDir(m.activeCfg().Paths.Home))
	if err != nil {
		m.log.Warn("failed to load skills on session load", "error", err)
	}
	st.ReplaceSkills(loadedSkills)
	st.ReplaceRulesCatalog(DiscoverRules(m.activeCfg(), cwd))

	st.SetPersistHook(m.makePersist(st))

	m.connectConfiguredMCPServers(ctx, st)

	for _, srv := range params.MCPServers {
		cfgSrv := acpMCPServerToConfig(srv)
		client, err := m.connectMCPServer(ctx, st, cfgSrv)
		if err != nil {
			m.log.Warn("failed to connect client MCP server", "server", srv.Name, "error", err)
			continue
		}
		st.AddSessionMCPClient(client)
	}

	m.mu.Lock()
	m.sessions[params.SessionID] = st
	m.mu.Unlock()

	publish := func() {
		m.sendContextUsageUpdate(params.SessionID, st)

		if err := m.replayConversation(params.SessionID, snap.Messages, snap.Dir); err != nil {
			m.log.Warn("replay conversation", "error", err)
		}

		if len(st.GetPlan()) > 0 && m.server != nil {
			_ = m.server.SendSessionUpdate(params.SessionID, acp.PlanUpdate{
				SessionUpdate: acp.UpdateTypePlan,
				Entries:       st.GetPlan(),
			})
		}
	}
	if deferPublish {
		st.setPendingReadyNotify(publish)
	} else {
		publish()
	}

	m.log.Info("session loaded", "id", params.SessionID, "cwd", cwd)

	return &acp.SessionLoadResult{
		Modes:         m.sessionResultModes(st),
		ConfigOptions: BuildACPConfigOptions(m.activeCfg(), st),
	}, nil
}

// HandleSessionLoad restores a session the client named itself, so the replayed
// history is written before the response, as ACP requires.
func (m *Manager) HandleSessionLoad(ctx context.Context, params acp.SessionLoadParams) (*acp.SessionLoadResult, error) {
	return m.loadSessionFromDisk(ctx, params, false)
}

// EnsureHTTPSession returns an in-memory session for an already-valid folder id:
// reuse active session, load from disk if a snapshot exists, or create an empty persisted bundle using the pinned id.
func (m *Manager) EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*State, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("empty session id")
	}
	if err := ValidateFolderSessionID(sessionID); err != nil {
		return nil, err
	}
	if existing := m.getSession(sessionID); existing != nil {
		return existing, nil
	}
	if m.store != nil && m.store.HasPersistedSnapshot(sessionID) {
		if _, err := m.HandleSessionLoad(ctx, acp.SessionLoadParams{
			SessionID: sessionID,
			CWD:       defaultCWD,
		}); err != nil {
			return nil, err
		}
		st := m.getSession(sessionID)
		if st == nil {
			return nil, fmt.Errorf("session load incomplete: %s", sessionID)
		}
		return st, nil
	}
	m.SetPreferredSessionID(sessionID)
	res, err := m.HandleSessionNew(ctx, acp.SessionNewParams{CWD: defaultCWD})
	if err != nil {
		return nil, err
	}
	if res.SessionID != sessionID {
		return nil, fmt.Errorf("session id mismatch creating %s vs %s", sessionID, res.SessionID)
	}
	st := m.getSession(sessionID)
	if st == nil {
		return nil, fmt.Errorf("internal session missing after new: %s", sessionID)
	}
	return st, nil
}

// ForgetLiveSession disconnects MCP clients for the id and removes it from the active map (does not touch disk).
func (m *Manager) ForgetLiveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.sessions[sessionID]; ok {
		st.CloseAll()
		delete(m.sessions, sessionID)
	}
}

// FileStore returns the persistence backend or nil when the manager runs without disk (tests only).
func (m *Manager) FileStore() *FileStore {
	return m.store
}

func (m *Manager) HandleSessionList(_ context.Context, params acp.SessionListParams) (*acp.SessionListResult, error) {
	if m.store == nil || m.store.Root == "" {
		return &acp.SessionListResult{Sessions: []acp.SessionListInfo{}}, nil
	}
	cwdFilter := ""
	if params.CWD != nil {
		cwdFilter = strings.TrimSpace(*params.CWD)
	}
	rows, err := m.store.ListSnapshots(cwdFilter, false)
	if err != nil {
		return nil, fmt.Errorf("session/list: %w", err)
	}

	out := make([]acp.SessionListInfo, 0, len(rows))
	for _, r := range rows {
		ent := acp.SessionListInfo{
			SessionID: r.SessionID,
			CWD:       r.CWD,
		}
		if strings.TrimSpace(r.Title) != "" {
			t := r.Title
			ent.Title = &t
		}
		if strings.TrimSpace(r.UpdatedAt) != "" {
			u := r.UpdatedAt
			ent.UpdatedAt = &u
		}
		out = append(out, ent)
	}

	return &acp.SessionListResult{Sessions: out}, nil
}

func (m *Manager) HandleSessionPrompt(ctx context.Context, params acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	return m.HandleSessionPromptWithSender(ctx, params, m.server, nil)
}

// PromptRunOpts configures HandleSessionPromptWithSender for HTTP paths that acquire the
// turn lock themselves - streaming ones before committing SSE headers, non-streaming ones
// before opening a relay for watchers.
type PromptRunOpts struct {
	// SkipTurnLock when true means the caller already holds the composer turn lock (e.g. coddy http SSE).
	SkipTurnLock bool
	// DetachFromRequest when true runs the turn on a context.WithoutCancel copy of ctx, so a
	// client that drops the HTTP connection mid-turn does not kill it. A streaming composer
	// POST sets this because its readers may come and go; a non-streaming caller keeps
	// request-scoped cancellation, since hanging up is the only way it can stop a turn.
	DetachFromRequest bool
}

// AcquireComposerTurnLock acquires the exclusive per-session turn lock used by agent turns.
func (m *Manager) AcquireComposerTurnLock(sessionID string, st *State) (unlock func(), err error) {
	return m.acquireTurnLockWithReloadDrain(sessionID, st)
}

// WriteCrossProcessCancelRequest writes the on-disk cancel signal for a persisted session bundle.
func (m *Manager) WriteCrossProcessCancelRequest(sessionID string) error {
	fs := m.FileStore()
	if fs == nil || !fs.HasPersistedSnapshot(sessionID) {
		return nil
	}
	return WriteCancelRequest(fs.SessionPath(sessionID))
}

// HandleSessionPromptWithSender runs a prompt turn using sender for agent updates (e.g. SSE over HTTP).
func (m *Manager) HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *PromptRunOpts) (*acp.SessionPromptResult, error) {
	if sender == nil {
		sender = m.server
	}
	state := m.getSession(params.SessionID)
	if state == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}

	// Before the lock, not after: a turn queued behind another one is already active as
	// far as a client watching the session is concerned.
	clearActive := m.markTurnActive(params.SessionID)
	defer clearActive()

	var unlock func()
	var err error
	if opts != nil && opts.SkipTurnLock {
		unlock = func() {}
	} else {
		unlock, err = m.acquireTurnLockWithReloadDrain(params.SessionID, state)
		if err != nil {
			return nil, err
		}
	}
	defer unlock()

	turnBase := ctx
	if opts != nil && opts.DetachFromRequest {
		turnBase = context.WithoutCancel(ctx)
	}
	turnCtx, cancel := context.WithCancel(turnBase)
	state.SetCancel(cancel)
	defer cancel()

	sessionDir := strings.TrimSpace(state.GetPersistedSessionDir())
	if sessionDir != "" {
		_ = ClearCancelRequest(sessionDir)
		go m.runCrossProcessCancelPoll(turnCtx, state, sessionDir)
	}

	if slug := RunPlanSlugFromPromptMeta(params.Meta); slug != "" {
		return m.RunPlan(turnCtx, params.SessionID, slug, sender)
	}

	if len(params.ImageParts) > 0 {
		parts := make([]llm.ImagePart, len(params.ImageParts))
		for i, p := range params.ImageParts {
			parts[i] = llm.ImagePart{DataURL: p.DataURL, Name: p.Name}
		}
		if err := SavePartsToAssets(parts, sessionDir); err != nil {
			m.log.Warn("save uploaded files to assets", "error", err)
		}
		state.SetPendingImageParts(parts)
	}

	cwdAbs, err := filepath.Abs(state.GetCWD())
	if err != nil {
		return nil, fmt.Errorf("session cwd: %w", err)
	}
	hydrated, err := HydratePromptContentBlocks(cwdAbs, params.Prompt)
	if err != nil {
		return nil, err
	}
	if sd := strings.TrimSpace(state.GetPersistedSessionDir()); sd != "" {
		hydrated, err = HydrateSessionPlanMentions(sd, hydrated)
		if err != nil {
			return nil, err
		}
		if mentionSlug := ExtractRunPlanSlugFromPromptText(contentBlocksToPlainText(hydrated)); mentionSlug != "" {
			return m.RunPlan(turnCtx, params.SessionID, mentionSlug, sender)
		}
	}

	var ranRunner bool
	defer func() {
		if ranRunner {
			state.BumpActivitySeq()
		}
	}()

	ranRunner = true
	stopReason, err := m.runner(turnCtx, state, hydrated, sender)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			state.AppendUILogError(CountUserTurns(state.GetMessages()), err.Error())
		}
		return nil, err
	}

	return &acp.SessionPromptResult{StopReason: acp.StopReason(stopReason)}, nil
}

func (m *Manager) HandleSessionSetMode(_ context.Context, params acp.SessionSetModeParams) error {
	state := m.getSession(params.SessionID)
	if state == nil {
		return fmt.Errorf("session not found: %s", params.SessionID)
	}

	if params.ModeID != string(ModeAgent) && params.ModeID != string(ModePlan) {
		return fmt.Errorf("unknown mode: %s", params.ModeID)
	}

	state.SetMode(params.ModeID)

	if err := m.server.SendSessionUpdate(params.SessionID, acp.ModeUpdate{
		SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
		CurrentModeID: params.ModeID,
	}); err != nil {
		m.log.Warn("failed to send mode update", "error", err)
	}

	m.sendConfigOptionUpdate(params.SessionID, state)

	m.log.Info("mode changed", "session", params.SessionID, "mode", params.ModeID)
	return nil
}

// HandleSessionSetConfigOption implements session/set_config_option (ACP Session Config Options).
func (m *Manager) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	state := m.getSession(params.SessionID)
	if state == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}

	switch params.ConfigID {
	case "mode":
		if params.Value != string(ModeAgent) && params.Value != string(ModePlan) {
			return nil, fmt.Errorf("invalid mode value: %q", params.Value)
		}
		state.SetMode(params.Value)
		if err := m.server.SendSessionUpdate(params.SessionID, acp.ModeUpdate{
			SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
			CurrentModeID: params.Value,
		}); err != nil {
			m.log.Warn("failed to send mode update", "error", err)
		}
	case "model":
		if len(m.activeCfg().Models) == 0 {
			return nil, fmt.Errorf("no models configured")
		}
		found := false
		for i := range m.activeCfg().Models {
			if m.activeCfg().Models[i].Model == params.Value {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown model value: %q", params.Value)
		}
		state.SetSelectedModelID(params.Value)
	case "permission_mode":
		switch params.Value {
		case config.PermModeAsk, config.PermModeAcceptEdits, config.PermModeBypass:
		default:
			return nil, fmt.Errorf("invalid permission_mode value: %q", params.Value)
		}
		state.SetPermissionMode(params.Value)
	default:
		return nil, fmt.Errorf("unknown config option: %q", params.ConfigID)
	}

	opts := BuildACPConfigOptions(m.activeCfg(), state)
	m.sendConfigOptionUpdate(params.SessionID, state)

	return &acp.SessionSetConfigOptionResult{ConfigOptions: opts}, nil
}

func (m *Manager) sendConfigOptionUpdate(sessionID string, state *State) {
	opts := BuildACPConfigOptions(m.activeCfg(), state)
	if err := m.server.SendSessionUpdate(sessionID, acp.ConfigOptionUpdate{
		SessionUpdate: acp.UpdateTypeConfigOptionUpdate,
		ConfigOptions: opts,
	}); err != nil {
		m.log.Warn("failed to send config option update", "error", err)
	}
}

func (m *Manager) HandleSessionCancel(params acp.SessionCancelParams) {
	_ = m.WriteCrossProcessCancelRequest(params.SessionID)
	state := m.getSession(params.SessionID)
	if state != nil {
		state.SetUserCancelledTurn()
		state.Cancel()
	}
	m.log.Info("session cancelled", "id", params.SessionID)
}

// ---- helpers ----

func (m *Manager) getSession(id string) *State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// SessionByID returns in-memory session state or nil.
func (m *Manager) SessionByID(id string) *State {
	return m.getSession(id)
}

// ToolCallResult returns the persisted full output of one tool call in this
// session ("", false when the session or the artifact is unavailable).
func (m *Manager) ToolCallResult(sessionID, toolCallID string) (string, bool) {
	st := m.getSession(sessionID)
	if st == nil {
		return "", false
	}
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return "", false
	}
	full, err := ReadToolCallResult(dir, toolCallID)
	if err != nil || full == "" {
		return "", false
	}
	return full, true
}

// HandleSessionReady publishes notifications that require the ACP client to
// have registered the session after receiving session/new or session/load.
func (m *Manager) HandleSessionReady(sessionID string) {
	st := m.getSession(sessionID)
	if st != nil {
		if publish := st.takePendingReadyNotify(); publish != nil {
			publish()
		}
	}
	m.sendAvailableSlashCommands(sessionID, st)
}

func (m *Manager) sendAvailableSlashCommands(sessionID string, st *State) {
	if m.server == nil || st == nil {
		return
	}
	sums := skills.ListSkills(st.GetSkills())
	builtins := skills.BuiltinCommands(m.activeCfg().Compaction.IsEnabled())
	cmds := make([]acp.AvailableCommand, 0, len(sums)+len(builtins))
	for _, b := range builtins {
		cmds = append(cmds, acp.AvailableCommand{Name: b.Name, Description: b.Description})
	}
	for _, s := range sums {
		cmds = append(cmds, acp.AvailableCommand{Name: s.Name, Description: s.Description})
	}
	_ = m.server.SendSessionUpdate(sessionID, acp.AvailableCommandsUpdate{
		SessionUpdate:     acp.UpdateTypeAvailableCommandsUpdate,
		AvailableCommands: cmds,
	})
}

// EffectiveMCPServers merges config.yaml servers with the global
// <home>/mcp.json and the project-local <cwd>/.coddy/mcp.json (later files
// override earlier ones by name). A broken mcp.json is logged and skipped so
// the session still starts.
func EffectiveMCPServers(cfg *config.Config, cwd string, log *slog.Logger) []config.MCPServerConfig {
	managed := mcp.ListManagedServersTolerant(cfg, cwd, log)
	out := make([]config.MCPServerConfig, 0, len(managed))
	for _, srv := range managed {
		out = append(out, srv.Config)
	}
	return out
}

// connectConfiguredMCPServers connects every enabled configured server
// (config.yaml merged with the two mcp.json levels) that the workspace trust
// gate admits, and installs the per-turn tool filter factory so disable
// toggles reach live sessions.
//
// The gate is what keeps a project-local .coddy/mcp.json from turning session
// creation into arbitrary process execution: entries the checkout brought
// with it stay cold until the operator approves that exact declaration.
func (m *Manager) connectConfiguredMCPServers(ctx context.Context, state *State) {
	cwd := state.GetCWD()
	for _, client := range m.dialConfiguredMCPServers(ctx, cwd) {
		state.addConfiguredMCPClient(client)
	}
	state.MCPFilterFactory = func() func(server, tool string) bool {
		return config.BuildMCPToolFilter(EffectiveMCPServers(m.activeCfg(), cwd, m.log))
	}
}

// dialConfiguredMCPServers connects every enabled configured server the trust
// gate admits for cwd and returns the clients without attaching them to a
// session. Both session creation and the settings hot reload go through here,
// so neither can reach a spawn without TrustGate.Connect: a project-local
// .coddy/mcp.json stays cold until its exact declaration is approved.
func (m *Manager) dialConfiguredMCPServers(ctx context.Context, cwd string) []*mcp.Client {
	cfg := m.activeCfg()
	gate := mcp.NewTrustGate(cfg)
	managed := mcp.ListManagedServersTolerant(cfg, cwd, m.log)
	clients := make([]*mcp.Client, 0, len(managed))
	for _, srv := range managed {
		if srv.Config.Disabled {
			continue
		}
		client, err := gate.Connect(ctx, srv, cwd, m.log)
		if err != nil {
			var blocked *mcp.BlockedError
			if errors.As(err, &blocked) {
				m.log.Warn("MCP server not started: project declaration is not approved for this workspace",
					"server", srv.Config.Name, "workspace", cwd, "state", string(blocked.State),
					"digest", blocked.Digest, "approve_with", "coddy mcp trust "+srv.Config.Name)
				continue
			}
			m.log.Warn("failed to connect MCP server", "server", srv.Config.Name, "error", err)
			continue
		}
		clients = append(clients, client)
		m.log.Info("connected MCP server", "name", srv.Config.Name,
			"transport", mcp.EffectiveTransport(srv.Config), "tools", len(client.Tools()))
	}
	return clients
}

// reloadConfiguredMCPServers reconnects the configured MCP servers of every
// active session after the settings changed, leaving ACP client-supplied
// per-session servers untouched. The reload is a fresh trust evaluation, not a
// replay of what the session started with, so a declaration whose approval has
// since been withdrawn does not come back.
//
// A session with a turn in flight is not touched here: swapping its configured
// clients would strand the tool definitions that turn already handed the model,
// so the MCP call it is running would resolve to a server that no longer exists.
// The reload is parked on the state and drained the moment the turn releases its
// lock (see drainPendingMCPReload). The flag is marked before the lock probe so
// a turn releasing concurrently either observes it in its own drain or leaves
// the lock free for us to take here.
func (m *Manager) reloadConfiguredMCPServers(ctx context.Context) {
	m.mu.RLock()
	states := make([]*State, 0, len(m.sessions))
	for _, state := range m.sessions {
		states = append(states, state)
	}
	m.mu.RUnlock()

	for _, state := range states {
		state.markMCPReloadPending()
		unlock, err := m.acquirePromptTurnLock(state.GetID(), state)
		if err != nil {
			// A turn holds the lock; its release drains the parked reload.
			continue
		}
		applied := true
		if state.takeMCPReloadPending() {
			applied = m.applyConfiguredMCPReload(ctx, state)
		}
		unlock()
		// A save that arrived while this one was dialing parked its reload
		// behind our lock. Draining here applies the newest configuration to an
		// idle session instead of leaving it on the superseded one until its
		// next turn. Skipped once the deadline is gone: the drain would dial on
		// a fresh budget and stretch this save well past the timeout that is
		// supposed to bound it.
		if applied {
			m.drainPendingMCPReload(state.GetID(), state)
		}
	}
}

// applyConfiguredMCPReload dials the configured servers for the session and
// installs them, replacing whatever the previous reload left. A dial the
// context cut short is discarded instead: an empty or partial result then says
// nothing about the operator's configuration, and installing it would strip a
// healthy session of its MCP tools. Sessions share one deadline per settings
// save, so this is what a server hanging in the session dialed first costs the
// rest. It reports whether the swap happened; a discarded dial leaves the
// reload parked for a later turn to retry.
func (m *Manager) applyConfiguredMCPReload(ctx context.Context, st *State) bool {
	clients := m.dialConfiguredMCPServers(ctx, st.GetCWD())
	if err := ctx.Err(); err != nil {
		for _, client := range clients {
			_ = client.Close()
		}
		st.markMCPReloadPending()
		m.log.Warn("configured MCP reload ran out of time; keeping the current servers",
			"session", st.GetID(), "error", err)
		return false
	}
	st.replaceConfiguredMCPClients(clients)
	return true
}

// drainPendingMCPReload applies a reload parked by reloadConfiguredMCPServers,
// if one is waiting and the session turn lock is free. It runs right after a
// turn releases the lock, so the configured clients are swapped between turns
// rather than under an in-flight MCP tool call. If a newer turn has already
// taken the lock, this returns and that turn's release drains the flag instead.
func (m *Manager) drainPendingMCPReload(sessionID string, st *State) {
	if st == nil || !st.hasPendingMCPReload() {
		return
	}
	unlock, err := m.acquirePromptTurnLock(sessionID, st)
	if err != nil {
		return
	}
	defer unlock()
	if !st.takeMCPReloadPending() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpReloadTimeout)
	defer cancel()
	_ = m.applyConfiguredMCPReload(ctx, st)
}

// acquireTurnLockWithReloadDrain wraps the raw turn lock so its release also
// applies any configured-MCP reload parked while the turn was running. Both
// turn entry points use it, so neither the ACP nor the HTTP composer path can
// finish a turn without draining a pending reload.
func (m *Manager) acquireTurnLockWithReloadDrain(sessionID string, st *State) (func(), error) {
	unlock, err := m.acquirePromptTurnLock(sessionID, st)
	if err != nil {
		return nil, err
	}
	return func() {
		unlock()
		m.drainPendingMCPReload(sessionID, st)
	}, nil
}

// acpMCPServerToConfig converts an ACP client-supplied MCP server definition
// to the config shape used by the connector (all transports, incl. headers).
func acpMCPServerToConfig(srv acp.MCPServer) config.MCPServerConfig {
	out := config.MCPServerConfig{
		Type:    srv.Type,
		Name:    srv.Name,
		Command: srv.Command,
		Args:    srv.Args,
		URL:     srv.URL,
	}
	for _, e := range srv.Env {
		out.Env = append(out.Env, config.EnvVarConfig{Name: e.Name, Value: e.Value})
	}
	for _, h := range srv.Headers {
		out.Headers = append(out.Headers, config.HTTPHeaderConfig{Name: h.Name, Value: h.Value})
	}
	return out
}

// connectMCPServer opens an ACP client-supplied server. This is the only
// ungated connect: the declaration came from the editor over the wire, not from
// a file the checkout carried. Configured servers must go through
// dialConfiguredMCPServers so TrustGate.Connect sees them.
func (m *Manager) connectMCPServer(ctx context.Context, state *State, srv config.MCPServerConfig) (*mcp.Client, error) {
	client, err := mcp.Connect(ctx, srv, state.GetCWD(), m.log)
	if err != nil {
		return nil, err
	}

	m.log.Info("connected MCP server", "name", srv.Name, "transport", srv.Type, "tools", len(client.Tools()))
	return client, nil
}

func newSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate session ID: " + err.Error())
	}
	return "sess_" + hex.EncodeToString(b)
}
