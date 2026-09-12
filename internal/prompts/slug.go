package prompts

import "strings"

// ModelSlug converts a model identifier (for example "neuraldeep/gemma-4-31b") into a
// filename-safe slug used to select a per-model prompt variant (agent.<slug>.md,
// model_notes_<slug>.md).
//
// The id is lowercased; any character outside [a-z0-9.-] (notably the "/" provider
// separator) becomes "-", runs of "-" collapse, and leading/trailing "-" are trimmed.
// Returns "" for an empty id.
func ModelSlug(modelID string) string {
	s := strings.ToLower(strings.TrimSpace(modelID))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		safe := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-'
		if safe {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
