package rules

import (
	"regexp"
	"strings"
)

var atMentionRE = regexp.MustCompile(`(?:^|[\s(\[{])@([a-zA-Z0-9][a-zA-Z0-9_-]*)`)

// MatchAuto returns rules newly matched this turn (alwaysApply true only).
// Rules with globs require a context file match; rules without globs match immediately.
func MatchAuto(catalog []*Rule, contextFiles []string) []*Rule {
	var out []*Rule
	for _, r := range catalog {
		if r == nil || r.ApplyMode != ApplyAuto || !r.AlwaysApply {
			continue
		}
		if len(r.Globs) == 0 {
			out = append(out, r)
			continue
		}
		if matchesRuleGlobs(r, contextFiles) {
			out = append(out, r)
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

// SelectMentioned returns alwaysApply false rules referenced via @name in userText.
func SelectMentioned(catalog []*Rule, userText string) []*Rule {
	names := ParseAtMentions(userText)
	if len(names) == 0 {
		return nil
	}
	byName := make(map[string]*Rule, len(catalog))
	for _, r := range catalog {
		if r == nil || r.ApplyMode != ApplyMention {
			continue
		}
		byName[strings.ToLower(r.CanonicalName())] = r
	}
	var out []*Rule
	seen := make(map[string]struct{})
	for _, n := range names {
		r, ok := byName[strings.ToLower(n)]
		if !ok || r == nil {
			continue
		}
		if _, dup := seen[r.ID]; dup {
			continue
		}
		seen[r.ID] = struct{}{}
		out = append(out, r)
	}
	return out
}

// ParseAtMentions extracts @ruleName tokens from user text (outside code fences skipped lightly).
func ParseAtMentions(text string) []string {
	lines := strings.Split(text, "\n")
	inFence := false
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range atMentionRE.FindAllStringSubmatch(line, -1) {
			if len(m) < 2 {
				continue
			}
			name := m[1]
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}
