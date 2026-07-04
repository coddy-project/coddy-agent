package rules

import (
	"os"
	"path/filepath"
	"strings"
)

// agentsSkipDirs are dependency trees never scanned for AGENTS.md.
var agentsSkipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
}

// AgentsProvider discovers nested **/AGENTS.md files (https://agents.md/)
// under the project root and injects them as always-loaded rules.
// The root AGENTS.md is excluded: it already enters the prompt
// unconditionally as a project docs preamble (see LoadProjectDocs).
type AgentsProvider struct{}

func (p *AgentsProvider) ID() Source { return SourceAgents }

// RulesRoot is empty: AGENTS.md files live in the project tree itself.
func (p *AgentsProvider) RulesRoot() string { return "" }

func (p *AgentsProvider) Load(root string) ([]*Rule, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}
	root = filepath.Clean(root)
	var out []*Rule
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || agentsSkipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(d.Name(), "AGENTS.md") {
			return nil
		}
		if filepath.Dir(path) == root {
			// Root AGENTS.md is the project docs preamble.
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		out = append(out, &Rule{
			ID:          string(SourceAgents) + ":" + path,
			Name:        filepath.ToSlash(rel),
			FilePath:    path,
			Source:      SourceAgents,
			AlwaysApply: true,
			ApplyMode:   ApplyAuto,
			Content:     content,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
