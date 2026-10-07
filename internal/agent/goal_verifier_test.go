package agent

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The verifier is a system child on the read-only tools: it reads the
// workspace and its final JSON is the verdict the supervisor gets.
func TestGoalVerifierReadsTheWorkspaceAndAnswersAVerdict(t *testing.T) {
	rig := newSubagentRig(t, nil)
	if err := os.WriteFile(filepath.Join(rig.cwd, "README.md"), []byte("# Done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := scripted(
		toolStep(llm.ToolCall{ID: "r1", Name: "read", InputJSON: `{"path":"README.md"}`}),
		answerStep("```json\n{\"verdict\":\"met\",\"reason\":\"README is written\",\"checklist\":[{\"text\":\"README exists\",\"status\":\"met\",\"evidence\":\"README.md:1\"}]}\n```"),
	)
	rig.setChildProvider(func(*session.State) llm.Provider { return child })

	verify := NewGoalVerifier(rig.mgr, nil)
	got, err := verify(context.Background(), rig.parent, session.GoalCheckRequest{
		Objective: "write the README", Digest: "[tool] write README.md",
		Checklist: []session.GoalItem{{Text: "README exists", Status: "met"}},
	})
	if err != nil || got.Verdict != session.GoalVerdictMet || len(got.Checklist) != 1 || got.Model != "fake/model" {
		t.Fatalf("verdict=%+v err=%v", got, err)
	}

	child.mu.Lock()
	offered := append([]string(nil), child.offered[0]...)
	var task string
	for _, m := range child.requests[0] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "Verify that") {
			task = m.Content
		}
	}
	child.mu.Unlock()
	sort.Strings(offered)
	if strings.Join(offered, ",") != "glob,grep,print_tree,read" {
		t.Fatalf("the verifier was offered %v, want the read-only tools", offered)
	}
	if !strings.Contains(task, "write the README") || !strings.Contains(task, "README exists") {
		t.Fatalf("verifier task = %q", task)
	}

	var verifier *bgtask.Snapshot
	for _, snap := range bgtask.Default().List(rig.parent.ID) {
		if snap.Agent != nil && snap.Agent.Name == session.SubagentKindGoalVerifier {
			s := snap
			verifier = &s
		}
	}
	if verifier == nil || !verifier.SystemTask() || verifier.Status != bgtask.StatusSucceeded {
		t.Fatalf("the verification is not a finished system task of the session: %+v", verifier)
	}
}

func TestGoalVerifierWithoutAVerdictIsAnError(t *testing.T) {
	rig := newSubagentRig(t, nil)
	rig.setChildProvider(func(*session.State) llm.Provider { return scripted(answerStep("Looks fine to me.")) })
	if _, err := NewGoalVerifier(rig.mgr, nil)(context.Background(), rig.parent, session.GoalCheckRequest{Objective: "x"}); err == nil {
		t.Fatal("a verifier answer without JSON was taken as a verdict")
	}
}

func TestCompactionKeepsTheActiveGoal(t *testing.T) {
	st := &session.State{ID: "sess_c", Mode: session.ModeAgent}
	if got := withGoalSummaryInstructions(st, "be brief"); got != "be brief" {
		t.Fatalf("no goal changed the instructions: %q", got)
	}
	goal, _ := session.NewGoal("migrate the store to Postgres")
	st.SetGoal(goal)
	got := withGoalSummaryInstructions(st, "be brief")
	if !strings.HasPrefix(got, "be brief") || !strings.Contains(got, "migrate the store to Postgres") || !strings.Contains(got, "still open") {
		t.Fatalf("instructions = %q", got)
	}
	goal.Status = session.GoalComplete
	st.SetGoal(goal)
	if got := withGoalSummaryInstructions(st, ""); got != "" {
		t.Fatalf("a finished goal still steers the summary: %q", got)
	}
}
