package rules

import "strings"

// ParseSystems maps config strings to Source values.
func ParseSystems(ss []string) []Source {
	if len(ss) == 0 {
		return nil
	}
	var out []Source
	for _, raw := range ss {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "user":
			out = append(out, SourceUser)
		case "coddy":
			out = append(out, SourceCoddy)
		case "agents-dir":
			out = append(out, SourceAgentsDir)
		case "cursor":
			out = append(out, SourceCursor)
		case "claude":
			out = append(out, SourceClaude)
		case "codex":
			out = append(out, SourceCodex)
		case "agents":
			out = append(out, SourceAgents)
		}
	}
	return out
}
