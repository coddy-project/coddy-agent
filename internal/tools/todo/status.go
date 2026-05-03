package todo

// ValidTodoStatuses lists allowed values for PlanEntry.Status in update tool.
var ValidTodoStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"completed":   true,
	"failed":      true,
	"cancelled":   true,
}
