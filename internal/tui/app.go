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

	agentRunning bool

	// focusedToolIdx is the m.chat index of the keyboard-focused tool entry (-1 = none).
	focusedToolIdx int

	// mdRenderer renders markdown to ANSI-styled text.
	// Stored as a pointer so model copies all share the same instance.
	mdRenderer *glamour.TermRenderer

	modal modalState

	showWelcome bool

	log *slog.Logger
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
	fmt.Printf("Continue: coddy -s %s\n\n", id)
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

	return m, nil
}

func (m AppModel) handleAgentDone(msg AgentDoneMsg) (tea.Model, tea.Cmd) {
	m.agentRunning = false
	m.runner.setCancel(nil)

	if msg.Err != nil {
		m.chat = append(m.chat, chatEntry{role: "error", content: msg.Err.Error()})
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

func (m AppModel) recalcLayout() AppModel {
	if m.width == 0 || m.height == 0 {
		return m
	}

	headerH := 1
	statusH := 1
	inputH := 5
	chatH := m.height - headerH - statusH - inputH - 2
	if chatH < 3 {
		chatH = 3
	}

	m.viewport.Width = m.width
	m.viewport.Height = chatH

	inputW := m.width - 2
	if inputW < 10 {
		inputW = 10
	}
	m.textarea.SetWidth(inputW)
	m.textarea.SetHeight(3)

	// Recreate the markdown renderer so code blocks wrap at the new width.
	mdWidth := m.width - 4
	if mdWidth < 20 {
		mdWidth = 20
	}
	m.mdRenderer = newMarkdownRenderer(mdWidth)

	m.refreshViewport()
	return m
}

// View implements tea.Model.
func (m AppModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	if m.showWelcome && len(m.chat) == 0 {
		b.WriteString(m.renderWelcome())
	} else {
		b.WriteString(m.viewport.View())
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

func (m AppModel) renderHeader() string {
	mode := m.state.GetMode()
	var modeStyle lipgloss.Style
	if mode == string(session.ModePlan) {
		modeStyle = styleModePlan
	} else {
		modeStyle = styleModeAgent
	}

	left := styleHeader.Render("coddy") + "  " + modeStyle.Render("["+mode+"]")
	right := lipgloss.NewStyle().Foreground(colorSubtle).Render(m.modelID)

	gap := m.width - visibleWidth(stripAnsi(left)) - visibleWidth(right) - 2
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m AppModel) renderStatusBar() string {
	var hints string
	switch {
	case m.agentRunning:
		hints = styleDone.Render("thinking...") + "  esc to cancel"
	case m.inputOff:
		hints = styleWarn.Render("[input off]") + "  ctrl+x enable  tab tool  space expand  arrows scroll"
	default:
		hints = "tab mode  ctrl+m model  ctrl+p commands  ctrl+x input"
	}

	hints = styleStatusBar.Render(hints)
	ver := styleVersion.Render(m.appVer)
	gap := m.width - visibleWidth(stripAnsi(hints)) - visibleWidth(ver) - 2
	if gap < 1 {
		gap = 1
	}
	return hints + strings.Repeat(" ", gap) + ver
}

func (m AppModel) renderInput() string {
	if m.inputOff {
		return styleInputBorder.Render(styleHint.Render("input disabled (ctrl+x to enable)"))
	}
	st := styleInputBorderFocused
	if m.agentRunning {
		st = styleInputBorder
	}
	return st.Render(m.textarea.View())
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
	w := m.width - 2
	if w < 10 {
		w = 10
	}
	switch e.role {
	case "user":
		return styleUserLabel.Render("You") + "\n" + wrapText(styleUserMessage.Render(e.content), w) + "\n"

	case "agent":
		body := m.renderMarkdown(e.content)
		return styleAgentLabel.Render("coddy") + "\n" + body

	case "tool":
		return m.renderToolEntry(idx, e)

	case "error":
		return styleErr.Render("Error: "+e.content) + "\n"
	}
	return e.content + "\n"
}

// renderToolEntry renders a collapsible tool call entry.
// Collapsed: [+] tool_name:\n      > command
// Expanded:  [-] tool_name:\n      > command\n\n      output...
func (m AppModel) renderToolEntry(idx int, e chatEntry) string {
	isFocused := idx == m.focusedToolIdx

	// Choose the expand/collapse toggle icon
	openClose := "+"
	if e.expanded {
		openClose = "-"
	}

	// Choose style based on status
	var s lipgloss.Style
	switch e.status {
	case "completed":
		s = styleToolDone
	case "failed":
		s = styleToolFailed
	case "in_progress":
		s = styleToolCall
	default:
		s = styleToolPending
	}
	if isFocused {
		s = styleToolFocused
	}

	name := e.toolName
	if name == "" {
		name = "tool"
	}
	header := s.Render(fmt.Sprintf("  [%s] %s:", openClose, name))

	var b strings.Builder
	b.WriteString(header + "\n")

	// Show the command / args summary (always visible, even collapsed)
	if e.toolArgs != "" {
		summary := toolCallSummary(e.toolName, e.toolArgs)
		if summary != "" {
			b.WriteString(styleToolArgs.Render("      "+summary) + "\n")
		}
	}

	// Show the output only when expanded
	if e.expanded && e.toolOutput != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(strings.TrimRight(e.toolOutput, "\n"), "\n") {
			b.WriteString(styleToolOutput.Render("      "+line) + "\n")
		}
	}

	return b.String()
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
			return "> " + a.Command
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
	b.WriteString(strings.Repeat(" ", padH) + hint + "\n")

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
