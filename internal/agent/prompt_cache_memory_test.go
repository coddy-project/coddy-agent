//go:build memory

package agent

// The memory child is a conversation of its own, and a provider caches it by
// its prefix like any other: the tool definitions, then the system message.
// The guides of the memory roots open the child's task, a user message after
// that prefix, so a guide the operator edits between two runs must not move a
// byte of what comes before it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// memoryChildRequest is the first request of the memory child of a turn: its
// system message, the user messages and the tool definitions it was offered.
type memoryChildRequest struct {
	system string
	users  []string
	defs   string
}

func (s *memoryFeatureState) firstRequestOfChild(task int) (memoryChildRequest, error) {
	tasks := s.memoryTasks()
	if task >= len(tasks) {
		return memoryChildRequest{}, fmt.Errorf("only %d memory tasks", len(tasks))
	}
	s.mu.Lock()
	p := s.childProviders[tasks[task].Agent.SessionID]
	s.mu.Unlock()
	if p == nil || !p.wasCalled() {
		return memoryChildRequest{}, fmt.Errorf("the memory child of task %d never called its model", task)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var req memoryChildRequest
	for _, m := range p.requests[0] {
		switch m.Role {
		case llm.RoleSystem:
			req.system += m.Content
		case llm.RoleUser:
			req.users = append(req.users, m.Content)
		}
	}
	defs, err := json.Marshal(p.definitions[0])
	if err != nil {
		return memoryChildRequest{}, err
	}
	req.defs = string(defs)
	return req, nil
}

func TestPromptCacheMemoryChildPrefixIgnoresItsGuides(t *testing.T) {
	s := &memoryFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.memoryRootHolds("global", "AGENTS.md", "Global guide, first edition: notes live under topics/."))
	must(s.memoryRootHolds("project", "AGENTS.md", "Project guide, first edition: {{.CWD}} decisions live in decisions/."))
	must(s.parentSession())
	must(s.userSendsAndChildAnswers("what did we decide about the API?", "(no memory hits)"))

	// The guides change between the runs: one rewritten, one gone.
	must(s.memoryRootHolds("global", "AGENTS.md", "Global guide, second edition: everything moved under archive/ and a much longer text follows it."))
	must(os.Remove(filepath.Join(s.cwd, ".coddy", "memory", "AGENTS.md")))
	must(s.userSendsAndChildAnswers("and what about the database?", "(no memory hits)"))

	first, err := s.firstRequestOfChild(0)
	must(err)
	second, err := s.firstRequestOfChild(1)
	must(err)

	if first.system == "" || first.system != second.system {
		t.Fatalf("the child's system message moved between runs whose guides differ:\n--- first\n%s\n--- second\n%s", first.system, second.system)
	}
	if first.defs != second.defs || !strings.Contains(first.defs, "coddy_memory_search") {
		t.Fatalf("the child's tool definitions moved between runs whose guides differ:\n%s\n%s", first.defs, second.defs)
	}
	for _, guide := range []string{"Global guide", "Project guide", "{{.CWD}}"} {
		if strings.Contains(first.system+first.defs, guide) || strings.Contains(second.system+second.defs, guide) {
			t.Fatalf("a guide reached the prefix: %q", guide)
		}
	}
	// The runs did differ where they may: in the task, after the prefix.
	if !strings.Contains(strings.Join(first.users, "\n"), "Project guide, first edition: {{.CWD}}") ||
		!strings.Contains(strings.Join(second.users, "\n"), "second edition") ||
		strings.Contains(strings.Join(second.users, "\n"), "Project guide") {
		t.Fatalf("the guides did not travel in the task:\nfirst: %q\nsecond: %q", first.users, second.users)
	}
}
