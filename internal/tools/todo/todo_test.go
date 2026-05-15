package todo_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

type mockSender struct {
	planUpdates []acp.PlanUpdate
}

func (m *mockSender) SendSessionUpdate(_ string, update interface{}) error {
	if pu, ok := update.(acp.PlanUpdate); ok {
		m.planUpdates = append(m.planUpdates, pu)
	}
	return nil
}

func (m *mockSender) RequestPermission(_ context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (m *mockSender) RequestQuestion(_ context.Context, _ acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func newRegistry() *apptools.Registry {
	return apptools.NewRegistry()
}

func TestPlanReplaceParsesMarkdown(t *testing.T) {
	sender := &mockSender{}
	plan := make([]acp.PlanEntry, 0)
	env := &apptools.Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}

	args := `{"markdown":"- [ ] Setup project\n- [ ] Write tests\n- [x] Plan feature"}`
	r := newRegistry()
	result, err := r.Execute(context.Background(), todo.ToolNamePlanReplace, args, env)
	if err != nil {
		t.Fatalf("plan replace: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if len(sender.planUpdates) == 0 {
		t.Fatal("expected PlanUpdate")
	}
	entries := sender.planUpdates[len(sender.planUpdates)-1].Entries
	if len(entries) != 3 {
		t.Fatalf("expected 3 plan entries, got %d", len(entries))
	}
	if entries[2].Status != "completed" {
		t.Errorf("entry[2] status = %q, want completed", entries[2].Status)
	}
}

func TestPlanReplaceRejectsWhenIncomplete(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{{Content: "doing", Status: "in_progress"}}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	_, err := r.Execute(context.Background(), todo.ToolNamePlanReplace, `{"markdown":"- [ ] new"}`, env)
	if err == nil || !strings.Contains(err.Error(), "complete or archive") {
		t.Fatalf("expected guard error, got %v", err)
	}
}

func TestPlanReplaceEmptyMarkdown(t *testing.T) {
	sender := &mockSender{}
	env := envWithPlan(sender, &[]acp.PlanEntry{})
	r := newRegistry()
	_, err := r.Execute(context.Background(), todo.ToolNamePlanReplace, `{"markdown":""}`, env)
	if err == nil {
		t.Fatal("expected error for empty markdown")
	}
}

func TestItemUpdateMarksDone(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "first task", Status: "pending"},
		{Content: "second task", Status: "pending"},
	}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	st := "completed"
	_, err := r.Execute(context.Background(), todo.ToolNameItemUpdate,
		`{"index":0,"status":"`+st+`"}`, env)
	if err != nil {
		t.Fatalf("item update: %v", err)
	}
	if len(sender.planUpdates) == 0 {
		t.Fatal("expected PlanUpdate")
	}
	entries := sender.planUpdates[0].Entries
	if entries[0].Status != "completed" || entries[1].Status != "pending" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestItemUpdateOutOfRange(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{{Content: "only one", Status: "pending"}}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	st := "completed"
	_, err := r.Execute(context.Background(), todo.ToolNameItemUpdate,
		`{"index":5,"status":"`+st+`"}`, env)
	if err == nil {
		t.Fatal("expected out of range")
	}
}

func TestItemUpdateInvalidStatus(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{{Content: "task", Status: "pending"}}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	u := "unknown_status"
	_, err := r.Execute(context.Background(), todo.ToolNameItemUpdate,
		`{"index":0,"status":"`+u+`"}`, env)
	if err == nil {
		t.Fatal("expected invalid status")
	}
}

func TestItemUpdateRequiresMutation(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{{Content: "task", Status: "pending"}}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	_, err := r.Execute(context.Background(), todo.ToolNameItemUpdate, `{"index":0}`, env)
	if err == nil {
		t.Fatal("expected error when content and status omitted")
	}
}

func TestCoddyTodoToolsRegistered(t *testing.T) {
	r := newRegistry()
	for _, name := range todo.AllCoddyTodoToolNames {
		if _, ok := r.Get(name); !ok {
			t.Errorf("%q missing from registry", name)
		}
	}
}

func TestPlanReadReturnsMarkdownChecklist(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "one", Status: "pending"},
		{Content: "two", Status: "completed"},
	}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	out, err := r.Execute(context.Background(), todo.ToolNamePlanRead, `{}`, env)
	if err != nil {
		t.Fatalf("plan read: %v", err)
	}
	if !strings.Contains(out, "- [ ] one") || !strings.Contains(out, "- [x] two") {
		t.Fatalf("unexpected markdown: %q", out)
	}
	if strings.Contains(out, `"content"`) {
		t.Fatalf("did not expect JSON in plan read output: %q", out)
	}
}

func TestItemRemove(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "pending"},
	}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	if _, err := r.Execute(context.Background(), todo.ToolNameItemRemove, `{"index":0}`, env); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(plan) != 1 || plan[0].Content != "b" {
		t.Fatalf("after delete: %+v", plan)
	}
}

func TestPlanArchiveFinalizeAndClear(t *testing.T) {
	td := t.TempDir()
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "open", Status: "in_progress"},
		{Content: "done", Status: "completed"},
	}
	env := &apptools.Env{
		CWD:        "/tmp",
		SessionID:  "s1",
		SessionDir: td,
		Sender:     sender,
		GetPlan:    func() []acp.PlanEntry { return plan },
		SetPlan:    func(entries []acp.PlanEntry) { plan = entries },
		WriteArchivedPlanMarkdown: func(md string) (string, error) {
			return session.WritePlanArchivedMarkdown(td, md)
		},
	}
	r := newRegistry()
	out, err := r.Execute(context.Background(), todo.ToolNamePlanArchive, `{}`, env)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected cleared plan, got %d items", len(plan))
	}
	if !strings.Contains(out, "archived") {
		t.Fatalf("unexpected message: %s", out)
	}
	matches, err := filepath.Glob(filepath.Join(td, "todos", "archive", "plan_*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one archive file, got %v", matches)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "[x]") || !strings.Contains(body, "open") {
		t.Fatalf("archive body: %s", body)
	}
}

func TestPlanArchiveNoopWhenEmpty(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	out, err := r.Execute(context.Background(), todo.ToolNamePlanArchive, `{}`, env)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, "no active items") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestItemAddAppendAndPrepend(t *testing.T) {
	sender := &mockSender{}

	t.Run("append", func(t *testing.T) {
		plan := []acp.PlanEntry{{Content: "a", Status: "pending"}}
		env := envWithPlan(sender, &plan)
		r := newRegistry()
		if _, err := r.Execute(context.Background(), todo.ToolNameItemAdd,
			`{"content":"tail","status":"pending"}`, env); err != nil {
			t.Fatal(err)
		}
		if len(plan) != 2 || plan[1].Content != "tail" {
			t.Fatalf("append: %+v", plan)
		}
	})

	t.Run("prepend", func(t *testing.T) {
		plan := []acp.PlanEntry{{Content: "a", Status: "pending"}}
		env := envWithPlan(sender, &plan)
		r := newRegistry()
		if _, err := r.Execute(context.Background(), todo.ToolNameItemAdd,
			`{"content":"head","after_index":-1}`, env); err != nil {
			t.Fatal(err)
		}
		if len(plan) != 2 || plan[0].Content != "head" {
			t.Fatalf("prepend: %+v", plan)
		}
	})

	t.Run("after_index", func(t *testing.T) {
		plan := []acp.PlanEntry{
			{Content: "a", Status: "pending"},
			{Content: "b", Status: "pending"},
		}
		env := envWithPlan(sender, &plan)
		r := newRegistry()
		if _, err := r.Execute(context.Background(), todo.ToolNameItemAdd,
			`{"content":"mid","after_index":0}`, env); err != nil {
			t.Fatal(err)
		}
		if len(plan) != 3 || plan[1].Content != "mid" {
			t.Fatalf("after 0: %+v", plan)
		}
	})
}

func TestItemMove(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "pending"},
		{Content: "c", Status: "pending"},
	}
	env := envWithPlan(sender, &plan)
	r := newRegistry()
	if _, err := r.Execute(context.Background(), todo.ToolNameItemMove,
		`{"from_index":2,"to_index":0}`, env); err != nil {
		t.Fatal(err)
	}
	if len(plan) != 3 || plan[0].Content != "c" || plan[1].Content != "a" || plan[2].Content != "b" {
		t.Fatalf("move result: %+v", plan)
	}
}

func envWithPlan(sender *mockSender, plan *[]acp.PlanEntry) *apptools.Env {
	return &apptools.Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return *plan },
		SetPlan:   func(entries []acp.PlanEntry) { *plan = entries },
	}
}
