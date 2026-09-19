//go:build http && memory

package httpserver

// Godog harness for features/memory_http.feature. It reuses the subagents
// HTTP harness (a real httptest server over a real session.Manager and the
// process-wide pool) and adds what a memory run is on the REST surface: a
// system agent task named memory, and a child session like any other.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type memoryHTTPState struct {
	*subagentsHTTPState
}

// startedMemoryRun registers the system task of a memory run under the
// session and creates its child inside the launch callback, as the memory
// runtime does.
func (s *memoryHTTPState) startedMemoryRun(childID string) error {
	pool := bgtask.Default()
	if dir := s.sessionDir(s.sessionID); dir != "" {
		pool.SetSessionDir(s.sessionID, dir)
	}
	handle := newBDDAgentHandle()
	spec := bgtask.Spec{
		SessionID: s.sessionID,
		Kind:      bgtask.KindAgent,
		Label:     "memory: what did we decide",
		CWD:       s.root,
		Agent:     &bgtask.AgentInfo{Name: session.SubagentKindMemory, SessionID: childID, System: true},
	}
	snap, err := pool.Launch(spec, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		_, _ = fmt.Fprintf(out, "subagent memory (task %s, session %s) starting\n", taskID, childID)
		_, err := s.mgr.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID:              childID,
			ParentSessionID: s.sessionID,
			Name:            session.SubagentKindMemory,
			TaskID:          taskID,
			CWD:             s.root,
			Mode:            "agent",
			Title:           "memory: what did we decide",
			Depth:           1,
			Kind:            session.SubagentKindMemory,
		})
		if err != nil {
			return nil, err
		}
		s.children = append(s.children, childID)
		return handle, nil
	})
	if err != nil {
		return err
	}
	s.tasks[childID] = subagentTaskRef{parentID: s.sessionID, taskID: snap.ID}
	return nil
}

func (s *memoryHTTPState) liveMemoryChildSaying(childID, text string) error {
	st, err := s.mgr.CreateSubagentSession(context.Background(), session.SubagentSpec{
		ID:              childID,
		ParentSessionID: s.sessionID,
		Name:            session.SubagentKindMemory,
		TaskID:          "bg_mem",
		CWD:             s.root,
		Mode:            "agent",
		Title:           "memory: bdd",
		Depth:           1,
		Kind:            session.SubagentKindMemory,
	})
	if err != nil {
		return err
	}
	s.children = append(s.children, childID)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "User message for this turn:\nwhat did we decide?"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: text})
	return s.mgr.FileStore().Save(st)
}

func (s *memoryHTTPState) taskRowIsSystem() error {
	if s.taskRow == nil {
		return fmt.Errorf("no task row matched yet")
	}
	info, ok := s.taskRow["agent"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("task row carries no agent object: %v", s.taskRow)
	}
	if info["system"] != true {
		return fmt.Errorf("agent object %v is not flagged as a system task", info)
	}
	return nil
}

func (s *memoryHTTPState) getParentMessages() error {
	return s.getMessages(s.sessionID)
}

func (s *memoryHTTPState) messagesReadOnlyWithParent() error {
	if s.status != http.StatusOK {
		return fmt.Errorf("messages answered %d: %v", s.status, s.body)
	}
	if s.body["readOnly"] != true {
		return fmt.Errorf("messages payload is not read-only: %v", s.body)
	}
	link, ok := s.body["subagent"].(map[string]interface{})
	if !ok || link["parentSessionId"] != s.sessionID || link["name"] != session.SubagentKindMemory {
		return fmt.Errorf("messages payload subagent link = %v, want the parent %q and the name memory", s.body["subagent"], s.sessionID)
	}
	return nil
}

func (s *memoryHTTPState) messagesPayloadLacks(field string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("messages answered %d: %v", s.status, s.body)
	}
	if _, present := s.body[field]; present {
		return fmt.Errorf("messages payload carries %q: %v", field, s.body[field])
	}
	return nil
}

func initializeMemoryHTTPScenario(sc *godog.ScenarioContext) {
	s := &memoryHTTPState{subagentsHTTPState: &subagentsHTTPState{}}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy serve server with a session$`, s.startServerWithSession)
	sc.Step(`^that session started a memory run backed by child session "([^"]*)"$`, s.startedMemoryRun)
	sc.Step(`^a live memory child session "([^"]*)" of that session whose transcript says "([^"]*)"$`, s.liveMemoryChildSaying)

	sc.Step(`^I GET the background tasks of that session$`, s.listTasks)
	sc.Step(`^I GET the messages of "([^"]*)"$`, s.getMessages)
	sc.Step(`^I GET the messages of the parent session$`, s.getParentMessages)

	sc.Step(`^the response lists a task of kind "([^"]*)"$`, s.listsTaskOfKind)
	sc.Step(`^that task row names the agent "([^"]*)" and the child session "([^"]*)"$`, s.taskRowNames)
	sc.Step(`^that task row is flagged as a system task$`, s.taskRowIsSystem)
	sc.Step(`^the messages contain "([^"]*)"$`, s.messagesContain)
	sc.Step(`^the messages payload is read-only and links the parent session$`, s.messagesReadOnlyWithParent)
	sc.Step(`^the messages payload carries no "([^"]*)" field$`, s.messagesPayloadLacks)
}

func TestMemoryHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "memory_http",
		ScenarioInitializer: initializeMemoryHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/memory_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("memory http feature suite failed")
	}
}
