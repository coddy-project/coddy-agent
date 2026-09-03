package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Child sessions spawned by spawn_agent live under this folder prefix, so a
// bundle is recognisable as a subagent run even before its meta is complete.
const subagentSessionPrefix = "sub_"

// ErrSubagentReadOnly is returned for any prompt against a child session that
// does not come from the child's own task turn: resuming or messaging a
// subagent is not supported, so its transcript is read-only for every surface.
var ErrSubagentReadOnly = errors.New("subagent sessions are read-only transcripts")

// NewSubagentSessionID returns a fresh child session id (sub_ plus 24 hex).
func NewSubagentSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate subagent session ID: " + err.Error())
	}
	return subagentSessionPrefix + hex.EncodeToString(b)
}

// SubagentSpec describes the child session the runtime asks the manager to
// create. Every decision (narrowed mode and permission mode, model, tool set,
// role) is made by the runtime before this call; the manager only builds and
// registers the session.
type SubagentSpec struct {
	// ID is the pre-generated child session id (NewSubagentSessionID).
	ID string
	// ParentSessionID is the spawning session.
	ParentSessionID string
	// Name is the subagent definition name.
	Name string
	// TaskID is the pool task representing this run, assigned before creation.
	TaskID string
	// CWD is the working directory, normally the parent's.
	CWD string
	// Mode is agent or plan (already narrowed against the parent).
	Mode string
	// PermissionMode is the effective permission mode (already narrowed).
	PermissionMode string
	// SelectedModelID is the child's model, or empty to follow the config.
	SelectedModelID string
	// Title is pinned as the session title (the task label).
	Title string
	// Role is the definition body the child's system prompt carries.
	Role string
	// Tools is the effective tool set the child may call.
	Tools []string
	// Depth is the child's nesting level.
	Depth int
	// ConnectMCP dials configured MCP servers (through the trust gate) and the
	// client-supplied declarations below. Off when the child's tool set cannot
	// contain MCP tools, so a read-only explorer never starts a process.
	ConnectMCP bool
	// ClientMCPServers are the parent's ACP client-supplied declarations to
	// redial; they exist nowhere in the configuration.
	ClientMCPServers []config.MCPServerConfig
}

// CreateSubagentSession builds, registers and persists a child session. The
// live entry is what transcript reads see while the child runs; afterwards
// RetireSubagentSession drops it and the bundle serves the transcript.
func (m *Manager) CreateSubagentSession(ctx context.Context, spec SubagentSpec) (*State, error) {
	if m.store == nil {
		return nil, fmt.Errorf("subagents need session persistence")
	}
	id := strings.TrimSpace(spec.ID)
	if !strings.HasPrefix(id, subagentSessionPrefix) {
		return nil, fmt.Errorf("subagent session id must start with %q", subagentSessionPrefix)
	}
	if err := ValidateFolderSessionID(id); err != nil {
		return nil, err
	}
	parentID := strings.TrimSpace(spec.ParentSessionID)
	if parentID == "" {
		return nil, fmt.Errorf("subagent session needs a parent session id")
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf("subagent session needs a definition name")
	}

	m.mu.RLock()
	_, occupied := m.sessions[id]
	m.mu.RUnlock()
	if occupied {
		return nil, fmt.Errorf("subagent session id already active: %s", id)
	}

	cwd, err := EffectiveSessionCWD(spec.CWD, m.defaultCWD)
	if err != nil {
		return nil, fmt.Errorf("subagent cwd: %w", err)
	}
	sessionDir, err := m.store.EnsureLayout(id)
	if err != nil {
		return nil, fmt.Errorf("subagent session layout: %w", err)
	}

	active := m.activeCfg()
	loadedSkills, err := m.loadSkills(cwd, active)
	if err != nil {
		m.log.Warn("failed to load skills for subagent", "error", err)
	}

	mode := ModeAgent
	if strings.EqualFold(strings.TrimSpace(spec.Mode), string(ModePlan)) {
		mode = ModePlan
	}
	state := &State{
		ID:              id,
		CWD:             cwd,
		Mode:            mode,
		Skills:          loadedSkills,
		SessionDir:      sessionDir,
		SelectedModelID: strings.TrimSpace(spec.SelectedModelID),
		PermissionMode:  strings.TrimSpace(spec.PermissionMode),
	}
	state.SetSubagentMeta(SubagentMeta{
		Name:            name,
		ParentSessionID: parentID,
		TaskID:          strings.TrimSpace(spec.TaskID),
		Depth:           spec.Depth,
		Role:            spec.Role,
		Tools:           spec.Tools,
	})
	if title := strings.TrimSpace(spec.Title); title != "" {
		state.SetTitlePinnedWithoutPersist(title)
	}
	state.ReplaceRulesCatalog(DiscoverRules(active, cwd))
	state.SetPersistHook(m.makePersist(state))

	if spec.ConnectMCP {
		m.connectConfiguredMCPServers(ctx, state)
		for _, srv := range spec.ClientMCPServers {
			client, err := m.connectMCPServer(ctx, state, srv)
			if err != nil {
				m.log.Warn("failed to redial client MCP server for subagent", "server", srv.Name, "error", err)
				continue
			}
			state.AddSessionMCPClient(client)
			state.RememberSessionMCPDeclaration(srv)
		}
	}

	m.mu.Lock()
	m.sessions[id] = state
	m.mu.Unlock()

	if err := m.store.Save(state); err != nil {
		m.log.Warn("initial subagent session save", "id", id, "error", err)
	}
	m.log.Info("subagent session created", "id", id, "parent", parentID, "agent", name, "task", spec.TaskID)
	return state, nil
}

// RunSubagentTurn runs the child's one and only prompt turn through the normal
// prompt path (turn lock, activity edges, cross-process cancel, persistence),
// carrying the internal option that lets it past the read-only guard.
func (m *Manager) RunSubagentTurn(ctx context.Context, sessionID string, prompt []acp.ContentBlock, sender acp.UpdateSender) (*acp.SessionPromptResult, error) {
	return m.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: sessionID,
		Prompt:    prompt,
	}, sender, &PromptRunOpts{subagentTurn: true})
}

// RetireSubagentSession drops a finished child from the live map, closing its
// MCP clients. The bundle stays on disk and serves the transcript from now on.
// Callers stop the tasks the child owns before retiring it.
func (m *Manager) RetireSubagentSession(sessionID string) {
	m.ForgetLiveSession(strings.TrimSpace(sessionID))
}

// subagentParentOf names the parent for error messages.
func subagentParentOf(st *State) string {
	if meta := st.Subagent(); meta != nil && meta.ParentSessionID != "" {
		return meta.ParentSessionID
	}
	return "an unknown parent session"
}

// SessionTreeNode is one session in a delete tree: the requested root or a
// descendant spawned under it.
type SessionTreeNode struct {
	ID              string
	ParentSessionID string
	SubagentTaskID  string
	SubagentRun     bool
}

// SessionTree returns rootID followed by every descendant child session,
// breadth first (parents before their children). Descendants are found by the
// parentSessionId their bundles record.
func (m *Manager) SessionTree(rootID string) ([]SessionTreeNode, error) {
	if m.store == nil || m.store.Root == "" {
		return nil, fmt.Errorf("session store unavailable")
	}
	rootID = strings.TrimSpace(rootID)
	if err := ValidateFolderSessionID(rootID); err != nil {
		return nil, err
	}

	// Index every child bundle by its parent once; the tree is small but the
	// sessions root can hold many bundles.
	children := map[string][]SessionTreeNode{}
	entries, err := os.ReadDir(m.store.Root)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, ent := range entries {
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), subagentSessionPrefix) {
			continue
		}
		snap, err := m.store.ReadSnapshot(ent.Name())
		if err != nil || strings.TrimSpace(snap.Meta.ParentSessionID) == "" {
			continue
		}
		parent := strings.TrimSpace(snap.Meta.ParentSessionID)
		children[parent] = append(children[parent], SessionTreeNode{
			ID:              ent.Name(),
			ParentSessionID: parent,
			SubagentTaskID:  strings.TrimSpace(snap.Meta.SubagentTaskID),
			SubagentRun:     true,
		})
	}

	root := SessionTreeNode{ID: rootID}
	if snap, err := m.store.ReadSnapshot(rootID); err == nil && snap.Meta.IsSubagentRun(rootID) {
		root.SubagentRun = true
		root.ParentSessionID = strings.TrimSpace(snap.Meta.ParentSessionID)
		root.SubagentTaskID = strings.TrimSpace(snap.Meta.SubagentTaskID)
	} else if st := m.getSession(rootID); st != nil {
		if meta := st.Subagent(); meta != nil {
			root.SubagentRun = true
			root.ParentSessionID = meta.ParentSessionID
			root.SubagentTaskID = meta.TaskID
		}
	}

	out := []SessionTreeNode{root}
	seen := map[string]bool{rootID: true}
	for i := 0; i < len(out); i++ {
		for _, child := range children[out[i].ID] {
			if seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			out = append(out, child)
		}
	}
	return out, nil
}

// DeleteSessionTree removes a session together with every child session it
// spawned, in an order that leaves no writer behind:
//
//  1. every node's representing task is stopped and awaited, root to leaf
//     (a child's task lives under its parent session, so this reaches a
//     running child and a running descendant alike);
//  2. every remaining task of every node is stopped (background commands the
//     children left running);
//  3. live entries are dropped and bundles removed deepest first, the
//     requested session last.
//
// pool may be nil when no task pool exists (tests without a pool).
func (m *Manager) DeleteSessionTree(rootID string, pool *bgtask.Pool) error {
	nodes, err := m.SessionTree(rootID)
	if err != nil {
		return err
	}
	if pool != nil {
		for _, n := range nodes {
			if n.SubagentRun && n.ParentSessionID != "" && n.SubagentTaskID != "" {
				if _, err := pool.Stop(n.ParentSessionID, n.SubagentTaskID); err != nil && !errors.Is(err, bgtask.ErrNotFound) {
					m.log.Warn("stop subagent task before delete", "session", n.ID, "task", n.SubagentTaskID, "error", err)
				}
			}
		}
		for _, n := range nodes {
			pool.StopSession(n.ID)
		}
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		m.ForgetLiveSession(n.ID)
		if err := os.RemoveAll(m.store.SessionPath(n.ID)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove session %s: %w", n.ID, err)
		}
	}
	return nil
}
