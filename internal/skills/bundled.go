package skills

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// bundledFS carries the standard delivery: one directory per skill, each with
// its SKILL.md and whatever that skill reads beside it. The all: prefix is what
// keeps the dot-directories of an example tree (a vendored .cursor/rules, a
// .codex/hooks) inside the binary - embed drops them otherwise, and a skill
// that points at a reference file it cannot open is worse than no skill.
//
//go:embed all:bundled
var bundledFS embed.FS

// bundledRoot is the directory inside bundledFS holding one folder per skill.
const bundledRoot = "bundled"

// BundledEntry is one skill of the standard delivery: its canonical name, the
// version its SKILL.md declares, and the subtree to copy when it is handed to
// an operator's home.
type BundledEntry struct {
	Name    string
	Version string
	Dir     fs.FS
}

// BundledEntries returns the standard delivery in name order. A directory
// without a readable SKILL.md is skipped rather than reported: the vendoring
// script is what guards the tree's shape, and a broken entry must not take the
// rest of the delivery down with it.
func BundledEntries() []BundledEntry {
	names, err := fs.ReadDir(bundledFS, bundledRoot)
	if err != nil {
		return nil
	}
	out := make([]BundledEntry, 0, len(names))
	for _, e := range names {
		if !e.IsDir() {
			continue
		}
		dirPath := path.Join(bundledRoot, e.Name())
		data, err := fs.ReadFile(bundledFS, path.Join(dirPath, "SKILL.md"))
		if err != nil {
			continue
		}
		sub, err := fs.Sub(bundledFS, dirPath)
		if err != nil {
			continue
		}
		skill := parseSkillBytes(path.Join(dirPath, "SKILL.md"), data)
		out = append(out, BundledEntry{
			Name:    CanonicalCommandName(skill),
			Version: skill.Version,
			Dir:     sub,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Bundled returns the standard delivery read straight out of the binary. The
// loader prepends these at the lowest priority, so they are what a session sees
// when the copies in ${CODDY_HOME}/skills are missing - a home that could not
// be written, a read-only image - and the on-disk copy wins everywhere else.
func Bundled() []*Skill {
	entries := BundledEntries()
	out := make([]*Skill, 0, len(entries))
	for _, e := range entries {
		data, err := fs.ReadFile(e.Dir, "SKILL.md")
		if err != nil {
			continue
		}
		out = append(out, parseSkillBytes(path.Join(bundledRoot, e.Name, "SKILL.md"), data))
	}
	return out
}

// parseSkillBytes builds a Skill from an in-binary SKILL.md. virtualPath is
// relative, which is what marks the result read-only (see SkillReadonly).
func parseSkillBytes(virtualPath string, data []byte) *Skill {
	name := strings.TrimSuffix(path.Base(virtualPath), path.Ext(virtualPath))
	if strings.EqualFold(name, "SKILL") {
		name = path.Base(path.Dir(virtualPath))
	}
	skill := &Skill{Name: name, FilePath: virtualPath}
	body, fm := parseFrontmatter(data)
	skill.Content = strings.TrimSpace(body)
	if fm != nil {
		if fm.Name != "" {
			skill.Name = fm.Name
		}
		skill.Description = fm.Description
		skill.Version = strings.TrimSpace(fm.Version)
	}
	return skill
}
