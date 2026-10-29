package fs

import "github.com/EvilFreelancer/coddy-agent/internal/platform"

// withNote appends the remark of a write report, when it has one, to the result
// line a model reads.
func withNote(line string, report platform.WriteReport) string {
	if note := report.Note(); note != "" {
		return line + "; " + note
	}
	return line
}
