// Package skills loads skill and cursor rule files, formats them for prompts,
// and implements CLI helpers (install, uninstall, list).
package skills

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded skill or cursor rule.
type Skill struct {
	// Name is the short identifier (from frontmatter or derived from filename/dir).
	Name string

	// FilePath is the absolute path to the source file.
	FilePath string

	// Description from frontmatter (required for valid skills).
	Description string

	// Version from frontmatter (optional; semantic version when present).
	Version string

	// Content is the body of the skill file (without frontmatter).
	Content string

	// Model and Reasoning, when set, are what the turn that uses the skill
	// runs on from then on: frontmatter model and reasoning (alias effort,
	// Claude Code's spelling). They last until that turn ends.
	Model     string
	Reasoning string
}

// Loader discovers and loads skills from the filesystem.
type Loader struct {
	// Dirs is the list of directories to search for skills.
	Dirs []string
}

// NewLoader creates a Loader with the given directories.
func NewLoader(dirs []string) *Loader {
	return &Loader{Dirs: dirs}
}

// LoadAll discovers and loads all skills from configured directories.
// cwd is the session working directory (${CWD}). agentHome is CODDY_HOME (${CODDY_HOME}).
// installDir is used to read the .disabled file; empty string skips disable filtering.
//
// Directory priority: later entries in Dirs override earlier ones — a skill with the
// same canonical name found in a later directory replaces the one from an earlier directory.
// This means ${CWD}/.coddy/skills (last by default) has the highest priority.
func (l *Loader) LoadAll(cwd, agentHome string, installDir ...string) ([]*Skill, error) {
	var disabled map[string]struct{}
	var withdrawn map[string]struct{}
	if len(installDir) > 0 && installDir[0] != "" {
		disabled = ReadDisabled(installDir[0])
		withdrawn = DeliveredAndDeleted(installDir[0])
	}

	// ordered tracks insertion order of first encounter; byName points to the slot
	// in ordered so a later directory can overwrite the skill for a given name.
	type slot struct {
		name  string
		skill *Skill
	}
	var ordered []slot
	byName := make(map[string]int) // canonical name → index in ordered
	seenPath := make(map[string]bool)

	addSkill := func(s *Skill) {
		if s == nil || seenPath[s.FilePath] {
			return
		}
		seenPath[s.FilePath] = true
		name := CanonicalCommandName(s)
		if idx, ok := byName[name]; ok {
			ordered[idx].skill = s // later dir wins
		} else {
			byName[name] = len(ordered)
			ordered = append(ordered, slot{name, s})
		}
	}

	// The standard delivery is prepended at the lowest priority, so the copy in
	// the managed dir overrides it. One the operator deleted from there is left
	// out: the in-binary copy is a fallback for a home the delivery could not
	// write, not a way for a deleted skill to come back as an undeletable one.
	for _, s := range Bundled() {
		if _, gone := withdrawn[CanonicalCommandName(s)]; gone {
			continue
		}
		addSkill(s)
	}

	// Load from directories in config order; later dirs override earlier ones.
	for _, dir := range l.Dirs {
		expanded := expandPath(dir, cwd, agentHome)
		found, err := loadFromDir(expanded)
		if err != nil {
			continue
		}
		for _, s := range found {
			addSkill(s)
		}
	}

	result := make([]*Skill, 0, len(ordered))
	for _, sl := range ordered {
		if !IsDisabled(disabled, sl.name) {
			result = append(result, sl.skill)
		}
	}
	return result, nil
}

// FilterForContext returns skills applicable to the given context.
// All loaded skills are always active (no glob filtering).
func FilterForContext(skills []*Skill, _ []string) []*Skill {
	return skills
}

// BuildSystemPromptSection builds the skills section for the system prompt.
func BuildSystemPromptSection(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Active Skills\n\n")
	for _, s := range skills {
		head := CanonicalCommandName(s)
		if s.Description != "" {
			b.WriteString("### ")
			b.WriteString(head)
			b.WriteString(" (")
			b.WriteString(s.Description)
			b.WriteString(")\n\n")
		} else {
			b.WriteString("### ")
			b.WriteString(head)
			b.WriteString("\n\n")
		}
		b.WriteString(s.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}

// ---- internal helpers ----

// loadFromDir loads all skill files from a directory.
func loadFromDir(dir string) ([]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var skills []*Skill
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			// Skip dotfiles/dirs: .git, .remote.json, and the transient
			// .tmp-/.bak- staging dirs used while syncing remote skills.
			continue
		}
		full := filepath.Join(dir, name)
		fi, err := os.Stat(full)
		if err != nil {
			continue
		}

		if fi.IsDir() {
			// One level deep: dirname/SKILL.md. Use Stat so symlinked skill dirs resolve.
			subSkill := filepath.Join(full, "SKILL.md")
			if _, err := os.Stat(subSkill); err == nil {
				s, err := loadFile(subSkill)
				if err == nil {
					skills = append(skills, s)
				}
			}
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".mdc" && name != "SKILL.md" {
			continue
		}

		s, err := loadFile(full)
		if err != nil {
			continue
		}
		skills = append(skills, s)
	}

	return skills, nil
}

// loadFile loads a single skill/rule file.
func loadFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	skill := &Skill{
		Name:     name,
		FilePath: path,
	}

	// Try to parse frontmatter.
	body, fm := parseFrontmatter(data)
	skill.Content = strings.TrimSpace(body)

	if fm != nil {
		if fm.Name != "" {
			skill.Name = fm.Name
		}
		skill.Description = fm.Description
		skill.Version = fm.version()
		skill.Model = strings.TrimSpace(fm.Model)
		skill.Reasoning = strings.ToLower(strings.TrimSpace(fm.Reasoning))
		if skill.Reasoning == "" {
			skill.Reasoning = strings.ToLower(strings.TrimSpace(fm.Effort))
		}
	}

	return skill, nil
}

// frontmatter is the YAML frontmatter of a skill file.
// name overrides the filesystem-derived name when set; version is optional.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Metadata is the Agent Skills map of free keys; its version is where a
	// skill declares its version now. Kept as a node: a metadata of another
	// shape must not fail the whole frontmatter and lose the name with it.
	Metadata yaml.Node `yaml:"metadata"`
	// Version is the top-level key skills declared their version with before
	// the metadata map; still read for the files that carry it.
	Version   string `yaml:"version"`
	Model     string `yaml:"model"`
	Reasoning string `yaml:"reasoning"`
	Effort    string `yaml:"effort"`
}

// version is the skill's declared version: metadata.version, else the
// top-level version key older files carry, else "".
func (fm *frontmatter) version() string {
	if fm.Metadata.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(fm.Metadata.Content); i += 2 {
			key, val := fm.Metadata.Content[i], fm.Metadata.Content[i+1]
			if key.Value != "version" || val.Kind != yaml.ScalarNode {
				continue
			}
			if v := strings.TrimSpace(val.Value); v != "" {
				return v
			}
		}
	}
	return strings.TrimSpace(fm.Version)
}

// parseFrontmatter splits a file into frontmatter and body.
// Frontmatter is delimited by --- lines.
func parseFrontmatter(data []byte) (string, *frontmatter) {
	scanner := bufio.NewScanner(bytes.NewReader(data))

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) < 3 || lines[0] != "---" {
		return string(data), nil
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}

	if endIdx < 0 {
		return string(data), nil
	}

	fmContent := strings.Join(lines[1:endIdx], "\n")
	body := strings.Join(lines[endIdx+1:], "\n")

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return body, nil
	}

	return body, &fm
}

// expandPath resolves ${CODDY_HOME}, ${CWD}, and ~ in a path.
func expandPath(path, cwd, agentHome string) string {
	if agentHome != "" {
		path = strings.ReplaceAll(path, "${CODDY_HOME}", agentHome)
	}
	path = strings.ReplaceAll(path, "${CWD}", cwd)
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}

// ExpandConfiguredPath resolves ${CODDY_HOME}, ${CWD}, and ~ the same way as skill loading.
func ExpandConfiguredPath(path, cwd, agentHome string) string {
	return expandPath(path, cwd, agentHome)
}
