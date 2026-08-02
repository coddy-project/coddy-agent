package skills

import (
	_ "embed"
	"path/filepath"
	"strings"
)

//go:embed bundled/generate-rules/SKILL.md
var bundledGenerateRules []byte

//go:embed bundled/configure-coddy/SKILL.md
var bundledConfigureCoddy []byte

// Bundled returns built-in skills shipped with the binary (prepended before skills.dirs).
func Bundled() []*Skill {
	sources := []struct {
		path string
		data []byte
	}{
		{filepath.Join("bundled", "generate-rules", "SKILL.md"), bundledGenerateRules},
		{filepath.Join("bundled", "configure-coddy", "SKILL.md"), bundledConfigureCoddy},
	}
	out := make([]*Skill, 0, len(sources))
	for _, source := range sources {
		if len(source.data) == 0 {
			continue
		}
		skill, err := parseSkillBytes(source.path, source.data)
		if err == nil {
			out = append(out, skill)
		}
	}
	return out
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
		if fm.Name != "" {
			skill.Name = fm.Name
		}
		skill.Description = fm.Description
	}
	return skill, nil
}
