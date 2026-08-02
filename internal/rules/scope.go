package rules

import (
	"path/filepath"
	"runtime"
	"strings"
)

// PathUnderDir reports whether p is dir itself or lives inside dir.
// Both sides are normalized for separators and, on Windows, for case.
// Neither side is made absolute: a relative path would resolve against the
// process working directory rather than the session one, so callers resolve
// paths themselves before calling.
func PathUnderDir(dir, p string) bool {
	d := normalizeScopePath(dir)
	q := normalizeScopePath(p)
	if d == "" || q == "" {
		return false
	}
	if d == q {
		return true
	}
	// The trailing separator is what keeps "internal/agent" from matching
	// "internal/agentx/f.go".
	return strings.HasPrefix(q, d+string(filepath.Separator))
}

// PathsUnderDir reports whether any of paths is dir itself or lives inside dir.
func PathsUnderDir(dir string, paths []string) bool {
	for _, p := range paths {
		if PathUnderDir(dir, p) {
			return true
		}
	}
	return false
}

// MatchScoped returns directory-scoped auto rules whose ScopeDir contains any
// of paths. Glob-based and unscoped rules are ignored on purpose: callers that
// feed tool-call paths would otherwise re-match the whole always-on set on
// every single tool call.
func MatchScoped(catalog []*Rule, paths []string) []*Rule {
	if len(paths) == 0 {
		return nil
	}
	var out []*Rule
	for _, r := range catalog {
		if r == nil || r.ScopeDir == "" || r.ApplyMode != ApplyAuto || !r.AlwaysApply {
			continue
		}
		if PathsUnderDir(r.ScopeDir, paths) {
			out = append(out, r)
		}
	}
	return out
}

// normalizeScopePath cleans a path for prefix comparison. filepath.Rel is
// deliberately avoided: it errors across Windows drive letters.
func normalizeScopePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.Clean(filepath.FromSlash(p))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}
