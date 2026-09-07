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

// MatchScoped returns the auto rules a set of tool-call paths brings into
// play: directory-scoped rules whose ScopeDir contains one of paths, and glob
// rules one of whose patterns matches one of paths (Claude Code loads a paths
// rule when a matching file is read; Cursor auto-attaches on a matching file
// in context). Rules with neither a scope nor globs are ignored on purpose:
// they are on from the first turn, and callers that feed tool-call paths
// would otherwise re-match that whole always-on set on every single call.
func MatchScoped(catalog []*Rule, paths []string) []*Rule {
	if len(paths) == 0 {
		return nil
	}
	var out []*Rule
	for _, r := range catalog {
		if r == nil || r.ApplyMode != ApplyAuto || !r.AlwaysApply {
			continue
		}
		switch {
		case r.ScopeDir != "":
			if PathsUnderDir(r.ScopeDir, paths) {
				out = append(out, r)
			}
		case len(r.Globs) > 0:
			if matchesRuleGlobs(r, paths) {
				out = append(out, r)
			}
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
