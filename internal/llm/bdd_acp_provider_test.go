package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
)

type acpFeatureState struct {
	t       *testing.T
	script  acptest.Script
	model   string
	logPath string
	conv    Conversation
	history []Message
	text    strings.Builder
	resp    *Response
	asked   []PermissionRequest
	allow   bool
	in      *ProviderInput
}

// provider builds the row once per scenario: every turn talks to the same
// agent and its log.
func (s *acpFeatureState) provider() (Provider, error) {
	if s.in == nil {
		in, logPath := acpRow(s.t, s.script)
		s.logPath = logPath
		if s.model != "" {
			in.Model = s.model
		}
		s.in = &in
	}
	return NewProvider(*s.in)
}

func (s *acpFeatureState) agentReadsAndAnswers(answer string) error {
	s.script = acptest.Script{Turns: []acptest.Turn{{
		Tools: []acptest.Tool{{ID: "t1", Title: "Read README.md"}},
		Text:  []string{answer},
	}}}
	return nil
}

func (s *acpFeatureState) agentCallsToolOn(tool, file string) error {
	s.script = acptest.Script{Turns: []acptest.Turn{{
		Tools: []acptest.Tool{{ID: "t1", Title: tool, Path: file}},
		Text:  []string{"done"},
	}}}
	return nil
}

func (s *acpFeatureState) agentEchoes() error {
	s.script = acptest.Script{}
	return nil
}

func (s *acpFeatureState) agentOffersModels(list string) error {
	var models []string
	for _, m := range strings.Split(list, ",") {
		models = append(models, strings.TrimSpace(m))
	}
	s.script = acptest.Script{Models: models}
	return nil
}

func (s *acpFeatureState) providersModel(model string) error {
	s.model = model
	return nil
}

func (s *acpFeatureState) agentAsksPermission(title string) error {
	s.script = acptest.Script{Turns: []acptest.Turn{{Permission: &acptest.Permission{
		Title: title, Kind: "edit", Content: "write hello",
		Options: []acptest.Option{
			{ID: "agent-allow", Name: "Allow once", Kind: "allow_once"},
			{ID: "agent-reject", Name: "Reject", Kind: "reject_once"},
		},
	}}}}
	return nil
}

func (s *acpFeatureState) ask(text string) error {
	if s.conv.SessionID == "" {
		s.conv = Conversation{SessionID: "sess_feature", CWD: s.t.TempDir(), Mode: "agent", PermissionMode: config.PermModeAsk}
		s.conv.AskPermission = func(_ context.Context, req PermissionRequest) (PermissionAnswer, error) {
			s.asked = append(s.asked, req)
			if s.allow {
				return PermissionAnswer{OptionID: "allow"}, nil
			}
			return PermissionAnswer{OptionID: "reject"}, nil
		}
	}
	p, err := s.provider()
	if err != nil {
		return err
	}
	s.history = append(s.history, Message{Role: RoleUser, Content: text})
	ctx, cancel := context.WithTimeout(WithConversation(context.Background(), s.conv), 60*time.Second)
	defer cancel()
	s.text.Reset()
	resp, err := p.Stream(ctx, append([]Message{{Role: RoleSystem, Content: "CODDY SYSTEM PROMPT"}}, s.history...), nil, func(c StreamChunk) {
		s.text.WriteString(c.TextDelta)
	})
	if err != nil {
		return err
	}
	s.resp = resp
	s.history = append(s.history, Message{Role: RoleAssistant, Content: resp.Content})
	return nil
}

func (s *acpFeatureState) askAndAllow(text string) error {
	s.allow = true
	return s.ask(text)
}

func (s *acpFeatureState) answerStreams(want string) error {
	if s.text.String() != want || s.resp.Content != want {
		return fmt.Errorf("streamed %q, answer %q, want %q", s.text.String(), s.resp.Content, want)
	}
	return nil
}

func (s *acpFeatureState) reasoningNamesTool(title string) error {
	if !strings.Contains(s.resp.Reasoning, "] "+title) {
		return fmt.Errorf("reasoning %q does not name %q", s.resp.Reasoning, title)
	}
	return nil
}

func (s *acpFeatureState) noSystemPrompt() error {
	for _, r := range acptest.ReadLog(s.t, s.logPath) {
		if strings.Contains(string(r.Params), "CODDY SYSTEM PROMPT") {
			return fmt.Errorf("the agent received the system prompt in %s", r.Method)
		}
	}
	return nil
}

func (s *acpFeatureState) oneSession() error {
	if n := len(acptest.Methods(acptest.ReadLog(s.t, s.logPath), "session/new")); n != 1 {
		return fmt.Errorf("session/new sent %d times", n)
	}
	return nil
}

func (s *acpFeatureState) secondPromptCarried(want string) error {
	prompts := acptest.Methods(acptest.ReadLog(s.t, s.logPath), "session/prompt")
	if len(prompts) != 2 {
		return fmt.Errorf("prompts = %d", len(prompts))
	}
	if got := promptTexts(s.t, prompts[1]); len(got) != 1 || got[0] != "text:"+want {
		return fmt.Errorf("second prompt = %q", got)
	}
	return nil
}

func (s *acpFeatureState) modelOptionSet(want string) error {
	for _, r := range acptest.Methods(acptest.ReadLog(s.t, s.logPath), "session/set_config_option") {
		var p struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		_ = json.Unmarshal(r.Params, &p)
		if p.ConfigID == "model" && p.Value == want {
			return nil
		}
	}
	return fmt.Errorf("the model option was not set to %q", want)
}

func (s *acpFeatureState) personAsked(title string) error {
	if len(s.asked) != 1 || s.asked[0].Title != title {
		return fmt.Errorf("prompts = %+v", s.asked)
	}
	return nil
}

func (s *acpFeatureState) agentHeardAllow() error {
	if !strings.Contains(s.resp.Content, "permission: agent-allow") {
		return fmt.Errorf("answer = %q", s.resp.Content)
	}
	return nil
}

func TestACPProviderFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "acp-provider",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &acpFeatureState{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = acpFeatureState{t: t}
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				StopACPAgents()
				return ctx, nil
			})
			sc.Step(`^an acp provider whose agent reads a file and answers "([^"]*)"$`, s.agentReadsAndAnswers)
			sc.Step(`^an acp provider whose agent echoes what it is asked$`, s.agentEchoes)
			sc.Step(`^an acp provider whose agent calls its tool "([^"]*)" and names the file "([^"]*)" only once it runs$`, s.agentCallsToolOn)
			sc.Step(`^an acp provider whose agent offers the models "([^"]*)"$`, s.agentOffersModels)
			sc.Step(`^the provider's model is "([^"]*)"$`, s.providersModel)
			sc.Step(`^an acp provider whose agent asks permission to "([^"]*)"$`, s.agentAsksPermission)
			sc.Step(`^a session asks "([^"]*)"$`, s.ask)
			sc.Step(`^the same session asks "([^"]*)"$`, s.ask)
			sc.Step(`^a session asks "([^"]*)" and the person allows it$`, s.askAndAllow)
			sc.Step(`^the answer streams as "([^"]*)"$`, s.answerStreams)
			sc.Step(`^the reasoning names the agent's tool "([^"]*)"$`, s.reasoningNamesTool)
			sc.Step(`^the agent did not receive Coddy's system prompt$`, s.noSystemPrompt)
			sc.Step(`^the agent opened one session$`, s.oneSession)
			sc.Step(`^the second prompt carried only "([^"]*)"$`, s.secondPromptCarried)
			sc.Step(`^the agent's model option was set to "([^"]*)"$`, s.modelOptionSet)
			sc.Step(`^the person was asked about "([^"]*)"$`, s.personAsked)
			sc.Step(`^the agent heard its own allow option$`, s.agentHeardAllow)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/acp_provider.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("acp provider feature failed")
	}
}
