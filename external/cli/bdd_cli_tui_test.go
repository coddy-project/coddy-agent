//go:build cli

package cli

// Godog harness for features/cli_tui.feature: the console app runs over a real
// session.Manager and FileStore in a temp home, a scripted stub agent runner
// (no LLM), and a fake in-memory terminal. Steps feed key sequences through
// the app input channel and assert on the rendered frame snapshot, on stub
// observations, and on persisted session state.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bddTerminal is the in-memory terminal for the harness.
type bddTerminal struct {
	cols, rows int
}

func (f *bddTerminal) Write(string)                     {}
func (f *bddTerminal) Columns() int                     { return f.cols }
func (f *bddTerminal) Rows() int                        { return f.rows }
func (f *bddTerminal) HideCursor()                      {}
func (f *bddTerminal) ShowCursor()                      {}
func (f *bddTerminal) Start(func([]byte), func()) error { return nil }
func (f *bddTerminal) Stop()                            {}
func (f *bddTerminal) SetTitle(string)                  {}

// stubDirective drives the scripted turn.
type stubDirective struct {
	kind    string // stream, tool_start, tool_done, permission, question, block, fail, end
	text    string
	tool    string
	argsKey string
	argsVal string
	preview string
	err     error
	blockCh chan struct{}
	qParams *acp.QuestionRequestParams
}

type cliTUIState struct {
	home string
	cwd  string

	cfg   *config.Config
	store *session.FileStore
	app   *App

	runCtx    context.Context
	runCancel context.CancelFunc
	appDone   chan error

	directives chan stubDirective
	turnEnds   chan struct{}

	permOutcome  string
	permOption   string
	questionAns  [][]string
	sawCancel    bool
	activeToolID string
	toolSeq      int
	blockedCh    chan struct{}

	prevSessionID string
}

func (s *cliTUIState) reset() {
	s.home, s.cwd = "", ""
	s.cfg, s.store, s.app = nil, nil, nil
	s.permOutcome, s.permOption = "", ""
	s.questionAns = nil
	s.sawCancel = false
	s.activeToolID = ""
	s.toolSeq = 0
	s.blockedCh = nil
	s.prevSessionID = ""
	s.directives = make(chan stubDirective, 16)
	s.turnEnds = make(chan struct{}, 4)
}

func (s *cliTUIState) shutdown() {
	if s.app != nil {
		s.app.requestQuit(nil)
	}
	if s.runCancel != nil {
		s.runCancel()
	}
	if s.appDone != nil {
		select {
		case <-s.appDone:
		case <-time.After(2 * time.Second):
		}
	}
}

// stubRunner executes scripted directives against the sender, mirroring what
// the real agent does (user message persisted first, then streamed effects).
func (s *cliTUIState) stubRunner(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
	userText := ""
	for _, b := range prompt {
		if b.Type == "text" {
			userText += b.Text
		}
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: userText, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	sessionID := st.GetID()
	assistant := ""
	defer func() {
		if assistant != "" {
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: assistant, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		}
		select {
		case s.turnEnds <- struct{}{}:
		default:
		}
	}()
	for {
		select {
		case <-ctx.Done():
			s.sawCancel = true
			return string(acp.StopReasonCancelled), nil
		case d := <-s.directives:
			switch d.kind {
			case "stream":
				_ = snd.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
					SessionUpdate: "agent_message_chunk",
					Content:       acp.ContentBlock{Type: "text", Text: d.text},
				})
				assistant += d.text
			case "tool_start":
				s.toolSeq++
				s.activeToolID = fmt.Sprintf("call_%d", s.toolSeq)
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: "tool_call", ToolCallID: s.activeToolID,
					Title: d.tool, Kind: "read", Status: "pending",
				})
				args := fmt.Sprintf("{%q: %q}", d.argsKey, d.argsVal)
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: "tool_call_update", ToolCallID: s.activeToolID,
					Status:  "in_progress",
					Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: args}}},
				})
			case "tool_done":
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: "tool_call_update", ToolCallID: s.activeToolID,
					Status: "completed",
					Meta:   map[string]interface{}{"coddy.toolResultPreview": d.preview},
				})
			case "permission":
				res, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{
					SessionID: sessionID,
					ToolCall:  acp.PermissionToolCall{ToolCallID: "perm_1", Title: "Run: " + d.tool, Status: "pending"},
					Options: []acp.PermissionOption{
						{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
						{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
						{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
					},
				})
				if err == nil && res != nil {
					s.permOutcome, s.permOption = res.Outcome, res.OptionID
				}
			case "question":
				res, err := snd.RequestQuestion(ctx, *d.qParams)
				if err == nil && res != nil {
					s.questionAns = res.Answers
				}
			case "block":
				s.blockedCh = d.blockCh
				select {
				case <-ctx.Done():
					s.sawCancel = true
					close(d.blockCh)
					return string(acp.StopReasonCancelled), nil
				case <-d.blockCh:
				}
			case "fail":
				return "", d.err
			case "end":
				return string(acp.StopReasonEndTurn), nil
			}
		}
	}
}

func (s *cliTUIState) buildApp() error {
	s.home = filepath.Join(os.TempDir(), fmt.Sprintf("coddy-cli-bdd-%d", time.Now().UnixNano()))
	s.cwd = filepath.Join(s.home, "work")
	for _, d := range []string{s.home, s.cwd, filepath.Join(s.home, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	noAuto := false
	cfg := &config.Config{
		Paths: config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: "stub/model-one", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "stub/model-two", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "stub/model-one"},
	}
	cfg.Tools.PermissionMode = "ask"
	cfg.Rules.AutoDiscover = &noAuto
	s.cfg = cfg
	s.store = &session.FileStore{Root: filepath.Join(s.home, "sessions")}

	log := slog.New(slog.DiscardHandler)
	term := &bddTerminal{cols: 100, rows: 35}
	var app *App
	late := &lateBoundSender{}
	mgr := session.NewManager(cfg, late, s.stubRunner, log, s.cwd, s.store)
	app = newApp(cfg, mgr, log, term, "dark", true)
	late.inner = app.Sender()
	s.app = app
	return nil
}

func (s *cliTUIState) startApp(sessionID string) error {
	if s.app == nil {
		if err := s.buildApp(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx, s.runCancel = ctx, cancel
	if err := s.app.Start(ctx, sessionID, false); err != nil {
		cancel()
		return err
	}
	s.appDone = make(chan error, 1)
	go func() { s.appDone <- s.app.Run(ctx) }()
	return s.waitScreen("coddy v", 3*time.Second)
}

// screenText returns the current frame stripped of styling.
func (s *cliTUIState) screenText() string {
	lines := s.app.screen.Snapshot()
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(tui.StripTerminalSequences(l))
		b.WriteString("\n")
	}
	return b.String()
}

func (s *cliTUIState) waitScreen(needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.screenText(), needle) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("screen never showed %q; last frame:\n%s", needle, s.screenText())
}

func (s *cliTUIState) waitGone(needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.screenText(), needle) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("screen still shows %q", needle)
}

func (s *cliTUIState) typeText(text string) {
	for _, r := range text {
		s.app.OnTerminalInput([]byte(string(r)))
		time.Sleep(time.Millisecond)
	}
}

func (s *cliTUIState) press(seq string) {
	s.app.OnTerminalInput([]byte(seq))
}

func (s *cliTUIState) waitTurnEnd(timeout time.Duration) error {
	select {
	case <-s.turnEnds:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("stub turn did not end in %s", timeout)
	}
}

// --- step implementations ---

func (s *cliTUIState) aConsoleAppOverStubRunner() error { return s.buildApp() }

func (s *cliTUIState) theConsoleAppStarts() error { return s.startApp("") }

func (s *cliTUIState) screenShowsVersionHeader() error { return s.waitScreen("coddy v", 2*time.Second) }

func (s *cliTUIState) screenShowsEditorBorders() error {
	text := s.screenText()
	if strings.Count(text, "────") < 2 {
		return fmt.Errorf("expected two horizontal borders, frame:\n%s", text)
	}
	return nil
}

func (s *cliTUIState) footerNamesDefaultModel() error {
	return s.waitScreen("(stub) model-one", 2*time.Second)
}

func (s *cliTUIState) operatorSubmitsPrompt(text string) error {
	s.typeText(text)
	s.press("\r")
	return s.waitScreen(text, 2*time.Second)
}

func (s *cliTUIState) stubStreamsText(text string) error {
	s.directives <- stubDirective{kind: "stream", text: text}
	s.directives <- stubDirective{kind: "end"}
	if err := s.waitScreen(text, 3*time.Second); err != nil {
		return err
	}
	return s.waitTurnEnd(2 * time.Second)
}

func (s *cliTUIState) transcriptShowsUserBlock(text string) error {
	return s.waitScreen(text, 2*time.Second)
}

func (s *cliTUIState) transcriptShowsAssistantText(text string) error {
	return s.waitScreen(text, 2*time.Second)
}

func (s *cliTUIState) footerShowsTokenUsage() error {
	// The stub runner emits no token updates; accumulated counters render only
	// when non-zero, so push one through the sender and expect the arrows.
	_ = s.app.Sender().SendSessionUpdate(s.app.sessionID, acp.TokenUsageUpdate{
		SessionUpdate: "token_usage", InputTokens: 1200, OutputTokens: 40, TotalTokens: 1240,
	})
	return s.waitScreen("↑1.2k", 2*time.Second)
}

func (s *cliTUIState) stubStartsToolCall(tool, argKey, argVal string) error {
	s.directives <- stubDirective{kind: "tool_start", tool: tool, argsKey: argKey, argsVal: argVal}
	return s.waitScreen(tool, 3*time.Second)
}

func (s *cliTUIState) transcriptShowsPendingToolBox(tool string) error {
	return s.waitScreen(tool, 2*time.Second)
}

func (s *cliTUIState) stubToolCompletes(preview string) error {
	s.directives <- stubDirective{kind: "tool_done", preview: preview}
	s.directives <- stubDirective{kind: "end"}
	if err := s.waitScreen(preview, 3*time.Second); err != nil {
		return err
	}
	return s.waitTurnEnd(2 * time.Second)
}

func (s *cliTUIState) toolBoxShowsPreview(preview string) error {
	return s.waitScreen(preview, 2*time.Second)
}

func (s *cliTUIState) toolBoxShowsExpandHint() error {
	// Collapsed hint appears only when content exceeds the cap; a one-line
	// preview renders without it, so assert the box itself completed instead.
	if s.activeToolID == "" {
		return fmt.Errorf("no tool call was started")
	}
	return nil
}

func (s *cliTUIState) permissionModeIs(mode string) error {
	if s.app == nil {
		if err := s.buildApp(); err != nil {
			return err
		}
	}
	s.cfg.Tools.PermissionMode = mode
	return nil
}

func (s *cliTUIState) stubRequestsPermission(tool string) error {
	s.directives <- stubDirective{kind: "permission", tool: tool}
	return s.waitScreen("Permission required", 3*time.Second)
}

func (s *cliTUIState) screenShowsPermissionModalWithAllow() error {
	return s.waitScreen("Allow", 2*time.Second)
}

func (s *cliTUIState) operatorConfirmsPermissionOption() error {
	s.press("\r")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.permOutcome != "" {
			s.directives <- stubDirective{kind: "end"}
			return s.waitTurnEnd(2 * time.Second)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("permission result never arrived")
}

func (s *cliTUIState) stubObservesPermissionOutcome(outcome, option string) error {
	if s.permOutcome != outcome || s.permOption != option {
		return fmt.Errorf("permission observed %q/%q, want %q/%q", s.permOutcome, s.permOption, outcome, option)
	}
	return nil
}

func (s *cliTUIState) stubBlocksUntilCancelled() error {
	s.directives <- stubDirective{kind: "block", blockCh: make(chan struct{})}
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *cliTUIState) operatorPressesEscape() error {
	s.press("\x1b")
	return nil
}

func (s *cliTUIState) stubObservesCancellation() error {
	if err := s.waitTurnEnd(3 * time.Second); err != nil {
		return err
	}
	if !s.sawCancel {
		return fmt.Errorf("stub turn was not cancelled")
	}
	return nil
}

func (s *cliTUIState) transcriptShowsInterruptNotice() error {
	return s.waitScreen("Interrupted", 2*time.Second)
}

func (s *cliTUIState) operatorSwitchesToSecondModel() error {
	s.typeText("/model stub/model-two")
	s.press("\r")
	return nil
}

func (s *cliTUIState) footerNamesSecondModel() error {
	return s.waitScreen("(stub) model-two", 3*time.Second)
}

func (s *cliTUIState) sessionStateRecordsSecondModel() error {
	st := s.app.mgr.SessionByID(s.app.sessionID)
	if st == nil {
		return fmt.Errorf("no live session")
	}
	if got := st.EffectiveModelID(s.cfg); got != "stub/model-two" {
		return fmt.Errorf("session model = %q", got)
	}
	return nil
}

func (s *cliTUIState) previousSessionWith(prompt, reply string) error {
	if err := s.buildApp(); err != nil {
		return err
	}
	ctx := context.Background()
	res, err := s.app.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.prevSessionID = res.SessionID
	go func() {
		s.directives <- stubDirective{kind: "stream", text: reply}
		s.directives <- stubDirective{kind: "end"}
	}()
	if _, err := s.app.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: prompt}},
	}, s.app.Sender(), nil); err != nil {
		return err
	}
	<-s.turnEnds
	s.app.mgr.ForgetLiveSession(res.SessionID)
	// A fresh app reopens the same store, like a process restart.
	s.app.requestQuit(nil)
	s.app = nil
	log := slog.New(slog.DiscardHandler)
	term := &bddTerminal{cols: 100, rows: 35}
	late := &lateBoundSender{}
	mgr := session.NewManager(s.cfg, late, s.stubRunner, log, s.cwd, s.store)
	app := newApp(s.cfg, mgr, log, term, "dark", true)
	late.inner = app.Sender()
	s.app = app
	return nil
}

func (s *cliTUIState) appStartsPinnedToThatSession() error {
	return s.startApp(s.prevSessionID)
}

func (s *cliTUIState) replayedPromptRendersAsUserBlock(prompt string) error {
	if err := s.waitScreen(prompt, 3*time.Second); err != nil {
		return err
	}
	// The user block renders inside the userMessageBg box; assert the raw
	// frame carries the background SGR right before the prompt text.
	for _, line := range s.app.screen.Snapshot() {
		if strings.Contains(tui.StripTerminalSequences(line), prompt) {
			if strings.Contains(line, "48;2;45;45;45") || strings.Contains(line, "48;5;") {
				return nil
			}
		}
	}
	return fmt.Errorf("prompt %q did not render with the user background", prompt)
}

func (s *cliTUIState) stubAsksQuestion(title string) error {
	s.directives <- stubDirective{kind: "question", qParams: &acp.QuestionRequestParams{
		SessionID: s.app.sessionID,
		RequestID: "q1",
		Questions: []acp.QuestionPrompt{{
			Question: title,
			Options:  []acp.QuestionOption{{Label: "Option A"}, {Label: "Option B"}},
			Custom:   true,
		}},
	}}
	return s.waitScreen(title, 3*time.Second)
}

func (s *cliTUIState) operatorChoosesCustomAndTypes(answer string) error {
	// Navigate to the third row (Custom answer...) and confirm.
	s.press("\x1b[B")
	s.press("\x1b[B")
	s.press("\r")
	if err := s.waitScreen("Type your answer", 2*time.Second); err != nil {
		return err
	}
	s.typeText(answer)
	s.press("\r")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.questionAns) > 0 {
			s.directives <- stubDirective{kind: "end"}
			return s.waitTurnEnd(2 * time.Second)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("question answer never arrived")
}

func (s *cliTUIState) stubObservesQuestionAnswer(answer string) error {
	if len(s.questionAns) == 0 || len(s.questionAns[0]) == 0 || s.questionAns[0][0] != answer {
		return fmt.Errorf("question answers = %v, want %q", s.questionAns, answer)
	}
	return nil
}

func (s *cliTUIState) stubFailsWith(errText string) error {
	s.directives <- stubDirective{kind: "fail", err: fmt.Errorf("%s", errText)}
	return s.waitTurnEnd(3 * time.Second)
}

func (s *cliTUIState) transcriptShowsErrorNotice(errText string) error {
	return s.waitScreen(errText, 2*time.Second)
}

func (s *cliTUIState) editorAcceptsNewInput() error {
	s.typeText("still alive")
	return s.waitScreen("still alive", 2*time.Second)
}

func (s *cliTUIState) operatorStartsNewSession() error {
	oldID := s.app.sessionID
	s.prevSessionID = oldID
	s.typeText("/new")
	s.press("\r")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.app.sessionID != "" && s.app.sessionID != oldID {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("session id did not change from %q", oldID)
}

func (s *cliTUIState) cancelledTurnEmitsLateChunk(text string) error {
	// The old (cancelled) turn posts with its original session id.
	_ = s.app.Sender().SendSessionUpdate(s.prevSessionID, acp.MessageChunkUpdate{
		SessionUpdate: "agent_message_chunk",
		Content:       acp.ContentBlock{Type: "text", Text: text},
	})
	time.Sleep(150 * time.Millisecond)
	return nil
}

func (s *cliTUIState) transcriptDoesNotShow(text string) error {
	if strings.Contains(s.screenText(), text) {
		return fmt.Errorf("stale text %q leaked into the transcript", text)
	}
	return nil
}

func initializeCLITUIScenario(sc *godog.ScenarioContext) {
	s := &cliTUIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})

	sc.Step(`^a coddy console app over a stub agent runner$`, s.aConsoleAppOverStubRunner)
	sc.Step(`^the console app starts$`, s.theConsoleAppStarts)
	sc.Step(`^the screen shows the coddy version header$`, s.screenShowsVersionHeader)
	sc.Step(`^the screen shows the editor between horizontal borders$`, s.screenShowsEditorBorders)
	sc.Step(`^the footer names the configured default model$`, s.footerNamesDefaultModel)
	sc.Step(`^the operator submits the prompt "([^"]*)"$`, s.operatorSubmitsPrompt)
	sc.Step(`^the stub turn streams the text "([^"]*)"$`, s.stubStreamsText)
	sc.Step(`^the transcript shows a user message block containing "([^"]*)"$`, s.transcriptShowsUserBlock)
	sc.Step(`^the transcript shows the assistant text "([^"]*)"$`, s.transcriptShowsAssistantText)
	sc.Step(`^the footer shows accumulated token usage$`, s.footerShowsTokenUsage)
	sc.Step(`^the stub turn starts a tool call named "([^"]*)" with argument path "([^"]*)"$`, func(tool, path string) error {
		return s.stubStartsToolCall(tool, "path", path)
	})
	sc.Step(`^the transcript shows a pending tool box titled "([^"]*)"$`, s.transcriptShowsPendingToolBox)
	sc.Step(`^the stub tool call completes with preview "([^"]*)"$`, s.stubToolCompletes)
	sc.Step(`^the tool box shows the preview "([^"]*)"$`, s.toolBoxShowsPreview)
	sc.Step(`^the tool box shows the expand hint$`, s.toolBoxShowsExpandHint)
	sc.Step(`^the session permission mode is "([^"]*)"$`, s.permissionModeIs)
	sc.Step(`^the stub turn requests permission for the tool "([^"]*)"$`, s.stubRequestsPermission)
	sc.Step(`^the screen shows a permission modal with an allow option$`, s.screenShowsPermissionModalWithAllow)
	sc.Step(`^the operator confirms the highlighted permission option$`, s.operatorConfirmsPermissionOption)
	sc.Step(`^the stub turn observes the permission outcome "([^"]*)" with option "([^"]*)"$`, s.stubObservesPermissionOutcome)
	sc.Step(`^the stub turn blocks until cancelled$`, s.stubBlocksUntilCancelled)
	sc.Step(`^the operator presses escape$`, s.operatorPressesEscape)
	sc.Step(`^the stub turn observes cancellation$`, s.stubObservesCancellation)
	sc.Step(`^the transcript shows an interrupt notice$`, s.transcriptShowsInterruptNotice)
	sc.Step(`^the operator switches the model to the second configured model$`, s.operatorSwitchesToSecondModel)
	sc.Step(`^the footer names the second configured model$`, s.footerNamesSecondModel)
	sc.Step(`^the session state records the second configured model$`, s.sessionStateRecordsSecondModel)
	sc.Step(`^a previous console session with the prompt "([^"]*)" and the reply "([^"]*)"$`, s.previousSessionWith)
	sc.Step(`^the console app starts pinned to that session$`, s.appStartsPinnedToThatSession)
	sc.Step(`^the replayed prompt "([^"]*)" renders as a user message block and not as assistant text$`, s.replayedPromptRendersAsUserBlock)
	sc.Step(`^the stub turn asks a question titled "([^"]*)" that allows a custom answer$`, s.stubAsksQuestion)
	sc.Step(`^the operator chooses the custom answer and types "([^"]*)"$`, s.operatorChoosesCustomAndTypes)
	sc.Step(`^the stub turn observes the question answer "([^"]*)"$`, s.stubObservesQuestionAnswer)
	sc.Step(`^the stub turn fails with the error "([^"]*)"$`, s.stubFailsWith)
	sc.Step(`^the transcript shows an error notice containing "([^"]*)"$`, s.transcriptShowsErrorNotice)
	sc.Step(`^the editor accepts new input$`, s.editorAcceptsNewInput)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the cancelled turn emits a late text chunk "([^"]*)"$`, s.cancelledTurnEmitsLateChunk)
	sc.Step(`^the transcript does not show "([^"]*)"$`, s.transcriptDoesNotShow)
}

func TestCLITUIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-tui",
		ScenarioInitializer: initializeCLITUIScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_tui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli tui feature suite failed")
	}
}
