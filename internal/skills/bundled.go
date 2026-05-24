package skills

import (
	_ "embed"
	"path/filepath"
	"strings"
)

//go:embed bundled/coddy-generate-rules/SKILL.md
var bundledCoddyGenerateRules []byte

// Bundled returns built-in skills shipped with the binary (prepended before skills.dirs).
func Bundled() []*Skill {
	if len(bundledCoddyGenerateRules) == 0 {
		return nil
	}
	virtual := filepath.Join("bundled", "coddy-generate-rules", "SKILL.md")
	s, err := parseSkillBytes(virtual, bundledCoddyGenerateRules)
	if err != nil {
		return nil
	}
	return []*Skill{s}
}

func parseSkillBytes(path string, data []byte) (*Skill, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.EqualFold(name, "SKILL") {
		name = filepath.Base(filepath.Dir(path))
	}
	skill := &Skill{Name: name, FilePath: path}
	body, fm := parseFrontmatter(data)
	skill.Content = strings.TrimSpace(body)
	if fm != nil {
		skill.Description = fm.Description
		skill.Globs = fm.Globs
		skill.AlwaysApply = fm.AlwaysApply
	}
	return skill, nil
}
