package rules

// A mention-only rule is attached by the prompt that names it ("@deploy",
// "@rule:deploy"): internal/session/mentions.go resolves it into that user
// message, never into the system prompt.

// MatchAuto returns the auto rules a set of context paths matches.
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

// AlwaysOnRules returns the rules of catalog a system prompt carries: the ones
// that apply from the first turn whatever the session touches (Rule.AlwaysOn).
// They depend on the catalog alone, so the block they render stays the same for
// as long as the catalog does.
func AlwaysOnRules(catalog []*Rule) []*Rule {
	var out []*Rule
	for _, r := range catalog {
		if r.AlwaysOn() {
			out = append(out, r)
		}
	}
	return out
}
