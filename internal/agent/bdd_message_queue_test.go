package agent

// Godog harness for features/message_queue.feature: runs real turns through the
// session manager with a scripted model, and queues follow-ups from inside the
// model call, which is exactly when an operator writes one - while the step
// they are watching is still running. Nothing here is timing-dependent: the
// queue is written during the call that precedes the step which must read it.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type queueFeatureState struct {
	root, home, cwd string
	cfg             *config.Config
	store           *session.FileStore
	mgr             *session.Manager
	sess            *session.State
	client          *recordingClient
	provider        *scriptedProvider

	mu sync.Mutex
	// pending is what the scenario asked to be queued from inside the model
	// call; cancelled names the texts taken back in the same breath.
	pending       []string
	pendingImages map[string][]acp.ImagePartRef
	// pendingModes names the texts queued for after the turn; the rest steer.
	pendingModes map[string]session.QueueMode
	cancelled    map[string]bool
	// enqueueErr is the first refusal the queue returned.
	enqueueErr error

	stopReason string
}

func (s *queueFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-queue-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "work")
	for _, d := range []string{s.home, s.cwd, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.client = &recordingClient{answer: "allow"}
	s.pending = nil
	s.pendingImages = map[string][]acp.ImagePartRef{}
	s.pendingModes = map[string]session.QueueMode{}
	s.cancelled = map[string]bool{}
	s.enqueueErr = nil
	s.stopReason = ""
	return nil
}

func (s *queueFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.mgr = nil
	s.sess = nil
	s.cfg = nil
	s.store = nil
	s.provider = nil
}

// drainPending queues whatever the scenario asked for, from inside the model
// call. Cancelled texts are taken back straight away, before the step that
// would have read them.
func (s *queueFeatureState) drainPending() {
	s.mu.Lock()
	texts := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, text := range texts {
		s.mu.Lock()
		images := s.pendingImages[text]
		mode, ok := s.pendingModes[text]
		s.mu.Unlock()
		if !ok {
			mode = session.QueueModeSteer
		}
		msg, _, err := s.mgr.EnqueueTurnMessageWithMode(s.sess.GetID(), text, mode, images)
		s.mu.Lock()
		if err != nil && s.enqueueErr == nil {
			s.enqueueErr = err
		}
		cancel := s.cancelled[text]
		s.mu.Unlock()
		if err == nil && cancel {
			if _, err := s.mgr.CancelQueuedTurnMessage(s.sess.GetID(), msg.ID); err != nil {
				s.mu.Lock()
				s.enqueueErr = err
				s.mu.Unlock()
			}
		}
	}
}

func (s *queueFeatureState) buildSession(steps []scriptStep) error {
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd, ConfigPath: filepath.Join(s.home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
		Sessions:  config.Sessions{Dir: filepath.Join(s.root, "sessions")},
	}
	s.cfg.Tools.PermissionMode = config.PermModeBypass
	s.cfg.Hooks.ApplyDefaults(s.cfg.Paths)
	s.cfg.Subagents.ApplyDefaults(s.cfg.Paths)
	s.cfg.Prompts.ApplyDefaults()

	s.store = &session.FileStore{Root: s.cfg.Sessions.Dir}
	provider := &scriptedProvider{steps: steps}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return provider, nil })
		return loop.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, s.client, runner, slog.Default(), s.cwd, s.store)
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return err
	}
	s.sess = s.mgr.SessionByID(res.SessionID)
	if s.sess == nil {
		return fmt.Errorf("session missing")
	}
	s.provider = provider
	return nil
}

// queueingStep wraps a scripted step so the scenario's follow-ups are queued
// while that model call is in flight.
func (s *queueFeatureState) queueingStep(next scriptStep) scriptStep {
	return func(messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		s.drainPending()
		return next(messages, defs, onChunk)
	}
}

func (s *queueFeatureState) turnCallsToolFirst() error {
	call := llm.ToolCall{ID: "call_q1", Name: "glob", InputJSON: `{"pattern":"**/*.go"}`}
	return s.buildSession([]scriptStep{
		s.queueingStep(toolStep(call)),
		answerStep("done"),
	})
}

// turnCallsToolThenHasAnotherAnswer scripts a turn whose first prompt calls a
// tool and answers, plus the answer of one more prompt: the one a message
// queued for after the turn starts.
func (s *queueFeatureState) turnCallsToolThenHasAnotherAnswer() error {
	call := llm.ToolCall{ID: "call_q1", Name: "glob", InputJSON: `{"pattern":"**/*.go"}`}
	return s.buildSession([]scriptStep{
		s.queueingStep(toolStep(call)),
		answerStep("done"),
		answerStep("changelog updated"),
	})
}

func (s *queueFeatureState) turnAnswersWithoutTool() error {
	return s.buildSession([]scriptStep{
		s.queueingStep(answerStep("here is the answer")),
		answerStep("and here is the follow-up answer"),
	})
}

// operatorQueues records a follow-up; it is written to the queue from inside
// the model call, when the turn the scenario describes is actually running.
func (s *queueFeatureState) operatorQueues(text string) error {
	s.mu.Lock()
	s.pending = append(s.pending, text)
	s.mu.Unlock()
	return nil
}

// operatorQueuesAfterTurn records a follow-up the operator wants answered
// after the current one, not inside it.
func (s *queueFeatureState) operatorQueuesAfterTurn(text string) error {
	s.mu.Lock()
	s.pending = append(s.pending, text)
	s.pendingModes[text] = session.QueueModeAfterTurn
	s.mu.Unlock()
	return nil
}

// stepAfterToolDoesNotCarry runs the turn and checks that the request which
// answers the tool result was built without the deferred message.
func (s *queueFeatureState) stepAfterToolDoesNotCarry(text string) error {
	if err := s.turnRuns(); err != nil {
		return err
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if len(s.provider.requests) < 2 {
		return fmt.Errorf("the model was called %d times; the step after the tool never happened", len(s.provider.requests))
	}
	for _, m := range s.provider.requests[1] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, text) {
			return fmt.Errorf("the step after the tool result carries %q: it steered the turn instead of waiting for it", text)
		}
	}
	return nil
}

// answeredByOwnPrompt checks that the deferred message was the prompt of a
// request made after the first answer, and that the transcript holds it
// between the two answers.
func (s *queueFeatureState) answeredByOwnPrompt(text string) error {
	s.provider.mu.Lock()
	requests := append([][]llm.Message(nil), s.provider.requests...)
	s.provider.mu.Unlock()
	if len(requests) != 3 {
		return fmt.Errorf("the model was called %d times, want 3 (tool, answer, the deferred prompt)", len(requests))
	}
	// The turn context block rides after the history of every request; it is
	// Coddy's, not the conversation's, so it takes no part in the order.
	var last []llm.Message
	for _, m := range requests[2] {
		if m.Role == llm.RoleUser && strings.HasPrefix(strings.TrimSpace(m.Content), "<turn_context>") {
			continue
		}
		last = append(last, m)
	}
	if len(last) < 2 {
		return fmt.Errorf("the deferred request is too short: %d messages", len(last))
	}
	prompt, before := last[len(last)-1], last[len(last)-2]
	if prompt.Role != llm.RoleUser || !strings.Contains(prompt.Content, text) {
		return fmt.Errorf("the deferred request ends with %s %q, want the operator's %q", prompt.Role, prompt.Content, text)
	}
	if before.Role != llm.RoleAssistant || !strings.Contains(before.Content, "done") {
		return fmt.Errorf("the deferred prompt follows %s %q, want the first answer", before.Role, before.Content)
	}
	if s.stopReason != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("turn ended with %q, want %q", s.stopReason, acp.StopReasonEndTurn)
	}
	return nil
}

// clientsSeeDeferredPromptBetweenAnswers checks the frames a watching client
// got, in order: the first answer, the deferred message as the operator's
// message, then the answer to it. Without the middle frame a live transcript
// runs the second answer on from the first, with no question above it.
func (s *queueFeatureState) clientsSeeDeferredPromptBetweenAnswers(text string) error {
	s.client.mu.Lock()
	updates := append([]interface{}(nil), s.client.updates...)
	s.client.mu.Unlock()
	first, user, second := -1, -1, -1
	for i, u := range updates {
		chunk, ok := u.(acp.MessageChunkUpdate)
		if !ok {
			continue
		}
		switch {
		case chunk.SessionUpdate == acp.UpdateTypeAgentMessageChunk && chunk.Content.Text == "done" && first < 0:
			first = i
		case chunk.SessionUpdate == acp.UpdateTypeUserMessageChunk && strings.Contains(chunk.Content.Text, text) && user < 0:
			user = i
		case chunk.SessionUpdate == acp.UpdateTypeAgentMessageChunk && chunk.Content.Text == "changelog updated" && second < 0:
			second = i
		}
	}
	if first < 0 || second < 0 {
		return fmt.Errorf("the clients did not get both answers (first at %d, second at %d)", first, second)
	}
	if user < 0 {
		return fmt.Errorf("the clients never got %q as a message of the operator", text)
	}
	if first >= user || user >= second {
		return fmt.Errorf("frames out of order: first answer %d, operator message %d, second answer %d", first, user, second)
	}
	return nil
}

func (s *queueFeatureState) operatorQueuesImage(text string) error {
	s.mu.Lock()
	s.pending = append(s.pending, text)
	s.pendingImages[text] = []acp.ImagePartRef{{Name: "pixel.png", DataURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/L9kAAAAASUVORK5CYII="}}
	s.mu.Unlock()
	return nil
}

func (s *queueFeatureState) nextRequestCarriesImage() error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if len(s.provider.requests) < 2 {
		return fmt.Errorf("no next model request")
	}
	for _, msg := range s.provider.requests[1] {
		if msg.Role == llm.RoleUser && len(msg.ImageParts) == 1 && msg.ImageParts[0].Name == "pixel.png" {
			return nil
		}
	}
	return fmt.Errorf("next request does not carry the queued image")
}

func (s *queueFeatureState) transcriptRecordsImage() error {
	for _, msg := range s.sess.GetMessages() {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "inspect this image") && len(msg.ImageParts) == 1 {
			return nil
		}
	}
	return fmt.Errorf("transcript does not show the queued image")
}

// cancelLastQueued takes the most recently written follow-up back, in the same
// moment it was queued - before the step that would have read it.
func (s *queueFeatureState) cancelLastQueued() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return fmt.Errorf("nothing was queued to cancel")
	}
	s.cancelled[s.pending[len(s.pending)-1]] = true
	return nil
}

func (s *queueFeatureState) turnRuns() error {
	if s.sess == nil {
		return fmt.Errorf("no session prepared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: s.sess.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "start the work"}},
	}, s.client, nil)
	if res != nil {
		s.stopReason = string(res.StopReason)
	}
	if err != nil {
		return fmt.Errorf("turn failed: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enqueueErr != nil {
		return fmt.Errorf("queueing failed: %w", s.enqueueErr)
	}
	return nil
}

// userMessages lists the transcript's user messages after the opening prompt.
func (s *queueFeatureState) userMessages() []string {
	var out []string
	for _, m := range s.sess.GetMessages() {
		if m.Role != llm.RoleUser {
			continue
		}
		if strings.TrimSpace(m.Content) == "start the work" {
			continue
		}
		out = append(out, m.Content)
	}
	return out
}

func (s *queueFeatureState) agentReadsOnNextStep(text string) error {
	if err := s.turnRuns(); err != nil {
		return err
	}
	for _, got := range s.userMessages() {
		if strings.Contains(got, text) {
			return nil
		}
	}
	return fmt.Errorf("the transcript has no queued message %q; user messages: %v", text, s.userMessages())
}

func (s *queueFeatureState) agentReadsBoth(first, second string) error {
	if err := s.turnRuns(); err != nil {
		return err
	}
	got := s.userMessages()
	if len(got) < 2 {
		return fmt.Errorf("want two queued messages read, got %v", got)
	}
	if !strings.Contains(got[0], first) || !strings.Contains(got[1], second) {
		return fmt.Errorf("queued messages read as %v, want %q then %q", got, first, second)
	}
	return nil
}

func (s *queueFeatureState) requestAfterToolResultCarries(text string) error {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if len(s.provider.requests) < 2 {
		return fmt.Errorf("the model was called %d times; the step after the tool never happened", len(s.provider.requests))
	}
	req := s.provider.requests[1]
	sawToolResult := false
	for _, m := range req {
		if m.Role == llm.RoleTool {
			sawToolResult = true
		}
		if m.Role == llm.RoleUser && strings.Contains(m.Content, text) {
			if !sawToolResult {
				return fmt.Errorf("the queued message reached the model before the tool result, not after it")
			}
			return nil
		}
	}
	return fmt.Errorf("the request after the tool result does not carry %q", text)
}

func (s *queueFeatureState) transcriptRecordsOperatorMessage(text string) error {
	for _, got := range s.userMessages() {
		if strings.Contains(got, text) {
			return nil
		}
	}
	return fmt.Errorf("the transcript has no user message %q", text)
}

func (s *queueFeatureState) agentNeverReads(text string) error {
	if err := s.turnRuns(); err != nil {
		return err
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	for i, req := range s.provider.requests {
		for _, m := range req {
			if strings.Contains(m.Content, text) {
				return fmt.Errorf("request %d carries the cancelled message %q", i+1, text)
			}
		}
	}
	return nil
}

func (s *queueFeatureState) transcriptHasNoOtherOperatorMessage() error {
	if got := s.userMessages(); len(got) != 0 {
		return fmt.Errorf("the transcript gained user messages it should not have: %v", got)
	}
	return nil
}

func (s *queueFeatureState) agentAnswersInSameTurn(text string) error {
	if err := s.turnRuns(); err != nil {
		return err
	}
	if err := s.transcriptRecordsOperatorMessage(text); err != nil {
		return err
	}
	s.provider.mu.Lock()
	calls := s.provider.calls
	s.provider.mu.Unlock()
	if calls < 2 {
		return fmt.Errorf("the model was called %d times: the queued message was never answered", calls)
	}
	if s.stopReason != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("turn ended with %q, want %q", s.stopReason, acp.StopReasonEndTurn)
	}
	return nil
}

func (s *queueFeatureState) turnFinishes() error {
	return s.turnRuns()
}

func (s *queueFeatureState) sessionHoldsNoQueuedMessages() error {
	left, err := s.mgr.QueuedTurnMessages(s.sess.GetID())
	if err != nil {
		return err
	}
	if len(left) != 0 {
		return fmt.Errorf("the session still holds %d queued messages", len(left))
	}
	if s.sess.MessageQueueOpen() {
		return fmt.Errorf("the queue is still open after the turn released")
	}
	return nil
}

func initializeMessageQueueScenario(sc *godog.ScenarioContext) {
	s := &queueFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^an agent turn that calls a tool before it answers$`, s.turnCallsToolFirst)
	sc.Step(`^an agent turn that answers without calling a tool$`, s.turnAnswersWithoutTool)
	sc.Step(`^the operator queues "([^"]*)" while the tool is running$`, s.operatorQueues)
	sc.Step(`^the operator queues an image with "([^"]*)" while the tool is running$`, s.operatorQueuesImage)
	sc.Step(`^the operator queues "([^"]*)" before the turn releases$`, s.operatorQueues)
	sc.Step(`^the operator cancels that queued message before the step ends$`, s.cancelLastQueued)
	sc.Step(`^the agent reads that message on its next step$`, func() error {
		return s.agentReadsOnNextStep("check the Windows path too")
	})
	sc.Step(`^the request that follows the tool result carries that message$`, func() error {
		return s.requestAfterToolResultCarries("check the Windows path too")
	})
	sc.Step(`^the transcript records it as a message of the operator$`, func() error {
		return s.transcriptRecordsOperatorMessage("check the Windows path too")
	})
	sc.Step(`^the agent reads both messages on its next step$`, func() error {
		return s.agentReadsBoth("first correction", "second correction")
	})
	sc.Step(`^they are read in the order they were written$`, func() error {
		got := s.userMessages()
		if len(got) < 2 || !strings.Contains(got[0], "first correction") {
			return fmt.Errorf("order lost: %v", got)
		}
		return nil
	})
	sc.Step(`^the agent never reads it$`, func() error { return s.agentNeverReads("wrong file, ignore that") })
	sc.Step(`^the transcript has no message of the operator besides the prompt$`, s.transcriptHasNoOtherOperatorMessage)
	sc.Step(`^the agent answers that message in the same turn$`, func() error {
		return s.agentAnswersInSameTurn("one more thing")
	})
	sc.Step(`^the turn finishes$`, s.turnFinishes)
	sc.Step(`^the session holds no queued messages$`, s.sessionHoldsNoQueuedMessages)
	sc.Step(`^the agent reads that image on its next step$`, func() error { return s.agentReadsOnNextStep("inspect this image") })
	sc.Step(`^the next model request carries the image$`, s.nextRequestCarriesImage)
	sc.Step(`^the transcript records the image on the operator's message$`, s.transcriptRecordsImage)
	sc.Step(`^an agent turn that calls a tool, answers, and has an answer for one more prompt$`, s.turnCallsToolThenHasAnotherAnswer)
	sc.Step(`^the operator queues "([^"]*)" for after the turn while the tool is running$`, s.operatorQueuesAfterTurn)
	sc.Step(`^the step after the tool result does not carry that message$`, func() error {
		return s.stepAfterToolDoesNotCarry("then update the changelog")
	})
	sc.Step(`^that message is answered by a prompt of its own after the first answer$`, func() error {
		return s.answeredByOwnPrompt("then update the changelog")
	})
	sc.Step(`^the clients see that message arrive between the two answers$`, func() error {
		return s.clientsSeeDeferredPromptBetweenAnswers("then update the changelog")
	})
}

func TestMessageQueueFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "message-queue",
		ScenarioInitializer: initializeMessageQueueScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/message_queue.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("message queue feature suite failed")
	}
}
