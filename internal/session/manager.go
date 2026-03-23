package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// AgentRunner is a function that runs the ReAct loop for a prompt turn.
// It is provided at Manager construction time to avoid circular imports.
type AgentRunner func(ctx context.Context, state *State, prompt []acp.ContentBlock) (string, error)

// Manager handles all active sessions and implements acp.Handler.
type Manager struct {
	cfg        *config.Config
	server     *acp.Server
	skillsLoad *skills.Loader
	runner     AgentRunner
	log        *slog.Logger

	sessions map[string]*State
	mu       sync.RWMutex
}

// NewManager creates a session manager.
func NewManager(cfg *config.Config, server *acp.Server, runner AgentRunner, log *slog.Logger) *Manager {
	skillsDirs := make([]string, len(cfg.Skills.Dirs))
	copy(skillsDirs, cfg.Skills.Dirs)

	return &Manager{
		cfg:        cfg,
		server:     server,
		runner:     runner,
		skillsLoad: skills.NewLoader(skillsDirs, cfg.Skills.ExtraFiles),
		log:        log,
		sessions:   make(map[string]*State),
	}
}

// SetServer injects the ACP server (used when server and manager are constructed together).
func (m *Manager) SetServer(server *acp.Server) {
	m.server = server
}

// ---- acp.Handler implementation ----

func (m *Manager) HandleInitialize(_ context.Context, params acp.InitializeParams) (*acp.InitializeResult, error) {
	m.log.Info("initialize", "client", params.ClientInfo, "version", params.ProtocolVersion)
	return &acp.InitializeResult{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: &acp.PromptCapabilities{
				EmbeddedContext: true,
			},
			MCPCapabilities: &acp.MCPCapabilities{
				HTTP: false,
			},
		},
		AgentInfo: acp.ImplementationInfo{
			Name:    acp.AgentName,
			Title:   acp.AgentTitle,
			Version: acp.AgentVersion,
		},
		AuthMethods: []string{},
	}, nil
}

func (m *Manager) HandleSessionNew(ctx context.Context, params acp.SessionNewParams) (*acp.SessionNewResult, error) {
	id := newSessionID()

	loadedSkills, err := m.skillsLoad.LoadAll(params.CWD)
	if err != nil {
		m.log.Warn("failed to load skills", "error", err)
	}

	state := &State{
		ID:     id,
		CWD:    params.CWD,
		Mode:   ModeAgent,
		Skills: loadedSkills,
	}

	// Also add project-level skills from CWD.
	projectSkillsDirs := []string{
		params.CWD + "/.cursor/rules",
		params.CWD + "/.cursor/skills",
	}
	projectLoader := skills.NewLoader(projectSkillsDirs, nil)
	projectSkills, _ := projectLoader.LoadAll(params.CWD)
	state.Skills = append(projectSkills, state.Skills...)

	// Connect global MCP servers from config.
	for _, srv := range m.cfg.MCPServers {
		if err := m.connectMCPServer(ctx, state, srv); err != nil {
			m.log.Warn("failed to connect global MCP server", "server", srv.Name, "error", err)
		}
	}

	// Connect per-session MCP servers from client.
	for _, srv := range params.MCPServers {
		cfgSrv := config.MCPServerConfig{
			Type:    srv.Type,
			Name:    srv.Name,
			Command: srv.Command,
			Args:    srv.Args,
			URL:     srv.URL,
		}
		for _, e := range srv.Env {
			cfgSrv.Env = append(cfgSrv.Env, config.EnvVarConfig{Name: e.Name, Value: e.Value})
		}
		if err := m.connectMCPServer(ctx, state, cfgSrv); err != nil {
			m.log.Warn("failed to connect client MCP server", "server", srv.Name, "error", err)
		}
	}

	m.mu.Lock()
	m.sessions[id] = state
	m.mu.Unlock()

	m.log.Info("session created", "id", id, "cwd", params.CWD, "mode", state.Mode)

	return &acp.SessionNewResult{
		SessionID: id,
		Modes: &acp.ModeState{
			CurrentModeID: string(state.Mode),
			AvailableModes: []acp.SessionMode{
				{ID: "agent", Name: "Agent", Description: "Execute tasks with full tool access"},
				{ID: "plan", Name: "Plan", Description: "Plan and design without code execution"},
			},
		},
	}, nil
}

func (m *Manager) HandleSessionLoad(ctx context.Context, params acp.SessionLoadParams) error {
	// For simplicity, session load creates a fresh session with the given ID.
	// A full implementation would restore conversation history from storage.
	_, err := m.HandleSessionNew(ctx, acp.SessionNewParams{
		CWD:        params.CWD,
		MCPServers: params.MCPServers,
	})
	return err
}

func (m *Manager) HandleSessionPrompt(ctx context.Context, params acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	state := m.getSession(params.SessionID)
	if state == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}

	// Create a cancellable context for this prompt turn.
	turnCtx, cancel := context.WithCancel(ctx)
	state.SetCancel(cancel)
	defer cancel()

	stopReason, err := m.runner(turnCtx, state, params.Prompt)
	if err != nil {
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
		ModeID:        params.ModeID,
	}); err != nil {
		m.log.Warn("failed to send mode update", "error", err)
	}

	m.log.Info("mode changed", "session", params.SessionID, "mode", params.ModeID)
	return nil
}

func (m *Manager) HandleSessionCancel(params acp.SessionCancelParams) {
	state := m.getSession(params.SessionID)
	if state == nil {
		return
	}
	state.Cancel()
	m.log.Info("session cancelled", "id", params.SessionID)
}

// ---- helpers ----

func (m *Manager) getSession(id string) *State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

func (m *Manager) connectMCPServer(ctx context.Context, state *State, srv config.MCPServerConfig) error {
	if srv.Type != "" && srv.Type != "stdio" {
		return fmt.Errorf("unsupported MCP transport: %s", srv.Type)
	}

	env := make([]string, len(srv.Env))
	for i, e := range srv.Env {
		env[i] = e.Name + "=" + e.Value
	}

	client, err := mcp.NewStdioClient(ctx, srv.Name, srv.Command, srv.Args, env, m.log)
	if err != nil {
		return err
	}

	state.MCPClients = append(state.MCPClients, client)
	m.log.Info("connected MCP server", "name", srv.Name, "tools", len(client.Tools()))
	return nil
}

func newSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate session ID: " + err.Error())
	}
	return "sess_" + hex.EncodeToString(b)
}
