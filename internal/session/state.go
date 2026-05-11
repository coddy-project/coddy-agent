// Package session manages per-session state for the agent.
package session

import (
	"context"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// Mode is the current operating mode of a session.
type Mode string

const (
	ModeAgent Mode = "agent"
	ModePlan  Mode = "plan"
)

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

	// Messages is the conversation history.
	Messages []llm.Message

	// UILog holds UI-only transcript lines (errors, etc.); excluded from LLM prompts.
	UILog []UILogEntry

	// MCPClients are connected MCP servers for this session.
	MCPClients []*mcp.Client

	// Skills are the loaded and active skills/rules.
	Skills []*skills.Skill

	// Plan holds the current todo list entries.
	Plan []acp.PlanEntry

	// AgentMemory is optional session notes included in the system prompt template (.Memory).
	AgentMemory string

	// TitlePinned, when set, is written to session.json and overrides derived titles from the first user message.
	TitlePinned string

	// MemoryCopilotBlock is per-turn text from the memory copilot (not persisted to session.json).
	MemoryCopilotBlock string

	// SessionDir is the persisted session bundle directory (<sessionsRoot>/<id>/).
	SessionDir string

	// Scheduler run metadata (cron / coddy_scheduler_job_run); written to session.json when SchedulerRun is true.
	SchedulerRun        bool
	SchedulerJobID      string
	SchedulerStartedAt  string // RFC3339 UTC
	SchedulerEndedAt    string // RFC3339 UTC when terminal
	SchedulerStopStatus string // running | completed | failed | cancelled

	// PermissionCommandGrants are session-scoped shell commands approved via "allow always" (same matching rules as tools.command_allowlist).
	PermissionCommandGrants []string
	// PermissionWriteGrants are keys "toolName|absolutePath" for filesystem tools approved via "allow always".
	PermissionWriteGrants []string

	// persist is invoked after persisted fields change (set by Manager; may be nil).
	persist func()

	// cancel cancels the active prompt turn.
	cancel context.CancelFunc
}

// GetID returns the session ID.
func (s *State) GetID() string {
	return s.ID
}

// GetCWD returns the session working directory.
func (s *State) GetCWD() string {
	return s.CWD
}

// GetPersistedSessionDir returns the filesystem bundle dir if persistence is enabled.
func (s *State) GetPersistedSessionDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SessionDir
}

// SetSchedulerRunMeta configures this state as a persisted scheduler run (writes scheduler* fields in session.json via Save).
func (s *State) SetSchedulerRunMeta(jobID string, startedRFC3339UTC string) {
	s.mu.Lock()
	s.SchedulerRun = true
	s.SchedulerJobID = strings.TrimSpace(jobID)
	s.SchedulerStartedAt = strings.TrimSpace(startedRFC3339UTC)
	s.SchedulerEndedAt = ""
	s.SchedulerStopStatus = "running"
	s.mu.Unlock()
}

// FinishSchedulerRun marks the scheduler run terminal (call before final Save).
func (s *State) FinishSchedulerRun(endedRFC3339UTC, status string) {
	s.mu.Lock()
	s.SchedulerEndedAt = strings.TrimSpace(endedRFC3339UTC)
	s.SchedulerStopStatus = strings.TrimSpace(status)
	s.mu.Unlock()
}

func (s *State) GetSchedulerRun() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerRun
}

func (s *State) GetSchedulerJobID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerJobID
}

func (s *State) GetSchedulerStartedAt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerStartedAt
}

func (s *State) GetSchedulerEndedAt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerEndedAt
}

func (s *State) GetSchedulerStopStatus() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SchedulerStopStatus
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
	return s.MCPClients
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
	s.touchPersist()
}

// GetMode returns the current mode as a string.
func (s *State) GetMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(s.Mode)
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
	s.touchPersist()
}

// EffectiveModelID returns the model id used for LLM calls for this session.
func (s *State) EffectiveModelID(cfg *config.Config) string {
	s.mu.RLock()
	sel := s.SelectedModelID
	s.mu.RUnlock()
	if sel != "" {
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
	s.mu.Unlock()
	s.touchPersist()
}

// GetMessages returns a copy of the message history.
func (s *State) GetMessages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs
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

// GetTitlePinned returns the user-pinned session title shown in snapshots, if any.
func (s *State) GetTitlePinned() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TitlePinned
}

// SetTitlePinned sets the pinned title and persists session metadata when a store is attached.
func (s *State) SetTitlePinned(text string) {
	s.mu.Lock()
	s.TitlePinned = strings.TrimSpace(text)
	s.mu.Unlock()
	s.touchPersist()
}

// SetTitlePinnedWithoutPersist restores pinned title from disk without writing.
func (s *State) SetTitlePinnedWithoutPersist(text string) {
	s.mu.Lock()
	s.TitlePinned = strings.TrimSpace(text)
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
func (s *State) ReplaceMessagesWithoutPersist(msgs []llm.Message) {
	s.mu.Lock()
	s.Messages = msgs
	s.mu.Unlock()
}

// RestoreMetaWithoutPersist restores mode and model/memory fields from disk (no persistence callback).
func (s *State) RestoreMetaWithoutPersist(mode Mode, selectedModelID, agentMemory string) {
	s.mu.Lock()
	s.Mode = mode
	s.SelectedModelID = selectedModelID
	s.AgentMemory = agentMemory
	s.mu.Unlock()
}

// ReplaceSkills replaces loaded skills without touching disk (used when rebuilding session).
func (s *State) ReplaceSkills(sk []*skills.Skill) {
	s.mu.Lock()
	s.Skills = sk
	s.mu.Unlock()
}

// SetCancel stores a cancel function for the active prompt turn.
func (s *State) SetCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = cancel
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

// CloseAll closes all MCP clients.
func (s *State) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.MCPClients {
		_ = c.Close()
	}
	s.MCPClients = nil
}

// RestorePermissionGrantsWithoutPersist loads grants from disk snapshot (session/load).
func (s *State) RestorePermissionGrantsWithoutPersist(commands, writes []string) {
	s.mu.Lock()
	s.PermissionCommandGrants = append([]string(nil), commands...)
	s.PermissionWriteGrants = append([]string(nil), writes...)
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
