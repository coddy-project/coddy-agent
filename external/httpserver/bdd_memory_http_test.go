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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type memoryHTTPState struct {
	*subagentsHTTPState
	// outside is a folder outside the session's folder a scenario linked to,
	// removed after the scenario; secret is what the file there holds.
	outside, secret string
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

// sessionFolderHoldsNote writes a note at a path relative to the session's
// folder, so the scenario names where on disk the project's notes live.
func (s *memoryHTTPState) sessionFolderHoldsNote(rel, text string) error {
	path := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// sessionFolderLinksNote puts a link at rel, relative to the session's
// folder, to a file outside that folder holding text: what a clone of a
// repository that carries such a link leaves on disk.
func (s *memoryHTTPState) sessionFolderLinksNote(rel, text string) error {
	link := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	outside, err := os.MkdirTemp("", "coddy-memory-outside-*")
	if err != nil {
		return err
	}
	s.outside = outside
	target := filepath.Join(outside, "credentials")
	if err := os.WriteFile(target, []byte(text), 0o600); err != nil {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return godog.ErrSkip
	}
	s.secret = text
	return nil
}

func (s *memoryHTTPState) noteIsNotServed() error {
	if s.status == http.StatusOK {
		return fmt.Errorf("the linked note was served: %v", s.body)
	}
	if content, _ := s.body["content"].(string); s.secret != "" && strings.Contains(content, s.secret) {
		return fmt.Errorf("the answer carries the file behind the link: %v", s.body)
	}
	return nil
}

func (s *memoryHTTPState) getWorkspaceNote(rel string) error {
	q := url.Values{"root": {"workspace"}, "path": {rel}}
	return s.do(http.MethodGet, "/coddy/sessions/"+s.sessionID+"/memory/file?"+q.Encode(), "")
}

func (s *memoryHTTPState) noteReads(text string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("memory file answered %d: %v", s.status, s.body)
	}
	if got := s.body["content"]; got != text {
		return fmt.Errorf("memory file content = %q, want %q", got, text)
	}
	return nil
}

func initializeMemoryHTTPScenario(sc *godog.ScenarioContext) {
	s := &memoryHTTPState{subagentsHTTPState: &subagentsHTTPState{}}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		if s.outside != "" {
			_ = os.RemoveAll(s.outside)
		}
		s.outside, s.secret = "", ""
		return ctx, nil
	})

	sc.Step(`^a running coddy serve server with a session$`, s.startServerWithSession)
	sc.Step(`^that session started a memory run backed by child session "([^"]*)"$`, s.startedMemoryRun)
	sc.Step(`^a live memory child session "([^"]*)" of that session whose transcript says "([^"]*)"$`, s.liveMemoryChildSaying)
	sc.Step(`^the session folder holds the memory note "([^"]*)" saying "([^"]*)"$`, s.sessionFolderHoldsNote)
	sc.Step(`^the session folder holds the memory note "([^"]*)" as a link to a file outside it saying "([^"]*)"$`, s.sessionFolderLinksNote)

	sc.Step(`^I GET the background tasks of that session$`, s.listTasks)
	sc.Step(`^I GET the messages of "([^"]*)"$`, s.getMessages)
	sc.Step(`^I GET the messages of the parent session$`, s.getParentMessages)
	sc.Step(`^I GET the workspace memory note "([^"]*)" of that session$`, s.getWorkspaceNote)

	sc.Step(`^the response lists a task of kind "([^"]*)"$`, s.listsTaskOfKind)
	sc.Step(`^that task row names the agent "([^"]*)" and the child session "([^"]*)"$`, s.taskRowNames)
	sc.Step(`^that task row is flagged as a system task$`, s.taskRowIsSystem)
	sc.Step(`^the messages contain "([^"]*)"$`, s.messagesContain)
	sc.Step(`^the messages payload is read-only and links the parent session$`, s.messagesReadOnlyWithParent)
	sc.Step(`^the messages payload carries no "([^"]*)" field$`, s.messagesPayloadLacks)
	sc.Step(`^the memory note reads "([^"]*)"$`, s.noteReads)
	sc.Step(`^the memory note is not served$`, s.noteIsNotServed)
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
