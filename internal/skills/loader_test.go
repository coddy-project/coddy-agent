package skills_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// withoutBundled drops the standard delivery from a listing, so a test can
// assert on what it put in a directory itself. The names come from the delivery
// rather than a list here, which is what keeps this from going stale every time
// a skill joins or leaves it.
func withoutBundled(loaded []*skills.Skill) []*skills.Skill {
	delivered := make(map[string]bool)
	for _, s := range skills.Bundled() {
		delivered[skills.CanonicalCommandName(s)] = true
	}
	var out []*skills.Skill
	for _, s := range loaded {
		if delivered[skills.CanonicalCommandName(s)] {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestLoadSkillWithFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	content := `---
name: "go-standards"
description: "Go coding standards"
version: "1.4.2"
---

# Go Standards

Write comments in English.
Use fmt.Errorf for error wrapping.
`
	path := filepath.Join(tmp, "go-standards.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}

	s := loaded[0]
	if s.Name != "go-standards" {
		t.Errorf("expected name %q from frontmatter, got %q", "go-standards", s.Name)
	}
	if s.Description != "Go coding standards" {
		t.Errorf("expected description %q, got %q", "Go coding standards", s.Description)
	}
	if s.Version != "1.4.2" {
		t.Errorf("expected version %q from frontmatter, got %q", "1.4.2", s.Version)
	}
	if !strings.Contains(s.Content, "Write comments") {
		t.Errorf("expected content in skill body, got: %q", s.Content)
	}
}

func TestLoadSkillNoFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	content := "# Simple Rule\n\nAlways write tests."
	path := filepath.Join(tmp, "simple.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if !strings.Contains(loaded[0].Content, "Always write tests") {
		t.Errorf("unexpected content: %q", loaded[0].Content)
	}
}

func TestLoadSKILLFile(t *testing.T) {
	tmp := t.TempDir()

	// Create skill in subdirectory.
	skillDir := filepath.Join(tmp, "my-skill")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "# My Skill\n\nDo something useful."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Name != "SKILL" {
		t.Errorf("expected name %q, got %q", "SKILL", loaded[0].Name)
	}
}

func TestLoadSymlinkDirWithSKILLMd(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real", "linked-skill")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: via symlink dir\n---\n\nBody."
	if err := os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "skills-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-skill")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skip("unsupported symlink:", err)
	}

	loader := skills.NewLoader([]string{root})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if got := skills.CanonicalCommandName(loaded[0]); got != "linked-skill" {
		t.Fatalf("canonical name: got %q want linked-skill", got)
	}
	if loaded[0].Description != "via symlink dir" {
		t.Errorf("description %q", loaded[0].Description)
	}
}

func TestFilterForContext(t *testing.T) {
	a := &skills.Skill{Name: "a", Content: "content a"}
	b := &skills.Skill{Name: "b", Content: "content b"}
	c := &skills.Skill{Name: "c", Content: "content c"}

	all := []*skills.Skill{a, b, c}

	// All skills are always returned regardless of context files.
	for _, contextFiles := range [][]string{nil, {"/project/main.go"}, {"/project/app.ts"}} {
		filtered := skills.FilterForContext(all, contextFiles)
		if len(filtered) != 3 {
			t.Errorf("expected 3 skills for context %v, got %d", contextFiles, len(filtered))
		}
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	loaded := []*skills.Skill{
		{Name: "rule1", FilePath: filepath.Join("/tmp", "rules", "rule1.md"), Description: "First rule", Content: "Content of rule 1"},
		{Name: "rule2", FilePath: filepath.Join("/tmp", "rules", "rule2.md"), Content: "Content of rule 2"},
	}

	section := skills.BuildSystemPromptSection(loaded)
	if !strings.Contains(section, "## Active Skills") {
		t.Error("missing section header")
	}
	if !strings.Contains(section, "rule1") {
		t.Error("missing rule1")
	}
	if !strings.Contains(section, "First rule") {
		t.Error("missing description")
	}
	if !strings.Contains(section, "Content of rule 1") {
		t.Error("missing content")
	}
}

func TestLoadAllExpandsCODDYHome(t *testing.T) {
	home := t.TempDir()
	skillRoot := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# In Home\n\nBody."
	if err := os.WriteFile(filepath.Join(skillRoot, "rule.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := skills.NewLoader([]string{"${CODDY_HOME}/skills"})
	loaded, err := loader.LoadAll("/tmp", home)
	if err != nil {
		t.Fatal(err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("got %d skills", len(loaded))
	}
}

func TestLoadFromNonexistentDir(t *testing.T) {
	loader := skills.NewLoader([]string{"/nonexistent/path"})
	loaded, err := loader.LoadAll("/tmp", "")
	// Should not error, just return empty.
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	loaded = withoutBundled(loaded)
	if len(loaded) != 0 {
		t.Errorf("expected no user skills for nonexistent dir, got %d", len(loaded))
	}
}

func TestLaterDirOverridesSameName(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Same skill name in two directories.
	skill1 := filepath.Join(dir1, "my-skill")
	if err := os.MkdirAll(skill1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill1, "SKILL.md"), []byte("---\ndescription: from dir1\n---\n\nBody1"), 0o644); err != nil {
		t.Fatal(err)
	}

	skill2 := filepath.Join(dir2, "my-skill")
	if err := os.MkdirAll(skill2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill2, "SKILL.md"), []byte("---\ndescription: from dir2\n---\n\nBody2"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{dir1, dir2})
	loaded, err := loader.LoadAll("/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill after dedup, got %d", len(loaded))
	}
	if loaded[0].Description != "from dir2" {
		t.Errorf("expected dir2 to win, got description %q", loaded[0].Description)
	}
	if !strings.Contains(loaded[0].Content, "Body2") {
		t.Errorf("expected dir2 content, got %q", loaded[0].Content)
	}
}

// Skills declare their version under the Agent Skills metadata map now
// (metadata.version); the top-level version key of older files still counts,
// the metadata one wins when both are there, and a metadata of another shape
// costs the version only, never the name or the description.
func TestLoadSkillVersionFromMetadata(t *testing.T) {
	cases := []struct {
		name, front, want string
	}{
		{"metadata", "metadata:\n  version: 1.0.1\n", "1.0.1"},
		{"top level", "version: 0.9.0\n", "0.9.0"},
		{"both", "version: 0.9.0\nmetadata:\n  author: me\n  version: 1.3.1\n", "1.3.1"},
		{"numeric", "metadata:\n  version: 2.0\n", "2.0"},
		{"metadata not a map", "metadata: just text\n", ""},
		{"none", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body := "---\nname: demo\ndescription: Does a thing.\n" + tc.front + "---\nBody.\n"
			if err := os.MkdirAll(filepath.Join(dir, "demo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "demo", "SKILL.md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			loaded, err := skills.NewLoader([]string{dir}).LoadAll(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			var got *skills.Skill
			for _, s := range loaded {
				if s.Name == "demo" {
					got = s
				}
			}
			if got == nil {
				names := []string{}
				for _, s := range loaded {
					names = append(names, s.Name+"@"+s.FilePath)
				}
				t.Fatalf("demo not loaded: %v", names)
			}
			if got.Version != tc.want {
				t.Fatalf("version = %q, want %q", got.Version, tc.want)
			}
			if got.Description != "Does a thing." {
				t.Fatalf("description lost: %q", got.Description)
			}
		})
	}
}

// writeNamedSkill writes <dir>/<name>/SKILL.md whose description says which
// folder it came from.
func writeNamedSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// describe loads the skills a workspace sees and returns the description of
// the one called name, "" when it is not there.
func describe(t *testing.T, loader *skills.Loader, cwd, coddyHome, name string) string {
	t.Helper()
	loaded, err := loader.LoadAll(cwd, coddyHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range loaded {
		if skills.CanonicalCommandName(s) == name {
			return s.Description
		}
	}
	return ""
}

// The default folders, lowest priority first: the user's agents skills, the
// project's agents skills, Coddy's own skills, the project's Coddy skills. A
// name found in several is taken from the last; taking the top layer away
// shows the one under it.
func TestDefaultSkillDirsLayerInOrder(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	coddyHome := t.TempDir()
	project := t.TempDir()
	layers := []struct{ dir, label string }{
		{filepath.Join(userHome, ".agents", "skills"), "user agents"},
		{filepath.Join(project, ".agents", "skills"), "project agents"},
		{filepath.Join(coddyHome, "skills"), "coddy home"},
		{filepath.Join(project, ".coddy", "skills"), "project coddy"},
	}
	for _, l := range layers {
		writeNamedSkill(t, l.dir, "chain", l.label)
	}
	loader := skills.NewLoader(config.DefaultSkillDirs())
	for i := len(layers) - 1; i >= 0; i-- {
		if got := describe(t, loader, project, coddyHome, "chain"); got != layers[i].label {
			t.Fatalf("with %d layers the skill came from %q, want %q", i+1, got, layers[i].label)
		}
		if err := os.RemoveAll(filepath.Join(layers[i].dir, "chain")); err != nil {
			t.Fatal(err)
		}
	}
	if got := describe(t, loader, project, coddyHome, "chain"); got != "" {
		t.Fatalf("with every layer gone the skill is still there: %q", got)
	}
}

// A relative entry names a folder of the workspace, like ${CWD}: the default
// ".agents/skills" of the request follows the session and the folder a new
// chat picked, never the directory the server process was started from.
func TestRelativeSkillDirResolvesAgainstTheWorkspace(t *testing.T) {
	project := t.TempDir()
	writeNamedSkill(t, filepath.Join(project, ".agents", "skills"), "relative", "from the workspace")
	loader := skills.NewLoader([]string{".agents/skills"})
	if got := describe(t, loader, project, "", "relative"); got != "from the workspace" {
		t.Fatalf("relative entry in the project: got %q", got)
	}
	if got := describe(t, loader, t.TempDir(), "", "relative"); got != "" {
		t.Fatalf("another workspace must not see it: got %q", got)
	}
	if got := skills.ExpandConfiguredPath(".agents/skills", project, ""); got != filepath.Join(project, ".agents", "skills") {
		t.Fatalf("ExpandConfiguredPath(.agents/skills) = %q", got)
	}
}

// ${HOME} names the user's home like ~ does, and an entry of the workspace
// with no workspace behind the call reads nothing rather than a folder at the
// root of the disk.
func TestSkillDirPlaceholdersHomeAndMissingWorkspace(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	if got := skills.ExpandConfiguredPath("${HOME}/.agents/skills", "/w", ""); got != filepath.Join(userHome, ".agents", "skills") {
		t.Fatalf("${HOME}: got %q", got)
	}
	for _, entry := range []string{"${CWD}/.coddy/skills", ".agents/skills"} {
		if got := skills.ExpandConfiguredPath(entry, "", ""); got != "" {
			t.Fatalf("%s with no workspace: got %q, want nothing", entry, got)
		}
	}
}

// A default folder may itself be a link that leads out of the project.
func TestDefaultSkillDirThatIsALink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	outside := t.TempDir()
	writeNamedSkill(t, outside, "linked-default", "outside the project")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, ".agents", "skills")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	loader := skills.NewLoader(config.DefaultSkillDirs())
	if got := describe(t, loader, project, t.TempDir(), "linked-default"); got != "outside the project" {
		t.Fatalf("skill behind a linked .agents/skills: got %q", got)
	}
}

// A folder named twice is read once, at its last place: the lowest entry is
// the strongest one, for a folder as for a skill, so a default folder named
// again in skills.dirs moves below the directories listed before it.
func TestFolderNamedTwiceIsReadAtItsLastPlace(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	writeNamedSkill(t, a, "twice", "from a")
	writeNamedSkill(t, b, "twice", "from b")
	if got := describe(t, skills.NewLoader([]string{a, b, a}), root, "", "twice"); got != "from a" {
		t.Fatalf("a, b, a: got %q, want the copy of a (read at its last place)", got)
	}
	if got := describe(t, skills.NewLoader([]string{a, b}), root, "", "twice"); got != "from b" {
		t.Fatalf("a, b: got %q, want b (the later directory)", got)
	}
	// A link to a folder already listed is that folder.
	link := filepath.Join(root, "link-to-a")
	if err := os.Symlink(a, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got := describe(t, skills.NewLoader([]string{link, b, a}), root, "", "twice"); got != "from a" {
		t.Fatalf("link-to-a, b, a: got %q, want a", got)
	}
}

// The search roots coddy skills list prints are the folders the loader reads,
// in its order: a folder named twice at its last place, a link to a listed
// folder counted as that folder, so the list says which folder wins a name.
func TestSearchRootsAreTheFoldersTheLoaderReads(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := skills.SearchRoots([]string{a, b, a}, root, ""); !reflect.DeepEqual(got, []string{b, a}) {
		t.Fatalf("a, b, a: roots %v, want b then a", got)
	}
	link := filepath.Join(root, "link-to-a")
	if err := os.Symlink(a, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got := skills.SearchRoots([]string{a, link, b}, root, ""); !reflect.DeepEqual(got, []string{link, b}) {
		t.Fatalf("a, link-to-a, b: roots %v, want the link once, then b", got)
	}
	// A ${CWD} entry without a workspace names no folder.
	if got := skills.SearchRoots([]string{"${CWD}/.coddy/skills", b}, "", ""); !reflect.DeepEqual(got, []string{b}) {
		t.Fatalf("no workspace: roots %v", got)
	}
}
