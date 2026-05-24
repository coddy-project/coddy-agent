package rules

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ruleFrontmatter struct {
	Description string   `yaml:"description"`
	Globs       []string `yaml:"globs"`
	Paths       []string `yaml:"paths"`
	AlwaysApply bool     `yaml:"alwaysApply"`
}

func parseRuleFile(path string, src Source, data []byte) (*Rule, error) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	body, fm := parseFrontmatter(data)
	r := &Rule{
		ID:       string(src) + ":" + path,
		Name:     name,
		FilePath: path,
		Source:   src,
		Content:  strings.TrimSpace(body),
	}
	if fm != nil {
		r.Description = fm.Description
		r.AlwaysApply = fm.AlwaysApply
		r.Globs = append([]string(nil), fm.Globs...)
		if len(fm.Paths) > 0 {
			r.Globs = append(r.Globs, fm.Paths...)
		}
		r.ApplyMode = classifyApplyMode(r)
	} else {
		r.AlwaysApply = true
		r.ApplyMode = ApplyAuto
	}
	return r, nil
}

func classifyApplyMode(r *Rule) ApplyMode {
	if !r.AlwaysApply {
		return ApplyMention
	}
	return ApplyAuto
}

func parseFrontmatter(data []byte) (string, *ruleFrontmatter) {
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
	var fm ruleFrontmatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return body, nil
	}
	return body, &fm
}

func loadMarkdownRulesFromRoot(root string, src Source) ([]*Rule, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}
	var out []*Rule
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".md" && ext != ".mdc" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		r, err := parseRuleFile(path, src, data)
		if err != nil {
			return nil
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MatchesAny reports whether any context file matches a glob pattern.
func MatchesAny(pattern string, files []string) bool {
	simplePattern := strings.TrimPrefix(pattern, "**/")
	for _, f := range files {
		if matched, err := filepath.Match(pattern, f); err == nil && matched {
			return true
		}
		if matched, err := filepath.Match(simplePattern, filepath.Base(f)); err == nil && matched {
			return true
		}
		if matched, err := filepath.Match(simplePattern, f); err == nil && matched {
			return true
		}
	}
	return false
}

func matchesRuleGlobs(r *Rule, contextFiles []string) bool {
	if r == nil || len(r.Globs) == 0 {
		return false
	}
	for _, p := range r.Globs {
		if MatchesAny(p, contextFiles) {
			return true
		}
	}
	return false
}
