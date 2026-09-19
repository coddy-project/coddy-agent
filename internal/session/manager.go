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
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
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

	// mentionSessions keeps the session listing "@session:" completion reads
	// (mention_search.go).
	mentionSessions mentionSessionCache

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
	// turnStarted is when each of those sessions went from no turn to one. A
	// client that joins a turn late counts its clock from here.
	turnStarted map[string]time.Time
	// turnWakes is the wake of each of those turns that finished background
	// tasks started, for a client that joins it after TurnPhaseWoken went out.
	turnWakes map[string]*llm.BackgroundWake

	// turnObservers receive the started/ended edges of activeTurns (see turn_events.go).
	turnObserverMu  sync.Mutex
	turnObservers   map[int]func(TurnEvent)
	turnObserverSeq int
	// queueObservers fan every message queue change out to the surfaces that
	// have clients of their own (manager_queue.go).
	queueObserverMu  sync.Mutex
	queueObservers   map[int]func(acp.MessageQueueUpdate)
	queueObserverSeq int
	// settingsObs fans every change of a session's settings out the same way
	// (settings.go).
	settingsObs settingsObservers

	// cfgObservers are told whenever the live configuration is replaced, from
	// whichever path replaced it (see config_observers.go).
	cfgObserverMu  sync.Mutex
	cfgObservers   map[int]func(*config.Config)
	cfgObserverSeq int

	// deleting marks sessions whose bundles are being removed by
	// DeleteSessionTree, so a turn racing the delete is refused instead of
	// recreating the bundle through its persist hook.
	// deleting counts the DeleteSessionTree calls currently covering a
	// session; the mark holds until the last of them finishes.
	deletingMu sync.Mutex
	deleting   map[string]int

	// usage is the provider usage cache and schedule (provider_usage.go).
	usage providerUsageState

	// windows caches the context windows provider listings report
	// (context_window.go).
	windows contextWindowState

	// testHooks pause the manager at points a test needs to observe; every
	// field is nil outside tests (see export_test.go).
	testHooks struct {
		// afterSubagentPublish runs once a child state is in the live map and
		// before its bundle exists.
		afterSubagentPublish func(*State)
		// beforeTurnAdmission runs at the start of beginTurn, after the
		// caller resolved its state and before anything is registered.
		beforeTurnAdmission func(sessionID string)
		// beforeTurnAdmissionRecheck runs after a turn installed its cancel
		// function and before it rechecks admission.
		beforeTurnAdmissionRecheck func(sessionID string)
		// afterTreeScan runs once DeleteSessionTree took its first snapshot
		// of the tree and before it marks anything.
		afterTreeScan func(rootID string)
	}
}

// NewManager creates a session manager. defaultCWD is the fallback filesystem root when the
// ACP client omits cwd; may be empty if every session supplies a non-empty cwd.
// store may be nil to disable persistence.
func NewManager(cfg *config.Config, server acp.UpdateSender, runner AgentRunner, log *slog.Logger, defaultCWD string, store *FileStore) *Manager {
	skillsDirs := append([]string(nil), cfg.Skills.Dirs...)
	m := &Manager{
		server:     server,
		runner:     runner,
		skillsLoad: skills.NewLoader(skillsDirs),
		// Tagged here rather than at every call site, so logger.levels can
		// name "session" whichever entrypoint built the manager.
		log:        logger.Component(log, logger.ComponentSession),
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
	previous := m.storeConfig(next)
	if previous != nil && reflect.DeepEqual(previous.MCPServers, next.MCPServers) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpReloadTimeout)
	defer cancel()
	m.reloadConfiguredMCPServers(ctx)
}

// storeConfig replaces the process configuration and the loader used by new
// sessions. It returns the previous configuration so callers can decide
// whether active MCP clients need reconnecting.
func (m *Manager) storeConfig(next *config.Config) *config.Config {
	previous := m.activeCfg()
	m.skillsLoad = skills.NewLoader(append([]string(nil), next.Skills.Dirs...))
	m.cfgAt.Store(next)
	// The provider rows behind the usage cache may have changed with the
	// configuration: work in flight for the old rows is dropped, the
	// snapshots and their pacing stay, and the fingerprint tells a changed
	// credential apart on the next read.
	m.pauseProviderUsage()
	m.publishConfigReplaced(next)
	return previous
}

// ReloadConfigForSession reloads config.yaml and applies runtime-owned state to
// the current session. Configured MCP clients are replaced; ACP session MCP
// clients are preserved. Individual MCP start failures are returned as warnings.
func (m *Manager) ReloadConfigForSession(ctx context.Context, st *State) ([]string, error) {
	current := m.activeCfg()
	if current == nil {
		return nil, fmt.Errorf("active config is unavailable")
	}
	next, err := config.LoadWithPaths(current.Paths)
	if err != nil {
		return nil, err
	}
	loader := skills.NewLoader(append([]string(nil), next.Skills.Dirs...))
	var warnings []string
	var loadedSkills []*skills.Skill
	if st != nil {
		loadedSkills, err = loader.LoadAll(st.GetCWD(), next.Paths.Home, next.Skills.ManagedDir(next.Paths.Home))
		if err != nil {
			warnings = append(warnings, "load skills: "+err.Error())
			loadedSkills = st.GetSkills()
		}
	}

	var nextGlobal []*mcp.Client
	if st != nil {
		cwd := st.GetCWD()
		gate := mcp.NewTrustGate(next)
		for _, srv := range mcp.ListManagedServersTolerant(next, cwd, m.log) {
			if srv.Config.Disabled {
				continue
			}
			client, connectErr := gate.Connect(ctx, srv, cwd, m.log)
			if connectErr != nil {
				warnings = append(warnings, fmt.Sprintf("connect MCP %s: %v", srv.Config.Name, connectErr))
				continue
			}
			nextGlobal = append(nextGlobal, client)
		}
	}

	previous := m.storeConfig(next)
	if st != nil {
		st.ReplaceSkills(loadedSkills)
		st.ReplaceRulesCatalog(DiscoverRules(next, st.GetCWD()))
		st.MCPFilterFactory = func() func(server, tool string) bool {
			return config.BuildMCPToolFilter(EffectiveMCPServers(m.activeCfg(), st.GetCWD(), m.log))
		}
		if ctx.Err() == nil {
			st.replaceConfiguredMCPClients(nextGlobal)
		} else {
			for _, client := range nextGlobal {
				_ = client.Close()
			}
			st.markMCPReloadPending()
		}
		m.sendAvailableSlashCommands(st.GetID(), st)
	}
	if previous == nil || !reflect.DeepEqual(previous.MCPServers, next.MCPServers) {
		reloadCtx, cancel := context.WithTimeout(context.Background(), mcpReloadTimeout)
		defer cancel()
		m.reloadConfiguredMCPServersExcept(reloadCtx, st)
	}
	return warnings, nil
}

func (m *Manager) loadSkills(cwd string, cfg *config.Config) ([]*skills.Skill, error) {
	return m.skillsLoad.LoadAll(cwd, cfg.Paths.Home, cfg.Skills.ManagedDir(cfg.Paths.Home))
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
			{ID: "ask", Name: "Ask", Description: "Answer questions with read-only research tools"},
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
		id = NewSessionID()
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

	m.runSessionStartHooks(ctx, state, hookSourceStartup)

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
	active := m.activeCfg()
	loadedSkills, err := m.loadSkills(cwd, active)
	if err != nil {
		m.log.Warn("failed to load skills", "error", err)
	}

	state := &State{
		ID:             id,
		CWD:            cwd,
		Mode:           ModeAgent,
		Skills:         loadedSkills,
		SessionDir:     sessionDir,
		contextWindows: m,
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
		state.RememberSessionMCPDeclaration(cfgSrv)
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
		ID:             params.SessionID,
		CWD:            cwd,
		SessionDir:     snap.Dir,
		contextWindows: m,
	}

	mode := Mode(snap.Meta.Mode)
	if !IsValidMode(string(mode)) {
		mode = ModeAgent
	}
	st.RestoreMetaWithoutPersist(mode, snap.Meta.SelectedModelID, snap.Meta.SelectedReasoning, snap.Meta.AgentMemory)
	jobSession := false
	if snap.Meta.IsSubagentRun() {
		// A restored child is a read-only transcript; the meta keeps the guard
		// and the parent link, the role and tool set are not needed any more.
		st.SetSubagentMeta(SubagentMeta{
			Name:            snap.Meta.SubagentName,
			ParentSessionID: snap.Meta.ParentSessionID,
			TaskID:          snap.Meta.SubagentTaskID,
			Depth:           snap.Meta.SubagentDepth,
			Scheduler:       schedulerRunMetaFromSnapshot(snap.Meta),
		})
	} else if snap.Meta.SchedulerRun {
		// The session of a scheduler job: the guard and the job name are all
		// it carries, and nothing below (skills, hooks, MCP) is for it, since
		// no turn ever runs on it.
		st.SetSchedulerJobWithoutPersist(snap.Meta.SchedulerJobID)
		jobSession = true
	}
	st.SetTitlePinnedWithoutPersist(snap.Meta.TitlePinned)
	st.SetTagsWithoutPersist(snap.Meta.Tags)
	st.SetArchivedWithoutPersist(snap.Meta.Archived, snap.Meta.ArchivedAt)
	st.SetOriginWithoutPersist(snap.Meta.Origin)
	st.SetPinnedWithoutPersist(snap.Meta.Pinned, snap.Meta.PinnedAt, snap.Meta.PinnedRank)
	st.RestoreHookContextWithoutPersist(snap.Meta.HookContext)
	st.ReplaceMessagesWithoutPersist(snap.Messages)
	st.SetPlanWithoutPersist(snap.Plan)
	st.RestorePermissionGrantsWithoutPersist(snap.PermissionCommands, snap.PermissionWriteKeys, snap.PermissionHTTPKeys)
	st.RestoreUILogWithoutPersist(snap.UILog)
	st.RestoreActivityFromSnapshot(snap.Meta.ActivitySeq, snap.Meta.ReadActivitySeq)
	restoreContextBreakdown(st)

	st.SetPersistHook(m.makePersist(st))
	if !jobSession {
		active := m.activeCfg()
		loadedSkills, err := m.loadSkills(cwd, active)
		if err != nil {
			m.log.Warn("failed to load skills on session load", "error", err)
		}
		st.ReplaceSkills(loadedSkills)
		st.ReplaceRulesCatalog(DiscoverRules(m.activeCfg(), cwd))

		m.runSessionStartHooks(ctx, st, hookSourceResume)

		m.connectConfiguredMCPServers(ctx, st)

		for _, srv := range params.MCPServers {
			cfgSrv := acpMCPServerToConfig(srv)
			client, err := m.connectMCPServer(ctx, st, cfgSrv)
			if err != nil {
				m.log.Warn("failed to connect client MCP server", "server", srv.Name, "error", err)
				continue
			}
			st.AddSessionMCPClient(client)
			st.RememberSessionMCPDeclaration(cfgSrv)
		}
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
		// No cwd is handed to the load. A stored session belongs to the folder
		// it was started in, and params.CWD outranks that - which is right for
		// an editor saying which checkout a session is for, and wrong here,
		// where the only cwd on offer is the server's own. Rebinding somebody's
		// conversation to another folder because a listing route had to load it
		// is not a thing a caller asked for, and it rewrites the bundle, which
		// moves the session in a listing ordered by when it last changed. The
		// load falls back to defaultCWD by itself for a bundle that recorded
		// none.
		if _, err := m.HandleSessionLoad(ctx, acp.SessionLoadParams{
			SessionID: sessionID,
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
	// SkipTurnLock when true means the caller already holds the composer turn lock (e.g. coddy serve SSE).
	SkipTurnLock bool
	// DetachFromRequest when true runs the turn on a context.WithoutCancel copy of ctx, so a
	// client that drops the HTTP connection mid-turn does not kill it. A streaming composer
	// POST sets this because its readers may come and go; a non-streaming caller keeps
	// request-scoped cancellation, since hanging up is the only way it can stop a turn.
	DetachFromRequest bool

	// SkipUsagePublish turns off the provider usage refresh a finished turn
	// normally triggers (provider_usage.go). Surfaces that cannot show the
	// numbers set it: coddy -p, the messenger gateway, the background wake.
	SkipUsagePublish bool

	// SurfaceSystemPrompt is what the surface running this turn wants the model
	// to know about answering through it: a complete system prompt block,
	// heading included, appended after the template. A messenger gateway
	// describes the syntax its chat renders and the shape an answer should
	// take there; the next integration describes its own.
	//
	// It belongs to the turn, not to the session. Nothing of it is persisted,
	// so the transcript reads the same whoever was answering, and a turn from
	// another surface on the same session carries a different prefix - which
	// costs that turn its cached prefix, deliberately.
	SurfaceSystemPrompt string

	// BackgroundWake says the prompt was not typed by anybody: finished
	// background tasks the model asked to be notified about started this
	// turn, and the prompt is the instruction that reports them. The turn's
	// first message is persisted with the marker, the agent tells the clients
	// before it (acp.BackgroundWakeUpdate), and the turn observers hear a
	// TurnPhaseWoken event once the turn holds the session.
	BackgroundWake *llm.BackgroundWake

	// SettingsTaken says the caller already took the settings commands off
	// the start of the prompt (TakeSettingsCommands) - the HTTP server does,
	// before it takes the turn lock - and TurnSettings are the turn-scoped
	// changes it found, applied once this turn is admitted.
	SettingsTaken bool
	TurnSettings  []SettingsChange

	// subagentTurn marks the one prompt a child session may run: its own task
	// turn, started by the subagent runtime. Every other prompt against a child
	// is refused with ErrSubagentReadOnly (see RunSubagentTurn).
	subagentTurn bool
}

// turnAdmission carries what beginTurn needs to know about the caller.
type turnAdmission struct {
	// skipLock says the caller already holds the composer turn lock.
	skipLock bool
	// publishUsage says the turn's release refreshes the provider usage of the
	// session's model, provided the turn reached its runner (MarkTurnRan).
	publishUsage bool
	// sender is where this turn publishes its session updates. It is held on
	// the state for the life of the turn so a message queue change made from
	// outside the turn's goroutine reaches the clients watching it.
	sender acp.UpdateSender
	// noQueue admits work that is not a prompt (BeginSessionWork): the message
	// queue stays shut, so a follow-up written meanwhile is refused as busy
	// instead of being accepted by a turn that never reads it.
	noQueue bool
}

// admissionFor derives the admission of a prompt from its options: a
// subagent turn and an opted-out caller publish no usage.
func admissionFor(opts *PromptRunOpts) turnAdmission {
	adm := turnAdmission{publishUsage: true}
	if opts != nil {
		adm.skipLock = opts.SkipTurnLock
		adm.publishUsage = !opts.SkipUsagePublish && !opts.subagentTurn
	}
	return adm
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

// beginTurn is the one admission path for anything that runs a turn on a
// session: it registers the turn, takes the turn lock unless the caller holds
// it, installs the turn's cancel on the state and decides admission against a
// concurrent deletion. It returns the context the turn runs on and the release
// that undoes all of it (cancel, unlock, unregister), in that order.
//
// Admission against deletion is decided twice. DeleteSessionTree marks the
// session and then cancels the installed turn; this turn installs its cancel
// and then rechecks the mark. Whichever order the two interleave in, either
// the delete sees this turn's cancel or this recheck sees the mark, so no turn
// runs on past the removal of its bundle.
func (m *Manager) beginTurn(ctx context.Context, sessionID string, state *State, adm turnAdmission) (context.Context, func(), error) {
	if hook := m.testHooks.beforeTurnAdmission; hook != nil {
		hook(sessionID)
	}
	if err := m.admissible(sessionID, state); err != nil {
		return nil, nil, err
	}
	// Before the lock, not after: a turn queued behind another one is already
	// active as far as a client watching the session is concerned.
	clearActive := m.markTurnActive(sessionID)
	// The turn's clock starts where the registry says the session became busy,
	// so the progress the loop reports and the turn_started event agree.
	turnStartedAt, _ := m.TurnStartedAt(sessionID)
	state.BeginTurnProgress(turnStartedAt)
	unlock := func() {}
	if !adm.skipLock {
		var err error
		unlock, err = m.acquireTurnLockWithReloadDrain(sessionID, state)
		if err != nil {
			clearActive()
			if !m.SessionTurnActiveInProcess(sessionID) {
				state.EndTurnProgress(turnStartedAt)
			}
			return nil, nil, err
		}
	}
	// From here the session is running a turn, so a follow-up written while it
	// works has somewhere to go (turn_queue.go), and the clients watching this
	// turn are told when it changes. The notifier is installed before the queue
	// opens, so every change of this turn's queue announces itself from one
	// place - whoever made it, including the turn's own drain. Work that is not
	// a prompt has no step to read a follow-up into and leaves the queue shut.
	if !adm.noQueue {
		state.SetTurnSender(adm.sender)
		state.SetQueueNotifier(func() { m.PublishMessageQueue(sessionID, state) })
		state.OpenMessageQueue()
	}
	// The ran marker lives on this admission's context, so a concurrent
	// admission that loses the lock cannot reset it.
	markedCtx, ran := withTurnRanMarker(ctx)
	turnCtx, cancel := context.WithCancel(markedCtx)
	state.SetCancel(cancel)
	if !adm.noQueue {
		// A follow-up is typed by the operator, like the prompt it follows:
		// its references resolve with the operator's scope, bound to the turn.
		state.SetQueuedMentionResolver(func(blocks []acp.ContentBlock) []acp.ContentBlock {
			return m.ResolvePromptMentions(turnCtx, state, blocks, m.mentionScope(state, false, state.GetMode() == string(ModeAsk)))
		})
	}
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			cancel()
			// Closed before anything else: the turn is over, so a follow-up
			// arriving now belongs to the next prompt, not to this one. A
			// cancelled or failed turn drops what it never got to read, which
			// is what Stop means, and says so rather than losing it quietly.
			// The close announces the empty queue through the notifier, which
			// is why both it and the sender are let go only afterwards.
			if !adm.noQueue {
				if left := state.CloseMessageQueue(); len(left) > 0 {
					m.log.Warn("message queue dropped with the turn",
						"session_id", sessionID, "messages", len(left))
				}
				state.SetQueueNotifier(nil)
				state.SetQueuedMentionResolver(nil)
				state.SetTurnSender(nil)
			}
			// The usage refresh is reserved before the turn is released: a
			// client that pulls the numbers on turn_ended joins that fetch
			// instead of reading the pre-turn snapshot.
			if adm.publishUsage && ran.Load() {
				m.publishProviderUsageAsync(sessionID, state)
			}
			unlock()
			clearActive()
			// Only the outer release ends the turn; an inner one (RunPlan)
			// leaves the session busy and the progress standing.
			if !m.SessionTurnActiveInProcess(sessionID) {
				state.EndTurnProgress(turnStartedAt)
			}
		})
	}
	if hook := m.testHooks.beforeTurnAdmissionRecheck; hook != nil {
		hook(sessionID)
	}
	if err := m.admissible(sessionID, state); err != nil {
		finish()
		return nil, nil, err
	}
	// The window this turn's compaction trigger and usage_update measure
	// against: a model without max_context_tokens reads its provider's model
	// listing, fetched here the first time (bounded) so the first turn of a
	// session already measures what GET /v1/models reports to the web UI.
	if cfg := m.activeCfg(); cfg != nil {
		m.AwaitContextWindows(turnCtx, cfg, []string{state.EffectiveModelID(cfg)}, ContextWindowWait)
	}
	return turnCtx, finish, nil
}

// admissible decides, under the live-map lock, whether a turn may run on
// state: the state must still be the session's live entry (a caller that
// resolved it before a delete or a forget completed holds a stale one whose
// persist hook would recreate the bundle) and no deletion may be covering
// the session.
func (m *Manager) admissible(sessionID string, state *State) error {
	m.mu.RLock()
	live := m.sessions[sessionID] == state
	deleting := m.isDeleting(sessionID)
	m.mu.RUnlock()
	if !live {
		return fmt.Errorf("%w: %s", ErrSessionGone, sessionID)
	}
	if deleting {
		return fmt.Errorf("%w: %s", ErrSessionDeleting, sessionID)
	}
	return nil
}

// BeginTurn admits a turn that a caller drives itself instead of going through
// HandleSessionPromptWithSender (the HTTP permission resume runs the ReAct
// loop directly). It applies the same rules: child sessions are read-only, a
// session being deleted refuses, the turn is registered, locked (unless
// opts.SkipTurnLock) and cancellable through State.Cancel. The caller runs on
// the returned context and calls finish when the turn is over.
func (m *Manager) BeginTurn(ctx context.Context, sessionID string, opts *PromptRunOpts) (context.Context, func(), error) {
	state := m.getSession(sessionID)
	if state == nil {
		return nil, nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err := readOnlyRefusal(state, sessionID); err != nil {
		return nil, nil, err
	}
	turnCtx, finish, err := m.beginTurn(ctx, sessionID, state, admissionFor(opts))
	if err != nil {
		return nil, nil, err
	}
	// The resume continues the turn that stopped on the prompt: it runs with
	// the settings that turn held and consumes nothing.
	state.ResumeTurnSettings()
	return turnCtx, func() {
		state.EndTurnSettings()
		finish()
	}, nil
}

// BeginSessionWork admits work that holds a session the way a turn does
// without being a prompt - a compaction asked for over REST. It takes the turn
// lock, installs the cancel a deletion uses and marks the session active, so
// every client watching the turn edges (GET /coddy/events) is told the work
// started and ended and reloads what it changed: an idle browser tab reads the
// smaller context stats instead of keeping the old ones. It opens no message
// queue; a follow-up written meanwhile is refused as busy, like a second turn.
// The caller runs on the returned context and calls finish when done.
func (m *Manager) BeginSessionWork(ctx context.Context, sessionID string) (context.Context, func(), error) {
	state := m.getSession(sessionID)
	if state == nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrSessionGone, sessionID)
	}
	if err := readOnlyRefusal(state, sessionID); err != nil {
		return nil, nil, err
	}
	return m.beginTurn(ctx, sessionID, state, turnAdmission{publishUsage: true, noQueue: true})
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
	if opts == nil || !opts.subagentTurn {
		if err := readOnlyRefusal(state, params.SessionID); err != nil {
			return nil, err
		}
	}
	// A turn an operator started: not a child's task, not a wake's report.
	// Only such a turn reads settings commands and consumes the settings
	// armed for the next turns (settings.go).
	operatorTurn := opts == nil || (!opts.subagentTurn && opts.BackgroundWake == nil)
	var turnSettings []SettingsChange
	if operatorTurn {
		if opts != nil && opts.SettingsTaken {
			turnSettings = opts.TurnSettings
		} else {
			taken, err := m.TakeSettingsCommands(ctx, params.SessionID, params.Prompt, "command")
			if err != nil {
				return nil, err
			}
			if taken.Handled {
				AnnounceSettingsNotice(sender, params.SessionID, taken.Notice)
				return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn, SettingsNotice: taken.Notice}, nil
			}
			params.Prompt = taken.Prompt
			turnSettings = taken.TurnChanges
		}
	}

	turnBase := ctx
	if opts != nil && opts.DetachFromRequest {
		turnBase = context.WithoutCancel(ctx)
	}
	adm := admissionFor(opts)
	adm.sender = sender
	turnCtx, finish, err := m.beginTurn(turnBase, params.SessionID, state, adm)
	if err != nil {
		return nil, err
	}
	defer finish()

	if operatorTurn {
		// Installed under the turn lock, so the turn this prompt starts is the
		// one that takes them; then this turn consumes one of every override.
		for _, ch := range turnSettings {
			if _, err := m.ApplySessionSettings(turnCtx, params.SessionID, ch); err != nil {
				return nil, err
			}
		}
		if m.beginOperatorTurnSettings(params.SessionID, state) {
			defer m.endTurnSettings(params.SessionID, state)
		} else {
			defer state.EndTurnSettings()
		}
	}

	// What the surface wants the model to know lasts exactly this turn: set
	// under the turn lock, cleared before it is released, never persisted.
	if opts != nil && strings.TrimSpace(opts.SurfaceSystemPrompt) != "" {
		state.SetSurfaceSystemPrompt(opts.SurfaceSystemPrompt)
		defer state.SetSurfaceSystemPrompt("")
	}
	// A turn no person started says so, the same way: held for this turn
	// only, taken by the agent for the first message, and announced to the
	// observers once the turn holds the session.
	if opts != nil && opts.BackgroundWake != nil {
		state.SetTurnWake(opts.BackgroundWake)
		defer state.SetTurnWake(nil)
		defer m.holdTurnWake(params.SessionID, opts.BackgroundWake)()
		m.publishWokenTurn(params.SessionID, opts.BackgroundWake)
	}

	sessionDir := strings.TrimSpace(state.GetPersistedSessionDir())
	if sessionDir != "" {
		_ = ClearCancelRequest(sessionDir)
		go m.runCrossProcessCancelPoll(turnCtx, state, sessionDir)
	}

	// A child's own task turn carries text the parent model wrote, never a
	// plan the operator saved: the run-plan delegation and the @plans mention
	// hydration read the (empty) child bundle and would refuse or fail the
	// child's only legitimate turn, so the prompt goes to the runner verbatim.
	subagentTurn := opts != nil && opts.subagentTurn
	// Ask mode is read-only: the run-plan metadata shortcut is refused, and a
	// plan mention below stays material to read (HydrateSessionPlanMentions
	// inlines the document) instead of turning into a run request.
	askMode := state.GetMode() == string(ModeAsk)

	if slug := RunPlanSlugFromPromptMeta(params.Meta); slug != "" && !subagentTurn {
		if askMode {
			return nil, fmt.Errorf("plan %q cannot be run in ask mode: switch to agent mode first", slug)
		}
		return m.runPlanAdmitted(turnCtx, params.SessionID, slug, state, sender)
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
	hydrated, err := hydrateClientResources(cwdAbs, params.Prompt)
	if err != nil {
		return nil, err
	}
	if sd := strings.TrimSpace(state.GetPersistedSessionDir()); sd != "" && !subagentTurn && !askMode {
		if mentionSlug := ExtractRunPlanSlugFromPromptText(contentBlocksToPlainText(hydrated)); mentionSlug != "" {
			return m.runPlanAdmitted(turnCtx, params.SessionID, mentionSlug, state, sender)
		}
	}
	// The "@" references of the prompt are resolved once, here, into
	// attachments of this message (mentions.go). A woken turn's prompt is
	// Coddy's own report of finished tasks, not text anybody typed.
	if opts == nil || opts.BackgroundWake == nil {
		hydrated = m.ResolvePromptMentions(turnCtx, state, hydrated, m.mentionScope(state, subagentTurn, askMode))
	}

	var ranRunner bool
	defer func() {
		if ranRunner {
			state.BumpActivitySeq()
		}
	}()

	ranRunner = true
	MarkTurnRan(turnCtx)
	stopReason, err := m.runner(turnCtx, state, hydrated, sender)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			state.AppendUILogError(CountUserTurns(state.GetMessages()), err.Error())
		}
		return nil, err
	}

	// The ReAct loop reads the queue between its own steps, which is where a
	// follow-up is meant to land. What it cannot catch is the last moment of
	// the turn: a message written while the answer was already being returned.
	// Rather than hand that one to a turn hours later, the same admitted turn
	// answers it - no second lock, no second admission, and the drain that
	// finds nothing closes the queue in the same step, so there is no window
	// where a message is accepted by a turn that is already over.
	//
	// Only a turn that ended with an answer continues. A cancelled turn is a
	// Stop, and a Stop drops what was waiting rather than answering it; a turn
	// that stopped for any other reason (its turn cap, a refusal, a hook) has
	// already said why, and running it again would bury that. The run count is
	// bounded for the same reason the ReAct loop is: each continuation is a
	// fresh Agent.Run with its own budget, so without a cap one admission
	// could hold the session's turn lock indefinitely.
	for runs := 0; stopReason == string(acp.StopReasonEndTurn) && turnCtx.Err() == nil; runs++ {
		if runs >= maxQueuedFollowUpRuns {
			left := state.CloseMessageQueue()
			m.log.Warn("message queue follow-ups capped; the rest is dropped",
				"session_id", params.SessionID, "runs", runs, "dropped", len(left))
			break
		}
		queued, more := state.TakeQueuedMessagesOrClose()
		if !more {
			break
		}
		stopReason, err = m.runner(turnCtx, state, state.ResolveQueuedMentions(QueuedPromptBlocks(queued)), sender)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				state.AppendUILogError(CountUserTurns(state.GetMessages()), err.Error())
			}
			return nil, err
		}
	}

	return &acp.SessionPromptResult{StopReason: acp.StopReason(stopReason)}, nil
}

// maxQueuedFollowUpRuns bounds how many times one admitted turn is continued by
// what arrived in its final moments.
//
// Each continuation is a full Agent.Run with its own agent.max_turns budget, so
// an operator (or a script) writing one follow-up per boundary could otherwise
// hold the session's turn lock for as long as they keep typing. Past the cap
// the queue is closed and what is left is dropped with a warning, exactly as a
// Stop drops it: the alternative is a session no other surface can ever enter.
const maxQueuedFollowUpRuns = 8

func (m *Manager) HandleSessionSetMode(ctx context.Context, params acp.SessionSetModeParams) error {
	mode := params.ModeID
	_, err := m.ApplySessionSettings(ctx, params.SessionID, SettingsChange{Mode: &mode, Source: "acp"})
	return err
}

// HandleSessionSetConfigOption implements session/set_config_option (ACP
// Session Config Options). Every option is a session setting and goes through
// ApplySessionSettings, whichever surface calls it.
func (m *Manager) HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return m.SetConfigOptionFrom(ctx, params, "acp")
}

// SetConfigOptionFrom is HandleSessionSetConfigOption with the source named,
// for a surface that reaches the manager in process (the console, the
// Telegram bot).
func (m *Manager) SetConfigOptionFrom(ctx context.Context, params acp.SessionSetConfigOptionParams, source string) (*acp.SessionSetConfigOptionResult, error) {
	value := params.Value
	ch := SettingsChange{Source: source}
	switch params.ConfigID {
	case "mode":
		ch.Mode = &value
	case "model":
		ch.Model = &value
	case "reasoning":
		ch.Reasoning = &value
	case "permission_mode":
		ch.PermissionMode = &value
	default:
		return nil, fmt.Errorf("unknown config option: %q", params.ConfigID)
	}
	if _, err := m.ApplySessionSettings(ctx, params.SessionID, ch); err != nil {
		return nil, err
	}
	state := m.getSession(params.SessionID)
	return &acp.SessionSetConfigOptionResult{ConfigOptions: BuildACPConfigOptions(m.activeCfg(), state)}, nil
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
	// The footer is populated before the first prompt: an automatic read,
	// served from the cache when it is warm.
	m.publishProviderUsageOnReady(sessionID, st)
}

func (m *Manager) sendAvailableSlashCommands(sessionID string, st *State) {
	if m.server == nil || st == nil {
		return
	}
	sums := skills.ListSkills(st.GetSkills())
	builtins := BuiltinCommandRows(m.activeCfg(), st, ActionCommandRows(m.activeCfg()))
	cmds := make([]acp.AvailableCommand, 0, len(sums)+len(builtins))
	for _, b := range builtins {
		cmd := acp.AvailableCommand{Name: b.Name, Description: b.Description}
		if b.Hint != "" {
			cmd.Input = &acp.AvailableCommandInput{Hint: b.Hint}
		}
		cmds = append(cmds, cmd)
	}
	// A built-in wins over a skill of the same name (the prompt path takes
	// the built-in first), so the skill is not listed twice.
	taken := make(map[string]bool, len(builtins))
	for _, b := range builtins {
		taken[b.Name] = true
		for _, a := range b.Aliases {
			taken[a] = true
		}
	}
	for _, s := range sums {
		if taken[s.Name] {
			continue
		}
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
	m.reloadConfiguredMCPServersExcept(ctx, nil)
}

// reloadConfiguredMCPServersExcept applies a settings reload to every active
// session except skip. config_commit uses skip for the session it refreshes at
// the safe boundary between its current and next model calls.
func (m *Manager) reloadConfiguredMCPServersExcept(ctx context.Context, skip *State) {
	m.mu.RLock()
	states := make([]*State, 0, len(m.sessions))
	for _, state := range m.sessions {
		if state == skip {
			continue
		}
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

// NewSessionID returns a fresh session id: sess_ followed by 24 hex
// characters. Every session carries this shape, whoever started it - a
// console run, a browser tab, a chat on a messenger gateway, or a subagent
// run another session spawned - so nothing downstream can read a session's
// origin off its id. What a session is, is in its bundle.
func NewSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate session ID: " + err.Error())
	}
	return "sess_" + hex.EncodeToString(b)
}
