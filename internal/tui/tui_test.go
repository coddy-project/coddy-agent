package tui

import (
	"fmt"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func makeTestConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	return Config{
		AppConfig: &config.Config{},
		Store:     store,
		CWD:       "/tmp",
	}
}

// agentChunk builds an AgentUpdateMsg that looks like a streamed text delta.
func agentChunk(sessionID, text string) AgentUpdateMsg {
	return AgentUpdateMsg{
		SessionID: sessionID,
		Update: acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: "text", Text: text},
		},
	}
}

func TestNewAppModel(t *testing.T) {
	cfg := makeTestConfig(t)
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.state == nil {
		t.Fatal("state must not be nil after New()")
	}
	if m.state.ID == "" {
		t.Fatal("session ID must not be empty")
	}
	if !m.showWelcome {
		t.Error("showWelcome must be true for a fresh session")
	}
	if m.runner == nil {
		t.Fatal("runner must not be nil")
	}
}

func TestToggleMode(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)

	if m.state.GetMode() != string(session.ModeAgent) {
		t.Fatalf("default mode should be agent, got %q", m.state.GetMode())
	}

	m = m.toggleMode()
	if m.state.GetMode() != string(session.ModePlan) {
		t.Fatalf("after toggle should be plan, got %q", m.state.GetMode())
	}

	m = m.toggleMode()
	if m.state.GetMode() != string(session.ModeAgent) {
		t.Fatalf("after double toggle should be agent, got %q", m.state.GetMode())
	}
}

func TestTabKeyTogglesMode(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m = m.recalcLayout()

	initial := m.state.GetMode()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m2 := updated.(AppModel)

	if m2.state.GetMode() == initial {
		t.Errorf("Tab should toggle mode: was %q, still %q", initial, m2.state.GetMode())
	}
}

func TestCtrlXTogglesInputOff(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24

	if m.inputOff {
		t.Fatal("input should be on by default")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m2 := updated.(AppModel)
	if !m2.inputOff {
		t.Error("after ctrl+x input should be off")
	}

	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	m3 := updated.(AppModel)
	if m3.inputOff {
		t.Error("after second ctrl+x input should be on again")
	}
}

func TestCtrlPOpensCommandsModal(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m2 := updated.(AppModel)
	if m2.modal.kind != modalCommands {
		t.Errorf("ctrl+p should open commands modal, got kind=%d", m2.modal.kind)
	}
}

func TestAltMOpensModelsModal(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24

	// alt+m opens the model picker (ctrl+m = Enter in standard terminals).
	updated, _ := m.Update(tea.KeyMsg{Alt: true, Runes: []rune("m")})
	m2 := updated.(AppModel)
	if m2.modal.kind != modalModels {
		t.Errorf("alt+m should open models modal, got kind=%d", m2.modal.kind)
	}
}

func TestEscapeClosesModal(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m.modal = modalState{kind: modalCommands}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m2 := updated.(AppModel)
	if m2.modal.kind != modalNone {
		t.Errorf("esc should close modal, got kind=%d", m2.modal.kind)
	}
}

func TestWindowSizeUpdatesLayout(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := updated.(AppModel)
	if m2.width != 120 || m2.height != 40 {
		t.Errorf("width/height not updated: got %dx%d", m2.width, m2.height)
	}
}

func TestAgentUpdateAppendsText(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m.showWelcome = false

	updated, _ := m.Update(agentChunk(m.state.ID, "hello from agent"))
	m2 := updated.(AppModel)

	if len(m2.chat) == 0 {
		t.Fatal("expected chat entry after agent update")
	}
	last := m2.chat[len(m2.chat)-1]
	if last.content != "hello from agent" {
		t.Errorf("unexpected content: %q", last.content)
	}
}

func TestAgentUpdateAccumulatesText(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24

	updated, _ := m.Update(agentChunk(m.state.ID, "hello "))
	m2 := updated.(AppModel)

	updated, _ = m2.Update(agentChunk(m.state.ID, "world"))
	m3 := updated.(AppModel)

	if len(m3.chat) != 1 {
		t.Fatalf("expected 1 chat entry (accumulated), got %d", len(m3.chat))
	}
	if m3.chat[0].content != "hello world" {
		t.Errorf("unexpected accumulated content: %q", m3.chat[0].content)
	}
}

func TestAgentDoneClearsRunning(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.agentRunning = true

	updated, _ := m.Update(AgentDoneMsg{SessionID: m.state.ID, StopReason: "end_turn"})
	m2 := updated.(AppModel)
	if m2.agentRunning {
		t.Error("agentRunning should be false after AgentDoneMsg")
	}
}

func TestAgentDoneWithErrorAddsErrorEntry(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.agentRunning = true

	updated, _ := m.Update(AgentDoneMsg{
		SessionID:  m.state.ID,
		StopReason: "agent_refused",
		Err:        fmt.Errorf("something went wrong"),
	})
	m2 := updated.(AppModel)

	if m2.agentRunning {
		t.Error("agentRunning should be false after error")
	}
	last := m2.chat[len(m2.chat)-1]
	if last.role != "error" {
		t.Errorf("expected error entry, got role=%q", last.role)
	}
}

func TestVersionInModel(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	if m.appVer == "" {
		t.Error("appVer must not be empty")
	}
}

func TestSessionSavedOnQuit(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewSessionStore(dir)
	cfg := Config{
		AppConfig: &config.Config{},
		Store:     store,
		CWD:       "/tmp",
	}
	m, _ := New(cfg)
	m.width = 80
	m.height = 24

	m.state.AddMessage(llm.Message{Role: llm.RoleUser, Content: "test"})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd on ctrl+c")
	}

	ids, _ := store.List()
	if len(ids) == 0 {
		t.Error("expected session to be saved after ctrl+c")
	}
}

func TestNewSessionCommand(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m.chat = []chatEntry{{role: "user", content: "old message"}}
	m.showWelcome = false

	oldID := m.state.ID

	m2, _ := m.doNewSession()
	m3 := m2.(AppModel)

	if m3.state.ID == oldID {
		t.Error("new session should have a different ID")
	}
	if len(m3.chat) != 0 {
		t.Error("chat should be cleared after new session")
	}
	if !m3.showWelcome {
		t.Error("showWelcome should be true after new session")
	}
}

func TestCommandsModalSearch(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.modal = modalState{kind: modalCommands}

	// Type "mode" to filter commands.
	for _, r := range "mode" {
		updated, _ := m.Update(tea.KeyMsg{Runes: []rune{r}})
		m = updated.(AppModel)
	}

	cmds := filteredCommands(m.modal.query)
	found := false
	for _, c := range cmds {
		if c.label == "Switch mode" {
			found = true
		}
	}
	if !found {
		t.Errorf("filtered commands for %q should include 'Switch mode', got %v", m.modal.query, cmds)
	}
}

func TestPermissionModalReply(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)

	replyCh := make(chan *acp.PermissionResult, 1)
	m.modal = modalState{
		kind: modalPermission,
		permParams: acp.PermissionRequestParams{
			SessionID: m.state.ID,
			ToolCall:  acp.PermissionToolCall{Title: "run_command"},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
				{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
			},
		},
		permReply: replyCh,
	}

	// Press enter to select the first option (Allow).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := updated.(AppModel)

	if m2.modal.kind != modalNone {
		t.Error("modal should be closed after enter")
	}

	select {
	case result := <-replyCh:
		if result.OptionID != "allow" {
			t.Errorf("expected allow, got %q", result.OptionID)
		}
	default:
		t.Error("expected result to be sent to reply channel")
	}
}
