//go:build cli

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

// appTerminal is the terminal contract the app needs beyond rendering.
type appTerminal interface {
	tui.Terminal
	Start(onInput func([]byte), onResize func()) error
	Stop()
	SetTitle(title string)
}

// turnDone is an internal loop message posted when a prompt worker returns.
type turnDone struct {
	sessionID string
	stop      string
	err       error
}

// App is the interactive console application: one UI goroutine over a
// session manager, rendering ACP updates into a component tree.
type App struct {
	cfg *config.Config
	mgr backend
	log *slog.Logger

	// remoteURL is set when mgr talks to a remote coddy http server.
	remoteURL string
	// configOpts is the last adopted session option set (model catalog).
	configOpts []acp.ConfigOption
	// toolFullCache and toolFullPending back the async remote ctrl+o expand.
	toolFullCache   map[string]string
	toolFullPending map[string]bool
	term            appTerminal
	theme           *tui.Theme

	screen *tui.MainScreen

	// UI tree.
	header     *header
	chat       *tui.Container
	status     *tui.Container
	plan       *planWidget
	editorWrap *tui.Container
	editor     *tui.Editor
	foot       *footer

	spinner       *tui.Loader
	stepStatus    liveStatus
	stepBlocked   string
	turnActive    bool
	turnSessionID string
	switching     bool
	pendingSwitch func()
	readyPending  string
	lastCtrlC     time.Time
	expanded      bool
	hideThink     bool
	themeName     string
	plain         bool

	// Streaming state.
	curAssistant *assistantMessage
	curMemory    *assistantMessage
	toolBoxes    map[string]*toolBox
	lastToolID   string

	// Local shell state (the `!!` prefix): the running command, its block, and
	// the most recent block, which ctrl+o expands.
	shellActive   bool
	shellStopping bool
	shellCmd      *shell.OperatorCommand
	curShell      *shellBox
	lastShell     *shellBox

	sessionID string
	modeID    string
	modelID   string
	reasoning string

	slashServer []tui.AutocompleteItem

	updatesCh chan updateMsg
	permCh    chan permRequest
	questCh   chan questRequest
	inputCh   chan []byte
	pasteCh   chan []byte
	resizeCh  chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	doneCh    chan struct{}
	workers   sync.WaitGroup
	workCtx   context.Context
	workStop  context.CancelFunc

	modal tui.Component

	mdTheme tui.MarkdownTheme

	quitErr error
}

// newApp assembles the application over an existing manager and terminal.
func newApp(cfg *config.Config, mgr backend, log *slog.Logger, term appTerminal, themeName string, plain bool) *App {
	a := &App{
		cfg:             cfg,
		mgr:             mgr,
		log:             log,
		term:            term,
		themeName:       themeName,
		plain:           plain,
		toolBoxes:       map[string]*toolBox{},
		toolFullCache:   map[string]string{},
		toolFullPending: map[string]bool{},
		updatesCh:       make(chan updateMsg, 1024),
		permCh:          make(chan permRequest),
		questCh:         make(chan questRequest),
		inputCh:         make(chan []byte, 64),
		pasteCh:         make(chan []byte, 8),
		resizeCh:        make(chan struct{}, 1),
		closed:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
	a.workCtx, a.workStop = context.WithCancel(context.Background())
	a.applyTheme(themeName)
	a.buildTree()
	return a
}

// Sender returns the acp.UpdateSender facade for the manager and turns.
func (a *App) Sender() acp.UpdateSender { return &sender{app: a} }

func (a *App) applyTheme(name string) {
	a.themeName = name
	a.theme = newTheme(name)
	a.mdTheme = markdownTheme(a.theme, !a.plain)
}

func (a *App) buildTree() {
	a.screen = tui.NewMainScreen(a.term)
	a.screen.SetClearOnShrink(false)

	a.header = newHeader(a.theme)
	a.chat = &tui.Container{}
	a.status = &tui.Container{}
	a.plan = newPlanWidget(a.theme)
	a.editor = tui.NewEditor(a.term, tui.EditorTheme{BorderColor: a.theme.FgFn(roleBorderMuted)}, 0)
	a.editor.OnSubmit = a.onSubmit
	a.editor.OnChange = a.onEditorChange
	a.editorWrap = &tui.Container{}
	a.editorWrap.AddChild(a.editor)
	a.foot = newFooter(a.theme, a.cfg.Paths.CWD)

	root := a.screen.Root
	root.AddChild(a.header)
	root.AddChild(a.chat)
	root.AddChild(a.status)
	root.AddChild(a.plan)
	root.AddChild(a.editorWrap)
	root.AddChild(a.foot)
	a.screen.SetFocus(a.editor)

	provider := newCompletionProvider(a.cfg.Paths.CWD, a.slashCatalog)
	a.editor.SetAutocomplete(provider, selectListTheme(a.theme), tui.SelectListLayout{MinPrimaryColumnWidth: 12, MaxPrimaryColumnWidth: 32}, a.screen.RequestRender)
}

// Start begins a session (new or pinned) and populates the header.
func (a *App) Start(ctx context.Context, sessionID string, resume bool) error {
	if resume {
		if err := a.pickSessionBlocking(ctx); err != nil {
			return err
		}
	} else {
		if sessionID != "" {
			if err := session.ValidateFolderSessionID(sessionID); err != nil {
				return fmt.Errorf("--session-id: %w", err)
			}
			a.mgr.SetPreferredSessionID(sessionID)
		}
		res, err := a.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: a.cfg.Paths.CWD})
		if err != nil {
			return fmt.Errorf("session/new: %w", err)
		}
		a.adoptSession(res.SessionID, res.Modes, res.ConfigOptions)
		// Ready (slash catalog + deferred replay for reopened bundles) fires
		// once the UI loop is draining updates, so a large replay can never
		// fill the channel while nothing consumes it.
		a.readyPending = res.SessionID
	}
	if a.remoteURL != "" {
		a.appendStatus(roleDim, "remote: "+tui.SanitizeText(a.remoteURL))
	}
	a.populateHeader()
	return nil
}

// ApplyStartupOptions applies --model/--mode/--permission-mode.
func (a *App) ApplyStartupOptions(ctx context.Context, model, mode, permMode string) error {
	if model != "" {
		if _, err := a.mgr.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
			SessionID: a.sessionID, ConfigID: "model", Value: model,
		}); err != nil {
			return fmt.Errorf("--model: %w", err)
		}
		a.modelID = model
	}
	if mode != "" {
		if err := a.mgr.HandleSessionSetMode(ctx, acp.SessionSetModeParams{SessionID: a.sessionID, ModeID: mode}); err != nil {
			return fmt.Errorf("--mode: %w", err)
		}
		a.modeID = mode
	}
	if permMode != "" {
		if _, err := a.mgr.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
			SessionID: a.sessionID, ConfigID: "permission_mode", Value: permMode,
		}); err != nil {
			return fmt.Errorf("--permission-mode: %w", err)
		}
	}
	a.refreshFooterModel()
	return nil
}

func (a *App) adoptSession(id string, modes *acp.ModeState, opts []acp.ConfigOption) {
	a.sessionID = id
	a.reasoning = ""
	if modes != nil {
		a.modeID = modes.CurrentModeID
	}
	a.configOpts = opts
	for _, opt := range opts {
		if opt.ID == "model" {
			a.modelID = opt.CurrentValue
		}
	}
	if a.modelID == "" {
		a.modelID = a.cfg.Agent.Model
	}
	a.refreshFooterModel()
	a.foot.SetSession("", a.modeID)
}

func (a *App) refreshFooterModel() {
	reasoning := a.reasoning
	if reasoning == "" {
		if st := a.mgr.SessionByID(a.sessionID); st != nil {
			reasoning = st.EffectiveReasoning(a.cfg)
		}
	}
	if reasoning == "" {
		if entry := a.cfg.FindModelEntry(a.modelID); entry != nil {
			reasoning = entry.ReasoningDefault
		}
	}
	a.foot.SetModel(a.modelID, reasoning)
}

func (a *App) populateHeader() {
	var contextFiles []string
	contextFiles = append(contextFiles, a.cfg.Instructions.Files...)
	var skillNames []string
	rulesCount := 0
	if st := a.mgr.SessionByID(a.sessionID); st != nil {
		for _, sk := range st.GetSkills() {
			skillNames = append(skillNames, sk.Name)
		}
		rulesCount = len(st.GetRulesCatalog())
	}
	var mcpNames []string
	for _, srv := range a.cfg.MCPServers {
		mcpNames = append(mcpNames, srv.Name)
	}
	a.header.SetSections(contextFiles, skillNames, rulesCount, mcpNames)
}

// Run drives the UI loop until quit. The terminal must already be started.
func (a *App) Run(ctx context.Context) error {
	defer close(a.doneCh)
	if id := a.readyPending; id != "" {
		a.readyPending = ""
		a.workers.Add(1)
		go func() {
			defer a.workers.Done()
			a.mgr.HandleSessionReady(id)
		}()
	}
	render := a.newRenderScheduler()
	render.now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.closed:
			// requestQuit closed the app: leave without waiting for the next
			// event (double ctrl+c used to hang here until another key).
			return a.quitErr
		case data, ok := <-a.inputCh:
			if !ok {
				return nil
			}
			if a.handleGlobalKey(data) {
				if a.quitRequested() {
					return a.quitErr
				}
				render.now()
				continue
			}
			a.dispatchInput(data)
			render.now()
		case body := <-a.pasteCh:
			a.handlePaste(body)
			render.now()
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
			render.throttled()
		case req := <-a.permCh:
			a.openPermissionModal(req)
			render.now()
		case req := <-a.questCh:
			a.openQuestionModal(req)
			render.now()
		case <-a.resizeCh:
			render.now()
		case <-a.screen.RenderSignal():
			render.throttled()
		case <-render.timerC():
			render.fire()
		}
		if a.quitRequested() {
			return a.quitErr
		}
	}
}

// Close releases the loop channels and cancels turn workers (idempotent). A
// running `!!` command dies with the console; its own worker does the killing
// (see startLocalShell), because Close can be called from another goroutine
// and the command handle belongs to the UI loop.
func (a *App) Close() {
	a.closeOnce.Do(func() {
		close(a.closed)
		a.workStop()
	})
}

// JoinWorkers waits for in-flight turn workers up to the timeout so
// persistence and lock release finish before the process exits.
func (a *App) JoinWorkers(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		a.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

type renderScheduler struct {
	app     *App
	last    time.Time
	timer   *time.Timer
	pending bool
}

func (a *App) newRenderScheduler() *renderScheduler { return &renderScheduler{app: a} }

func (r *renderScheduler) now() {
	r.pending = false
	r.last = time.Now()
	r.app.screen.RenderNow()
}

func (r *renderScheduler) throttled() {
	since := time.Since(r.last)
	if since >= 16*time.Millisecond {
		r.now()
		return
	}
	if !r.pending {
		r.pending = true
		r.timer = time.NewTimer(16*time.Millisecond - since)
	}
}

func (r *renderScheduler) timerC() <-chan time.Time {
	if r.timer == nil {
		return nil
	}
	return r.timer.C
}

func (r *renderScheduler) fire() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.pending {
		r.now()
	}
}

func (a *App) quitRequested() bool {
	select {
	case <-a.closed:
		return true
	default:
		return false
	}
}

// OnTerminalInput feeds raw terminal sequences (called from the read loop via
// the stdin buffer).
func (a *App) OnTerminalInput(seq []byte) {
	select {
	case a.inputCh <- seq:
	case <-a.closed:
	}
}

// OnTerminalPaste feeds a bracketed paste body.
func (a *App) OnTerminalPaste(body []byte) {
	select {
	case a.pasteCh <- body:
	case <-a.closed:
	}
}

// OnTerminalResize signals a size change.
func (a *App) OnTerminalResize() {
	select {
	case a.resizeCh <- struct{}{}:
	default:
	}
}

// handleGlobalKey processes app-level bindings before the focused component.
func (a *App) handleGlobalKey(data []byte) bool {
	key, ok := tui.ParseKey(data)
	if !ok {
		return false
	}
	// Modal open: esc handled by the modal itself; global keys suspended.
	if a.modal != nil {
		return false
	}
	switch key.String() {
	case "escape":
		if a.editor.AutocompleteOpen() {
			return false // editor closes its own menu
		}
		if a.shellActive {
			a.stopLocalShell()
			return true
		}
		if a.turnActive {
			a.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: a.sessionID})
			a.appendStatus(roleWarning, "Interrupted by escape")
			return true
		}
		return false
	case "ctrl+c":
		if !a.editor.IsEmpty() {
			a.editor.SetText("")
			return true
		}
		if time.Since(a.lastCtrlC) < 2*time.Second {
			a.requestQuit(nil)
			return true
		}
		a.lastCtrlC = time.Now()
		a.appendStatus(roleDim, "Press ctrl+c again to exit")
		return true
	case "ctrl+d":
		if a.editor.IsEmpty() {
			a.requestQuit(nil)
			return true
		}
		return false
	case "ctrl+l":
		a.openModelSelector()
		return true
	case "ctrl+p":
		a.cycleModel(1)
		return true
	case "ctrl+shift+p":
		a.cycleModel(-1)
		return true
	case "ctrl+o":
		a.toggleExpanded()
		return true
	case "ctrl+t":
		a.toggleThinking()
		return true
	case "shift+tab":
		a.cycleReasoning()
		return true
	}
	return false
}

func (a *App) dispatchInput(data []byte) {
	if a.modal != nil {
		if h, ok := a.modal.(tui.InputHandler); ok {
			h.HandleInput(data)
		}
		return
	}
	// The loop renders right after dispatch, so route to the focused
	// component directly instead of the renderer's render-on-input helper.
	if h, ok := a.screen.Focused().(tui.InputHandler); ok {
		h.HandleInput(data)
	}
}

func (a *App) requestQuit(err error) {
	a.quitErr = err
	a.Close()
}

// appendStatus adds a dim status row to the transcript.
func (a *App) appendStatus(role, msg string) {
	a.chat.AddChild(newStatusLine(a.theme, role, msg))
}

// --- modal management ---

func (a *App) openModal(c tui.Component) {
	a.modal = c
	a.editorWrap.Clear()
	a.editorWrap.AddChild(c)
	if f, ok := c.(tui.Focusable); ok {
		f.SetFocused(true)
	}
}

func (a *App) closeModal() {
	// The gate is answered; the status line goes back to the gated step itself
	// (an approved tool only starts executing now, so its clock restarts too).
	a.unblockStatus()
	a.modal = nil
	a.editorWrap.Clear()
	a.editorWrap.AddChild(a.editor)
	a.screen.SetFocus(a.editor)
}

func (a *App) openPermissionModal(req permRequest) {
	a.blockStatus("Waiting for your approval")
	m := newPermissionModal(a.theme, req.params, a.screen.RequestRender)
	m.OnDone = func(res *acp.PermissionResult) {
		req.reply <- res
		a.closeModal()
		a.screen.RequestRender()
	}
	a.openModal(m)
}

func (a *App) openQuestionModal(req questRequest) {
	a.blockStatus("Waiting for your answer")
	m := newQuestionModal(a.theme, req.params, a.screen.RequestRender)
	m.OnDone = func(res *acp.QuestionResult) {
		req.reply <- res
		a.closeModal()
		a.screen.RequestRender()
	}
	a.openModal(m)
}

// --- submit / turn ---

func (a *App) onSubmit(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	a.editor.AddToHistory(text)
	if a.dispatchLocalShell(text) {
		return
	}
	text = unescapeLocalShell(text)
	if a.dispatchSlash(text) {
		return
	}
	a.submitPrompt(text)
}

func (a *App) submitPrompt(text string) {
	if a.turnActive {
		a.appendStatus(roleWarning, "A turn is already running (escape to interrupt)")
		return
	}
	if a.shellActive {
		a.appendStatus(roleWarning, "A local command is running (escape to stop it)")
		return
	}
	a.chat.AddChild(newUserMessage(a.theme, text))
	a.curAssistant = nil
	a.stepStatus = newWaitingStatus()
	a.stepBlocked = ""
	a.startSpinner()
	a.turnActive = true
	sessionID := a.sessionID
	a.turnSessionID = sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		res, err := a.mgr.HandleSessionPromptWithSender(a.workCtx, acp.SessionPromptParams{
			SessionID: sessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: text}},
		}, a.Sender(), nil)
		stop := ""
		if res != nil {
			stop = string(res.StopReason)
		}
		select {
		case a.updatesCh <- updateMsg{sessionID: sessionID, update: turnDone{sessionID: sessionID, stop: stop, err: err}}:
		case <-a.closed:
		}
	}()
}

func (a *App) startSpinner() {
	if a.spinner == nil {
		a.spinner = tui.NewLoader(a.screen.RequestRender, a.theme.FgFn(roleAccent), a.theme.FgFn(roleMuted), statusWaitingModel)
		// Rendered per frame so the elapsed counter ticks off the loader's own 80 ms
		// tick instead of a second timer.
		a.spinner.SetMessageFunc(a.statusMessage)
	}
	a.status.Clear()
	a.status.AddChild(a.spinner)
	a.spinner.Start()
	if !a.plain {
		a.term.SetTitle("coddy · working")
	}
}

func (a *App) stopSpinner() {
	if a.spinner != nil {
		a.spinner.Stop()
	}
	a.status.Clear()
	a.status.AddChild(tui.NewSpacer(1))
	if !a.plain {
		a.term.SetTitle("coddy")
	}
}

// --- model / reasoning / expand toggles ---

func (a *App) modelIDs() []string {
	if values := a.adoptedModelValues(); len(values) > 0 {
		return values
	}
	var ids []string
	for _, m := range a.cfg.Models {
		ids = append(ids, m.Model)
	}
	return ids
}

// adoptedModelValues lists the model option values from the current session
// options (empty when the backend advertised none).
func (a *App) adoptedModelValues() []string {
	for _, opt := range a.configOpts {
		if opt.ID != "model" {
			continue
		}
		var values []string
		for _, v := range opt.Options {
			values = append(values, v.Value)
		}
		return values
	}
	return nil
}

func (a *App) openModelSelector() {
	if a.busyWithLocalShell() {
		return
	}
	ids := a.modelIDs()
	items := make([]tui.SelectItem, 0, len(ids))
	current := 0
	for i, id := range ids {
		items = append(items, tui.SelectItem{Value: id, Label: tui.SanitizeText(id)})
		if id == a.modelID {
			current = i
		}
	}
	sel := newSelectorModal(a.theme, "Select model", items, 10, a.screen.RequestRender)
	sel.list.SetSelectedIndex(current)
	sel.OnDone = func(item *tui.SelectItem) {
		a.closeModal()
		if item == nil {
			a.screen.RequestRender()
			return
		}
		a.setModel(item.Value)
	}
	a.openModal(sel)
}

func (a *App) setModel(id string) {
	sessionID := a.sessionID
	go func() {
		if _, err := a.mgr.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
			SessionID: sessionID, ConfigID: "model", Value: id,
		}); err != nil {
			_ = a.Sender().SendSessionUpdate(sessionID, statusErr{msg: "model: " + err.Error()})
			return
		}
	}()
}

// statusErr is an internal update rendered as an error status line. Errors
// from an abandoned session are dropped unless always is set (session-switch
// failures must surface even though the ids no longer match).
type statusErr struct {
	msg    string
	always bool
}

func (a *App) cycleModel(dir int) {
	ids := a.modelIDs()
	if len(ids) == 0 {
		return
	}
	cur := 0
	for i, id := range ids {
		if id == a.modelID {
			cur = i
			break
		}
	}
	next := (cur + dir + len(ids)) % len(ids)
	a.setModel(ids[next])
}

func (a *App) cycleReasoning() {
	entry := a.cfg.FindModelEntry(a.modelID)
	// Cycle the levels the model actually offers, not just a configured override:
	// ReasoningLevelsFor is the same resolved, provider-aware list the composer and
	// GET /v1/models expose, so shift+tab works for auto-detected reasoning models too.
	levels := a.cfg.ReasoningLevelsFor(entry)
	if entry == nil || len(levels) == 0 {
		return
	}
	cur := -1
	for i, l := range levels {
		if l == a.reasoning {
			cur = i
			break
		}
	}
	next := levels[(cur+1+len(levels))%len(levels)]
	a.reasoning = next
	if st := a.mgr.SessionByID(a.sessionID); st != nil {
		st.SetSelectedReasoning(next)
	}
	a.refreshFooterModel()
}

func (a *App) toggleExpanded() {
	a.expanded = !a.expanded
	a.header.SetExpanded(a.expanded)
	if tb, ok := a.toolBoxes[a.lastToolID]; ok {
		tb.SetExpanded(a.expanded)
	}
	if a.lastShell != nil {
		a.lastShell.SetExpanded(a.expanded)
	}
}

func (a *App) toggleThinking() {
	a.hideThink = !a.hideThink
	// The toggle is global: every rendered assistant/memory block follows.
	for _, child := range a.chat.Children() {
		if msg, ok := child.(*assistantMessage); ok {
			msg.SetHideThinking(a.hideThink)
		}
	}
}

// loadToolResult reads the persisted full tool output for expand. Locally
// this is a disk read; remotely the fetch runs on a worker goroutine (the
// REST call can take seconds) and the box re-expands when the result lands.
func (a *App) loadToolResult(toolCallID string) (string, bool) {
	if full, ok := a.toolFullCache[toolCallID]; ok {
		return full, true
	}
	if a.remoteURL == "" {
		return a.mgr.ToolCallResult(a.sessionID, toolCallID)
	}
	if a.toolFullPending[toolCallID] {
		return "", false
	}
	a.toolFullPending[toolCallID] = true
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		full, ok := a.mgr.ToolCallResult(sessionID, toolCallID)
		select {
		case a.updatesCh <- updateMsg{sessionID: sessionID, update: toolFullLoaded{id: toolCallID, text: full, ok: ok}}:
		case <-a.closed:
		}
	}()
	return "", false
}

// toolFullLoaded delivers an asynchronously fetched full tool result.
type toolFullLoaded struct {
	id   string
	text string
	ok   bool
}

// pickSessionBlocking shows the resume picker before the UI loop starts.
func (a *App) pickSessionBlocking(ctx context.Context) error {
	cwd := a.cfg.Paths.CWD
	res, err := a.mgr.HandleSessionList(ctx, acp.SessionListParams{CWD: &cwd})
	if err != nil {
		return fmt.Errorf("session list: %w", err)
	}
	if len(res.Sessions) == 0 {
		newRes, err := a.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: a.cfg.Paths.CWD})
		if err != nil {
			return fmt.Errorf("session/new: %w", err)
		}
		a.adoptSession(newRes.SessionID, newRes.Modes, newRes.ConfigOptions)
		go a.mgr.HandleSessionReady(newRes.SessionID)
		return nil
	}
	items := make([]tui.SelectItem, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		label := s.SessionID
		if s.Title != nil && *s.Title != "" {
			label = tui.SanitizeText(*s.Title)
		}
		desc := ""
		if s.UpdatedAt != nil {
			desc = *s.UpdatedAt
		}
		items = append(items, tui.SelectItem{Value: s.SessionID, Label: label, Description: desc})
	}
	sel := newSelectorModal(a.theme, "Resume Session", items, 10, a.screen.RequestRender)
	choice := make(chan *tui.SelectItem, 1)
	sel.OnDone = func(item *tui.SelectItem) { choice <- item }
	a.openModal(sel)
	a.screen.RenderNow()

	// Drive a minimal input loop until a choice lands.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data := <-a.inputCh:
			a.dispatchInput(data)
			a.screen.RenderNow()
		case item := <-choice:
			a.closeModal()
			if item == nil {
				newRes, err := a.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: a.cfg.Paths.CWD})
				if err != nil {
					return fmt.Errorf("session/new: %w", err)
				}
				a.adoptSession(newRes.SessionID, newRes.Modes, newRes.ConfigOptions)
				go a.mgr.HandleSessionReady(newRes.SessionID)
				return nil
			}
			return a.loadSession(ctx, item.Value)
		}
	}
}

// loadSession loads an existing session (worker goroutine drains updates in
// the background because replay is synchronous).
func (a *App) loadSession(ctx context.Context, id string) error {
	a.resetTranscript()
	a.sessionID = id
	type loadResult struct {
		err  error
		mode string
		opts []acp.ConfigOption
	}
	resCh := make(chan loadResult, 1)
	go func() {
		res, err := a.mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: id, CWD: a.cfg.Paths.CWD})
		mode := ""
		var opts []acp.ConfigOption
		if err == nil && res != nil {
			if res.Modes != nil {
				mode = res.Modes.CurrentModeID
			}
			opts = res.ConfigOptions
		}
		a.mgr.HandleSessionReady(id)
		resCh <- loadResult{err: err, mode: mode, opts: opts}
	}()
	// Drain replay updates while the load runs.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		case res := <-resCh:
			if res.err != nil {
				return fmt.Errorf("session load: %w", res.err)
			}
			if res.mode != "" {
				a.modeID = res.mode
			}
			if len(res.opts) > 0 {
				a.configOpts = res.opts
				for _, opt := range res.opts {
					if opt.ID == "model" {
						a.modelID = opt.CurrentValue
					}
				}
			}
			// Drain any remaining queued replay updates.
			for {
				select {
				case msg := <-a.updatesCh:
					a.applyLoopMessage(msg)
				default:
					a.refreshFooterModel()
					a.foot.SetSession("", a.modeID)
					return nil
				}
			}
		}
	}
}

// onEditorChange tints the editor borders while the buffer holds a `!!`
// command, so it is visible before enter that the text runs locally instead
// of reaching the model (pi's bash-mode border).
func (a *App) onEditorChange(string) {
	// Read the buffer the way submit will deliver it - trimmed, with paste
	// markers expanded - so the border promises exactly what enter will do:
	// "  !!rm -rf x" and a collapsed paste starting with !! both run.
	if _, ok := parseLocalCommand(a.editor.PendingText()); ok {
		a.editor.SetBorderColor(a.theme.FgFn(roleBashMode))
		return
	}
	a.editor.SetBorderColor(a.theme.FgFn(roleBorderMuted))
}

func (a *App) resetTranscript() {
	a.chat.Clear()
	a.plan.SetEntries(nil)
	a.toolBoxes = map[string]*toolBox{}
	a.curShell, a.lastShell = nil, nil
	a.toolFullCache = map[string]string{}
	a.toolFullPending = map[string]bool{}
	a.curAssistant = nil
	a.curMemory = nil
	a.lastToolID = ""
	a.foot.ResetTokens()
}

// newSession starts a fresh session (used by /new). Switches serialize: a
// second switch while one is in flight is refused, and a switch during a
// live turn waits for that turn to end before touching manager state.
func (a *App) newSession() {
	if a.switching {
		a.appendStatus(roleWarning, "A session switch is already in progress")
		return
	}
	if a.shellActive {
		// The transcript reset would detach the running block and its output.
		a.appendStatus(roleWarning, "A local command is running (escape to stop it)")
		return
	}
	a.switching = true
	a.deferUntilTurnEnd(func() { a.startNewSessionWorker(a.sessionID) })
}

// deferUntilTurnEnd runs fn now when idle, or after the active turn's
// turnDone arrives (the turn is cancelled to make that prompt).
func (a *App) deferUntilTurnEnd(fn func()) {
	if !a.turnActive {
		fn()
		return
	}
	a.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: a.turnSessionID})
	a.pendingSwitch = fn
}

func (a *App) startNewSessionWorker(old string) {
	a.resetTranscript()
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		res, err := a.mgr.HandleSessionNew(a.workCtx, acp.SessionNewParams{CWD: a.cfg.Paths.CWD})
		if err != nil {
			_ = a.Sender().SendSessionUpdate(old, statusErr{msg: "new session: " + err.Error(), always: true})
			return
		}
		if old != "" && old != res.SessionID {
			a.mgr.ForgetLiveSession(old)
		}
		select {
		case a.updatesCh <- updateMsg{sessionID: res.SessionID, update: sessionSwitched{res: res}}:
		case <-a.closed:
		}
		a.mgr.HandleSessionReady(res.SessionID)
	}()
}

// sessionSwitched is an internal update completing /new.
type sessionSwitched struct{ res *acp.SessionNewResult }

// slashCatalog merges server-advertised commands with client-side ones.
func (a *App) slashCatalog() []tui.AutocompleteItem {
	var items []tui.AutocompleteItem
	items = append(items,
		tui.AutocompleteItem{Value: "model", Label: "model", Description: "Select model (opens selector UI)"},
		tui.AutocompleteItem{Value: "mode", Label: "mode", Description: "Switch between agent and plan mode"},
		tui.AutocompleteItem{Value: "resume", Label: "resume", Description: "Resume another session"},
		tui.AutocompleteItem{Value: "new", Label: "new", Description: "Start a new session"},
		tui.AutocompleteItem{Value: "theme", Label: "theme", Description: "Switch color theme"},
		tui.AutocompleteItem{Value: "hotkeys", Label: "hotkeys", Description: "Show keyboard shortcuts"},
		tui.AutocompleteItem{Value: "quit", Label: "quit", Description: "Exit coddy"},
	)
	items = append(items, a.slashServer...)
	return items
}

// refreshServerCommands converts an AvailableCommandsUpdate into catalog rows.
func (a *App) refreshServerCommands(cmds []acp.AvailableCommand) {
	var items []tui.AutocompleteItem
	for _, c := range cmds {
		name := tui.SanitizeText(strings.TrimPrefix(c.Name, "/"))
		items = append(items, tui.AutocompleteItem{Value: name, Label: name, Description: tui.SanitizeText(c.Description)})
	}
	a.slashServer = items
}

// handlePaste routes bracketed-paste bodies: the editor when no modal is
// open, the question modal's custom editor when it is typing, and dropped
// otherwise (pastes must never leak into a hidden editor behind a modal).
func (a *App) handlePaste(body []byte) {
	if a.modal == nil {
		a.editor.InsertPaste(string(body))
		return
	}
	if qm, ok := a.modal.(*questionModal); ok {
		qm.InsertPaste(string(body))
	}
}

// ExitHint names the finished session and the command that reopens it,
// printed after the terminal is restored (pi/opencode-style resume hint).
func (a *App) ExitHint() string {
	if a.sessionID == "" {
		return ""
	}
	if a.remoteURL != "" {
		return fmt.Sprintf("session: %s\ncontinue: coddy --remote %s --session-id %s  (or: coddy --remote %s -c)",
			a.sessionID, a.remoteURL, a.sessionID, a.remoteURL)
	}
	return fmt.Sprintf("session: %s\ncontinue: coddy cli --session-id %s  (or: coddy -c)", a.sessionID, a.sessionID)
}

// StartContinue reopens the most recent session recorded for this folder
// (the -c/--continue flag).
func (a *App) StartContinue(ctx context.Context) error {
	id, err := latestBackendSessionID(ctx, a.mgr, a.cfg.Paths.CWD)
	if err != nil {
		return err
	}
	return a.Start(ctx, id, false)
}
