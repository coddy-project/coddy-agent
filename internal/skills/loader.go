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
	// Name is the short identifier (filename without extension).
	Name string

	// FilePath is the absolute path to the source file.
	FilePath string

	// Description from frontmatter.
	Description string

	// Globs is a list of file patterns. If any match, the skill is applied.
	Globs []string

	// AlwaysApply means the skill is always included regardless of context.
	AlwaysApply bool

	// Content is the body of the skill file (without frontmatter).
	Content string
}

// Loader discovers and loads skills from the filesystem.
type Loader struct {
	// Dirs is the list of directories to search for skills.
	Dirs []string

	// ExtraFiles are specific skill files to always load.
	ExtraFiles []string
}

// NewLoader creates a Loader with the given directories.
func NewLoader(dirs, extraFiles []string) *Loader {
	return &Loader{
		Dirs:       dirs,
		ExtraFiles: extraFiles,
	}
}

// LoadAll discovers and loads all skills from configured directories.
func (l *Loader) LoadAll(cwd string) ([]*Skill, error) {
	var skills []*Skill
	seen := make(map[string]bool)

	// Load from directories in order.
	for _, dir := range l.Dirs {
		expanded := expandPath(dir, cwd)
		found, err := loadFromDir(expanded)
		if err != nil {
			// Skip directories that don't exist.
			continue
		}
		for _, s := range found {
			if !seen[s.FilePath] {
				seen[s.FilePath] = true
				skills = append(skills, s)
			}
		}
	}

	// Load extra files.
	for _, f := range l.ExtraFiles {
		expanded := expandPath(f, cwd)
		if seen[expanded] {
			continue
		}
		s, err := loadFile(expanded)
		if err != nil {
			continue
		}
		seen[expanded] = true
		skills = append(skills, s)
	}

	return skills, nil
}

// FilterForContext returns skills applicable to the given context files.
// Always-apply skills are always included.
func FilterForContext(skills []*Skill, contextFiles []string) []*Skill {
	var result []*Skill
	for _, s := range skills {
		if s.AlwaysApply {
			result = append(result, s)
			continue
		}
		if len(s.Globs) == 0 {
			// No globs and not alwaysApply = always apply.
			result = append(result, s)
			continue
		}
		for _, pattern := range s.Globs {
			if matchesAny(pattern, contextFiles) {
				result = append(result, s)
				break
			}
		}
	}
	return result
}

// BuildSystemPromptSection builds the skills section for the system prompt.
func BuildSystemPromptSection(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Active Rules and Skills\n\n")
	for _, s := range skills {
		if s.Description != "" {
			b.WriteString("### ")
			b.WriteString(s.Name)
			b.WriteString(" (")
			b.WriteString(s.Description)
			b.WriteString(")\n\n")
		} else {
			b.WriteString("### ")
			b.WriteString(s.Name)
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
		if e.IsDir() {
			// Recurse one level for skill subdirectories with SKILL.md.
			subSkill := filepath.Join(dir, e.Name(), "SKILL.md")
			if _, err := os.Stat(subSkill); err == nil {
				s, err := loadFile(subSkill)
				if err == nil {
					skills = append(skills, s)
				}
			}
			continue
		}

		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".mdc" && name != "SKILL.md" {
			continue
		}

		s, err := loadFile(filepath.Join(dir, name))
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
		skill.Description = fm.Description
		skill.Globs = fm.Globs
		skill.AlwaysApply = fm.AlwaysApply
	}

	return skill, nil
}

// frontmatter is the YAML frontmatter of a skill file.
type frontmatter struct {
	Description string   `yaml:"description"`
	Globs       []string `yaml:"globs"`
	AlwaysApply bool     `yaml:"alwaysApply"`
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

// matchesAny checks if any context file matches a glob pattern.
// Handles **/ prefix patterns by matching against the basename or full path.
func matchesAny(pattern string, files []string) bool {
	// Strip **/ prefix for simpler matching.
	simplePattern := pattern
	if strings.HasPrefix(simplePattern, "**/") {
		simplePattern = simplePattern[3:]
	}

	for _, f := range files {
		// Match against full path.
		if matched, err := filepath.Match(pattern, f); err == nil && matched {
			return true
		}
		// Match against just the basename.
		if matched, err := filepath.Match(simplePattern, filepath.Base(f)); err == nil && matched {
			return true
		}
		// Match simplified pattern against full path.
		if matched, err := filepath.Match(simplePattern, f); err == nil && matched {
			return true
		}
	}
	return false
}

// expandPath resolves ${WORKSPACE} and ~ in a path.
func expandPath(path, cwd string) string {
	path = strings.ReplaceAll(path, "${WORKSPACE}", cwd)
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	return path
}

// ExpandConfiguredPath resolves ${WORKSPACE} and ~ the same way as skill loading.
func ExpandConfiguredPath(path, cwd string) string {
	return expandPath(path, cwd)
}
