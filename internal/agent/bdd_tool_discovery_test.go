package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/cucumber/godog"
)

func TestToolDiscoveryFeature(t *testing.T) {
	var ag *Agent
	var provider *scriptedProvider
	var actions *int
	initialize := func(sc *godog.ScenarioContext) {
		sc.Step(`^a virtual fixture with permitted read, grep and write tools$`, func() error { ag, _, provider, actions = discoveryFixture(t); return nil })
		sc.Step(`^a model that only greets$`, func() error { provider.steps = []scriptStep{answerStep("hello")}; return nil })
		sc.Step(`^a model that activates search and read, searches, then reads twice$`, func() error {
			provider.steps = []scriptStep{discoveryResponse(discoveryActivation("activate", "grep", "read")), discoveryResponse(llm.ToolCall{ID: "search", Name: "grep", InputJSON: `{"pattern":"value"}`}), discoveryResponse(llm.ToolCall{ID: "read_one", Name: "read", InputJSON: `{"path":"/virtual/one"}`}), discoveryResponse(llm.ToolCall{ID: "read_two", Name: "read", InputJSON: `{"path":"/virtual/two"}`}), answerStep("done")}
			return nil
		})
		sc.Step(`^the fixture turn runs$`, func() error {
			_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "Use the virtual fixture."}})
			return err
		})
		sc.Step(`^every permitted name is discoverable$`, func() error {
			names := promptToolNames(provider.requests[0][0].Content)
			if strings.Join(names, ",") != "grep,read,write" {
				return fmt.Errorf("catalog=%v", names)
			}
			return nil
		})
		sc.Step(`^the first native offer contains only activation$`, func() error {
			if strings.Join(provider.offered[0], ",") != "activate_tools" {
				return fmt.Errorf("native offer=%v", provider.offered[0])
			}
			return nil
		})
		sc.Step(`^the fixture performed (\d+) tool actions in (\d+) model requests$`, func(wantActions, wantRequests int) error {
			if *actions != wantActions || provider.calls != wantRequests {
				return fmt.Errorf("actions=%d requests=%d", *actions, provider.calls)
			}
			return nil
		})
		sc.Step(`^the discovery catalog stayed unchanged while native schemas grew$`, func() error {
			for i, r := range provider.requests[1:] {
				if r[0].Content != provider.requests[0][0].Content || !contains(provider.offered[i+1], "grep") || !contains(provider.offered[i+1], "read") {
					return fmt.Errorf("catalog or loaded definitions moved at request%d", i+1)
				}
			}
			return nil
		})
	}
	suite := godog.TestSuite{Name: "tool_discovery", ScenarioInitializer: initialize, Options: &godog.Options{Format: "progress", Paths: []string{"../../features/tool_discovery.feature"}, TestingT: t}}
	if code := suite.Run(); code != 0 {
		t.Fatal("tool discovery feature failed")
	}
}
