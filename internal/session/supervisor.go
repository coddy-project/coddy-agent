package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// SupervisorContinuationPrefix marks a visible automatic follow-up prompt.
const SupervisorContinuationPrefix = "[Supervisor continuation] "

// SupervisorVerdict is one completion check by the small supervisor model.
type SupervisorVerdict struct {
	Done      bool   `json:"done"`
	Remaining string `json:"remaining"`
}

type supervisorJudge func(context.Context, *config.Config, *State, string) (SupervisorVerdict, error)

// SetSupervisorJudge replaces the model-based verdict reader. Tests use it to
// exercise continuation policy without a network model.
func (m *Manager) SetSupervisorJudge(judge func(context.Context, *config.Config, *State, string) (SupervisorVerdict, error)) {
	m.supervisorJudge = judge
}

func (m *Manager) supervisorEnabled(st *State, opts *PromptRunOpts) bool {
	if st.GetMode() != string(ModeAgent) || st.Subagent() != nil || opts != nil && opts.subagentTurn {
		return false
	}
	goal := st.GetGoal()
	return goal.Text != "" && goal.Status == GoalActive || m.activeCfg().Supervisor.Enable
}

func isSupervisorCommandPrompt(prompt []acp.ContentBlock) bool {
	if len(prompt) != 1 || prompt[0].Type != acp.ContentTypeText {
		return false
	}
	text := strings.TrimSpace(prompt[0].Text)
	for _, command := range []string{"/goal", "/compact", "/plugin", "/export"} {
		if text == command || strings.HasPrefix(text, command+" ") || strings.HasPrefix(text, command+"\n") {
			return true
		}
	}
	return false
}

func (m *Manager) runSupervisedTurn(ctx context.Context, st *State, prompt []acp.ContentBlock, sender acp.UpdateSender, opts *PromptRunOpts) (string, error) {
	if !m.supervisorEnabled(st, opts) || isSupervisorCommandPrompt(prompt) || opts != nil && opts.BackgroundWake != nil && st.GetGoal().Status != GoalActive {
		return m.runner(ctx, st, prompt, sender)
	}
	cfg := m.activeCfg()
	goal := st.GetGoal()
	objective := ""
	used := 0
	if goal.Status == GoalActive {
		objective = goal.Text
		used = goal.Continuations
	}
	if objective == "" {
		for _, block := range prompt {
			if block.Type == acp.ContentTypeText {
				objective += block.Text + "\n"
			}
		}
		objective = strings.TrimSpace(objective)
	}
	if objective == "" {
		return m.runner(ctx, st, prompt, sender)
	}
	judge := m.supervisorJudge
	if judge == nil {
		judge = judgeSessionGoal
	}
	limit := cfg.Supervisor.ContinuationLimit()
	nudges := 0
	continuation := false
	for {
		stepCtx, cancel := context.WithCancel(ctx)
		if continuation {
			stepCtx = withPromptEcho(stepCtx, st.ID)
		}
		watch := newTurnWatch(sender, st, cancel, cfg.Supervisor.StallTimeout(), cfg.Supervisor.LoopRepeatLimit())
		done := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			watch.run(stepCtx, done)
		}()
		stop, err := m.runner(stepCtx, st, prompt, watch)
		close(done)
		cancel()
		<-watchDone
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			return stop, err
		}
		cause := watch.cause()
		if cause != "" {
			if nudges >= cfg.Supervisor.NudgeLimit() || used >= limit {
				return m.stopSupervisedTurn(st, cause, used)
			}
			nudges++
			used++
			prompt = supervisorPrompt("The previous turn was interrupted because it was " + cause + ". Summarize what you tried, change approach and finish the remaining work.")
			continuation = true
			m.recordSupervisorContinuation(st, used, cause)
			continue
		}
		if err != nil || stop != string(acp.StopReasonEndTurn) {
			return stop, err
		}
		if runningBackgroundWork(st.ID) {
			return stop, nil
		}
		verdictCtx, verdictCancel := context.WithTimeout(ctx, 30*time.Second)
		verdict, verdictErr := judge(verdictCtx, cfg, st, objective)
		verdictCancel()
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			return string(acp.StopReasonCancelled), nil
		}
		if verdictErr != nil {
			m.log.Warn("supervisor completion check failed", "session", st.ID, "error", verdictErr)
			return m.stopSupervisedTurn(st, "completion check failed: "+verdictErr.Error(), used)
		}
		if verdict.Done {
			if goal := st.GetGoal(); goal.Text != "" && goal.Status == GoalActive {
				goal.Status = GoalComplete
				goal.Remaining = ""
				st.SetGoal(goal)
			}
			return stop, nil
		}
		remaining := strings.TrimSpace(verdict.Remaining)
		if remaining == "" {
			remaining = "Finish the outstanding parts of the request."
		}
		if used >= limit {
			return m.stopSupervisedTurn(st, remaining, used)
		}
		used++
		prompt = supervisorPrompt("Continue the task. Remaining: " + remaining)
		continuation = true
		m.recordSupervisorContinuation(st, used, remaining)
	}
}

func supervisorPrompt(text string) []acp.ContentBlock {
	return []acp.ContentBlock{{Type: acp.ContentTypeText, Text: SupervisorContinuationPrefix + text}}
}

func (m *Manager) recordSupervisorContinuation(st *State, used int, remaining string) {
	if goal := st.GetGoal(); goal.Text != "" && goal.Status == GoalActive {
		goal.Continuations = used
		goal.Remaining = remaining
		st.SetGoal(goal)
	}
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), fmt.Sprintf("Supervisor continuation %d: %s", used, remaining))
}

func (m *Manager) stopSupervisedTurn(st *State, remaining string, used int) (string, error) {
	if goal := st.GetGoal(); goal.Text != "" && goal.Status == GoalActive {
		goal.Status = GoalStopped
		goal.Remaining = remaining
		goal.Continuations = used
		st.SetGoal(goal)
	}
	st.SetTurnStopNotice(fmt.Sprintf("Supervisor stopped after %d automatic continuations. Remaining: %s", used, remaining))
	return string(acp.StopReasonRefused), nil
}

type turnWatch struct {
	acp.UpdateSender
	sessionID string
	state     *State
	cancel    context.CancelFunc
	stall     time.Duration
	repeat    int
	mu        sync.Mutex
	last      time.Time
	waiting   int
	lastTool  string
	toolRuns  int
	seenTools map[string]bool
	stoppedBy string
}

func newTurnWatch(sender acp.UpdateSender, state *State, cancel context.CancelFunc, stall time.Duration, repeat int) *turnWatch {
	return &turnWatch{UpdateSender: sender, sessionID: state.ID, state: state, cancel: cancel, stall: stall, repeat: repeat, last: time.Now(), seenTools: make(map[string]bool)}
}

func (w *turnWatch) SendSessionUpdate(sessionID string, update interface{}) error {
	var toolKey string
	if event, ok := update.(acp.ToolCallStatusUpdate); ok && (event.Status == "completed" || event.Status == "failed") {
		toolKey = w.toolKey(event.ToolCallID)
	}
	w.mu.Lock()
	switch event := update.(type) {
	case acp.MessageChunkUpdate:
		if event.SessionUpdate == acp.UpdateTypeAgentMessageChunk && event.Content.Text != "" {
			w.last = time.Now()
			w.toolRuns = 0
		}
	case acp.ToolCallUpdate:
		w.last = time.Now()
	case acp.ToolCallStatusUpdate:
		w.last = time.Now()
		if toolKey != "" && !w.seenTools[event.ToolCallID] {
			w.seenTools[event.ToolCallID] = true
			if toolKey == w.lastTool {
				w.toolRuns++
			} else {
				w.lastTool, w.toolRuns = toolKey, 1
			}
			if w.repeat > 0 && w.toolRuns >= w.repeat && w.stoppedBy == "" {
				w.stoppedBy = "repeating the same tool operation"
				w.cancel()
			}
		}
	}
	w.mu.Unlock()
	return w.UpdateSender.SendSessionUpdate(sessionID, update)
}

func (w *turnWatch) toolKey(id string) string {
	w.state.mu.RLock()
	var found llm.ToolCall
	for i := len(w.state.Messages) - 1; i >= 0 && found.ID == ""; i-- {
		for _, call := range w.state.Messages[i].ToolCalls {
			if call.ID == id {
				found = call
				break
			}
		}
	}
	w.state.mu.RUnlock()
	if found.ID == "" {
		return ""
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(found.InputJSON), &args); err != nil {
		return found.Name + "\x00" + found.InputJSON
	}
	canonical, _ := json.Marshal(args)
	return found.Name + "\x00" + string(canonical)
}

func (w *turnWatch) RequestPermission(ctx context.Context, req acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	w.setWaiting(1)
	defer w.setWaiting(-1)
	return w.UpdateSender.RequestPermission(ctx, req)
}

func (w *turnWatch) RequestQuestion(ctx context.Context, req acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	w.setWaiting(1)
	defer w.setWaiting(-1)
	return w.UpdateSender.RequestQuestion(ctx, req)
}

func (w *turnWatch) setWaiting(delta int) {
	w.mu.Lock()
	w.waiting += delta
	w.last = time.Now()
	w.mu.Unlock()
}

func (w *turnWatch) cause() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stoppedBy
}

func (w *turnWatch) run(ctx context.Context, done <-chan struct{}) {
	if w.stall <= 0 {
		return
	}
	interval := min(w.stall/4, time.Second)
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-timer.C:
			w.mu.Lock()
			if w.waiting > 0 || runningBackgroundWork(w.sessionID) {
				w.last = time.Now()
			} else if time.Since(w.last) >= w.stall && w.stoppedBy == "" {
				w.stoppedBy = "stalled"
				w.cancel()
			}
			w.mu.Unlock()
		}
	}
}

func runningBackgroundWork(sessionID string) bool {
	for _, task := range bgtask.Default().List(sessionID) {
		if task.Kind != bgtask.KindServer && (task.Status == bgtask.StatusRunning || task.Status == bgtask.StatusQueued) {
			return true
		}
	}
	return false
}

func judgeSessionGoal(ctx context.Context, cfg *config.Config, st *State, objective string) (SupervisorVerdict, error) {
	model := strings.TrimSpace(cfg.Supervisor.Model)
	if model == "" {
		model = st.EffectiveModelID(cfg)
	}
	rm, err := cfg.ResolveLLM(model)
	if err != nil {
		return SupervisorVerdict{}, err
	}
	provider, err := llm.NewProvider(llm.ProviderInput{
		Name: rm.ProviderName, Type: rm.ProviderType, Model: rm.Model,
		APIKey: rm.APIKey, BaseURL: rm.BaseURL, ProxyURL: rm.ProxyURL,
		AuthPath: rm.AuthPath, NoCLILogin: rm.NoCLILogin,
		MaxTokens: supervisorMaxTokens(rm.MaxTokens), DisableStream: true,
		Timeout: time.Duration(rm.TimeoutMS) * time.Millisecond,
	})
	if err != nil {
		return SupervisorVerdict{}, err
	}
	messages := st.GetMessages()
	if len(messages) > 12 {
		messages = messages[len(messages)-12:]
	}
	var transcript strings.Builder
	for _, msg := range messages {
		fmt.Fprintf(&transcript, "%s: %s\n", msg.Role, truncateSupervisorText(msg.Content, 1200))
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(&transcript, "tool_use: %s %s\n", call.Name, truncateSupervisorText(call.InputJSON, 300))
		}
	}
	response, err := provider.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: "You are a session supervisor. Decide whether the user's objective is fully complete using only the visible transcript. Return only JSON with keys done (boolean) and remaining (short string). Mark done true only when the result is explicit. Do not request tools or continue the task yourself."},
		{Role: llm.RoleUser, Content: "Objective: " + objective + "\nRecent transcript:\n" + transcript.String()},
	}, nil)
	if err != nil {
		return SupervisorVerdict{}, err
	}
	if response == nil {
		return SupervisorVerdict{}, fmt.Errorf("supervisor model returned no response")
	}
	var parsed struct {
		Done      *bool  `json:"done"`
		Remaining string `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &parsed); err != nil {
		return SupervisorVerdict{}, fmt.Errorf("supervisor verdict: %w", err)
	}
	if parsed.Done == nil {
		return SupervisorVerdict{}, fmt.Errorf("supervisor verdict has no done field")
	}
	return SupervisorVerdict{Done: *parsed.Done, Remaining: parsed.Remaining}, nil
}

func truncateSupervisorText(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

func supervisorMaxTokens(configured int) int {
	if configured <= 0 {
		return 512
	}
	return min(configured, 512)
}
