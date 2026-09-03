package subagents

import (
	"sort"
	"strings"
)

// permissionRank orders modes from strictest to loosest. Narrowing picks the
// smaller rank, so a definition can tighten what the parent runs with but
// never loosen it.
var permissionRank = map[string]int{
	PermissionAsk:         0,
	PermissionAcceptEdits: 1,
	PermissionBypass:      2,
}

// NormalizePermissionMode maps a requested mode, including the Claude Code
// spellings (default, acceptEdits, bypassPermissions), onto Coddy's values.
func NormalizePermissionMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case PermissionAsk, "default", "prompt":
		return PermissionAsk, true
	case PermissionAcceptEdits, "acceptedits", "accept-edits":
		return PermissionAcceptEdits, true
	case PermissionBypass, "bypasspermissions", "bypass-permissions", "dontask":
		return PermissionBypass, true
	default:
		return "", false
	}
}

// NarrowPermissionMode returns the stricter of the parent's effective mode
// and the definition's request. An empty request inherits; an unknown or
// empty parent counts as ask, so nothing can widen past what the parent had.
func NarrowPermissionMode(parent, requested string) string {
	p, ok := NormalizePermissionMode(parent)
	if !ok {
		p = PermissionAsk
	}
	if strings.TrimSpace(requested) == "" {
		return p
	}
	r, ok := NormalizePermissionMode(requested)
	if !ok {
		return p
	}
	if permissionRank[r] < permissionRank[p] {
		return r
	}
	return p
}

// EffectiveTools computes the tool set a child may call:
//
//	parent ∩ modeSet ∩ definition allowlist − definition denylist − exclusions
//
// parent is every name the parent could call (MCP names included). modeSet is
// the child mode's allowlist, or nil when that mode is unrestricted. The
// result is sorted so prompts and tests are stable.
func EffectiveTools(parent []string, modeSet []string, def *Definition, exclusions []string) []string {
	var modeAllowed map[string]bool
	if modeSet != nil {
		modeAllowed = make(map[string]bool, len(modeSet))
		for _, n := range modeSet {
			modeAllowed[strings.TrimSpace(n)] = true
		}
	}
	excluded := make(map[string]bool, len(exclusions))
	for _, n := range exclusions {
		excluded[strings.TrimSpace(n)] = true
	}

	seen := make(map[string]bool, len(parent))
	out := make([]string, 0, len(parent))
	for _, raw := range parent {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] || excluded[name] {
			continue
		}
		if modeAllowed != nil && !modeAllowed[name] {
			continue
		}
		if !def.Allows(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Timeout resolution mirrors the background pool: an explicit limit wins, an
// estimate buys a multiple of itself with a floor, otherwise the default.
const (
	timeoutEstimateMultiplier  = 3
	minEstimatedTimeoutSeconds = 60
)

// ResolveTimeoutSeconds picks the hard limit for one run in this order: the
// call's timeout, the definition's timeout, the estimate rule, the configured
// default. The pool still caps the result by its own maximum.
func ResolveTimeoutSeconds(call, definition, expected, fallback int) int {
	switch {
	case call > 0:
		return call
	case definition > 0:
		return definition
	case expected > 0:
		return max(expected*timeoutEstimateMultiplier, minEstimatedTimeoutSeconds)
	default:
		return fallback
	}
}
