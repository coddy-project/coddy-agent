package agent

// Godog harness for the @subagent scenarios of features/hooks_subagents.feature:
// the subagents harness (a real manager, scripted parent and child providers,
// the process-wide pool) with a hooks.json in its temporary home that points at
// this test binary re-executed as the hook process.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type hooksSubagentsState struct {
	*subagentsFeatureState
	entries    []hooktest.Entry
	recordFile string
}

func (s *hooksSubagentsState) reset() error {
	if s.subagentsFeatureState == nil {
		s.subagentsFeatureState = &subagentsFeatureState{}
	}
	if err := s.subagentsFeatureState.reset(); err != nil {
		return err
	}
	s.entries = nil
	s.recordFile = filepath.Join(s.root, "payload.json")
	return nil
}

func (s *hooksSubagentsState) addHook(event, matcher string, handler hooks.Handler) error {
	s.entries = append(s.entries, hooktest.Entry{Event: event, Matcher: matcher, Handlers: []hooks.Handler{handler}})
	return hooktest.Write(filepath.Join(s.home, "hooks.json"), s.entries...)
}

func (s *hooksSubagentsState) hookRecords(event, matcher string) error {
	return s.addHook(event, matcher, hooktest.Handler("record", s.recordFile))
}

func (s *hooksSubagentsState) hookAddsContext(event, matcher, text string) error {
	return s.addHook(event, matcher, hooktest.Handler("context", text))
}

func (s *hooksSubagentsState) hookBlocks(event, matcher, reason string) error {
	return s.addHook(event, matcher, hooktest.Handler("block", reason))
}

func (s *hooksSubagentsState) recordedPayload() (map[string]interface{}, error) {
	data, err := os.ReadFile(s.recordFile)
	if err != nil {
		return nil, fmt.Errorf("the recording hook did not run: %w", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("recorded payload is not JSON: %w", err)
	}
	return payload, nil
}

func (s *hooksSubagentsState) payloadNamesTool(event, tool string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["hook_event_name"] != event || payload["tool_name"] != tool {
		return fmt.Errorf("payload event %v tool %v, want %s %s", payload["hook_event_name"], payload["tool_name"], event, tool)
	}
	return nil
}

func (s *hooksSubagentsState) payloadCarriesSubagent(name string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	sub, _ := payload["subagent"].(map[string]interface{})
	if sub["name"] != name || sub["parent_session_id"] != s.parent.ID {
		return fmt.Errorf("payload subagent block %v, want name %q and parent %s", payload["subagent"], name, s.parent.ID)
	}
	return nil
}

func (s *hooksSubagentsState) childTaskCarried(text string) error {
	if s.lastChildID == "" {
		return fmt.Errorf("no child session recorded")
	}
	s.mu.Lock()
	p, ok := s.childProviders[s.lastChildID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("no provider for child %s", s.lastChildID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return fmt.Errorf("the child model was never called")
	}
	for _, m := range p.requests[0] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, text) {
			return nil
		}
	}
	return fmt.Errorf("the child's task carried no %q", text)
}

func (s *hooksSubagentsState) spawnBlockedByHook(reason string) error {
	if len(s.spawnResults) == 0 {
		return fmt.Errorf("no spawn result")
	}
	res := s.spawnResults[len(s.spawnResults)-1]
	if !strings.Contains(res, "blocked by hook") || !strings.Contains(res, reason) {
		return fmt.Errorf("spawn result %q does not report the hook block with reason %q", res, reason)
	}
	return nil
}

func (s *hooksSubagentsState) payloadNamesSubagentEvent(event, name string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["hook_event_name"] != event || payload["agent_name"] != name {
		return fmt.Errorf("payload event %v agent %v, want %s %s", payload["hook_event_name"], payload["agent_name"], event, name)
	}
	return nil
}

func (s *hooksSubagentsState) payloadCarriesReport(fragment string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	report, _ := payload["report"].(string)
	if !strings.Contains(report, fragment) {
		return fmt.Errorf("payload report %q lacks %q", report, fragment)
	}
	return nil
}

func (s *hooksSubagentsState) payloadCarriesStatus(status string) error {
	payload, err := s.recordedPayload()
	if err != nil {
		return err
	}
	if payload["status"] != status {
		return fmt.Errorf("payload status %v, want %s", payload["status"], status)
	}
	return nil
}

func initializeHooksSubagentsScenario(sc *godog.ScenarioContext) {
	s := &hooksSubagentsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a workspace with a subagent definition "([^"]*)" under \.coddy/agents$`, func(name string) error { return s.workspaceDefinition(name) })
	sc.Step(`^the workspace definition "([^"]*)" is approved for that workspace$`, func(name string) error { return s.approveDefinition(name) })
	sc.Step(`^a parent agent session in that workspace$`, func() error { return s.parentSession() })
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that records its stdin$`, s.hookRecords)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that adds the context "([^"]*)"$`, s.hookAddsContext)
	sc.Step(`^the operator's hooks\.json has a (\w+) hook for "([^"]*)" that blocks with the reason "([^"]*)"$`, s.hookBlocks)
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child answers "([^"]*)"$`, func(agent, answer string) error { return s.spawnForeground(agent, answer) })
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child runs a command before answering "([^"]*)"$`, func(agent, answer string) error { return s.spawnForegroundCommand(agent, answer) })
	sc.Step(`^the spawn_agent tool result contains "([^"]*)"$`, func(text string) error { return s.spawnResultContains(text) })
	sc.Step(`^no child session was created$`, func() error { return s.noChildSessionCreated() })
	sc.Step(`^the recorded payload names the event "([^"]*)" and the tool "([^"]*)"$`, s.payloadNamesTool)
	sc.Step(`^the recorded payload carries the subagent "([^"]*)" spawned by the parent session$`, s.payloadCarriesSubagent)
	sc.Step(`^the child model's task carried the context "([^"]*)"$`, s.childTaskCarried)
	sc.Step(`^the spawn_agent tool result says the spawn was blocked by a hook with the reason "([^"]*)"$`, s.spawnBlockedByHook)
	sc.Step(`^the recorded payload names the event "([^"]*)" for the subagent "([^"]*)"$`, s.payloadNamesSubagentEvent)
	sc.Step(`^the recorded payload carries a report mentioning "([^"]*)"$`, s.payloadCarriesReport)
	sc.Step(`^the recorded payload carries the status "([^"]*)"$`, s.payloadCarriesStatus)
}

func TestHooksSubagentsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "hooks-subagents",
		ScenarioInitializer: initializeHooksSubagentsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/hooks_subagents.feature"},
			Tags:     "@subagent",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("hooks subagents feature suite failed")
	}
}
