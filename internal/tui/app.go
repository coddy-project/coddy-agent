// Package tui implements the terminal user interface for the coddy agent.
// It can be compiled independently from the ACP server layer.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts/react"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// chatEntry is a single rendered entry in the chat history.
type chatEntry struct {
	role    string // "user", "agent", "tool", "error"
	content string // for user/agent entries

	// Agent-specific: populated when the agent finishes the response.
	agentMode  string        // mode used when generating (e.g. "agent", "plan")
	agentModel string        // model ID used
	elapsed    time.Duration // generation time (0 = still running)

	// Tool-specific fields (role == "tool")
	toolID     string // tool call ID for matching status updates
	toolName   string // function name e.g. "run_command"
	toolArgs   string // raw InputJSON (set when in_progress)
	toolOutput string // execution result (set when completed/failed)
	status     string // "pending", "in_progress", "completed", "failed", "cancelled"
	expanded   bool   // whether output is visible
}

// agentRunner holds mutable agent state shared via pointer between bubbletea model copies.
type agentRunner struct {
	mu     sync.Mutex
	disp   *Dispatcher
	cancel context.CancelFunc
}

func (r *agentRunner) setCancel(cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
}

func (r *agentRunner) stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// AppModel is the bubbletea model for the TUI application.
type AppModel struct {
	// Config and session.
	cfg      *config.Config
	state    *session.State
	store    *SessionStore
	cwd      string
	appVer   string
	modelID  string
	modelIDs []string

	// Shared mutable agent runner (pointer so copies share state).
	runner *agentRunner

	// UI state.
	width  int
	height int

	viewport viewport.Model
	textarea textarea.Model
	inputOff bool

	chat      []chatEntry
	streamBuf string // accumulated streaming text; plain string avoids Builder-copy panic

	agentRunning   bool
	agentStartedAt time.Time // records when the current agent run started

	// focusedToolIdx is the m.chat index of the keyboard-focused tool entry (-1 = none).
	focusedToolIdx int

	// mdRenderer renders markdown to ANSI-styled text.
	// Stored as a pointer so model copies all share the same instance.
	mdRenderer *glamour.TermRenderer

	modal modalState

	showWelcome bool

	log *slog.Logger

	// Sidebar: plan and token tracking.
	planEntries        []acp.PlanEntry
	sidebarScroll      int // lines scrolled in the sidebar
	totalInputTokens   int
	totalOutputTokens  int

	// Derived layout widths (set by recalcLayout).
	leftWidth   int
	sidebarWidth int
}

// Config holds options for creating the TUI.
type Config struct {
	AppConfig *config.Config
	Store     *SessionStore
	CWD       string
	SessionID string // if non-empty, resume this session
	Log       *slog.Logger
}

// New creates an AppModel ready to run.
func New(cfg Config) (AppModel, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	if cfg.CWD == "" {
		var err error
		cfg.CWD, err = os.Getwd()
		if err != nil {
			cfg.CWD = "."
		}
	}

	var modelIDs []string
	for _, def := range cfg.AppConfig.Models.Defs {
		modelIDs = append(modelIDs, def.ID)
	}
	modelID := cfg.AppConfig.ModelForMode("agent")

	ta := textarea.New()
	ta.Placeholder = "Ask anything..."
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Prompt = "" // remove default "> " prompt that creates a second vertical bar

	vp := viewport.New(80, 20)

	m := AppModel{
		cfg:            cfg.AppConfig,
		store:          cfg.Store,
		cwd:            cfg.CWD,
		appVer:         version.Get(),
		modelID:        modelID,
		modelIDs:       modelIDs,
		viewport:       vp,
		textarea:       ta,
		showWelcome:    true,
		runner:         &agentRunner{},
		log:            cfg.Log,
		mdRenderer:     newMarkdownRenderer(78),
		focusedToolIdx: -1,
	}

	if cfg.SessionID != "" {
		if stored, err := cfg.Store.Load(cfg.SessionID); err == nil {
			m.state = createStateFromStored(stored)
			for _, msg := range stored.Messages {
				switch msg.Role {
				case llm.RoleUser:
					m.chat = append(m.chat, chatEntry{role: "user", content: msg.Content})
				case llm.RoleAssistant:
					m.chat = append(m.chat, chatEntry{role: "agent", content: msg.Content})
				}
			}
			m.showWelcome = false
		}
	}

	if m.state == nil {
		m.state = createFreshState(cfg.CWD)
	}

	return m, nil
}

func createFreshState(cwd string) *session.State {
	return &session.State{
		ID:   fmt.Sprintf("tui_%d", time.Now().UnixNano()),
		CWD:  cwd,
		Mode: session.ModeAgent,
	}
}

func createStateFromStored(stored *StoredSession) *session.State {
	return &session.State{
		ID:       stored.ID,
		CWD:      stored.CWD,
		Mode:     session.Mode(stored.Mode),
		Messages: stored.Messages,
	}
}

// Run creates the bubbletea program, wires the dispatcher, and blocks until exit.
// After the program exits the terminal is restored and a session-resume hint is printed.
func Run(cfg Config) error {
	m, err := New(cfg)
	if err != nil {
		return err
	}

	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
	p := tea.NewProgram(m, opts...)

	// Wire the dispatcher so the react agent can send updates to the UI.
	m.runner.disp = NewDispatcher(p.Send)

	final, err := p.Run()

	// After AltScreen exits, the terminal is back to normal.
	// Print a session-resume hint if the session had any messages.
	if final != nil {
		if m2, ok := final.(AppModel); ok {
			printSessionHint(m2)
		}
	}

	return err
}

// printSessionHint prints the session ID and resume command after the TUI exits.
func printSessionHint(m AppModel) {
	if m.state == nil {
		return
	}
	msgs := m.state.GetMessages()
	if len(msgs) == 0 {
		return
	}
	id := m.state.ID
	fmt.Printf("Continue: coddy -s %s\n", id)
}

// Init implements tea.Model.
func (m AppModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update implements tea.Model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()
		return m, nil

	case tea.KeyMsg:
		if m.modal.kind != modalNone {
			return m.updateModal(msg)
		}
		return m.updateKeys(msg)

	case AgentUpdateMsg:
		return m.handleAgentUpdate(msg)

	case AgentDoneMsg:
		return m.handleAgentDone(msg)

	case PermissionRequestMsg:
		m.modal = modalState{
			kind:       modalPermission,
			permParams: msg.Params,
			permReply:  msg.Response,
		}
		return m, nil
	}

	if m.modal.kind == modalNone && !m.inputOff {
		var taCmd tea.Cmd
		m.textarea, taCmd = m.textarea.Update(msg)
		cmds = append(cmds, taCmd)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m AppModel) updateKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Check alt+letter combinations first (before switch on msg.String()).
	if msg.Alt && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'm':
			m.modal = modalState{kind: modalModels}
			return m, nil
		}
	}

	// Sidebar scroll: ctrl+up / ctrl+down.
	if m.sidebarWidth > 0 {
		switch msg.String() {
		case "ctrl+up":
			if m.sidebarScroll > 0 {
				m.sidebarScroll--
			}
			return m, nil
		case "ctrl+down":
			m.sidebarScroll++
			return m, nil
		}
	}

	// Global shortcuts that work in all modes.
	switch msg.String() {
	case "ctrl+c":
		return m.doQuit()
	case "ctrl+p":
		m.modal = modalState{kind: modalCommands}
		return m, nil
	case "ctrl+x":
		m.inputOff = !m.inputOff
		if m.inputOff {
			m.textarea.Blur()
		} else {
			m.textarea.Focus()
			m.focusedToolIdx = -1
		}
		return m, nil
	}

	// When input is disabled, arrow/page keys scroll the viewport and
	// tab/enter control tool call expansion.
	if m.inputOff {
		switch msg.String() {
		case "tab":
			return m.focusNextTool(), nil
		case "shift+tab":
			return m.focusPrevTool(), nil
		case " ", "enter":
			return m.toggleFocusedTool(), nil
		case "esc":
			m.focusedToolIdx = -1
			return m, nil
		default:
			// Pass everything else (arrows, pgup/pgdn, home/end) to viewport.
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	// Normal (input active) mode.
	switch msg.String() {
	case "esc":
		m.runner.stop()
		return m, nil
	case "tab", "ctrl+i":
		m = m.toggleMode()
		return m, nil
	case "enter":
		if !msg.Alt {
			return m.submitInput()
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// toolEntryIndices returns the m.chat indices of all tool entries.
func (m AppModel) toolEntryIndices() []int {
	var out []int
	for i, e := range m.chat {
		if e.role == "tool" {
			out = append(out, i)
		}
	}
	return out
}

// focusNextTool moves keyboard focus to the next tool entry, wrapping around.
func (m AppModel) focusNextTool() AppModel {
	indices := m.toolEntryIndices()
	if len(indices) == 0 {
		return m
	}
	// Find current position.
	cur := -1
	for i, idx := range indices {
		if idx == m.focusedToolIdx {
			cur = i
			break
		}
	}
	if cur < len(indices)-1 {
		m.focusedToolIdx = indices[cur+1]
	} else {
		m.focusedToolIdx = indices[0]
	}
	m.refreshViewportInPlace()
	return m
}

// focusPrevTool moves keyboard focus to the previous tool entry, wrapping around.
func (m AppModel) focusPrevTool() AppModel {
	indices := m.toolEntryIndices()
	if len(indices) == 0 {
		return m
	}
	cur := -1
	for i, idx := range indices {
		if idx == m.focusedToolIdx {
			cur = i
			break
		}
	}
	if cur > 0 {
		m.focusedToolIdx = indices[cur-1]
	} else {
		m.focusedToolIdx = indices[len(indices)-1]
	}
	m.refreshViewportInPlace()
	return m
}

// toggleFocusedTool expands or collapses the currently focused tool entry.
func (m AppModel) toggleFocusedTool() AppModel {
	if m.focusedToolIdx < 0 || m.focusedToolIdx >= len(m.chat) {
		return m
	}
	e := &m.chat[m.focusedToolIdx]
	if e.role != "tool" {
		return m
	}
	e.expanded = !e.expanded
	m.refreshViewportInPlace()
	return m
}

func (m AppModel) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.modal.kind == modalPermission && m.modal.permReply != nil {
			m.modal.permReply <- &acp.PermissionResult{Outcome: "cancelled"}
		}
		m.modal = modalState{}
		return m, nil

	case "up":
		if m.modal.selected > 0 {
			m.modal.selected--
		}
		return m, nil

	case "down":
		if max := m.modalMaxSelected(); m.modal.selected < max-1 {
			m.modal.selected++
		}
		return m, nil

	case "enter":
		return m.confirmModal()

	case "backspace":
		if m.modal.kind == modalCommands && len(m.modal.query) > 0 {
			r := []rune(m.modal.query)
			m.modal.query = string(r[:len(r)-1])
			m.modal.selected = 0
		}
		return m, nil

	default:
		if m.modal.kind == modalCommands && len(msg.Runes) > 0 {
			m.modal.query += string(msg.Runes)
			m.modal.selected = 0
		}
		return m, nil
	}
}

func (m AppModel) modalMaxSelected() int {
	switch m.modal.kind {
	case modalCommands:
		return len(filteredCommands(m.modal.query))
	case modalModels:
		return len(m.modelIDs)
	case modalPermission:
		return len(m.modal.permParams.Options)
	}
	return 0
}

func (m AppModel) confirmModal() (tea.Model, tea.Cmd) {
	switch m.modal.kind {
	case modalCommands:
		cmds := filteredCommands(m.modal.query)
		var label string
		if m.modal.selected < len(cmds) {
			label = cmds[m.modal.selected].label
		}
		m.modal = modalState{}
		return m.executeCommand(label)

	case modalModels:
		if m.modal.selected < len(m.modelIDs) {
			m.modelID = m.modelIDs[m.modal.selected]
		}
		m.modal = modalState{}
		return m, nil

	case modalPermission:
		opts := m.modal.permParams.Options
		if m.modal.selected < len(opts) && m.modal.permReply != nil {
			m.modal.permReply <- &acp.PermissionResult{
				Outcome:  "allow",
				OptionID: opts[m.modal.selected].OptionID,
			}
		}
		m.modal = modalState{}
		return m, nil
	}
	return m, nil
}

func (m AppModel) executeCommand(label string) (tea.Model, tea.Cmd) {
	switch label {
	case "Switch mode":
		return m.toggleMode(), nil
	case "Switch model":
		m.modal = modalState{kind: modalModels}
		return m, nil
	case "New session":
		return m.doNewSession()
	case "Exit the app":
		return m.doQuit()
	}
	return m, nil
}

func (m AppModel) toggleMode() AppModel {
	if m.state.GetMode() == string(session.ModeAgent) {
		m.state.SetMode(string(session.ModePlan))
	} else {
		m.state.SetMode(string(session.ModeAgent))
	}
	return m
}

func (m AppModel) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" || m.agentRunning {
		return m, nil
	}

	m.textarea.Reset()
	m.showWelcome = false
	m.chat = append(m.chat, chatEntry{role: "user", content: text})
	m.streamBuf = ""
	m.agentRunning = true
	m.agentStartedAt = time.Now()
	m.refreshViewport()

	return m, m.agentCmd(text)
}

// agentCmd returns a tea.Cmd that runs the react agent and sends back messages.
func (m AppModel) agentCmd(prompt string) tea.Cmd {
	cfg := m.cfg
	state := m.state
	log := m.log
	runner := m.runner

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		runner.setCancel(cancel)
		defer cancel()

		runner.mu.Lock()
		disp := runner.disp
		runner.mu.Unlock()

		var sender acp.UpdateSender
		if disp != nil {
			sender = disp
		} else {
			sender = &noopDispatcher{}
		}

		agent := react.NewAgent(cfg, state, sender, log)
		blocks := []acp.ContentBlock{{Type: "text", Text: prompt}}
		stopReason, err := agent.Run(ctx, blocks)
		return AgentDoneMsg{
			SessionID:  state.ID,
			StopReason: stopReason,
			Err:        err,
		}
	}
}

func (m AppModel) handleAgentUpdate(msg AgentUpdateMsg) (tea.Model, tea.Cmd) {
	if delta, ok := extractTextDelta(msg.Update); ok && delta != "" {
		m.streamBuf += delta
		if len(m.chat) > 0 && m.chat[len(m.chat)-1].role == "agent" {
			m.chat[len(m.chat)-1].content = m.streamBuf
		} else {
			m.chat = append(m.chat, chatEntry{role: "agent", content: m.streamBuf})
		}
		m.refreshViewport()
		return m, nil
	}

	if modeID, ok := extractModeID(msg.Update); ok {
		m.state.SetMode(modeID)
		return m, nil
	}

	if id, title, _, ok := extractToolCall(msg.Update); ok {
		name := title
		if strings.HasPrefix(name, "Calling: ") {
			name = strings.TrimPrefix(name, "Calling: ")
		}
		m.chat = append(m.chat, chatEntry{
			role:     "tool",
			toolID:   id,
			toolName: name,
			status:   "pending",
		})
		m.refreshViewport()
		return m, nil
	}

	if id, status, content, ok := extractToolCallStatusFull(msg.Update); ok {
		for i := range m.chat {
			if m.chat[i].role == "tool" && m.chat[i].toolID == id {
				m.chat[i].status = status
				switch status {
				case "in_progress":
					if content != "" {
						m.chat[i].toolArgs = content
					}
				case "completed", "failed":
					if content != "" {
						m.chat[i].toolOutput = content
					}
				case "cancelled":
					// nothing extra
				}
				break
			}
		}
		m.refreshViewportInPlace()
		return m, nil
	}

	if entries, ok := extractPlanUpdate(msg.Update); ok {
		m.planEntries = entries
		m.sidebarScroll = 0
		return m, nil
	}

	if inputTok, outputTok, ok := extractTokenUsage(msg.Update); ok {
		m.totalInputTokens += inputTok
		m.totalOutputTokens += outputTok
		return m, nil
	}

	return m, nil
}

func (m AppModel) handleAgentDone(msg AgentDoneMsg) (tea.Model, tea.Cmd) {
	elapsed := time.Since(m.agentStartedAt)
	m.agentRunning = false
	m.runner.setCancel(nil)

	if msg.Err != nil {
		m.chat = append(m.chat, chatEntry{role: "error", content: msg.Err.Error()})
	}

	// Stamp timing/context onto the last agent entry.
	for i := len(m.chat) - 1; i >= 0; i-- {
		if m.chat[i].role == "agent" {
			m.chat[i].elapsed = elapsed
			m.chat[i].agentMode = m.state.GetMode()
			m.chat[i].agentModel = m.modelID
			break
		}
	}

	m.streamBuf = ""
	m.refreshViewport()
	m.textarea.Focus()
	return m, nil
}

func (m AppModel) doNewSession() (tea.Model, tea.Cmd) {
	m.runner.stop()
	m.saveSession()
	m.state = createFreshState(m.cwd)
	m.chat = nil
	m.streamBuf = ""
	m.agentRunning = false
	m.showWelcome = true
	m.planEntries = nil
	m.sidebarScroll = 0
	m.totalInputTokens = 0
	m.totalOutputTokens = 0
	m.refreshViewport()
	return m, nil
}

func (m AppModel) doQuit() (tea.Model, tea.Cmd) {
	m.runner.stop()
	m.saveSession()
	return m, tea.Quit
}

func (m *AppModel) saveSession() {
	if m.store == nil || m.state == nil {
		return
	}
	msgs := m.state.GetMessages()
	if len(msgs) == 0 {
		return
	}
	stored := &StoredSession{
		ID:        m.state.ID,
		CWD:       m.state.CWD,
		Mode:      m.state.GetMode(),
		Messages:  msgs,
		CreatedAt: time.Now(),
	}
	if err := m.store.Save(stored); err != nil {
		m.log.Warn("failed to save session", "error", err)
	}
}

// refreshViewport rebuilds chat content and scrolls to the bottom.
// Use this for new messages and streaming output.
// Note: mutates m.viewport in place; call on *AppModel or assign back.
func (m *AppModel) refreshViewport() {
	m.viewport.SetContent(m.renderChat())
	m.viewport.GotoBottom()
}

// refreshViewportInPlace rebuilds chat content without moving the scroll position.
// Use this for tool status updates and expansion toggles.
func (m *AppModel) refreshViewportInPlace() {
	m.viewport.SetContent(m.renderChat())
}

// minSidebarWidth is the minimum terminal width needed to show the sidebar.
const minSidebarWidth = 60

func (m AppModel) recalcLayout() AppModel {
	if m.width == 0 || m.height == 0 {
		return m
	}

	headerH := 0
	statusH := 1
	// inputH = textarea rows (3); left-border-only style adds no top/bottom lines.
	inputH := 3
	chatH := m.height - headerH - statusH - inputH - 2
	if chatH < 3 {
		chatH = 3
	}

	// Decide sidebar width: 1/5 of total width when terminal is wide enough.
	if m.width >= minSidebarWidth {
		m.sidebarWidth = m.width / 5
		if m.sidebarWidth < 15 {
			m.sidebarWidth = 15
		}
	} else {
		m.sidebarWidth = 0
	}
	m.leftWidth = m.width - m.sidebarWidth

	// Chat viewport occupies the left panel minus the scrollbar column.
	m.viewport.Width = m.leftWidth - 1
	m.viewport.Height = chatH

	// Input spans the left panel only when sidebar is present; full width otherwise.
	// NormalBorder left(1) + PaddingLeft(1) = 2 chars overhead.
	var inputW int
	if m.sidebarWidth > 0 {
		inputW = m.leftWidth - 2
	} else {
		inputW = m.width - 2
	}
	if inputW < 10 {
		inputW = 10
	}
	m.textarea.SetWidth(inputW)
	m.textarea.SetHeight(inputH)

	// Recreate markdown renderer to match viewport width (which now excludes scrollbar column).
	mdWidth := m.viewport.Width - 2
	if mdWidth < 20 {
		mdWidth = 20
	}
	m.mdRenderer = newMarkdownRenderer(mdWidth)

	m.refreshViewport()
	return m
}

// renderScrollbar builds a single-column scrollbar string of `height` lines.
func (m AppModel) renderScrollbar(height int) string {
	total := m.viewport.TotalLineCount()
	if total <= height || height <= 0 {
		// Content fits - render an empty column
		return strings.Repeat(" \n", height)
	}

	thumbH := height * height / total
	if thumbH < 1 {
		thumbH = 1
	}
	thumbOffset := int(m.viewport.ScrollPercent() * float64(height-thumbH))

	var lines []string
	for i := 0; i < height; i++ {
		if i >= thumbOffset && i < thumbOffset+thumbH {
			lines = append(lines, styleScrollThumb.Render("▐"))
		} else {
			lines = append(lines, styleScrollTrack.Render("│"))
		}
	}
	return strings.Join(lines, "\n")
}

// renderChatWithScrollbar renders the viewport and appends a 1-char scrollbar column.
func (m AppModel) renderChatWithScrollbar() string {
	vpView := m.viewport.View()
	vpLines := strings.Split(vpView, "\n")
	scrollLines := strings.Split(m.renderScrollbar(len(vpLines)), "\n")

	var b strings.Builder
	for i, line := range vpLines {
		b.WriteString(line)
		if i < len(scrollLines) {
			b.WriteString(scrollLines[i])
		}
		if i < len(vpLines)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// View implements tea.Model.
func (m AppModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.sidebarWidth > 0 {
		// Full-height split: left panel (chat+input+status) | right sidebar.
		var left strings.Builder
		if m.showWelcome && len(m.chat) == 0 {
			left.WriteString(m.renderWelcome())
		} else {
			left.WriteString(m.renderChatWithScrollbar())
		}
		left.WriteString("\n")
		left.WriteString(m.renderInput())
		left.WriteString("\n")
		left.WriteString(m.renderStatusBar())

		base := lipgloss.JoinHorizontal(lipgloss.Top, left.String(), m.renderSidebar())
		if m.modal.kind != modalNone {
			base = m.renderWithModal(base)
		}
		return base
	}

	// No sidebar: single-column layout.
	var b strings.Builder
	if m.showWelcome && len(m.chat) == 0 {
		b.WriteString(m.renderWelcome())
	} else {
		b.WriteString(m.renderChatWithScrollbar())
	}
	b.WriteString("\n")
	b.WriteString(m.renderInput())
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())

	base := b.String()
	if m.modal.kind != modalNone {
		base = m.renderWithModal(base)
	}
	return base
}

// renderSidebar builds the right-side panel with token stats and the plan.
// The panel spans the full terminal height (m.height) so it covers chat+input+status.
func (m AppModel) renderSidebar() string {
	h := m.height
	w := m.sidebarWidth
	innerW := w - 2 // border(1) + padding(1)
	if innerW < 5 {
		innerW = 5
	}

	var lines []string

	// --- Token usage section ---
	lines = append(lines, styleSidebarTitle.Render("Tokens"))
	inTok := fmt.Sprintf("in  %d", m.totalInputTokens)
	outTok := fmt.Sprintf("out %d", m.totalOutputTokens)
	total := m.totalInputTokens + m.totalOutputTokens
	totTok := fmt.Sprintf("tot %d", total)
	lines = append(lines,
		styleSidebarTokenValue.Render(truncateLine(inTok, innerW)),
		styleSidebarTokenValue.Render(truncateLine(outTok, innerW)),
		styleSidebarTokenValue.Render(truncateLine(totTok, innerW)),
	)

	// Divider
	lines = append(lines, styleDivider.Render(strings.Repeat("-", innerW)))

	// --- Plan section ---
	lines = append(lines, styleSidebarSection.Render("Plan"))

	if len(m.planEntries) == 0 {
		lines = append(lines, styleSidebarScrollHint.Render("no plan yet"))
	} else {
		for i, e := range m.planEntries {
			checkbox := "[ ]"
			var itemStyle lipgloss.Style
			switch e.Status {
			case "completed":
				checkbox = "[x]"
				itemStyle = stylePlanItemCompleted
			case "in_progress":
				checkbox = "[>]"
				itemStyle = stylePlanItemInProgress
			case "failed":
				checkbox = "[!]"
				itemStyle = stylePlanItemFailed
			case "cancelled":
				checkbox = "[-]"
				itemStyle = stylePlanItemPending
			default:
				itemStyle = stylePlanItemPending
			}
			prefix := fmt.Sprintf("%d %s ", i+1, checkbox)
			content := prefix + e.Content
			lines = append(lines, itemStyle.Render(truncateLine(content, innerW)))
		}
	}

	// Apply scroll offset.
	contentLines := lines
	scrollable := len(contentLines) > h
	if m.sidebarScroll > 0 && m.sidebarScroll < len(contentLines) {
		contentLines = contentLines[m.sidebarScroll:]
	}

	// Pad or trim to exactly h lines.
	for len(contentLines) < h {
		contentLines = append(contentLines, "")
	}
	visible := contentLines
	if len(visible) > h {
		visible = visible[:h]
		// Show scroll hint on last visible line if there's more content.
		visible[h-1] = styleSidebarScrollHint.Render("-- more --")
	}
	if scrollable && m.sidebarScroll > 0 && len(visible) > 0 {
		visible[0] = styleSidebarScrollHint.Render("-- more --")
	}

	content := strings.Join(visible, "\n")
	return styleSidebarBorder.
		Width(innerW).
		Height(h).
		Render(content)
}

func (m AppModel) renderHeader() string {
	return styleHeader.Render("coddy")
}

func (m AppModel) renderStatusBar() string {
	mode := m.state.GetMode()
	var modeStyle lipgloss.Style
	if mode == string(session.ModePlan) {
		modeStyle = styleModePlan
	} else {
		modeStyle = styleModeAgent
	}
	modeTag := modeStyle.Render("[" + mode + "]")

	var hints string
	switch {
	case m.agentRunning:
		hints = modeTag + "  " + styleDone.Render("thinking...") + "  esc cancel"
	case m.inputOff:
		hints = modeTag + "  " + styleWarn.Render("input off") + "  ctrl+x enable  tab tool  space expand  arrows scroll"
	default:
		hints = modeTag + "  tab mode  ctrl+m model  ctrl+p commands  ctrl+x input"
	}

	hints = styleStatusBar.Render(hints)

	// Right side: model name + version
	right := lipgloss.NewStyle().Foreground(colorSubtle).Render(m.modelID) +
		"  " + styleVersion.Render(m.appVer)

	statusW := m.width
	if m.sidebarWidth > 0 {
		statusW = m.leftWidth
	}
	gap := statusW - visibleWidth(stripAnsi(hints)) - visibleWidth(right) - 2
	if gap < 1 {
		gap = 1
	}
	return hints + strings.Repeat(" ", gap) + right
}

func (m AppModel) renderInput() string {
	panelW := m.width
	if m.sidebarWidth > 0 {
		panelW = m.leftWidth
	}
	if m.inputOff {
		// Width() in lipgloss includes padding but not border.
		// Total visual = border(1) + Width = panelW, so Width = panelW - 1.
		contentW := panelW - 1
		if contentW < 10 {
			contentW = 10
		}
		return styleInputBorder.Width(contentW).Render(styleHint.Render("input disabled (ctrl+x to enable)"))
	}
	return styleInputBorder.Render(m.textarea.View())
}

func (m AppModel) renderChat() string {
	if len(m.chat) == 0 {
		return ""
	}
	var b strings.Builder
	for i, e := range m.chat {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.renderEntry(i, e))
	}
	return b.String()
}

func (m AppModel) renderEntry(idx int, e chatEntry) string {
	// Content width = viewport width (scrollbar already deducted).
	w := m.viewport.Width
	if w < 10 {
		w = 10
	}
	switch e.role {
	case "user":
		return m.renderUserMessage(e, w)

	case "agent":
		body := m.renderMarkdown(e.content)
		header := styleAgentLabel.Render("coddy") + "\n"
		footer := m.renderAgentFooter(e)
		return header + body + footer

	case "tool":
		return m.renderToolEntry(idx, e)

	case "error":
		return styleErr.Render("Error: "+e.content) + "\n"
	}
	return e.content + "\n"
}

// renderUserMessage renders a user message with a blue left border,
// matching the input field style.
func (m AppModel) renderUserMessage(e chatEntry, w int) string {
	// Width includes left padding (1). Left border takes 1 char outside Width.
	// Total visual = 1(border) + w-1(Width) = w.
	contentW := w - 1
	if contentW < 4 {
		contentW = 4
	}
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorSubtle).
		Foreground(colorFg).
		PaddingLeft(1).
		Width(contentW).
		Render(e.content)
	return box + "\n"
}

// renderAgentFooter renders the "mode · model · time" line after an agent response.
func (m AppModel) renderAgentFooter(e chatEntry) string {
	if e.elapsed == 0 {
		return ""
	}
	secs := fmt.Sprintf("%.1fs", e.elapsed.Seconds())
	line := "  " + e.agentMode + " · " + e.agentModel + " · " + secs
	return styleAgentFooter.Render(line) + "\n"
}

// renderToolEntry renders a tool call entry in OpenCode box style.
//
// Pending/in_progress: header + small status box
// Completed/failed collapsed: header + preview box with "enter to expand" footer
// Completed/failed expanded: header + raw output (no box)
func (m AppModel) renderToolEntry(idx int, e chatEntry) string {
	isFocused := idx == m.focusedToolIdx

	// Label style based on status
	var labelStyle lipgloss.Style
	switch e.status {
	case "completed":
		labelStyle = styleToolDone
	case "failed":
		labelStyle = styleToolFailed
	case "in_progress":
		labelStyle = styleToolCall
	default:
		labelStyle = styleToolPending
	}
	if isFocused {
		labelStyle = styleToolFocused
	}

	name := e.toolName
	if name == "" {
		name = "tool"
	}

	var b strings.Builder

	// Header line: "  run_command:  > git status"
	header := "  " + name + ":"
	if e.toolArgs != "" {
		if summary := toolCallSummary(e.toolName, e.toolArgs); summary != "" {
			header += "  " + summary
		}
	}
	b.WriteString(labelStyle.Render(header) + "\n\n")

	// Box dimensions.
	// viewport.Width already accounts for the scrollbar column.
	// Box total visual width = indent(2) + border(2) + padding(2) + content = m.viewport.Width
	// So contentW = m.viewport.Width - 2(indent) - 2(border) - 2(padding) = m.viewport.Width - 6
	vpW := m.viewport.Width
	if vpW < 14 {
		vpW = 14
	}
	const boxIndent = 2
	contentW := vpW - boxIndent - 4 // 4 = border(2) + padding(2)
	if contentW < 10 {
		contentW = 10
	}

	borderColor := colorBorder
	if isFocused {
		borderColor = colorHighlight
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(contentW).
		Padding(0, 1)

	var boxContent string

	switch {
	case e.expanded && (e.status == "completed" || e.status == "failed"):
		// Expanded: raw indented output, no box
		if e.toolOutput != "" {
			lines := strings.Split(strings.TrimRight(e.toolOutput, "\n"), "\n")
			for _, l := range lines {
				b.WriteString(styleToolOutput.Render("    "+truncateLine(l, contentW+2)) + "\n")
			}
		}
		return b.String()

	case e.status == "completed" || e.status == "failed":
		// Collapsed with output: preview box
		var previewLines []string
		if e.toolOutput != "" {
			rawLines := strings.Split(strings.TrimRight(e.toolOutput, "\n"), "\n")
			const maxPreview = 3
			for i, l := range rawLines {
				if i >= maxPreview {
					previewLines = append(previewLines, styleHint.Render("..."))
					break
				}
				previewLines = append(previewLines, truncateLine(stripAnsi(l), contentW-2))
			}
		}
		hint := "enter to expand"
		if isFocused {
			hint = "↵ expand"
		}
		if len(previewLines) > 0 {
			boxContent = strings.Join(previewLines, "\n") + "\n\n" + styleHint.Render(hint)
		} else {
			boxContent = styleHint.Render(hint)
		}

	default:
		// Pending or in_progress: minimal status box
		switch e.status {
		case "in_progress":
			boxContent = styleToolCall.Render("running...")
		default:
			boxContent = styleToolPending.Render("waiting...")
		}
	}

	// Render box with left indent
	rendered := boxStyle.Render(boxContent)
	for i, line := range strings.Split(rendered, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.Repeat(" ", boxIndent) + line)
	}
	b.WriteString("\n")

	return b.String()
}

// truncateLine shortens a plain-text line to maxW runes, adding "..." if needed.
func truncateLine(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW < 4 {
		return "..."
	}
	return string(runes[:maxW-3]) + "..."
}

// toolCallSummary returns a short human-readable line describing the tool args.
func toolCallSummary(toolName, argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	switch toolName {
	case "run_command":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) == nil && a.Command != "" {
			return a.Command
		}
	case "read_file", "write_file", "apply_diff", "create_file":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) == nil && a.Path != "" {
			return a.Path
		}
	case "list_dir":
		var a struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) == nil && a.Path != "" {
			if a.Recursive {
				return a.Path + "  (recursive)"
			}
			return a.Path
		}
	case "search_files":
		var a struct {
			Query     string `json:"query"`
			Directory string `json:"directory"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) == nil {
			if a.Directory != "" {
				return a.Query + "  in  " + a.Directory
			}
			return a.Query
		}
	}
	// Fallback: truncated raw JSON
	if len(argsJSON) > 80 {
		return argsJSON[:77] + "..."
	}
	return argsJSON
}

func (m AppModel) renderWelcome() string {
	var b strings.Builder
	vHeight := m.viewport.Height
	if vHeight < 1 {
		vHeight = 20
	}

	logoStr := logo()
	logoLines := strings.Split(logoStr, "\n")
	contentH := len(logoLines) + 4
	topPad := (vHeight - contentH) / 2
	if topPad < 0 {
		topPad = 0
	}

	for i := 0; i < topPad; i++ {
		b.WriteString("\n")
	}
	for _, line := range logoLines {
		pad := (m.width - visibleWidth(stripAnsi(line))) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}
	b.WriteString("\n")

	sid := styleSessionID.Render("Session   " + m.state.ID)
	padSid := (m.width - visibleWidth(sid)) / 2
	if padSid < 0 {
		padSid = 0
	}
	b.WriteString(strings.Repeat(" ", padSid) + sid + "\n")

	hint := styleHint.Render("Continue: coddy -s " + m.state.ID)
	padH := (m.width - visibleWidth(hint)) / 2
	if padH < 0 {
		padH = 0
	}
	b.WriteString(strings.Repeat(" ", padH) + hint)

	dir := styleCurrentPath.Render(m.cwd)
	padD := (m.width - visibleWidth(dir)) / 2
	if padD < 0 {
		padD = 0
	}
	b.WriteString(strings.Repeat(" ", padD) + dir + "\n")

	rendered := b.String()
	lines := strings.Count(rendered, "\n")
	for i := lines; i < vHeight; i++ {
		b.WriteString("\n")
	}

	return b.String()
}

func (m AppModel) renderWithModal(base string) string {
	var modal string
	switch m.modal.kind {
	case modalCommands:
		modal = renderCommandsModal(m.modal.query, m.modal.selected)
	case modalModels:
		modal = renderModelsModal(m.modelIDs, m.modelID, m.modal.selected)
	case modalPermission:
		modal = renderPermissionModal(m.modal.permParams, m.modal.selected)
	}

	lines := strings.Split(base, "\n")
	return centerOverlay(base, modal, m.width, len(lines))
}

func wrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}
	var b strings.Builder
	for _, para := range strings.Split(text, "\n") {
		plain := []rune(stripAnsi(para))
		if len(plain) <= maxWidth {
			b.WriteString(para + "\n")
			continue
		}
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			if line == "" {
				line = w
			} else if len([]rune(line))+1+len([]rune(w)) <= maxWidth {
				line += " " + w
			} else {
				b.WriteString(line + "\n")
				line = w
			}
		}
		if line != "" {
			b.WriteString(line + "\n")
		}
	}
	result := b.String()
	return strings.TrimRight(result, "\n")
}

// newMarkdownRenderer creates a glamour renderer for the given content width.
// WithStylePath("dark") avoids the OSC-11 terminal query that WithAutoStyle()
// sends, which causes garbage output and a 10-20 second startup delay.
func newMarkdownRenderer(width int) *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	return r
}

// renderMarkdown renders markdown text to ANSI. Falls back to plain text on error.
// During streaming the content is partial, so glamour may produce slightly rough
// output - that's acceptable; it corrects itself once the turn is complete.
func (m AppModel) renderMarkdown(content string) string {
	if m.mdRenderer == nil || content == "" {
		return styleAgentMessage.Render(content) + "\n"
	}
	rendered, err := m.mdRenderer.Render(content)
	if err != nil {
		return styleAgentMessage.Render(content) + "\n"
	}
	return rendered
}

// Style aliases used in app.go.
var (
	styleDone        = lipgloss.NewStyle().Foreground(colorSuccess)
	styleWarn        = lipgloss.NewStyle().Foreground(colorWarning)
	styleErr         = lipgloss.NewStyle().Foreground(colorError)
	styleCurrentPath = lipgloss.NewStyle().Foreground(colorSubtle).Italic(true)
)

// noopDispatcher is used when the bubbletea program isn't yet wired.
type noopDispatcher struct{}

func (d *noopDispatcher) SendSessionUpdate(_ string, _ interface{}) error { return nil }
func (d *noopDispatcher) RequestPermission(_ context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}
