//go:build cli

package cli

// Godog harness for features/cli_tui.feature: the console app runs over a real
// session.Manager and FileStore in a temp home, a scripted stub agent runner
// (no LLM), and a fake in-memory terminal. Steps feed key sequences through
// the app input channel and assert on the rendered frame snapshot, on stub
// observations, and on persisted session state.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// neuralDeepUsageBDDKey is the stored hub login of the console scenarios.
const neuralDeepUsageBDDKey = "sk-cli-bdd-usage-key-0123456789ab"

// usageStandPayload renders the hub's schema-1 GET /limits answer for a pro
// wallet key with the session counter at used (limit 15000).
func usageStandPayload(used int) string {
	return `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","tier_expires_at":null,
 "unlimited_volume":false,"options":[],"bypass":false,"fair_use":true,
 "key":{"name":"coddy","status":"ok","billing_mode":"wallet","cap":null},
 "decision":{"scope":"chat","can_request":true,"blockers":[],"retry_after_sec":null},
 "chat":{"session":{"used":` + strconv.Itoa(used) + `,"limit":15000,"remaining":` + strconv.Itoa(15000-used) + `,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":2,"limit":120,"remaining":118,"reset_in_sec":58},"cooldown_sec":0,"scope":"account"},
 "daily_capacity":{"pct_used":0.0,"exhausted":false,"resets_at":"2026-09-07T00:00:00+00:00"},
 "night":{"enabled":true,"active":false,"capacity_factor":2},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},"kimi":null}`
}

// usageClock is the manager's clock in the neuraldeep scenarios: real time
// plus whatever the steps added, so the pacing floor can be crossed at will.
type usageClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *usageClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *usageClock) advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

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
	mu   sync.Mutex
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
	prompts      []string
	sawCancel    bool
	activeToolID string
	toolSeq      int
	blockedCh    chan struct{}

	prevSessionID string

	printOut  *syncBuffer
	printDone chan error

	// The neuraldeep scenarios: the manager, the stand-in GET /limits, its
	// session counter, the manager clock, and the environment to restore.
	mgr         *session.Manager
	usageStand  *httptest.Server
	usageUsed   int
	usageClock  *usageClock
	prevBaseEnv string
	prevKeyEnv  string
	usageEnvSet bool
	// usageAsked counts the stand-in's requests (under mu).
	usageAsked int
}

// syncBuffer is a goroutine-safe string sink for the one-shot print steps.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *cliTUIState) reset() {
	s.home, s.cwd = "", ""
	s.cfg, s.store, s.app = nil, nil, nil
	s.permOutcome, s.permOption = "", ""
	s.questionAns = nil
	s.prompts = nil
	s.sawCancel = false
	s.activeToolID = ""
	s.toolSeq = 0
	s.blockedCh = nil
	s.prevSessionID = ""
	s.printOut = nil
	s.printDone = nil
	s.mgr = nil
	s.usageStand = nil
	s.usageUsed = 407
	s.usageClock = nil
	s.usageEnvSet = false
	s.usageAsked = 0
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
	// The stand-in is selected through a process-wide variable: no usage
	// fetch may outlive the scenario, or it lands on the next one's server.
	if s.mgr != nil {
		s.mgr.ShutdownProviderUsage(2 * time.Second)
	}
	if s.usageStand != nil {
		s.usageStand.Close()
		s.usageStand = nil
	}
	if s.usageEnvSet {
		_ = os.Setenv(llm.EnvNeuralDeepBaseURL, s.prevBaseEnv)
		_ = os.Setenv("NEURALDEEP_API_KEY", s.prevKeyEnv)
		s.usageEnvSet = false
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
	s.mu.Lock()
	s.prompts = append(s.prompts, userText)
	s.mu.Unlock()
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
			s.mu.Lock()
			s.sawCancel = true
			s.mu.Unlock()
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
				lineCount := strings.Count(d.preview, "\n") + 1
				var meta map[string]interface{}
				if lineCount > 10 {
					meta = map[string]interface{}{
						"coddy": map[string]interface{}{
							"toolResultPreview": map[string]interface{}{
								"truncated": true, "totalLines": lineCount + 5, "previewLines": lineCount,
							},
						},
					}
				}
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: "tool_call_update", ToolCallID: s.activeToolID,
					Status:  "completed",
					Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: d.preview}}},
					Meta:    meta,
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
					s.mu.Lock()
					s.permOutcome, s.permOption = res.Outcome, res.OptionID
					s.mu.Unlock()
				}
			case "question":
				// The real agent runs the question tool like any other call:
				// a tool_call block opens, the arguments stream in, the
				// operator answers, and the JSON result closes the block.
				s.toolSeq++
				s.activeToolID = fmt.Sprintf("call_%d", s.toolSeq)
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallUpdate{
					SessionUpdate: "tool_call", ToolCallID: s.activeToolID,
					Title: "question", Kind: "other", Status: "pending",
				})
				args, _ := json.Marshal(map[string]interface{}{"questions": d.qParams.Questions})
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: "tool_call_update", ToolCallID: s.activeToolID,
					Status:  "in_progress",
					Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: string(args)}}},
				})
				res, err := snd.RequestQuestion(ctx, *d.qParams)
				var answers [][]string
				if err == nil && res != nil {
					answers = res.Answers
					s.mu.Lock()
					s.questionAns = answers
					s.mu.Unlock()
				}
				result, _ := json.Marshal(acp.QuestionResult{Answers: answers})
				_ = snd.SendSessionUpdate(sessionID, acp.ToolCallStatusUpdate{
					SessionUpdate: "tool_call_update", ToolCallID: s.activeToolID,
					Status:  "completed",
					Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: string(result)}}},
				})
			case "block":
				s.blockedCh = d.blockCh
				select {
				case <-ctx.Done():
					s.mu.Lock()
					s.sawCancel = true
					s.mu.Unlock()
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

func (s *cliTUIState) buildApp() error { return s.buildAppWith(false) }

// buildAppWith assembles the console; with neuraldeep the default model
// belongs to a neuraldeep provider whose stored hub login points at a
// stand-in GET /limits, and the manager runs on the scenario's clock.
func (s *cliTUIState) buildAppWith(neuraldeep bool) error {
	return s.buildAppWithUsagePanel(neuraldeep, true)
}

// buildAppWithUsagePanel is buildAppWith with the neuraldeep row's usage
// limits panel switched on or off (providers[].usage_limits_panel).
func (s *cliTUIState) buildAppWithUsagePanel(neuraldeep, panel bool) error {
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
	if neuraldeep {
		if err := s.startUsageStand(); err != nil {
			return err
		}
		if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(s.home, "neuraldeep"), neuralDeepUsageBDDKey, "https://hub.bdd.invalid", llm.NeuralDeepClientID, "coddy"); err != nil {
			return err
		}
		row := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
		if !panel {
			off := false
			row.UsageLimitsPanel = &off
		}
		cfg.Providers = append(cfg.Providers, row)
		cfg.Models = append(cfg.Models, config.ModelEntry{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 1000, MaxContextTokens: 100000})
		cfg.Agent.Model = "neuraldeep/qwen3.8-27b"
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
	if neuraldeep {
		s.usageClock = &usageClock{}
		mgr.SetProviderUsageClock(s.usageClock.Now, func(d time.Duration, fn func()) func() bool {
			return time.AfterFunc(d, fn).Stop
		})
	}
	s.mgr = mgr
	app = newApp(cfg, mgr, log, term, "dark", true)
	late.inner = app.Sender()
	s.app = app
	return nil
}

// startUsageStand serves GET /limits with the scenario's session counter
// and routes the neuraldeep provider at it.
func (s *cliTUIState) startUsageStand() error {
	s.usageStand = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/limits" || r.Header.Get("Authorization") != "Bearer "+neuralDeepUsageBDDKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		s.usageAsked++
		used := s.usageUsed
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, usageStandPayload(used))
	}))
	s.prevBaseEnv = os.Getenv(llm.EnvNeuralDeepBaseURL)
	s.prevKeyEnv = os.Getenv("NEURALDEEP_API_KEY")
	s.usageEnvSet = true
	if err := os.Setenv(llm.EnvNeuralDeepBaseURL, s.usageStand.URL); err != nil {
		return err
	}
	return os.Setenv("NEURALDEEP_API_KEY", "")
}

// --- provider usage steps ---

func (s *cliTUIState) aConsoleAppWithNeuralDeep() error { return s.buildAppWith(true) }

func (s *cliTUIState) aConsoleAppWithNeuralDeepPanelOff() error {
	return s.buildAppWithUsagePanel(true, false)
}

// standNeverAsked joins the manager's fetches first, so a request still in
// flight would be counted.
func (s *cliTUIState) standNeverAsked() error {
	if s.mgr != nil {
		if err := s.mgr.WaitProviderUsageIdle(2 * time.Second); err != nil {
			return err
		}
	}
	s.mu.Lock()
	n := s.usageAsked
	s.mu.Unlock()
	if n != 0 {
		return fmt.Errorf("the stand-in limits API was asked %d times, want none", n)
	}
	return nil
}

func (s *cliTUIState) standReportsSessionAt(pct int) error {
	s.mu.Lock()
	s.usageUsed = pct * 15000 / 100
	s.mu.Unlock()
	return nil
}

func (s *cliTUIState) footerShowsUsage(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliTUIState) transcriptShowsUsageNotice(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliTUIState) usageClockMoves(seconds int) error {
	if s.usageClock == nil {
		return fmt.Errorf("no usage clock: the scenario has no neuraldeep provider")
	}
	s.usageClock.advance(time.Duration(seconds) * time.Second)
	return nil
}

func (s *cliTUIState) operatorSubmitsCommand(text string) error {
	s.typeText(text)
	s.press("\r")
	return nil
}

func (s *cliTUIState) usageReportShows(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

func (s *cliTUIState) operatorSwitchesModelTo(id string) error {
	s.typeText("/model " + id)
	s.press("\r")
	return nil
}

func (s *cliTUIState) footerNamesModel(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

// footerHidesUsage waits for the usage line to leave the frame: the model
// switch renders asynchronously, so the absence is polled rather than read
// once.
func (s *cliTUIState) footerHidesUsage() error {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.screenText(), "3h 3%") {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the usage line is still on screen:\n%s", s.screenText())
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
	if s.printDone != nil {
		// One-shot print mode has no screen; the output buffer is asserted
		// by the dedicated step.
		return s.waitTurnEnd(2 * time.Second)
	}
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

func (s *cliTUIState) stubToolCompletesWithLines(count int) error {
	var lines []string
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("preview line %d", i))
	}
	s.directives <- stubDirective{kind: "tool_done", preview: strings.Join(lines, "\n")}
	s.directives <- stubDirective{kind: "end"}
	if err := s.waitScreen("preview line 1", 3*time.Second); err != nil {
		return err
	}
	return s.waitTurnEnd(2 * time.Second)
}

// statusLineShows waits for the spinner's status message. The verb and its target are
// composed only by the status line, so "Reading README.md" cannot come from the tool box.
func (s *cliTUIState) statusLineShows(text string) error {
	return s.waitScreen(text, 3*time.Second)
}

// stubToolCompletesWithoutEndingTurn finishes the call but leaves the turn running, which
// is the state where the status line has to fall back to waiting on the model.
func (s *cliTUIState) stubToolCompletesWithoutEndingTurn() error {
	s.directives <- stubDirective{kind: "tool_done", preview: "done"}
	return s.waitScreen("done", 3*time.Second)
}

func (s *cliTUIState) toolBoxShowsPreview(preview string) error {
	return s.waitScreen(preview, 2*time.Second)
}

func (s *cliTUIState) toolBoxShowsExpandHint() error {
	// The 14-line preview exceeds the 10-line collapse cap, so the box must
	// advertise expansion.
	return s.waitScreen("ctrl+o to expand", 2*time.Second)
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
		s.mu.Lock()
		got := s.permOutcome
		s.mu.Unlock()
		if got != "" {
			s.directives <- stubDirective{kind: "end"}
			return s.waitTurnEnd(2 * time.Second)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("permission result never arrived")
}

// operatorAllowsPermissionKeepingTurn confirms the highlighted option but leaves the
// turn running, which is the state where the status line has to name the gated tool
// again rather than claim the model is being waited on.
func (s *cliTUIState) operatorAllowsPermissionKeepingTurn() error {
	s.press("\r")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := s.permOutcome
		s.mu.Unlock()
		if got != "" {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("permission result never arrived")
}

func (s *cliTUIState) stubObservesPermissionOutcome(outcome, option string) error {
	s.mu.Lock()
	gotOutcome, gotOption := s.permOutcome, s.permOption
	s.mu.Unlock()
	if gotOutcome != outcome || gotOption != option {
		return fmt.Errorf("permission observed %q/%q, want %q/%q", gotOutcome, gotOption, outcome, option)
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
	s.mu.Lock()
	saw := s.sawCancel
	s.mu.Unlock()
	if !saw {
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
		s.mu.Lock()
		n := len(s.questionAns)
		s.mu.Unlock()
		if n > 0 {
			s.directives <- stubDirective{kind: "end"}
			return s.waitTurnEnd(2 * time.Second)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("question answer never arrived")
}

// The option texts of a real question are sentences, not menu entries: the
// label of the recommendation and the explanation under it both have to stay
// readable at 100 columns.
const (
	bddLongOptionLabel = "Relay and node both on nas02 (recommended)"
	bddLongOptionDesc  = "A self-contained pair on nas02 itself: the relay listens on a port and the node dials it, " +
		"so the swarm survives the laptop going away."
)

func (s *cliTUIState) stubAsksQuestionWithLongDescriptions() error {
	s.directives <- stubDirective{kind: "question", qParams: &acp.QuestionRequestParams{
		SessionID: s.app.sessionID,
		RequestID: "q2",
		Questions: []acp.QuestionPrompt{{
			Header:   "Swarm topology",
			Question: "Where should the relay live?",
			Options: []acp.QuestionOption{
				{Label: bddLongOptionLabel, Description: bddLongOptionDesc},
				{Label: "Relay on the laptop, node only on nas02", Description: "The swarm is visible from the laptop only."},
			},
		}},
	}}
	return s.waitScreen("Where should the relay live?", 3*time.Second)
}

func (s *cliTUIState) questionModalShowsWholeFirstLabel() error {
	return s.waitScreen(bddLongOptionLabel, 2*time.Second)
}

// The description sits under the list, wrapped: its tail is on a row of its
// own instead of being cut off at the end of the option row.
func (s *cliTUIState) questionModalWrapsSelectedDescription() error {
	if err := s.waitScreen("A self-contained pair on nas02 itself", 2*time.Second); err != nil {
		return err
	}
	return s.waitScreen("swarm survives the laptop going away.", 2*time.Second)
}

func (s *cliTUIState) operatorChoosesHighlightedOption() error {
	s.press("\r")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := len(s.questionAns)
		s.mu.Unlock()
		if n > 0 {
			s.directives <- stubDirective{kind: "end"}
			return s.waitTurnEnd(2 * time.Second)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("question answer never arrived")
}

func (s *cliTUIState) transcriptShowsQuestionAnswered(question, answer string) error {
	if err := s.waitScreen(question, 3*time.Second); err != nil {
		return err
	}
	return s.waitScreen("\u2192 "+answer, 3*time.Second)
}

func (s *cliTUIState) stubObservesQuestionAnswer(answer string) error {
	s.mu.Lock()
	ans := s.questionAns
	s.mu.Unlock()
	if len(ans) == 0 || len(ans[0]) == 0 || ans[0][0] != answer {
		return fmt.Errorf("question answers = %v, want %q", ans, answer)
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
	// Captured before any input is sent: the later sessionID write on the UI
	// goroutine is ordered after this read via the input channel.
	s.prevSessionID = s.app.sessionID
	s.typeText("/new")
	s.press("\r")
	// The status line confirms adoption; reading the frame snapshot is
	// mutex-guarded, so no direct app-field polling is needed.
	return s.waitScreen("Started new session", 3*time.Second)
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

// --- local shell (`!!`) steps ---

func (s *cliTUIState) operatorRunsLocalCommand(command string) error {
	s.typeText(localShellPrefix + command)
	s.press("\r")
	return s.waitScreen("$ "+command, 5*time.Second)
}

func (s *cliTUIState) transcriptShowsLocalShellBlock(command string) error {
	return s.waitScreen("$ "+command, 3*time.Second)
}

// localShellBlockShowsOutput matches a whole row, not a substring of the
// frame: the block title repeats the command, so `echo hidden` would "find"
// its own output even with rendering completely broken.
func (s *cliTUIState) localShellBlockShowsOutput(text string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range s.app.screen.Snapshot() {
			if strings.TrimSpace(tui.StripTerminalSequences(line)) == text {
				return nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("no row held the command output %q; last frame:\n%s", text, s.screenText())
}

// noAgentTurnReceived proves the private half of the feature: no prompt the
// stub runner ever saw carries the command or its output.
func (s *cliTUIState) noAgentTurnReceived(text string) error {
	s.mu.Lock()
	prompts := append([]string(nil), s.prompts...)
	s.mu.Unlock()
	if len(prompts) == 0 {
		return fmt.Errorf("no agent turn ran, so the assertion proves nothing")
	}
	for _, p := range prompts {
		if strings.Contains(p, text) {
			return fmt.Errorf("agent prompt %q carried %q", p, text)
		}
	}
	return nil
}

// persistedSessionCarriesNoTrace checks the live message list and every file
// of the session bundle: a hidden command must not reach either. The walk
// starts only once the turn worker has returned: after the runner ends it
// still rewrites session.json (activity bookkeeping) through a temp file, and
// a walk that lists that temp file loses it to the rename before the read
// ("The system cannot find the file specified" on Windows CI).
func (s *cliTUIState) persistedSessionCarriesNoTrace(text string) error {
	if err := s.waitWorkersIdle(5 * time.Second); err != nil {
		return err
	}
	if st := s.app.mgr.SessionByID(s.app.sessionID); st != nil {
		for _, msg := range st.GetMessages() {
			if strings.Contains(msg.Content, text) {
				return fmt.Errorf("session message %q carried %q", msg.Content, text)
			}
		}
	}
	dir := s.store.SessionPath(s.app.sessionID)
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path) // #nosec G304 -- test-owned temp path
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), text) {
			return fmt.Errorf("session file %s carried %q", path, text)
		}
		return nil
	})
}

// waitWorkersIdle blocks until every app worker (the turn, the `!!` poller)
// has returned, so nothing writes into the session bundle while a step reads
// it. Unlike App.JoinWorkers it reports a timeout instead of moving on.
func (s *cliTUIState) waitWorkersIdle(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		s.app.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("app workers still busy after %s", timeout)
	}
}

func (s *cliTUIState) operatorTypesWithoutSending(text string) error {
	s.typeText(text)
	return s.waitScreen(text, 2*time.Second)
}

// editorBordersUseLocalShellColor asserts the border rule carries the
// bashMode role, which is what tells the operator enter runs a command.
func (s *cliTUIState) editorBordersUseLocalShellColor() error {
	want := s.app.theme.Fg(roleBashMode, "─")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range s.app.screen.Snapshot() {
			if strings.Contains(line, want) {
				return nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("no editor border used the bashMode color; frame:\n%s", s.screenText())
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
	sc.Step(`^the stub turn starts a tool call named "([^"]*)" with argument agent "([^"]*)"$`, func(tool, agent string) error {
		return s.stubStartsToolCall(tool, "agent", agent)
	})
	sc.Step(`^the stub turn starts a tool call named "([^"]*)" with argument command "([^"]*)"$`, func(tool, command string) error {
		// A run_command box titles itself "$ <command>", not with the tool name,
		// so readiness is the command string appearing on screen.
		s.directives <- stubDirective{kind: "tool_start", tool: tool, argsKey: "command", argsVal: command}
		return s.waitScreen(command, 3*time.Second)
	})
	sc.Step(`^the operator allows the pending permission without ending the turn$`, s.operatorAllowsPermissionKeepingTurn)
	sc.Step(`^the transcript shows a pending tool box titled "([^"]*)"$`, s.transcriptShowsPendingToolBox)
	sc.Step(`^the stub tool call completes with a preview of (\d+) lines$`, s.stubToolCompletesWithLines)
	sc.Step(`^the tool box shows the preview "([^"]*)"$`, s.toolBoxShowsPreview)
	sc.Step(`^the tool box shows the expand hint$`, s.toolBoxShowsExpandHint)
	sc.Step(`^the stub tool call completes without ending the turn$`, s.stubToolCompletesWithoutEndingTurn)
	sc.Step(`^the status line shows "([^"]*)"$`, s.statusLineShows)
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
	sc.Step(`^the stub turn asks a question whose options carry long descriptions$`, s.stubAsksQuestionWithLongDescriptions)
	sc.Step(`^the question modal shows the whole label of the first option$`, s.questionModalShowsWholeFirstLabel)
	sc.Step(`^the question modal wraps the selected option's description below the list$`, s.questionModalWrapsSelectedDescription)
	sc.Step(`^the operator chooses the highlighted option$`, s.operatorChoosesHighlightedOption)
	sc.Step(`^the transcript shows the question "([^"]*)" answered with "([^"]*)"$`, s.transcriptShowsQuestionAnswered)
	sc.Step(`^the stub turn observes the question answer "([^"]*)"$`, s.stubObservesQuestionAnswer)
	sc.Step(`^the stub turn fails with the error "([^"]*)"$`, s.stubFailsWith)
	sc.Step(`^the transcript shows an error notice containing "([^"]*)"$`, s.transcriptShowsErrorNotice)
	sc.Step(`^the editor accepts new input$`, s.editorAcceptsNewInput)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the cancelled turn emits a late text chunk "([^"]*)"$`, s.cancelledTurnEmitsLateChunk)
	sc.Step(`^the transcript does not show "([^"]*)"$`, s.transcriptDoesNotShow)
	sc.Step(`^the operator presses ctrl\+c twice$`, s.operatorPressesCtrlCTwice)
	sc.Step(`^the console app stops within two seconds$`, s.consoleStopsWithinTwoSeconds)
	sc.Step(`^the exit hint names the session and the continue command$`, s.exitHintNamesSessionAndContinue)
	sc.Step(`^the console app starts continuing the latest session$`, s.appStartsContinuingLatest)
	sc.Step(`^the operator runs the local command "([^"]*)"$`, s.operatorRunsLocalCommand)
	sc.Step(`^the transcript shows a local shell block for "([^"]*)"$`, s.transcriptShowsLocalShellBlock)
	sc.Step(`^the local shell block shows the output "([^"]*)"$`, s.localShellBlockShowsOutput)
	sc.Step(`^no agent turn ever received "([^"]*)"$`, s.noAgentTurnReceived)
	sc.Step(`^the persisted session carries no trace of "([^"]*)"$`, s.persistedSessionCarriesNoTrace)
	sc.Step(`^the operator types "([^"]*)" without sending it$`, s.operatorTypesWithoutSending)
	sc.Step(`^the editor borders render in the local shell color$`, s.editorBordersUseLocalShellColor)
	sc.Step(`^the operator runs a one-shot prompt "([^"]*)"$`, s.operatorRunsOneShot)
	sc.Step(`^a coddy console app over a stub agent runner with a neuraldeep provider$`, s.aConsoleAppWithNeuralDeep)
	sc.Step(`^the stand-in limits API reports the session window at (\d+)%$`, s.standReportsSessionAt)
	sc.Step(`^the footer shows the neuraldeep usage "([^"]*)"$`, s.footerShowsUsage)
	sc.Step(`^the transcript shows a usage notice containing "([^"]*)"$`, s.transcriptShowsUsageNotice)
	sc.Step(`^the usage clock moves (\d+) seconds forward$`, s.usageClockMoves)
	sc.Step(`^the operator submits the command "([^"]*)"$`, s.operatorSubmitsCommand)
	sc.Step(`^the usage report shows "([^"]*)"$`, s.usageReportShows)
	sc.Step(`^the operator switches the model to "([^"]*)"$`, s.operatorSwitchesModelTo)
	sc.Step(`^the footer names the model "([^"]*)"$`, s.footerNamesModel)
	sc.Step(`^the footer does not show the neuraldeep usage$`, s.footerHidesUsage)
	sc.Step(`^a coddy console app over a stub agent runner with a neuraldeep provider whose usage limits panel is switched off$`, s.aConsoleAppWithNeuralDeepPanelOff)
	sc.Step(`^the stand-in limits API was never asked$`, s.standNeverAsked)
	sc.Step(`^the one-shot output contains "([^"]*)"$`, s.oneShotOutputContains)
	sc.Step(`^the one-shot run ends cleanly$`, s.oneShotEndsCleanly)
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

// --- ctrl+c exit, continue, and one-shot print steps ---

func (s *cliTUIState) operatorPressesCtrlCTwice() error {
	s.press("\x03")
	time.Sleep(50 * time.Millisecond)
	s.press("\x03")
	return nil
}

func (s *cliTUIState) consoleStopsWithinTwoSeconds() error {
	select {
	case err := <-s.appDone:
		if err != nil {
			return fmt.Errorf("app returned an error on quit: %v", err)
		}
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("the console did not stop after double ctrl+c")
	}
}

func (s *cliTUIState) exitHintNamesSessionAndContinue() error {
	hint := s.app.ExitHint()
	if !strings.Contains(hint, s.app.sessionID) {
		return fmt.Errorf("exit hint %q does not name session %q", hint, s.app.sessionID)
	}
	if !strings.Contains(hint, "coddy cli --session-id") {
		return fmt.Errorf("exit hint %q lacks the continue command", hint)
	}
	return nil
}

func (s *cliTUIState) appStartsContinuingLatest() error {
	if s.app == nil {
		if err := s.buildApp(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx, s.runCancel = ctx, cancel
	if err := s.app.StartContinue(ctx); err != nil {
		cancel()
		return err
	}
	s.appDone = make(chan error, 1)
	go func() { s.appDone <- s.app.Run(ctx) }()
	return s.waitScreen("coddy v", 3*time.Second)
}

func (s *cliTUIState) operatorRunsOneShot(prompt string) error {
	if s.app == nil {
		if err := s.buildApp(); err != nil {
			return err
		}
	}
	s.printOut = &syncBuffer{}
	s.printDone = make(chan error, 1)
	go func() {
		s.printDone <- PrintPrompt(context.Background(), s.app.mgr, PrintOptions{
			Prompt: prompt,
			Out:    s.printOut,
			ErrOut: &syncBuffer{},
			Config: s.cfg,
		})
	}()
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (s *cliTUIState) oneShotOutputContains(text string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.printOut.String(), text) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("one-shot output %q never contained %q", s.printOut.String(), text)
}

func (s *cliTUIState) oneShotEndsCleanly() error {
	select {
	case err := <-s.printDone:
		return err
	case <-time.After(3 * time.Second):
		return fmt.Errorf("one-shot run did not finish")
	}
}
