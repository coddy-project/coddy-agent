// Package session manages per-session state for the agent.
package session

import (
	"context"
	"sync"

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

	// Messages is the conversation history.
	Messages []llm.Message

	// MCPClients are connected MCP servers for this session.
	MCPClients []*mcp.Client

	// Skills are the loaded and active skills/rules.
	Skills []*skills.Skill

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

// SetMode updates the session mode (accepts string for interface compatibility).
func (s *State) SetMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Mode = Mode(mode)
}

// GetMode returns the current mode as a string.
func (s *State) GetMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return string(s.Mode)
}

// AddMessage appends a message to the conversation history.
func (s *State) AddMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, msg)
}

// GetMessages returns a copy of the message history.
func (s *State) GetMessages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return msgs
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
