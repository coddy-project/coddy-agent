//go:build http

package httpserver

// Godog harness for features/session_title_commands.feature: what POST
// /coddy/describe tells the model about the slash commands a first message
// invokes, and the title that comes back. The model is a stub that names the
// chat well only when it was told what the commands do, which is the
// difference the feature is about; it repeats the text otherwise, as a model
// with nothing to go on does.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// commandAwareTitleModel answers the scripted phrase once the system prompt
// describes the invoked commands, and the user's text otherwise.
type commandAwareTitleModel struct {
	reply string

	mu     sync.Mutex
	system string
}

func (p *commandAwareTitleModel) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	var system, user string
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			system += m.Content
		case llm.RoleUser:
			user = m.Content
		}
	}
	p.mu.Lock()
	p.system = system
	p.mu.Unlock()
	if strings.Contains(system, describeCommandsHeading) {
		return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
	}
	return &llm.Response{Content: user, StopReason: "end_turn"}, nil
}

func (p *commandAwareTitleModel) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	return p.Complete(ctx, msgs, tools)
}

func (p *commandAwareTitleModel) lastSystem() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.system
}

type sessTitleCommandsState struct {
	sessTagsState
	model *commandAwareTitleModel
}

func (s *sessTitleCommandsState) titleModelAware(phrase, tags string) error {
	s.model = &commandAwareTitleModel{reply: phrase + "\ntags: " + tags}
	s.srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return s.model, nil
	}
	return nil
}

// writeQuotedSkill writes a skill whose description is a double-quoted YAML
// string, so a description with a colon in it stays one value.
func writeQuotedSkill(dir, name, description string) error {
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: %q\n---\n\n# %s\n", name, description, name)
	return os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644)
}

func (s *sessTitleCommandsState) workspaceHasSkill(name, description string) error {
	return writeQuotedSkill(filepath.Join(s.root, ".agents", "skills"), name, description)
}

func (s *sessTitleCommandsState) folderHasSkill(folder, name, description string) error {
	return writeQuotedSkill(filepath.Join(s.root, folder, ".agents", "skills"), name, description)
}

// newChatInFolderAsks is the web UI's first send in a chat whose folder was
// picked on the start screen: the session the header names does not exist on
// the server yet, so the folder travels as the cwd query parameter.
func (s *sessTitleCommandsState) newChatInFolderAsks(folder, text string) error {
	q := url.Values{"cwd": {filepath.Join(s.root, folder)}}
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/coddy/describe?"+q.Encode(),
		strings.NewReader(fmt.Sprintf(`{"text":%q}`, text)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", session.NewSessionID())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("describe returned %d", res.StatusCode)
	}
	return nil
}

func (s *sessTitleCommandsState) modelWasToldCommand(command, description string) error {
	want := "- " + command + ": " + description
	for _, line := range strings.Split(s.model.lastSystem(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), want) {
			return nil
		}
	}
	return fmt.Errorf("the title model was not told %q; system prompt:\n%s", want, s.model.lastSystem())
}

func (s *sessTitleCommandsState) modelWasToldNoCommands() error {
	if sys := s.model.lastSystem(); strings.Contains(sys, describeCommandsHeading) {
		return fmt.Errorf("the title model was told about commands:\n%s", sys)
	}
	return nil
}

func initializeSessionTitleCommandsScenario(sc *godog.ScenarioContext) {
	s := &sessTitleCommandsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.describe = nil
		s.model = nil
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^the title model answers "([^"]*)" with tags "([^"]*)" once it is told what the commands do, and repeats the text otherwise$`, s.titleModelAware)
	sc.Step(`^the workspace has a skill "([^"]*)" described "([^"]*)"$`, s.workspaceHasSkill)
	sc.Step(`^the folder "([^"]*)" has a skill "([^"]*)" described "([^"]*)"$`, s.folderHasSkill)

	sc.Step(`^I ask for a description of "([^"]*)"$`, s.askForDescriptionOf)
	sc.Step(`^a new chat in the folder "([^"]*)" asks for a description of "([^"]*)"$`, s.newChatInFolderAsks)

	sc.Step(`^the title model was told that "([^"]*)" means "([^"]*)"$`, s.modelWasToldCommand)
	sc.Step(`^the title model was told about no commands$`, s.modelWasToldNoCommands)
	sc.Step(`^the description is "([^"]*)"$`, s.descriptionIs)
	sc.Step(`^the description proposes tags "([^"]*)"$`, s.descriptionProposesTags)
}

func TestSessionTitleCommandsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-title-commands",
		ScenarioInitializer: initializeSessionTitleCommandsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_title_commands.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session title commands feature failed")
	}
}
