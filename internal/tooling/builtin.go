package tooling

// Builtin describes a construction that yields exactly one [*Tool]. Subpackages
// under internal/tools return *Tool directly or implement this pattern for uniformity.
type Builtin interface {
	AsTool() *Tool
}
