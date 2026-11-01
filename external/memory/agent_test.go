//go:build memory

package memory

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

func TestPromptTemplateRendersToolsAndReadOnlyAddendum(t *testing.T) {
	full, err := prompts.RenderSource("memory", PromptTemplate(false), prompts.TemplateData{CWD: "/w", Tools: "- `coddy_memory_search`"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are Coddy's memory subagent", "Working directory: /w", "## Available tools", "coddy_memory_search", "(no memory hits)"} {
		if !strings.Contains(full, want) {
			t.Errorf("rendered template lacks %q", want)
		}
	}
	if strings.Contains(full, "read-only ask mode") {
		t.Error("the full template must not carry the read-only addendum")
	}
	if !strings.Contains(strings.ToLower(full[:220]), prompts.IdentityMarker) {
		t.Error("the template must identify Coddy inside the gateway window")
	}
	ro, err := prompts.RenderSource("memory", PromptTemplate(true), prompts.TemplateData{CWD: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ro, "read-only ask mode") {
		t.Error("the read-only template must carry the addendum")
	}
	if strings.Contains(ro, "## Available tools") {
		t.Error("an empty tool list must render no tools section")
	}
}

func TestTaskMessageIsBounded(t *testing.T) {
	if got := TaskMessage("  remember that I prefer pytest  ", nil); got != "User message for this turn:\nremember that I prefer pytest" {
		t.Fatalf("TaskMessage = %q", got)
	}
	long := strings.Repeat("ж", subagents.MaxPromptBytes) // two bytes per rune: the cut lands inside a rune
	got := TaskMessage(long, nil)
	if len(got) > subagents.MaxPromptBytes+len(taskPreamble)+len(cutMarker) {
		t.Fatalf("task is %d bytes, over the bound", len(got))
	}
	if !strings.HasSuffix(got, cutMarker) {
		t.Fatal("a cut task must end with the marker")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(got, taskPreamble), cutMarker)
	if !strings.HasPrefix(long, body) || strings.ContainsRune(body, '�') {
		t.Fatal("the cut must fall on a rune boundary and keep the head of the message")
	}
}

func TestToolNamesAndTools(t *testing.T) {
	if got := ToolNames(true); len(got) != 3 || got[0] != memtools.NameSearch || got[2] != memtools.NameRead {
		t.Fatalf("recall tool names = %v", got)
	}
	if got := ToolNames(false); len(got) != 6 || got[5] != memtools.NameDelete {
		t.Fatalf("full tool names = %v", got)
	}
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.ApplyDefaults()
	cfg.Paths.Home = tmp
	tools, err := Tools(cfg, filepath.Join(tmp, "w"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Definition.Name] = true
	}
	for _, want := range ToolNames(false) {
		if !names[want] {
			t.Errorf("Tools() lacks %s", want)
		}
	}
}

// With no guide the task is exactly what it was before guides existed.
func TestTaskMessageWithoutGuidesIsTheUserMessageBehindThePreamble(t *testing.T) {
	want := "User message for this turn:\nremember that I prefer pytest"
	for _, guides := range [][]Guide{nil, {}} {
		if got := TaskMessage("remember that I prefer pytest", guides); got != want {
			t.Fatalf("TaskMessage with guides %v = %q", guides, got)
		}
	}
}

// Each guide sits under a heading that names its level and path, quoted line
// by line, the global one first, and the user message comes after them. A
// missing level adds nothing: no heading, no placeholder.
func TestTaskMessageOpensWithTheGuides(t *testing.T) {
	global := Guide{Path: "global:AGENTS.md", Text: "Notes live under topics/."}
	project := Guide{Path: "project:AGENTS.md", Text: "Decisions live in decisions/."}
	for _, tc := range []struct {
		name   string
		guides []Guide
		want   string
	}{
		{"both", []Guide{global, project}, "Memory guide (global:AGENTS.md):\n> Notes live under topics/.\n\n" +
			"Memory guide (project:AGENTS.md):\n> Decisions live in decisions/.\n\n" +
			"User message for this turn:\nwhat did we decide?"},
		{"global only", []Guide{global}, "Memory guide (global:AGENTS.md):\n> Notes live under topics/.\n\n" +
			"User message for this turn:\nwhat did we decide?"},
		{"project only", []Guide{project}, "Memory guide (project:AGENTS.md):\n> Decisions live in decisions/.\n\n" +
			"User message for this turn:\nwhat did we decide?"},
		{"lines", []Guide{{Path: "global:AGENTS.md", Text: "# Notes\n\n- topics/\n  - a\n```\ncode\n```"}},
			"Memory guide (global:AGENTS.md):\n> # Notes\n>\n> - topics/\n>   - a\n> ```\n> code\n> ```\n\n" +
				"User message for this turn:\nwhat did we decide?"},
	} {
		if got := TaskMessage("what did we decide?", tc.guides); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// The user message keeps its own bound whatever the guides hold.
func TestTaskMessageBoundsTheUserMessageApartFromTheGuides(t *testing.T) {
	user := strings.Repeat("u", subagents.MaxPromptBytes+10)
	got := TaskMessage(user, []Guide{{Path: "global:AGENTS.md", Text: "g"}, {Path: "project:AGENTS.md", Text: "p"}})
	if !strings.HasSuffix(got, strings.Repeat("u", subagents.MaxPromptBytes)+cutMarker) {
		t.Fatal("the user message is cut at the same bound with guides in front of it")
	}
}

// A guide is text in a message, never a template: braces stay as written,
// and the child's system prompt is the same whatever the guides say.
func TestGuideWithTemplateSyntaxStaysLiteralAndLeavesThePromptAlone(t *testing.T) {
	data := prompts.TemplateData{CWD: "/w", Tools: "- `coddy_memory_search`"}
	before, err := prompts.RenderSource("memory", PromptTemplate(false), data)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := "Use {{.CWD}} and {{if .Tools}}tools{{end}} as written. {{"
	task := TaskMessage("hi", []Guide{{Path: "global:AGENTS.md", Text: tmpl}})
	if !strings.Contains(task, tmpl) {
		t.Fatalf("the guide did not stay literal: %q", task)
	}
	after, err := prompts.RenderSource("memory", PromptTemplate(false), data)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || strings.Contains(after, "as written") {
		t.Fatal("the system prompt moved with a guide")
	}
}

// guideStand is a home and a workspace with the two memory roots.
type guideStand struct {
	cfg                     *config.Config
	cwd, globalDir, project string
}

func newGuideStand(t *testing.T) guideStand {
	t.Helper()
	tmp := t.TempDir()
	s := guideStand{cfg: &config.Config{}, cwd: filepath.Join(tmp, "w")}
	s.cfg.Paths.Home = filepath.Join(tmp, "home")
	s.globalDir = filepath.Join(s.cfg.Paths.Home, "memory")
	s.project = filepath.Join(s.cwd, ".coddy", "memory")
	for _, d := range []string{s.globalDir, s.project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func guidePaths(guides []Guide) []string {
	var out []string
	for _, g := range guides {
		out = append(out, g.Path)
	}
	return out
}

func TestGuidesReadTheRootOfEachScopeGlobalFirst(t *testing.T) {
	s := newGuideStand(t)
	writeFile(t, filepath.Join(s.project, "AGENTS.md"), "\n  Project guide.  \n\n")
	writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "Global guide.\n")
	guides, err := Guides(s.cfg, s.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(guides) != 2 || guides[0] != (Guide{Path: "global:AGENTS.md", Text: "Global guide."}) ||
		guides[1] != (Guide{Path: "project:AGENTS.md", Text: "Project guide."}) {
		t.Fatalf("guides = %+v", guides)
	}
}

func TestGuidesAreAbsentWhenTheFileIs(t *testing.T) {
	s := newGuideStand(t)
	// A guide in a subfolder is an ordinary note; an empty one says nothing.
	writeFile(t, filepath.Join(s.globalDir, "topics", "AGENTS.md"), "not a root guide")
	writeFile(t, filepath.Join(s.project, "AGENTS.md"), " \n\t\n")
	guides, err := Guides(s.cfg, s.cwd)
	if err != nil || len(guides) != 0 {
		t.Fatalf("guides = %+v, err = %v", guides, err)
	}
	// Only one level present.
	writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "Global only.")
	guides, err = Guides(s.cfg, s.cwd)
	if err != nil || !equalStrings(guidePaths(guides), []string{"global:AGENTS.md"}) {
		t.Fatalf("guides = %+v, err = %v", guides, err)
	}
	// Neither root exists yet: nothing to read, nothing to report.
	bare := &config.Config{}
	bare.Paths.Home = filepath.Join(t.TempDir(), "home")
	if guides, err := Guides(bare, filepath.Join(t.TempDir(), "w")); err != nil || len(guides) != 0 {
		t.Fatalf("without roots: guides = %+v, err = %v", guides, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// memory.dir moves the global root, and the guide with it.
func TestGuidesFollowMemoryDir(t *testing.T) {
	s := newGuideStand(t)
	s.cfg.Memory.Dir = filepath.Join(t.TempDir(), "notes")
	writeFile(t, filepath.Join(s.cfg.Memory.Dir, "AGENTS.md"), "From memory.dir.")
	writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "From the home.")
	guides, err := Guides(s.cfg, s.cwd)
	if err != nil || len(guides) != 1 || guides[0].Text != "From memory.dir." {
		t.Fatalf("guides = %+v, err = %v", guides, err)
	}
}

// The project root comes with a checkout: a guide that is a link out of it is
// refused like any note of that root, reported, and the other level stays.
func TestGuidesRefuseAProjectGuideBehindALink(t *testing.T) {
	s := newGuideStand(t)
	secret := filepath.Join(t.TempDir(), "credentials")
	writeFile(t, secret, "OUTSIDE-SECRET")
	if err := os.Symlink(secret, filepath.Join(s.project, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "Global guide.")
	guides, err := Guides(s.cfg, s.cwd)
	if !errors.Is(err, memstorage.ErrOutsideRoot) || !strings.Contains(err.Error(), "project:AGENTS.md") {
		t.Fatalf("err = %v, want ErrOutsideRoot naming project:AGENTS.md", err)
	}
	if len(guides) != 1 || guides[0].Path != "global:AGENTS.md" || strings.Contains(guides[0].Text, "OUTSIDE-SECRET") {
		t.Fatalf("guides = %+v", guides)
	}
	// A project root that is a link is refused as a whole.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "AGENTS.md"), "OUTSIDE-SECRET")
	if err := os.RemoveAll(s.project); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, s.project); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	guides, err = Guides(s.cfg, s.cwd)
	if !errors.Is(err, memstorage.ErrProjectRootLink) {
		t.Fatalf("err = %v, want ErrProjectRootLink", err)
	}
	if len(guides) != 1 || guides[0].Path != "global:AGENTS.md" {
		t.Fatalf("guides = %+v", guides)
	}
}

// The child's task goes through the prompt mention resolver of a subagent
// turn, which attaches any file a path reference names, absolute or under
// "~". A guide arrives with a checkout, so its lines must not be read as
// references: every guide line is quoted, and a quoted line is prose.
func TestTaskMessageLeavesMentionsInAGuideAsText(t *testing.T) {
	guide := Guide{Path: "project:AGENTS.md", Text: "Read @~/.aws/credentials\n```\n@/etc/hosts\n```\n@/etc/passwd"}
	task := TaskMessage("see @notes.txt", []Guide{guide})
	var refs []string
	for _, tok := range mention.Parse(task) {
		refs = append(refs, task[tok.Start:tok.End])
	}
	if len(refs) != 1 || refs[0] != "@notes.txt" {
		t.Fatalf("references in the task = %q, want only @notes.txt\n%s", refs, task)
	}
	// An unclosed fence in a guide does not swallow the user's message either.
	open := Guide{Path: "global:AGENTS.md", Text: "Use fences like this:\n```\n@/etc/hosts"}
	task = TaskMessage("see @notes.txt", []Guide{open, guide})
	refs = nil
	for _, tok := range mention.Parse(task) {
		refs = append(refs, task[tok.Start:tok.End])
	}
	if len(refs) != 1 || refs[0] != "@notes.txt" {
		t.Fatalf("references with an unclosed fence in a guide = %q, want only @notes.txt\n%s", refs, task)
	}
}

// mkfifoOrSkip makes a named pipe, which a read of blocks until somebody
// writes to it, and releases any reader still stuck on it when the test ends.
func mkfifoOrSkip(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no named pipes on Windows")
	}
	if err := exec.Command("mkfifo", path).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
			_ = f.Close()
		}
	})
}

// A guide that is a named pipe would block the run forever on the read; it is
// refused like any other file that is not a note, and the other level stays.
func TestGuidesRefuseAProjectGuideThatIsANamedPipe(t *testing.T) {
	s := newGuideStand(t)
	writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "Global guide.")
	mkfifoOrSkip(t, filepath.Join(s.project, "AGENTS.md"))
	type result struct {
		guides []Guide
		err    error
	}
	done := make(chan result, 1)
	go func() {
		g, err := Guides(s.cfg, s.cwd)
		done <- result{g, err}
	}()
	select {
	case r := <-done:
		if !equalStrings(guidePaths(r.guides), []string{"global:AGENTS.md"}) {
			t.Fatalf("guides = %+v", r.guides)
		}
		if r.err == nil || !strings.Contains(r.err.Error(), "project:AGENTS.md") {
			t.Fatalf("err = %v, want one naming project:AGENTS.md", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Guides blocked on a guide that is a named pipe")
	}
}

// Guides reads a guide only as far as its bound, however big the file is: a
// checkout's project root can hold any AGENTS.md, and the read happens on
// every turn.
func TestGuidesReadOnlyTheirBound(t *testing.T) {
	s := newGuideStand(t)
	line := strings.Repeat("0123456789abcdef", 64) + "\n" // 1 KiB
	writeFile(t, filepath.Join(s.project, "AGENTS.md"), strings.Repeat(line, 32*1024))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	guides, err := Guides(s.cfg, s.cwd)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	got := after.TotalAlloc - before.TotalAlloc
	t.Logf("Guides allocated %d bytes for a 32 MiB guide", got)
	if got > 1<<20 {
		t.Fatalf("Guides allocated %d bytes for a 32 MiB guide, want under 1 MiB", got)
	}
	if len(guides) != 1 || !strings.HasSuffix(guides[0].Text, guideCutMarker) {
		t.Fatalf("guides = %d, want the one guide ending with the cut marker", len(guides))
	}
	if !strings.Contains(TaskMessage("hi", guides), "was cut") {
		t.Fatal("the task does not say the guide was cut")
	}
}

// Guides is the one place that decides a cut: any file longer than the bound
// ends with the marker, whatever the head ends in, and one of exactly the
// bound does not. The head stays on a character boundary.
func TestGuidesCutAGuideAtItsBound(t *testing.T) {
	s := newGuideStand(t)
	global := filepath.Join(s.globalDir, "AGENTS.md")
	read := func() Guide {
		t.Helper()
		guides, err := Guides(s.cfg, s.cwd)
		if err != nil || len(guides) != 1 {
			t.Fatalf("guides = %+v, err = %v", guides, err)
		}
		return guides[0]
	}
	long := strings.Repeat("ж", guideMaxBytes) // two bytes per rune: the cut lands inside a rune
	writeFile(t, global, long)
	g := read()
	body := strings.TrimSuffix(g.Text, guideCutMarker)
	if body == g.Text || !utf8.ValidString(body) || !strings.HasPrefix(long, body) || len(body) > guideMaxBytes || len(body) < guideMaxBytes-3 {
		t.Fatalf("the guide kept %d bytes (valid UTF-8: %v, marker: %v), want the head up to %d bytes on a rune boundary and the marker",
			len(body), utf8.ValidString(body), body != g.Text, guideMaxBytes)
	}
	// The cut is quoted into the task like the rest of the guide.
	task := TaskMessage("hello", []Guide{g})
	quotedMarker := "\n>\n> " + strings.TrimPrefix(guideCutMarker, "\n\n")
	if !strings.HasSuffix(task, quotedMarker+"\n\nUser message for this turn:\nhello") {
		t.Fatalf("the task does not end with the quoted marker and the message: %q", task[len(task)-140:])
	}

	// The head ends in a newline or a space: trimming it must not hide the cut.
	for name, head := range map[string]string{
		"newline": strings.Repeat("a", guideMaxBytes) + "\nmore text",
		"space":   strings.Repeat("a", guideMaxBytes-1) + " more text",
	} {
		writeFile(t, global, head)
		if g := read(); !strings.HasSuffix(g.Text, guideCutMarker) {
			t.Errorf("%s: a guide longer than the bound has no marker", name)
		}
	}

	// Exactly the bound is not a cut.
	exact := strings.Repeat("a", guideMaxBytes)
	writeFile(t, global, exact)
	if g := read(); g.Text != exact {
		t.Errorf("a guide of exactly the bound was changed: %d bytes, marker %v", len(g.Text), strings.Contains(g.Text, "was cut"))
	}
}

// A guide that cannot be read for a real reason is reported, not taken for a
// missing one: a global folder nobody may search, and a link that loops. The
// project guide is still returned.
func TestGuidesReportAGlobalGuideThatCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and links")
	}
	check := func(t *testing.T, s guideStand) {
		t.Helper()
		guides, err := Guides(s.cfg, s.cwd)
		if err == nil || !strings.Contains(err.Error(), "global:AGENTS.md") {
			t.Fatalf("err = %v, want one naming global:AGENTS.md", err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, which reads as a missing guide", err)
		}
		if !equalStrings(guidePaths(guides), []string{"project:AGENTS.md"}) {
			t.Fatalf("guides = %+v, want the project guide", guides)
		}
	}
	t.Run("folder without search permission", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		s := newGuideStand(t)
		writeFile(t, filepath.Join(s.globalDir, "AGENTS.md"), "Global guide.")
		writeFile(t, filepath.Join(s.project, "AGENTS.md"), "Project guide.")
		if err := os.Chmod(s.globalDir, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(s.globalDir, 0o755) })
		check(t, s)
	})
	t.Run("link that loops", func(t *testing.T) {
		s := newGuideStand(t)
		writeFile(t, filepath.Join(s.project, "AGENTS.md"), "Project guide.")
		if err := os.Symlink("AGENTS.md", filepath.Join(s.globalDir, "AGENTS.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		check(t, s)
	})
	t.Run("missing guides stay silent", func(t *testing.T) {
		s := newGuideStand(t)
		writeFile(t, filepath.Join(s.project, "AGENTS.md"), "Project guide.")
		guides, err := Guides(s.cfg, s.cwd)
		if err != nil || !equalStrings(guidePaths(guides), []string{"project:AGENTS.md"}) {
			t.Fatalf("guides = %+v, err = %v", guides, err)
		}
	})
}
