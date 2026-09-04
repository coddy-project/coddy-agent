package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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

// ErrReservedSessionID is returned when a caller asks to create an ordinary
// session under the sub_ prefix, which only CreateSubagentSession may use.
var ErrReservedSessionID = errors.New("session id uses the reserved subagent prefix")

// ErrSessionDeleting is returned to a turn that arrives while DeleteSessionTree
// is removing the session.
var ErrSessionDeleting = errors.New("session is being deleted")

// ErrTurnNotSettled is returned by DeleteSessionTree when a cancelled turn of
// the tree is still running after the settle timeout; nothing was removed.
var ErrTurnNotSettled = errors.New("a turn of the session did not stop in time; nothing was deleted")

// IsSubagentSessionID reports whether an id uses the reserved child prefix.
// The prefix is a second line of defence next to the persisted metadata: an
// id shaped like a child is treated as one even before its bundle says so.
func IsSubagentSessionID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), subagentSessionPrefix)
}

// subagentMCPDialTimeout bounds the MCP handshakes a child performs while it is
// created. A hung server must not hold the launch, the limiter slot and the
// parent's foreground wait hostage; a dial that runs out of time is logged and
// skipped like a failed one.
const subagentMCPDialTimeout = mcpReloadTimeout

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
	// MaxTurns caps the child's ReAct rounds; 0 uses the configured default.
	MaxTurns int
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

	cwd, err := EffectiveSessionCWD(spec.CWD, m.defaultCWD)
	if err != nil {
		return nil, fmt.Errorf("subagent cwd: %w", err)
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
		SelectedModelID: strings.TrimSpace(spec.SelectedModelID),
		PermissionMode:  strings.TrimSpace(spec.PermissionMode),
	}
	// The child metadata is attached before the state is visible anywhere:
	// every reader that finds the live entry must already see a read-only
	// child, never an ordinary session that turns into one later.
	state.SetSubagentMeta(SubagentMeta{
		Name:            name,
		ParentSessionID: parentID,
		TaskID:          strings.TrimSpace(spec.TaskID),
		Depth:           spec.Depth,
		MaxTurns:        spec.MaxTurns,
		Role:            spec.Role,
		Tools:           spec.Tools,
	})
	if title := strings.TrimSpace(spec.Title); title != "" {
		state.SetTitlePinnedWithoutPersist(title)
	}
	state.ReplaceRulesCatalog(DiscoverRules(active, cwd))
	state.SetPersistHook(m.makePersist(state))

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("subagent session creation cancelled: %w", err)
	}

	// Reserve the live entry before the bundle exists on disk. The HTTP read
	// path serves a live session when it finds one and loads the bundle
	// otherwise; registering first means a transcript read racing this call
	// finds the one state the child will run with, never a second state built
	// from a half-written bundle.
	// The parent must be live and not under deletion at the moment of the
	// publish, decided under the same lock DeleteSessionTree's rescan reads,
	// so a child cannot slip in between the delete's snapshot and its removal.
	m.mu.Lock()
	if _, occupied := m.sessions[id]; occupied {
		m.mu.Unlock()
		return nil, fmt.Errorf("subagent session id already active: %s", id)
	}
	if _, live := m.sessions[parentID]; !live {
		m.mu.Unlock()
		return nil, fmt.Errorf("subagent parent session is not live: %s", parentID)
	}
	if m.isDeleting(parentID) {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: parent %s", ErrSessionDeleting, parentID)
	}
	m.sessions[id] = state
	m.mu.Unlock()
	if hook := m.testHooks.afterSubagentPublish; hook != nil {
		hook(state)
	}

	// Everything after the publish rolls back on failure: the live entry goes,
	// and so does a bundle this call created, so no half-built sub_ snapshot
	// without its metadata survives to be listed or loaded later.
	sessionPath := m.store.SessionPath(id)
	_, statErr := os.Stat(sessionPath)
	createdBundle := os.IsNotExist(statErr)
	failed := true
	defer func() {
		if !failed {
			return
		}
		m.mu.Lock()
		if m.sessions[id] == state {
			delete(m.sessions, id)
		}
		m.mu.Unlock()
		state.CloseAll()
		if createdBundle {
			_ = os.RemoveAll(sessionPath)
		}
	}()

	sessionDir, err := m.store.EnsureLayout(id)
	if err != nil {
		return nil, fmt.Errorf("subagent session layout: %w", err)
	}
	state.setSessionDir(sessionDir)

	if spec.ConnectMCP {
		dialCtx, cancel := context.WithTimeout(ctx, subagentMCPDialTimeout)
		m.connectConfiguredMCPServers(dialCtx, state)
		for _, srv := range spec.ClientMCPServers {
			client, err := m.connectMCPServer(dialCtx, state, srv)
			if err != nil {
				m.log.Warn("failed to redial client MCP server for subagent", "server", srv.Name, "error", err)
				continue
			}
			state.AddSessionMCPClient(client)
			state.RememberSessionMCPDeclaration(srv)
		}
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("subagent session creation cancelled: %w", err)
	}

	// A deletion that ran while this call was in flight has dropped the live
	// entry or marked the parent; the bundle must not be written after that,
	// or it would outlive the tree it belongs to.
	m.mu.Lock()
	stillLive := m.sessions[id] == state
	parentDeleting := m.isDeleting(parentID)
	m.mu.Unlock()
	if !stillLive || parentDeleting {
		return nil, fmt.Errorf("%w: parent %s", ErrSessionDeleting, parentID)
	}

	// The first save is what makes the bundle a child on disk; without it a
	// later read would load a generic session, so it is fatal, not a warning.
	if err := m.store.Save(state); err != nil {
		return nil, fmt.Errorf("initial subagent session save: %w", err)
	}
	failed = false
	m.log.Info("subagent session created", "id", id, "parent", parentID, "agent", name, "task", spec.TaskID)
	return state, nil
}

// isDeleting reports whether DeleteSessionTree is removing the session.
func (m *Manager) isDeleting(id string) bool {
	m.deletingMu.Lock()
	defer m.deletingMu.Unlock()
	return m.deleting[strings.TrimSpace(id)]
}

func (m *Manager) markDeleting(ids []string, on bool) {
	m.deletingMu.Lock()
	defer m.deletingMu.Unlock()
	if m.deleting == nil {
		m.deleting = map[string]bool{}
	}
	for _, id := range ids {
		if on {
			m.deleting[id] = true
		} else {
			delete(m.deleting, id)
		}
	}
}

// deleteSettleTimeout bounds how long DeleteSessionTree waits for a cancelled
// turn to release. A turn still running afterwards aborts the deletion: a
// runner that ignores its context is a bug elsewhere, and removing the bundle
// under it would only turn that bug into a resurrected session.
var deleteSettleTimeout = 15 * time.Second

// cancelAndAwaitTurns cancels the running turn of every node and waits until
// none of them is active in this process, so no persist hook fires after the
// bundle is gone. It returns ErrTurnNotSettled when a turn outlives the wait.
func (m *Manager) cancelAndAwaitTurns(nodes []SessionTreeNode) error {
	for _, n := range nodes {
		if st := m.getSession(n.ID); st != nil {
			st.SetUserCancelledTurn()
			st.Cancel()
		}
	}
	deadline := time.Now().Add(deleteSettleTimeout)
	for {
		busy := ""
		for _, n := range nodes {
			if m.SessionTurnActiveInProcess(n.ID) {
				busy = n.ID
				break
			}
		}
		if busy == "" {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: %s", ErrTurnNotSettled, busy)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	indexed := map[string]bool{}
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
		indexed[ent.Name()] = true
	}
	// A child that is published but not yet persisted exists only in the
	// live map; the tree must see it too, or a delete racing its creation
	// would leave it behind.
	m.mu.RLock()
	for id, st := range m.sessions {
		if indexed[id] {
			continue
		}
		meta := st.Subagent()
		if meta == nil || strings.TrimSpace(meta.ParentSessionID) == "" {
			continue
		}
		parent := strings.TrimSpace(meta.ParentSessionID)
		children[parent] = append(children[parent], SessionTreeNode{
			ID:              id,
			ParentSessionID: parent,
			SubagentTaskID:  strings.TrimSpace(meta.TaskID),
			SubagentRun:     true,
		})
		indexed[id] = true
	}
	m.mu.RUnlock()

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
//  0. every node is marked as deleting (new turns are refused) and its active
//     turn, if any, is cancelled and awaited; a turn that does not stop within
//     the settle timeout aborts the deletion with ErrTurnNotSettled;
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
	if hook := m.testHooks.afterTreeScan; hook != nil {
		hook(rootID)
	}
	// From here on a new turn against any node is refused, a new child under
	// any node is refused, and an active turn is cancelled and awaited: a turn
	// that outlived the delete would recreate the bundle through its persist
	// hook (atomic writes MkdirAll their target). The tree is rescanned after
	// marking until no unmarked node appears, so a child published between
	// the first snapshot and the mark is caught rather than orphaned.
	marked := map[string]bool{}
	mark := func(ns []SessionTreeNode) {
		ids := make([]string, 0, len(ns))
		for _, n := range ns {
			if !marked[n.ID] {
				marked[n.ID] = true
				ids = append(ids, n.ID)
			}
		}
		m.markDeleting(ids, true)
	}
	mark(nodes)
	defer func() {
		ids := make([]string, 0, len(marked))
		for id := range marked {
			ids = append(ids, id)
		}
		m.markDeleting(ids, false)
	}()
	for round := 0; round < 8; round++ {
		again, err := m.SessionTree(rootID)
		if err != nil {
			return err
		}
		fresh := 0
		for _, n := range again {
			if !marked[n.ID] {
				fresh++
			}
		}
		nodes = again
		if fresh == 0 {
			break
		}
		mark(again)
	}
	if err := m.cancelAndAwaitTurns(nodes); err != nil {
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
