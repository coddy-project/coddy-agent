package fs

import "github.com/EvilFreelancer/coddy-agent/internal/platform"

// leftAsItWas ends the error of a write that failed: the replacement is atomic,
// so the model can rely on the file still holding what it held before the call.
const leftAsItWas = " (the file was left as it was)"

// withNote appends the remark of a write report, when it has one, to the result
// line a model reads.
func withNote(line string, report platform.WriteReport) string {
	if note := report.Note(); note != "" {
		return line + "; " + note
	}
	return line
}
