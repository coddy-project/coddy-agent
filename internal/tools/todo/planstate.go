package todo

import "github.com/EvilFreelancer/coddy-agent/internal/acp"

// PlanHasIncompleteItems reports whether the list has any item not marked completed.
// An empty slice means there is no active list.
func PlanHasIncompleteItems(entries []acp.PlanEntry) bool {
	for _, e := range entries {
		if e.Status != "completed" {
			return true
		}
	}
	return false
}
