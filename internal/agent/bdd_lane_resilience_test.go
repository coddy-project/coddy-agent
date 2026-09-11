package agent

// Godog harness for features/llm_lane_resilience.feature: drives the real
// Agent.Run against a fake provider that plays a load-balanced lane whose first
// deployment is sick. One member stays mute until the first-token guard cuts it,
// the other returns internal reasoning with neither text nor a tool call. A real
// proxy picks the member at random, so simulation is what makes the spec
// deterministic and LLM-free.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bddLaneAnswer is what the healthy member of the lane replies.
const bddLaneAnswer = "The project holds one Go file and a readme."

// bddLaneReasoning is what the sick member produces instead of an answer: the
// harmony tool call it meant to make, flattened into the reasoning channel.
const bddLaneReasoning = `We have src/main.go and readme.txt. Let's read them.{"path":"readme.txt"}`

// bddLaneFailure selects how the first call of a scenario misbehaves.
type bddLaneFailure int

const (
	// bddLaneSilent never emits a chunk and returns only when the caller's
	// context is cancelled, exactly as a provider does when the upstream
	// hangs with no first byte.
	bddLaneSilent bddLaneFailure = iota
	// bddLaneReasoningOnly streams reasoning and finishes with no content and
	// no tool call.
	bddLaneReasoningOnly
)

// bddLaneProvider is a lane of interchangeable deployments where the first call
// lands on the sick one and every later call on a healthy one.
type bddLaneProvider struct {
	failure   bddLaneFailure
	sickCalls int

	calls int
	seen  [][]llm.Message
}

func (p *bddLaneProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the lane resilience suite")
}

func (p *bddLaneProvider) Stream(ctx context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))

	if p.calls <= p.sickCalls {
		switch p.failure {
		case bddLaneSilent:
			<-ctx.Done()
			return nil, ctx.Err()
		case bddLaneReasoningOnly:
			onChunk(llm.StreamChunk{ReasoningDelta: bddLaneReasoning})
			return &llm.Response{Reasoning: bddLaneReasoning, StopReason: "end_turn"}, nil
		}
	}

	onChunk(llm.StreamChunk{TextDelta: bddLaneAnswer})
	return &llm.Response{Content: bddLaneAnswer, StopReason: "end_turn"}, nil
}

type laneResilienceState struct {
	tmpDirs  []string
	st       *session.State
	ag       *Agent
	provider *bddLaneProvider
	sender   *loopGuardSender
	cfg      *config.Config

	stop   string
	runErr error
}

func (s *laneResilienceState) reset() error {
	s.close()
	s.sender = &loopGuardSender{}
	s.provider = nil
	s.stop = ""
	s.runErr = nil
	return nil
}

func (s *laneResilienceState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
	s.cfg = nil
}

func (s *laneResilienceState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-lane-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

// agentOnLane builds the agent; firstToken bounds the silence guard so the
// silent-deployment scenario does not wait out the 90 s default.
func (s *laneResilienceState) agentOnLane(firstTokenMS int) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_lane",
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	ms := firstTokenMS
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "lane", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "lane/model", MaxTokens: 100}},
		Agent: config.Agent{
			Model:                  "lane/model",
			MaxTurns:               12,
			LLMFirstTokenTimeoutMS: &ms,
		},
	}
	return nil
}

func (s *laneResilienceState) agentWithQuickFirstTokenGuard() error {
	return s.agentOnLane(150)
}

func (s *laneResilienceState) agentOnReasoningOnlyLane() error {
	// The guard must not be what rescues this scenario: leave it at a value the
	// reasoning-only reply never reaches.
	return s.agentOnLane(60000)
}

func (s *laneResilienceState) buildAgent() {
	s.ag = NewAgent(s.cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return s.provider, nil
	}
}

func (s *laneResilienceState) deploymentSilentThenAnswers() error {
	s.provider = &bddLaneProvider{failure: bddLaneSilent, sickCalls: 1}
	s.buildAgent()
	return nil
}

func (s *laneResilienceState) deploymentReasoningOnlyThenAnswers() error {
	s.provider = &bddLaneProvider{failure: bddLaneReasoningOnly, sickCalls: 1}
	s.buildAgent()
	return nil
}

func (s *laneResilienceState) userSendsPromptOnLane() error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.stop, s.runErr = s.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "describe this project"}})
	return nil
}

func (s *laneResilienceState) laneReceivesSameRequestTwice() error {
	if s.provider.calls < 2 {
		return fmt.Errorf("the lane was called %d time(s): the failed attempt was never re-issued", s.provider.calls)
	}
	if len(s.provider.seen) < 2 {
		return fmt.Errorf("only %d request(s) were recorded", len(s.provider.seen))
	}
	first, second := s.provider.seen[0], s.provider.seen[1]
	if len(first) != len(second) {
		return fmt.Errorf("the re-issued request has %d messages, the first had %d: it is not the same request",
			len(second), len(first))
	}
	for i := range first {
		if first[i].Role != second[i].Role {
			return fmt.Errorf("message %d changed role between the two requests: %q vs %q",
				i, first[i].Role, second[i].Role)
		}
		// The system prompt is deliberately re-rendered before every call (todo
		// rows, and a UTCNow that moves on its own), so the conversation is what
		// has to come back unchanged, not that one message byte for byte.
		if first[i].Role == llm.RoleSystem {
			continue
		}
		if first[i].Content != second[i].Content {
			return fmt.Errorf("message %d differs between the two requests: %q vs %q",
				i, first[i].Content, second[i].Content)
		}
	}
	return nil
}

func (s *laneResilienceState) retriedRequestCarriesNoNudgeOrEmptyTurn() error {
	if len(s.provider.seen) < 2 {
		return fmt.Errorf("no second request was recorded")
	}
	for i, m := range s.provider.seen[1] {
		if strings.Contains(m.Content, emptyAssistantContinuationNudge) {
			return fmt.Errorf("message %d of the re-issued request is the nudge; the plain replay must come first", i)
		}
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			return fmt.Errorf("message %d of the re-issued request is the empty assistant turn", i)
		}
	}
	return nil
}

func (s *laneResilienceState) turnEndsWithAnswer() error {
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleAssistant && strings.Contains(m.Content, bddLaneAnswer) {
			return nil
		}
	}
	return fmt.Errorf("the transcript has no assistant message with the answer; stop reason %q, error %v",
		s.stop, s.runErr)
}

func (s *laneResilienceState) turnReportsNoError() error {
	if s.runErr != nil {
		return fmt.Errorf("the turn reported %v", s.runErr)
	}
	if s.stop == string(acp.StopReasonRefused) {
		return fmt.Errorf("the turn was refused instead of recovering")
	}
	return nil
}

func initializeLaneResilienceScenario(sc *godog.ScenarioContext) {
	s := &laneResilienceState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy agent whose first-token guard fires quickly$`, s.agentWithQuickFirstTokenGuard)
	sc.Step(`^a coddy agent on a lane that can answer with reasoning only$`, s.agentOnReasoningOnlyLane)
	sc.Step(`^a deployment that stays silent on its first call and answers on the next$`, s.deploymentSilentThenAnswers)
	sc.Step(`^a deployment that returns reasoning without an answer on its first call$`, s.deploymentReasoningOnlyThenAnswers)
	sc.Step(`^the user sends a prompt on the lane$`, s.userSendsPromptOnLane)
	sc.Step(`^the lane receives the same request a second time$`, s.laneReceivesSameRequestTwice)
	sc.Step(`^the retried request carries no nudge and no empty assistant turn$`, s.retriedRequestCarriesNoNudgeOrEmptyTurn)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
	sc.Step(`^the turn reports no error to the user$`, s.turnReportsNoError)
}

func TestLLMLaneResilienceFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-lane-resilience",
		ScenarioInitializer: initializeLaneResilienceScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_lane_resilience.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("llm lane resilience feature suite failed")
	}
}
