package todo

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestPlanHasIncompleteItems(t *testing.T) {
	if PlanHasIncompleteItems(nil) || PlanHasIncompleteItems([]acp.PlanEntry{}) {
		t.Fatal("expected no open items for empty list")
	}
	if !PlanHasIncompleteItems([]acp.PlanEntry{{Status: "pending"}}) {
		t.Fatal("pending should count as incomplete")
	}
	if PlanHasIncompleteItems([]acp.PlanEntry{{Status: "completed"}}) {
		t.Fatal("all completed should allow new list creation")
	}
}
