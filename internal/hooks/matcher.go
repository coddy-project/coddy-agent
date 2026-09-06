package hooks

import (
	"regexp"
	"strings"
	"sync"
)

// simpleMatcher recognises the values Claude Code treats as an exact name or
// a list of exact names rather than a regular expression.
var simpleMatcher = regexp.MustCompile(`^[A-Za-z0-9_\-,| ]+$`)

var (
	regexCacheMu sync.Mutex
	regexCache   = map[string]*regexp.Regexp{}
	regexBroken  = map[string]bool{}
)

// Match applies Claude Code's matcher rules to a subject: empty or "*"
// matches everything; a value made only of letters, digits, "_", "-", spaces,
// "," and "|" is an exact name or a list of exact names separated by "|" or
// ","; anything else is an unanchored regular expression. An invalid
// expression never matches.
func Match(matcher, subject string) bool {
	m := strings.TrimSpace(matcher)
	if m == "" || m == "*" {
		return true
	}
	if simpleMatcher.MatchString(m) {
		for _, alt := range splitAlternatives(m) {
			if alt == subject {
				return true
			}
		}
		return false
	}
	re := compileMatcher(m)
	return re != nil && re.MatchString(subject)
}

func splitAlternatives(m string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(m, func(r rune) bool { return r == '|' || r == ',' }) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func compileMatcher(m string) *regexp.Regexp {
	regexCacheMu.Lock()
	defer regexCacheMu.Unlock()
	if re, ok := regexCache[m]; ok {
		return re
	}
	if regexBroken[m] {
		return nil
	}
	re, err := regexp.Compile(m)
	if err != nil {
		regexBroken[m] = true
		return nil
	}
	regexCache[m] = re
	return re
}

// toolAliases maps a Coddy tool name to the names other agents use for the
// same capability, so a matcher written for Claude Code or Codex applies.
var toolAliases = map[string][]string{
	"run_command": {"Bash", "PowerShell", "Shell"},
	"edit":        {"Edit"},
	"write":       {"Write"},
	"apply_patch": {"Edit", "Write"},
	"read":        {"Read"},
	"glob":        {"Glob"},
	"grep":        {"Grep"},
	"webfetch":    {"WebFetch"},
	"websearch":   {"WebSearch"},
	"spawn_agent": {"Task", "Agent"},
	"question":    {"AskUserQuestion"},
	"plan_exit":   {"ExitPlanMode"},
}

// MatchTool matches a tool name the way tool events do: the Coddy name, its
// aliases from other agents, and for MCP tools (server__tool) the
// mcp__server__tool spelling as well.
func MatchTool(matcher, tool string) bool {
	if Match(matcher, tool) {
		return true
	}
	for _, alias := range toolAliases[tool] {
		if Match(matcher, alias) {
			return true
		}
	}
	if strings.Contains(tool, "__") && !strings.HasPrefix(tool, "mcp__") && Match(matcher, "mcp__"+tool) {
		return true
	}
	return false
}
