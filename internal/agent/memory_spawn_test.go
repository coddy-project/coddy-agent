//go:build memory

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// spawnMemoryReq asks for the bundled memory subagent.
func spawnMemoryReq(prompt string) tooling.SpawnRequest {
	return tooling.SpawnRequest{Agent: session.SubagentKindMemory, Prompt: prompt, Description: "memory task"}
}

// A spawn under the bundled "memory" definition runs the memory subagent:
// its own tools, its prompt template and the guides of the memory roots
// ahead of the task, not the parent's tool set or the definition body.
func TestSpawnSubagentMemoryRunsTheMemoryChild(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Memory.Enabled = true
	})
	if err := os.MkdirAll(filepath.Join(rig.cwd, ".coddy", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rig.cwd, ".coddy", "memory", "AGENTS.md"), []byte("Project guide: decisions live in decisions/.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var childTools [][]string
	rig.setChildProvider(func(st *session.State) llm.Provider {
		p := scripted(answerStep("REPORT: nothing saved yet"))
		childTools = append(childTools, nil)
		return p
	})
	result, err := rig.parentAgent().spawnSubagent(context.Background(), spawnMemoryReq("What did we decide about the API?"))
	if err != nil {
		t.Fatal(err)
	}
	env := parseSubagentEnvelope(t, result)
	_ = env
	child := rig.childByDepth(1)
	meta := child.Subagent()
	if meta == nil {
		t.Fatal("the child carries no subagent meta")
	}
	if meta.Kind != session.SubagentKindMemory {
		t.Fatalf("child kind = %q, want %q", meta.Kind, session.SubagentKindMemory)
	}
	if meta.PromptTemplate == "" {
		t.Fatal("the memory child runs on its own prompt template")
	}
	want := []string{"coddy_memory_search", "coddy_memory_list", "coddy_memory_read", "coddy_memory_mkdir", "coddy_memory_save", "coddy_memory_delete"}
	if strings.Join(meta.Tools, ",") != strings.Join(want, ",") {
		t.Fatalf("child tools = %v, want %v", meta.Tools, want)
	}
	p := rig.childProviderOf(child)
	sys, ok := p.firstSystemPrompt()
	if !ok {
		t.Fatal("the child sent no request")
	}
	if !strings.Contains(sys, "MODE RECALL") {
		t.Fatalf("the child's system prompt is not the memory template:\n%s", sys)
	}
	var task string
	for _, m := range p.requests[0] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "User message for this turn:") {
			task = m.Content
		}
	}
	if task == "" {
		t.Fatalf("the child's request carries no task message: %+v", p.requests[0])
	}
	if !strings.Contains(task, "Memory guide (project:AGENTS.md):") || !strings.Contains(task, "Project guide: decisions live in decisions/.") {
		t.Fatalf("the task lacks the project guide:\n%s", task)
	}
	if !strings.Contains(task, "What did we decide about the API?") {
		t.Fatalf("the task lacks the parent's prompt:\n%s", task)
	}
	if p.everOffered("spawn_agent") || p.everOffered("run_command") {
		t.Fatalf("the memory child was offered a tool outside its set: %v", p.offered[0])
	}
	_ = childTools
}

// With the feature off the bundled "memory" definition is out of the
// catalog, so a spawn under that name is refused as an unknown subagent.
func TestSpawnSubagentMemoryRefusedWhenDisabled(t *testing.T) {
	rig := newSubagentRig(t, nil)
	_, err := rig.parentAgent().spawnSubagent(context.Background(), spawnMemoryReq("remember this"))
	if err == nil || !strings.Contains(err.Error(), "unknown subagent") {
		t.Fatalf("error = %v, want an unknown subagent refusal", err)
	}
}

// The memory roots belong to the session's workspace, so the child may not
// run inside a worktree of its own.
func TestSpawnSubagentMemoryRefusesAWorktree(t *testing.T) {
	rig := newSubagentRig(t, func(cfg *config.Config) {
		cfg.Memory.Enabled = true
	})
	req := spawnMemoryReq("remember this")
	req.Isolation = "worktree"
	_, err := rig.parentAgent().spawnSubagent(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("error = %v, want a worktree refusal", err)
	}
}
