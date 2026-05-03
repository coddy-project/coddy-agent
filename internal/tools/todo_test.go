package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// mockSender captures plan updates sent during tool execution.
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

func makeEnvWithPlan(sender *mockSender) *Env {
	plan := make([]acp.PlanEntry, 0)
	return &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan: func(entries []acp.PlanEntry) {
			plan = entries
		},
	}
}

func TestCreateTodoListParsesMarkdown(t *testing.T) {
	sender := &mockSender{}
	env := makeEnvWithPlan(sender)

	args := `{"items":"- [ ] Setup project\n- [ ] Write tests\n- [x] Plan feature"}`
	r := NewRegistry()
	result, err := r.Execute(context.Background(), "create_todo_list", args, env)
	if err != nil {
		t.Fatalf("create_todo_list returned error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Verify plan update was sent.
	if len(sender.planUpdates) == 0 {
		t.Fatal("expected at least one PlanUpdate to be sent")
	}
	entries := sender.planUpdates[len(sender.planUpdates)-1].Entries
	if len(entries) != 3 {
		t.Fatalf("expected 3 plan entries, got %d", len(entries))
	}
	if entries[0].Content != "Setup project" {
		t.Errorf("entry[0] content = %q, want %q", entries[0].Content, "Setup project")
	}
	if entries[0].Status != "pending" {
		t.Errorf("entry[0] status = %q, want pending", entries[0].Status)
	}
	if entries[2].Status != "completed" {
		t.Errorf("entry[2] (checked item) status = %q, want completed", entries[2].Status)
	}
}

func TestCreateTodoListStoresPlan(t *testing.T) {
	sender := &mockSender{}
	plan := make([]acp.PlanEntry, 0)
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}

	args := `{"items":"- [ ] first\n- [ ] second"}`
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "create_todo_list", args, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored := env.GetPlan()
	if len(stored) != 2 {
		t.Fatalf("expected 2 stored entries, got %d", len(stored))
	}
}

func TestCreateTodoListRejectsWhenIncomplete(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "doing", Status: "in_progress"},
	}
	env := todoTestEnv(sender, &plan)
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "create_todo_list", `{"items":"- [ ] new"}`, env)
	if err == nil || !strings.Contains(err.Error(), "incomplete items remain") {
		t.Fatalf("expected incomplete-list error, got %v", err)
	}
}

func TestCreateTodoListEmptyItemsError(t *testing.T) {
	sender := &mockSender{}
	env := makeEnvWithPlan(sender)

	args := `{"items":""}`
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "create_todo_list", args, env)
	if err == nil {
		t.Fatal("expected error for empty items")
	}
}

func TestUpdateTodoItemMarksDone(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "first task", Status: "pending"},
		{Content: "second task", Status: "pending"},
	}
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}

	args := `{"index":0,"status":"completed"}`
	r := NewRegistry()
	result, err := r.Execute(context.Background(), "update_todo_item", args, env)
	if err != nil {
		t.Fatalf("update_todo_item returned error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Check the plan was updated.
	if len(sender.planUpdates) == 0 {
		t.Fatal("expected PlanUpdate to be sent")
	}
	entries := sender.planUpdates[0].Entries
	if entries[0].Status != "completed" {
		t.Errorf("entry[0] status = %q, want completed", entries[0].Status)
	}
	if entries[1].Status != "pending" {
		t.Errorf("entry[1] status should remain pending, got %q", entries[1].Status)
	}
}

func TestUpdateTodoItemOutOfRange(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "only one", Status: "pending"},
	}
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}

	args := `{"index":5,"status":"completed"}`
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "update_todo_item", args, env)
	if err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

func TestUpdateTodoItemInvalidStatus(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "task", Status: "pending"},
	}
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}

	args := `{"index":0,"status":"unknown_status"}`
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "update_todo_item", args, env)
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestTodoToolsRegistered(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{
		"create_todo_list",
		"update_todo_item",
		"get_todo_list",
		"delete_todo_item",
		"done_todo_item",
		"undone_todo_item",
		"clean_todo_list",
	} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("%q missing from registry", name)
		}
	}
}

func TestCreateTodoListAllowedInPlanMode(t *testing.T) {
	r := NewRegistry()
	tool, ok := r.Get("create_todo_list")
	if !ok {
		t.Fatal("create_todo_list not registered")
	}
	if !tool.AllowedInPlanMode {
		t.Error("create_todo_list should be allowed in plan mode")
	}
}

func TestUpdateTodoItemAllowedInPlanMode(t *testing.T) {
	r := NewRegistry()
	tool, ok := r.Get("update_todo_item")
	if !ok {
		t.Fatal("update_todo_item not registered")
	}
	if !tool.AllowedInPlanMode {
		t.Error("update_todo_item should be allowed in plan mode")
	}
}

func TestGetTodoListReturnsJSONAndMarkdown(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "one", Status: "pending"},
		{Content: "two", Status: "completed"},
	}
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}
	r := NewRegistry()
	out, err := r.Execute(context.Background(), "get_todo_list", `{}`, env)
	if err != nil {
		t.Fatalf("get_todo_list: %v", err)
	}
	if out == "" {
		t.Fatal("expected output")
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, `"completed"`) {
		t.Errorf("expected JSON content: %q", out)
	}
}

func TestDeleteTodoItem(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "pending"},
	}
	env := &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return plan },
		SetPlan:   func(entries []acp.PlanEntry) { plan = entries },
	}
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "delete_todo_item", `{"index":0}`, env)
	if err != nil {
		t.Fatalf("delete_todo_item: %v", err)
	}
	if len(plan) != 1 || plan[0].Content != "b" {
		t.Fatalf("after delete expected [b], got %+v", plan)
	}
}

func TestDoneAndUndoneTodoItem(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "step", Status: "pending"},
	}
	env := todoTestEnv(sender, &plan)
	r := NewRegistry()

	if _, err := r.Execute(context.Background(), "done_todo_item", `{"index":0}`, env); err != nil {
		t.Fatalf("done_todo_item: %v", err)
	}
	if plan[0].Status != "completed" {
		t.Fatalf("want completed, got %q", plan[0].Status)
	}

	if _, err := r.Execute(context.Background(), "undone_todo_item", `{"index":0}`, env); err != nil {
		t.Fatalf("undone_todo_item: %v", err)
	}
	if plan[0].Status != "pending" {
		t.Fatalf("want pending, got %q", plan[0].Status)
	}
}

func TestCleanTodoList(t *testing.T) {
	sender := &mockSender{}
	plan := []acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "completed"},
	}
	env := todoTestEnv(sender, &plan)
	r := NewRegistry()
	if _, err := r.Execute(context.Background(), "clean_todo_list", `{}`, env); err != nil {
		t.Fatalf("clean_todo_list: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected empty plan, got %d items", len(plan))
	}
}

func todoTestEnv(sender *mockSender, plan *[]acp.PlanEntry) *Env {
	return &Env{
		CWD:       "/tmp",
		SessionID: "test-session",
		Sender:    sender,
		GetPlan:   func() []acp.PlanEntry { return *plan },
		SetPlan:   func(entries []acp.PlanEntry) { *plan = entries },
	}
}
