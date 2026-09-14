package docsgen

import (
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// ignoredPaths is what git leaves out of the tree: paths relative to the
// repository root, slash-separated, a directory that is ignored whole ending
// in a slash.
type ignoredPaths map[string]bool

// gitIgnored asks git which paths under dirs it ignores. Only untracked files
// count, so a tracked page an exclude rule happens to match stays part of the
// documentation; what this drops is local scratch (docs/superpowers/ and the
// like), which nobody publishes and which must not be reported as missing from
// the map. Without git, or outside a repository, the answer is empty and every
// walk sees exactly what it saw before.
func gitIgnored(root string, dirs ...string) ignoredPaths {
	out := ignoredPaths{}
	args := append([]string{"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z", "--"}, dirs...)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, p := range strings.Split(string(data), "\x00") {
		if p != "" {
			out[filepath.ToSlash(p)] = true
		}
	}
	return out
}

// has reports whether rel, a path relative to the repository root, is ignored
// in its own right or through a directory above it.
func (p ignoredPaths) has(rel string) bool {
	if len(p) == 0 {
		return false
	}
	for dir := rel; dir != "" && dir != "." && dir != "/"; dir = path.Dir(dir) {
		if p[dir] || p[dir+"/"] {
			return true
		}
	}
	return false
}
