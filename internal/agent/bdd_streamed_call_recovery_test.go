package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func TestStreamedCallRecoveryFeature(t *testing.T) {
	suite := godog.TestSuite{Options: &godog.Options{Format: "progress", Paths: []string{"../../features/streamed_call_recovery.feature"}, Strict: true, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		var p outputLimitProvider
		var st *session.State
		var sender *outputLimitSender
		sc.Step(`^the model announces a read in a batch rejected as malformed$`, func() {
			call := notesRead("read-id", 1)
			p.steps = []outputLimitStep{{chunks: []llm.StreamChunk{{ToolCallNamed: &call}}, err: &llm.StreamedToolCallError{Index: 1, FinishReason: "stop"}}}
		})
		sc.Step(`^the model sends a replacement read with the same id$`, func() error {
			call := notesRead("read-id", 2)
			p.steps = append(p.steps, outputLimitStep{calls: []llm.ToolCall{call}, chunks: []llm.StreamChunk{{ToolCallNamed: &call}, {ToolCall: &call}}}, answered)
			var ag *Agent
			ag, st, sender = cutCallAgent(t, &p, llmRetryMax(1))
			st.SessionDir = ""
			_, err := runOutputLimitTurn(ag)
			return err
		})
		sc.Step(`^only the replacement has a paired result and its own completed row$`, func() error {
			results := toolResults(st.GetMessages())
			if len(results) != 1 || results["read-id_2"] != "line 2" || strings.Join(sender.toolStatuses("read-id"), ",") != "failed" || strings.Join(sender.toolStatuses("read-id_2"), ",") != "in_progress,completed" {
				return fmt.Errorf("replacement results=%v; original=%v replacement=%v", results, sender.toolStatuses("read-id"), sender.toolStatuses("read-id_2"))
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("streamed call recovery feature failed")
	}
}
