package rules

import (
	"os"
	"path/filepath"
	"strings"
)

// agentsSkipDirs are dependency trees whose AGENTS.md never governs a prompt.
var agentsSkipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
}

// AgentsOnDemand reports whether rules.systems admits nested AGENTS.md files;
// an empty list means every system.
func AgentsOnDemand(systems []Source) bool {
	if len(systems) == 0 {
		return true
	}
	for _, s := range systems {
		if s == SourceAgents {
			return true
		}
	}
	return false
}

// AgentsForPaths returns the nested AGENTS.md (https://agents.md/) rules that
// govern paths, read on the spot: for every path under root, each directory
// on the chain from root (exclusive) down to the path's own directory is
// probed for an AGENTS.md. Nothing is walked. A session never looks at a
// folder the agent has not entered - the way Codex reads the AGENTS.md chain
// of the folder it works in - so a workspace of any size, a home directory
// included, costs nothing until a filesystem tool or a file:// attachment
// targets a path in it. Every file on the chain enters together: touching
// a/b/c/f.go brings in a/AGENTS.md, a/b/AGENTS.md and a/b/c/AGENTS.md. A
// hidden directory, node_modules or vendor ends the chain. Rules already in
// active are not read again. Paths outside root yield nothing; a path that
// does not exist yet (a write) counts as a file, so its parent chain is read.
// The root AGENTS.md is excluded: it enters the prompt unconditionally as the
// project docs preamble (see LoadProjectDocs).
func AgentsForPaths(root string, paths []string, active []*Rule) []*Rule {
	root = strings.TrimSpace(root)
	if root == "" || len(paths) == 0 {
		return nil
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	known := make(map[string]bool, len(active))
	for _, r := range active {
		if r != nil {
			known[r.ID] = true
		}
	}
	var out []*Rule
	for _, p := range paths {
		for _, dir := range agentsChainDirs(root, p) {
			r := readAgentsRule(root, dir)
			if r == nil || known[r.ID] {
				continue
			}
			known[r.ID] = true
			out = append(out, r)
		}
	}
	return out
}

// agentsChainDirs lists the directories between root (exclusive) and p's own
// directory (inclusive), outermost first, stopping at a segment the
// discovery never enters.
func agentsChainDirs(root, p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if !PathUnderDir(root, p) {
		return nil
	}
	dir := p
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		dir = filepath.Dir(p)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return nil
	}
	var dirs []string
	cur := root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, ".") || agentsSkipDirs[seg] {
			break
		}
		cur = filepath.Join(cur, seg)
		dirs = append(dirs, cur)
	}
	return dirs
}

// readAgentsRule reads dir/AGENTS.md into a directory-scoped rule, or returns
// nil when the folder has none (or it is empty).
func readAgentsRule(root, dir string) *Rule {
	path := filepath.Join(dir, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}
	if len(content) > projectDocMaxBytes {
		content = content[:projectDocMaxBytes] + "\n\n...(truncated)"
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return &Rule{
		ID:          string(SourceAgents) + ":" + path,
		Name:        filepath.ToSlash(rel),
		FilePath:    path,
		Source:      SourceAgents,
		Format:      FormatAgentsMD,
		AlwaysApply: true,
		ApplyMode:   ApplyAuto,
		Content:     content,
		Root:        root,
		ScopeDir:    dir,
	}
}
