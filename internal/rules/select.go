package rules

// A mention-only rule is attached by the prompt that names it ("@deploy",
// "@rule:deploy"): internal/session/mentions.go resolves it into that user
// message, never into the system prompt.

// MatchAuto returns rules newly matched this turn (alwaysApply true only).
// Directory-scoped rules require a context path inside their subtree; rules with
// globs require a context file match; rules with neither match immediately.
func MatchAuto(catalog []*Rule, contextFiles []string) []*Rule {
	var out []*Rule
	for _, r := range catalog {
		if r == nil || r.ApplyMode != ApplyAuto || !r.AlwaysApply {
			continue
		}
		switch {
		case r.ScopeDir != "":
			if PathsUnderDir(r.ScopeDir, contextFiles) {
				out = append(out, r)
			}
		case len(r.Globs) == 0:
			out = append(out, r)
		default:
			if matchesRuleGlobs(r, contextFiles) {
				out = append(out, r)
			}
		}
	}
	return out
}

// UnionStable merges newly matched auto rules into sticky set by ID.
func UnionStable(sticky, newly []*Rule) []*Rule {
	if len(newly) == 0 {
		return sticky
	}
	seen := make(map[string]struct{}, len(sticky)+len(newly))
	out := append([]*Rule(nil), sticky...)
	for _, r := range sticky {
		if r != nil {
			seen[r.ID] = struct{}{}
		}
	}
	for _, r := range newly {
		if r == nil {
			continue
		}
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		out = append(out, r)
	}
	return out
}
