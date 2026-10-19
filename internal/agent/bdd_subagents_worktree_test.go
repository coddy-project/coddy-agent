package agent

// Step definitions of features/subagents_worktree.feature, on the harness of
// features/subagents.feature: the workspace is made a git repository, and the
// child's cwd, its worktree and its branch are read back from the child's
// bundle, the task row and git.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func (s *subagentsFeatureState) gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitWorkspace makes the workspace a repository with one commit; the
// definitions under .coddy stay untracked, as in a checkout that never
// committed them.
func (s *subagentsFeatureState) gitWorkspace() error {
	if _, err := exec.LookPath("git"); err != nil {
		return godog.ErrSkip
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "README.md"), []byte("bdd\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "README.md"},
		{"-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "initial"},
	} {
		if _, err := s.gitIn(s.cwd, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *subagentsFeatureState) gitWorkspaceWithWorktreeDefinition(name string) error {
	if err := s.gitWorkspace(); err != nil {
		return err
	}
	return s.writeDefinition(name, "isolation: worktree\n")
}

func (s *subagentsFeatureState) gitWorkspaceWithDefinition(name string) error {
	if err := s.gitWorkspace(); err != nil {
		return err
	}
	return s.writeDefinition(name, "")
}

func (s *subagentsFeatureState) spawnWritingFile(agent, file, answer string) error {
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		args, _ := json.Marshal(map[string]string{"path": file, "content": "the child's fix\n"})
		return []scriptStep{
			toolStep(llm.ToolCall{ID: "call_write", Name: "write", InputJSON: string(args)}),
			answerStep(answer),
		}
	}
	s.mu.Unlock()
	return s.spawnWith(agent, false)
}

func (s *subagentsFeatureState) spawnInWorktree(agent, answer string) error {
	s.setChildAnswer(answer)
	s.lastAgentName = agent
	call := spawnCall("call_spawn", agent, false)
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(call.InputJSON), &args); err != nil {
		return err
	}
	args["isolation"] = "worktree"
	b, _ := json.Marshal(args)
	call.InputJSON = string(b)
	results, err := s.runParentTurn(toolStep(call), answerStep("parent done"))
	if err != nil {
		return err
	}
	res, ok := results["call_spawn"]
	if !ok {
		return fmt.Errorf("spawn_agent produced no tool result")
	}
	s.spawnResults = append(s.spawnResults, res)
	s.noteLastAgentTask()
	return nil
}

// childWorktree is the worktree and branch the last agent task names.
func (s *subagentsFeatureState) childWorktree() (string, string, error) {
	t, err := s.lastAgentTask()
	if err != nil {
		return "", "", err
	}
	if t.Agent.Worktree == "" || t.Agent.Branch == "" {
		return "", "", fmt.Errorf("the agent task %+v names no worktree of its own", t.Agent)
	}
	return t.Agent.Worktree, t.Agent.Branch, nil
}

func (s *subagentsFeatureState) childWorkedInWorktree() error {
	wt, _, err := s.childWorktree()
	if err != nil {
		return err
	}
	snap, err := s.childSnapshot()
	if err != nil {
		return err
	}
	cwd := snap.Meta.CWD
	if session.SameWorkspacePath(cwd, s.cwd) {
		return fmt.Errorf("the child worked in the parent's folder %s", cwd)
	}
	if !session.SameWorkspacePath(cwd, wt) {
		return fmt.Errorf("the child worked in %s, want its worktree %s", cwd, wt)
	}
	root := filepath.Join(s.cwd, ".coddy", "worktrees")
	if rel, err := filepath.Rel(root, wt); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("the child's worktree %s is not under %s", wt, root)
	}
	return nil
}

func (s *subagentsFeatureState) noWorktreeOfTheChildLeft() error {
	wt, branch, err := s.childWorktree()
	if err != nil {
		return err
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		return fmt.Errorf("the child's worktree %s is still there (%v)", wt, err)
	}
	list, err := s.gitIn(s.cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if strings.Contains(list, wt) {
		return fmt.Errorf("git still lists %s:\n%s", wt, list)
	}
	if out, err := s.gitIn(s.cwd, "branch", "--list", branch); err != nil || out != "" {
		return fmt.Errorf("branch %s is still there (%q, %v)", branch, out, err)
	}
	return nil
}

func (s *subagentsFeatureState) parentFolderHasNo(file string) error {
	if _, err := os.Stat(filepath.Join(s.cwd, file)); !os.IsNotExist(err) {
		return fmt.Errorf("%s is in the parent's folder (%v)", file, err)
	}
	return nil
}

func (s *subagentsFeatureState) childWorktreeKeeps(file string) error {
	wt, branch, err := s.childWorktree()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(wt, file)); err != nil {
		return fmt.Errorf("the child's worktree lost %s: %w", file, err)
	}
	if got, err := s.gitIn(wt, "rev-parse", "--abbrev-ref", "HEAD"); err != nil || got != branch {
		return fmt.Errorf("the child's worktree is on %q (%v), want %s", got, err, branch)
	}
	return nil
}

func (s *subagentsFeatureState) spawnResultNamesWorktree() error {
	wt, branch, err := s.childWorktree()
	if err != nil {
		return err
	}
	res, err := s.lastSpawnResult()
	if err != nil {
		return err
	}
	if !strings.Contains(res, wt) || !strings.Contains(res, branch) {
		return fmt.Errorf("spawn_agent result %q names neither the worktree %s nor the branch %s", res, wt, branch)
	}
	return nil
}

func registerSubagentWorktreeSteps(sc *godog.ScenarioContext, s *subagentsFeatureState) {
	sc.Step(`^a git repository as the workspace with a subagent definition "([^"]*)" that works in a worktree$`, s.gitWorkspaceWithWorktreeDefinition)
	sc.Step(`^a git repository as the workspace with a subagent definition "([^"]*)" under \.coddy/agents$`, s.gitWorkspaceWithDefinition)
	sc.Step(`^the parent model spawns "([^"]*)" in the foreground and the child writes "([^"]*)" before it answers "([^"]*)"$`, s.spawnWritingFile)
	sc.Step(`^the parent model spawns "([^"]*)" in a worktree of its own and the child answers "([^"]*)"$`, s.spawnInWorktree)
	sc.Step(`^the child session worked in a worktree of the repository, not in the parent's folder$`, s.childWorkedInWorktree)
	sc.Step(`^the repository has no worktree and no branch of the child left$`, s.noWorktreeOfTheChildLeft)
	sc.Step(`^the parent's folder has no "([^"]*)"$`, s.parentFolderHasNo)
	sc.Step(`^the child's worktree keeps "([^"]*)"$`, s.childWorktreeKeeps)
	sc.Step(`^the spawn_agent tool result names the child's worktree and its branch$`, s.spawnResultNamesWorktree)
}
