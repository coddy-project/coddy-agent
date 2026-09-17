package agent

// Godog harness for features/session_describe_tool.feature: a real Agent.Run
// with a fake provider that calls session_describe once and then answers, over
// a real session bundle on disk. The scenario asserts both halves of the tool -
// what it reported to the model, and what the store actually holds afterwards -
// because the filing is only worth anything if it survives the process.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

const (
	bddFilingCallID = "call_session_describe"
	bddFilingAnswer = "The session is filed."
)

// bddFilingProvider requests one session_describe call on its first turn and
// answers on the next.
type bddFilingProvider struct {
	args  string
	calls int
}

func (p *bddFilingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the session filing suite")
}

func (p *bddFilingProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		tc := llm.ToolCall{ID: bddFilingCallID, Name: tools.ToolSessionDescribe, InputJSON: p.args}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: bddFilingAnswer})
	return &llm.Response{Content: bddFilingAnswer, StopReason: "end_turn"}, nil
}

// bddFilingReport is what the tool answered the model with.
type bddFilingReport struct {
	Object  string   `json:"object"`
	Title   string   `json:"title"`
	Tags    []string `json:"tags"`
	Changed []string `json:"changed"`
}

type sessionFilingFeatureState struct {
	root   string
	store  *session.FileStore
	st     *session.State
	cfg    *config.Config
	report bddFilingReport
}

func (s *sessionFilingFeatureState) reset() error {
	s.close()
	s.report = bddFilingReport{}
	return nil
}

func (s *sessionFilingFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
	}
	s.root = ""
	s.store = nil
	s.st = nil
	s.cfg = nil
}

func (s *sessionFilingFeatureState) storedSession(title, tags string) error {
	root, err := os.MkdirTemp("", "coddy-bdd-filing-*")
	if err != nil {
		return err
	}
	s.root = root
	s.store = &session.FileStore{Root: root}
	dir, err := s.store.EnsureLayout("sess_bdd_filing")
	if err != nil {
		return err
	}
	s.st = &session.State{ID: "sess_bdd_filing", CWD: root, Mode: session.ModeAgent, SessionDir: dir}
	s.st.SetPersistHook(func() {
		if err := s.store.Save(s.st); err != nil {
			panic(err)
		}
	})
	s.st.SetTitlePinned(title)
	s.st.SetTags(splitListStep(tags))
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	return s.store.Save(s.st)
}

func (s *sessionFilingFeatureState) modelCalls(doc *godog.DocString) error {
	if s.st == nil {
		return fmt.Errorf("no session prepared")
	}
	provider := &bddFilingProvider{args: strings.TrimSpace(doc.Content)}
	ag := NewAgent(s.cfg, s.st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "file this session"}}); err != nil {
		return err
	}
	for _, m := range s.st.GetMessages() {
		if m.Role != llm.RoleTool || m.ToolCallID != bddFilingCallID {
			continue
		}
		if err := json.Unmarshal([]byte(m.Content), &s.report); err != nil {
			return fmt.Errorf("the tool answered %q, which is not a filing report: %w", m.Content, err)
		}
		return nil
	}
	return fmt.Errorf("no answer for tool call %s in the transcript", bddFilingCallID)
}

func (s *sessionFilingFeatureState) reportedTitle(want string) error {
	if s.report.Title != want {
		return fmt.Errorf("the tool reported the title %q, want %q", s.report.Title, want)
	}
	return nil
}

func (s *sessionFilingFeatureState) reportedTags(want string) error {
	got := strings.Join(s.report.Tags, ", ")
	if got != want {
		return fmt.Errorf("the tool reported the tags %q, want %q", got, want)
	}
	return nil
}

func (s *sessionFilingFeatureState) reportedNoChange() error {
	if len(s.report.Changed) != 0 {
		return fmt.Errorf("the tool reported %v as changed, want nothing", s.report.Changed)
	}
	return nil
}

func (s *sessionFilingFeatureState) storedTitle(want string) error {
	snap, err := s.store.ReadSnapshot(s.st.ID)
	if err != nil {
		return err
	}
	if snap.Meta.Title != want {
		return fmt.Errorf("session.json holds the title %q, want %q", snap.Meta.Title, want)
	}
	return nil
}

func (s *sessionFilingFeatureState) storedTags(want string) error {
	snap, err := s.store.ReadSnapshot(s.st.ID)
	if err != nil {
		return err
	}
	got := strings.Join(snap.Meta.Tags, ", ")
	if got != want {
		return fmt.Errorf("session.json holds the tags %q, want %q", got, want)
	}
	return nil
}

// splitListStep reads the comma separated list a step carries.
func splitListStep(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func TestSessionDescribeToolFeature(t *testing.T) {
	state := &sessionFilingFeatureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, state.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.close()
				return ctx, nil
			})
			sc.Step(`^a stored session titled "([^"]*)" tagged "([^"]*)"$`, state.storedSession)
			sc.Step(`^the model calls session_describe with:$`, state.modelCalls)
			sc.Step(`^the tool reports the title "([^"]*)"$`, state.reportedTitle)
			sc.Step(`^the tool reports the tags "([^"]*)"$`, state.reportedTags)
			sc.Step(`^the tool reports that nothing changed$`, state.reportedNoChange)
			sc.Step(`^the stored session is titled "([^"]*)"$`, state.storedTitle)
			sc.Step(`^the stored session is tagged "([^"]*)"$`, state.storedTags)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_describe_tool.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session_describe feature failed")
	}
}
