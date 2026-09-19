package session_test

// Godog harness for the @acp scenario of features/devin_provider.feature: a
// real ReAct turn through the ACP session flow whose LLM is the REAL devin
// provider built by llm.NewProvider, signed in through a Coddy-managed
// credential, against the offline Devin stand. The stand asks for coddy's own
// read tool on the first request and quotes its result on the second.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	acpDevinToken     = "devin-session-token$acp-stand"
	acpDevinFileText  = "DEVIN-ACP-FILE-OK"
	acpDevinSignature = "EpMCCpABCBEY-stand-signature"
	acpDevinThinking  = "The file is in the workspace, so I read it."
)

type acpDevinState struct {
	root     string
	home     string
	cwd      string
	readPath string
	stand    *devinfake.Server
	ts       *httptest.Server
	mgr      *session.Manager
	state    *session.State
	envPrev  map[string]*string
	cfg      *config.Config
}

func (s *acpDevinState) setEnv(name, value string) error {
	if _, saved := s.envPrev[name]; !saved {
		if prev, ok := os.LookupEnv(name); ok {
			s.envPrev[name] = &prev
		} else {
			s.envPrev[name] = nil
		}
	}
	return os.Setenv(name, value)
}

func (s *acpDevinState) reset() error {
	s.close()
	s.envPrev = map[string]*string{}
	root, err := os.MkdirTemp("", "coddy-bdd-acp-devin-*")
	if err != nil {
		return err
	}
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.readPath = filepath.Join(s.cwd, "devin-acp.txt")
	return os.WriteFile(s.readPath, []byte(acpDevinFileText+"\n"), 0o644)
}

func (s *acpDevinState) close() {
	if s.state != nil {
		s.state.CloseAll()
		s.state = nil
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	for name, prev := range s.envPrev {
		if prev == nil {
			_ = os.Unsetenv(name)
		} else {
			_ = os.Setenv(name, *prev)
		}
	}
	s.envPrev = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.mgr = nil
}

// standReadsThenQuotes scripts the model: the first request thinks, signs
// its reasoning and calls coddy's read tool; the second quotes the result.
func (s *acpDevinState) standReadsThenQuotes() error {
	readPath := s.readPath
	s.stand = devinfake.New(devinfake.Options{
		SessionToken: acpDevinToken,
		Models: []devinfake.Model{
			{UID: "claude-opus-5-medium", Family: "claude-opus-5", FamilyLabel: "Claude Opus 5", Default: true, ContextWindow: 1000000, MaxOutput: 128000},
			{UID: "claude-opus-5-high", Family: "claude-opus-5", FamilyLabel: "Claude Opus 5", ContextWindow: 1000000, MaxOutput: 128000},
		},
		Reply: func(req devinfake.ChatRequest, n int) devinfake.Turn {
			for _, p := range req.Prompts {
				if p.Source == 4 {
					return devinfake.Turn{Text: "The file says: " + strings.TrimSpace(p.Text), StopReason: 4, Input: 40, Output: 8}
				}
			}
			args, _ := json.Marshal(map[string]string{"path": readPath})
			return devinfake.Turn{
				Thinking: acpDevinThinking, Signature: acpDevinSignature, SignatureType: "anthropic",
				ToolCalls:  []devinfake.ToolCall{{ID: "toolu_devin_1", Name: "read", Args: string(args)}},
				StopReason: 10, Input: 30, Output: 12,
			}
		},
	})
	s.ts = httptest.NewServer(s.stand)
	return nil
}

// managerSignedInThroughCoddy mirrors cmd/coddy's ACP wiring: the real agent
// runner and provider factory, a managed credential on disk, and the process
// pointed at the stand.
func (s *acpDevinState) managerSignedInThroughCoddy() error {
	for _, env := range []string{llm.EnvDevinAPIServerURL, llm.EnvDevinWebappURL, llm.EnvDevinAPIURL} {
		if err := s.setEnv(env, s.ts.URL); err != nil {
			return err
		}
	}
	if err := s.setEnv(llm.EnvDevinCLICredentials, filepath.Join(s.root, "no-devin-cli.toml")); err != nil {
		return err
	}
	if err := s.setEnv("DEVIN_API_KEY", ""); err != nil {
		return err
	}
	authPath := config.DevinAuthPath(s.home, "devin")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		return err
	}
	cred, _ := json.Marshal(map[string]string{"session_token": acpDevinToken})
	if err := os.WriteFile(authPath, cred, 0o600); err != nil {
		return err
	}
	levels := []string{"medium", "high"}
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "devin", Type: "devin"}},
		Agent:     config.Agent{Model: "devin/claude-opus-5"},
	}
	s.cfg.Models = []config.ModelEntry{{Model: "devin/claude-opus-5", ReasoningLevels: &levels, ReasoningDefault: "medium"}}
	return nil
}

func (s *acpDevinState) runPromptAtLevel(model, level string) error {
	ent := s.cfg.FindModelEntry(model)
	if ent == nil {
		return fmt.Errorf("model %q is not configured", model)
	}
	ent.ReasoningDefault = level
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		s.state = st
		return agent.NewAgent(s.cfg, st, snd, log).Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, noopSender{}, runner, log, s.cwd, nil)
	ctx := context.Background()
	newRes, err := s.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	res, err := s.mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "read the workspace file"}},
	})
	if err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	if res.StopReason != acp.StopReasonEndTurn {
		return fmt.Errorf("stop reason = %q, want end_turn", res.StopReason)
	}
	return nil
}

func (s *acpDevinState) standReceivedUID(uid string) error {
	chats := s.stand.Chats()
	if len(chats) == 0 {
		return fmt.Errorf("the stand received no chat request")
	}
	for i, c := range chats {
		if c.ModelUID != uid {
			return fmt.Errorf("request %d model uid = %q, want %q", i+1, c.ModelUID, uid)
		}
		if c.IDE != "devin-desktop" {
			return fmt.Errorf("request %d came from IDE %q, want devin-desktop", i+1, c.IDE)
		}
		if c.APIKey != acpDevinToken || c.JWT == "" {
			return fmt.Errorf("request %d did not carry the session token and a user JWT", i+1)
		}
	}
	if ides := s.stand.CatalogIDEs(); len(ides) == 0 || ides[0] != "windsurf" {
		return fmt.Errorf("catalog asked for as %v, want a windsurf client", ides)
	}
	return nil
}

func (s *acpDevinState) requestCarriedCoddyToolsAndPrompt() error {
	chats := s.stand.Chats()
	if len(chats) == 0 {
		return fmt.Errorf("the stand received no chat request")
	}
	first := chats[0]
	if !strings.Contains(strings.ToLower(first.System), "coddy") {
		return fmt.Errorf("system prompt is not coddy's: %.200q", first.System)
	}
	for _, want := range []string{"read", "glob", "run_command"} {
		found := false
		for _, name := range first.Tools {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("coddy tool %q was not offered; offered %v", want, first.Tools)
		}
	}
	if first.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens = %d, want the catalog's output limit", first.MaxTokens)
	}
	return nil
}

func (s *acpDevinState) secondRequestReplayedToolRound() error {
	chats := s.stand.Chats()
	if len(chats) < 2 {
		return fmt.Errorf("the stand saw %d chat requests, want 2", len(chats))
	}
	var call, result *devinfake.Prompt
	for i := range chats[1].Prompts {
		p := &chats[1].Prompts[i]
		switch {
		case p.Source == 2 && len(p.ToolCalls) > 0:
			call = p
		case p.Source == 4:
			result = p
		}
	}
	if call == nil || call.ToolCalls[0].ID != "toolu_devin_1" || call.ToolCalls[0].Name != "read" {
		return fmt.Errorf("the assistant tool call was not replayed: %+v", chats[1].Prompts)
	}
	if call.Signature != acpDevinSignature || call.SignatureType != "anthropic" || call.Thinking != acpDevinThinking {
		return fmt.Errorf("the signed reasoning was not replayed verbatim: thinking %q signature %q type %q", call.Thinking, call.Signature, call.SignatureType)
	}
	if result == nil || result.ToolCallID != "toolu_devin_1" || !strings.Contains(result.Text, acpDevinFileText) {
		return fmt.Errorf("the tool result was not sent back: %+v", result)
	}
	return nil
}

func (s *acpDevinState) finalAnswerQuotesFile() error {
	if s.state == nil {
		return fmt.Errorf("no session state captured")
	}
	msgs := s.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && strings.TrimSpace(msgs[i].Content) != "" {
			if !strings.Contains(msgs[i].Content, acpDevinFileText) {
				return fmt.Errorf("final assistant message %q does not quote the file", msgs[i].Content)
			}
			return nil
		}
	}
	return fmt.Errorf("no assistant message in the transcript")
}

func initializeACPDevinScenario(sc *godog.ScenarioContext) {
	s := &acpDevinState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a Devin stand whose model reads a workspace file with coddy's tool and then quotes it$`, s.standReadsThenQuotes)
	sc.Step(`^an ACP session manager with a devin provider signed in through Coddy$`, s.managerSignedInThroughCoddy)
	sc.Step(`^I run an agent prompt on "([^"]+)" at reasoning level "([^"]+)"$`, s.runPromptAtLevel)
	sc.Step(`^the Devin stand received the model uid "([^"]+)" from a Devin Desktop client$`, s.standReceivedUID)
	sc.Step(`^the chat request carried coddy's own tools and system prompt$`, s.requestCarriedCoddyToolsAndPrompt)
	sc.Step(`^the second chat request replayed the tool call, its result and the signed reasoning$`, s.secondRequestReplayedToolRound)
	sc.Step(`^the final assistant message quotes the workspace file$`, s.finalAnswerQuotesFile)
}

func TestDevinProviderACPE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "devin_provider_acp",
		ScenarioInitializer: initializeACPDevinScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/devin_provider.feature"},
			Tags:     "@acp",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("devin_provider @acp feature failed")
	}
}
