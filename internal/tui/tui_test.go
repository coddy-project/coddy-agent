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
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
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

func TestMultiTurnTextAfterToolStartsNewBubble(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m.chat = []chatEntry{
		{role: "user", content: "q"},
		{role: "agent", content: "hello"},
		{role: "tool", toolID: "1", toolName: "x", status: "completed"},
	}
	m.streamBuf = "hello"

	updated, _ := m.Update(AgentUpdateMsg{
		SessionID: m.state.ID,
		Update: acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "world"},
		},
	})
	m2 := updated.(AppModel)

	if len(m2.chat) != 4 {
		t.Fatalf("expected 4 chat entries, got %d", len(m2.chat))
	}
	if m2.chat[3].role != "agent" || m2.chat[3].content != "world" {
		t.Errorf("new assistant bubble should only contain second-turn text, got %+v", m2.chat[3])
	}
}

func TestPlaceholderAgentBeforeToolGetsFirstText(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 80
	m.height = 24
	m.chat = []chatEntry{
		{role: "user", content: "q"},
		{role: "agent"},
		{role: "tool", toolID: "1", toolName: "x", status: "pending"},
	}

	updated, _ := m.Update(AgentUpdateMsg{
		SessionID: m.state.ID,
		Update: acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "done"},
		},
	})
	m2 := updated.(AppModel)

	if len(m2.chat) != 3 {
		t.Fatalf("expected 3 chat entries, got %d", len(m2.chat))
	}
	if m2.chat[1].role != "agent" || m2.chat[1].content != "done" {
		t.Errorf("placeholder assistant should be filled, got %+v", m2.chat[1])
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

func TestPlanUpdateStoresPlanEntries(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 120
	m.height = 40
	m = m.recalcLayout()

	entries := []acp.PlanEntry{
		{Content: "step one", Status: "pending"},
		{Content: "step two", Status: "completed"},
	}
	msg := AgentUpdateMsg{
		SessionID: m.state.ID,
		Update: acp.PlanUpdate{
			SessionUpdate: acp.UpdateTypePlan,
			Entries:       entries,
		},
	}

	updated, _ := m.Update(msg)
	m2 := updated.(AppModel)

	if len(m2.planEntries) != 2 {
		t.Fatalf("expected 2 plan entries, got %d", len(m2.planEntries))
	}
	if m2.planEntries[0].Content != "step one" {
		t.Errorf("entry[0] content = %q, want 'step one'", m2.planEntries[0].Content)
	}
	if m2.planEntries[1].Status != "completed" {
		t.Errorf("entry[1] status = %q, want 'completed'", m2.planEntries[1].Status)
	}
}

func TestTokenUsageAccumulates(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)

	sendUsage := func(m AppModel, in, out int) AppModel {
		msg := AgentUpdateMsg{
			SessionID: m.state.ID,
			Update: acp.TokenUsageUpdate{
				SessionUpdate: acp.UpdateTypeTokenUsage,
				InputTokens:   in,
				OutputTokens:  out,
				TotalTokens:   in + out,
			},
		}
		updated, _ := m.Update(msg)
		return updated.(AppModel)
	}

	m = sendUsage(m, 100, 50)
	m = sendUsage(m, 200, 80)

	if m.totalInputTokens != 300 {
		t.Errorf("totalInputTokens = %d, want 300", m.totalInputTokens)
	}
	if m.totalOutputTokens != 130 {
		t.Errorf("totalOutputTokens = %d, want 130", m.totalOutputTokens)
	}
}

func TestNewSessionResetsSidebar(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.planEntries = []acp.PlanEntry{{Content: "old task", Status: "pending"}}
	m.totalInputTokens = 500
	m.totalOutputTokens = 200
	m.sidebarScroll = 3

	m2, _ := m.doNewSession()
	m3 := m2.(AppModel)

	if len(m3.planEntries) != 0 {
		t.Errorf("planEntries should be empty after new session, got %d", len(m3.planEntries))
	}
	if m3.totalInputTokens != 0 || m3.totalOutputTokens != 0 {
		t.Errorf("token counters should be zero after new session")
	}
	if m3.sidebarScroll != 0 {
		t.Errorf("sidebarScroll should be zero after new session, got %d", m3.sidebarScroll)
	}
}

func TestSidebarRenderedWhenWide(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 120
	m.height = 40
	m = m.recalcLayout()

	if m.sidebarWidth == 0 {
		t.Fatal("sidebarWidth should be non-zero for width=120")
	}
	// Sidebar rendering should not panic and return non-empty content.
	sidebar := m.renderSidebar()
	if sidebar == "" {
		t.Error("renderSidebar returned empty string")
	}
}

func TestSidebarHiddenWhenNarrow(t *testing.T) {
	cfg := makeTestConfig(t)
	m, _ := New(cfg)
	m.width = 40
	m.height = 24
	m = m.recalcLayout()

	if m.sidebarWidth != 0 {
		t.Errorf("sidebarWidth should be 0 for narrow terminal (width=40), got %d", m.sidebarWidth)
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
