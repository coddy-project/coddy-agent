package session_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// userInstructions reads instructions.files the way a session does: the
// entries resolved by session.ResolveInstructionFiles, read by
// rules.LoadStanding after the AGENTS.md and DESIGN.md of layerHome and
// layerCWD ("" for none), which a file of the list is never sent next to.
// It returns the texts read, in order, without the heading each one gets in
// the prompt (rules.RenderUserDocs).
func userInstructions(cwd, home string, files []string, layerHome, layerCWD string) string {
	var parts []string
	for _, doc := range rules.LoadStanding(layerHome, layerCWD, session.ResolveInstructionFiles(files, cwd, home)).User {
		parts = append(parts, doc.Content)
	}
	return strings.Join(parts, "\n\n")
}

// write creates a file with body and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadInstructionsSingleFile(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "AGENTS.md", "# Hello\nworld")
	got := userInstructions(tmp, "", []string{"AGENTS.md"}, "", "")
	if want := "# Hello\nworld"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestLoadInstructionsMultipleFilesKeepConfiguredOrder(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "a.md", "first")
	write(t, tmp, "docs/b.md", "second")
	got := userInstructions(tmp, "", []string{"a.md", "docs/b.md"}, "", "")
	want := "first\n\nsecond"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestLoadInstructionsMissingFilesSkipped(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "real.md", "content")
	got := userInstructions(tmp, "", []string{"missing.md", "real.md", "also-missing.md"}, "", "")
	if want := "content"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestLoadInstructionsEmptyFilesSkipped(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "empty.md", "   \n  ")
	write(t, tmp, "content.md", "real")
	got := userInstructions(tmp, "", []string{"empty.md", "content.md"}, "", "")
	if want := "real"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestLoadInstructionsNoneExist(t *testing.T) {
	tmp := t.TempDir()
	if got := userInstructions(tmp, "", []string{"AGENTS.md"}, "", ""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestLoadInstructionsNoFiles(t *testing.T) {
	if got := userInstructions("/some/dir", "", nil, "", ""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestLoadInstructionsBlankNameSkipped(t *testing.T) {
	tmp := t.TempDir()
	write(t, tmp, "real.md", "hello")
	got := userInstructions(tmp, "", []string{"", "  ", "real.md"}, "", "")
	if want := "hello"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// TestLoadInstructionsCODDYHomeEntry covers a ${CODDY_HOME} entry an operator
// wrote by hand. The home AGENTS.md itself needs no such entry: it is a
// preamble document (rules.LoadStanding), read whenever it exists.
func TestLoadInstructionsCODDYHomeEntry(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	write(t, home, "AGENTS.md", "USER_RULES")
	got := userInstructions(cwd, home, []string{"${CODDY_HOME}/AGENTS.md"}, "", "")
	if got != "USER_RULES" {
		t.Fatalf("content = %q, want the CODDY_HOME file", got)
	}
}

// TestLoadInstructionsAbsoluteEntry covers the shape the issue asked for:
// instructions.files entries pointing at a shared rules folder outside any
// workspace. filepath.Join used to turn such an entry into <cwd>/<abs>.
func TestLoadInstructionsAbsoluteEntry(t *testing.T) {
	shared := t.TempDir()
	cwd := t.TempDir()
	abs := write(t, shared, "codebase.md", "SHARED_RULES")
	got := userInstructions(cwd, "", []string{abs}, "", "")
	if !strings.Contains(got, "SHARED_RULES") {
		t.Fatalf("content = %q, want the file at the absolute path", got)
	}
}

func TestLoadInstructionsCWDPlaceholder(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "docs/house.md", "HOUSE_STYLE")
	got := userInstructions(cwd, "", []string{"${CWD}/docs/house.md"}, "", "")
	if !strings.Contains(got, "HOUSE_STYLE") {
		t.Fatalf("content = %q, want the ${CWD} entry", got)
	}
}

func TestLoadInstructionsTildeEntry(t *testing.T) {
	userHome := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", userHome)
	default:
		t.Setenv("HOME", userHome)
	}
	write(t, userHome, "rules/global.md", "TILDE_RULES")
	got := userInstructions(t.TempDir(), "", []string{"~/rules/global.md"}, "", "")
	if !strings.Contains(got, "TILDE_RULES") {
		t.Fatalf("content = %q, want the ~ entry", got)
	}
}

// TestLoadInstructionsSkipAlreadyLoaded is the dedupe: the AGENTS.md layers
// of the agent home and of the session folder are already in the prompt, so
// an instructions.files entry naming either one adds nothing.
func TestLoadInstructionsSkipAlreadyLoaded(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	write(t, home, "AGENTS.md", "USER_RULES")
	write(t, cwd, "AGENTS.md", "PROJECT_RULES")
	got := userInstructions(cwd, home, []string{"${CODDY_HOME}/AGENTS.md", "AGENTS.md"}, home, cwd)
	if got != "" {
		t.Fatalf("content = %q, want both entries skipped: the layers already carry them", got)
	}
}

func TestLoadInstructionsSameFileTwiceLoadedOnce(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "AGENTS.md", "ONCE")
	got := userInstructions(cwd, "", []string{"AGENTS.md", "./AGENTS.md"}, "", "")
	if n := strings.Count(got, "ONCE"); n != 1 {
		t.Fatalf("content = %q, carries the body %d times, want 1", got, n)
	}
}

func TestResolveInstructionFile(t *testing.T) {
	// Real temporary directories, not synthetic "/agent/home": on Windows a
	// rooted path without a volume is not absolute, so a hand-written fixture
	// would resolve differently there than the assertions expect.
	home := t.TempDir()
	cwd := t.TempDir()
	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{"relative anchors at the workspace", "AGENTS.md", filepath.Join(cwd, "AGENTS.md")},
		{"nested relative", "docs/rules.md", filepath.Join(cwd, "docs", "rules.md")},
		{"coddy home placeholder", "${CODDY_HOME}/AGENTS.md", filepath.Join(home, "AGENTS.md")},
		{"cwd placeholder", "${CWD}/AGENTS.md", filepath.Join(cwd, "AGENTS.md")},
		{"absolute stays itself", filepath.Join(home, "shared.md"), filepath.Join(home, "shared.md")},
		{"blank resolves to nothing", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := session.ResolveInstructionFile(tc.entry, cwd, home); got != tc.want {
				t.Fatalf("ResolveInstructionFile(%q) = %q, want %q", tc.entry, got, tc.want)
			}
		})
	}
}

// TestResolveInstructionFileWithoutHome keeps an unresolvable ${CODDY_HOME}
// entry out of the filesystem instead of reading a literal "${CODDY_HOME}"
// directory next to the workspace.
func TestResolveInstructionFileWithoutHome(t *testing.T) {
	if got := session.ResolveInstructionFile("${CODDY_HOME}/AGENTS.md", t.TempDir(), ""); got != "" {
		t.Fatalf("ResolveInstructionFile without a home = %q, want empty", got)
	}
}

// TestLoadInstructionsSymlinkedFileLoadedOnce covers the layout this repository
// itself uses: CLAUDE.md is a symlink to AGENTS.md, so naming both in
// instructions.files must not put the same text in the prompt twice.
func TestLoadInstructionsSymlinkedFileLoadedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink on Windows needs a privilege the runner may not have")
	}
	cwd := t.TempDir()
	write(t, cwd, "AGENTS.md", "SAME_BYTES")
	if err := os.Symlink(filepath.Join(cwd, "AGENTS.md"), filepath.Join(cwd, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	got := userInstructions(cwd, "", []string{"AGENTS.md", "CLAUDE.md"}, "", "")
	if n := strings.Count(got, "SAME_BYTES"); n != 1 {
		t.Fatalf("content = %q, carries the body %d times, want 1", got, n)
	}

	// The same holds against the layers: the session folder's AGENTS.md is
	// in the prompt, so an entry pointing at it through the link is skipped.
	if got := userInstructions(cwd, "", []string{"CLAUDE.md"}, "", cwd); got != "" {
		t.Fatalf("content = %q, want the symlinked project doc skipped", got)
	}
}

// TestRulesPromptLastsForOneGeneration: the standing part of the system prompt
// is kept for as long as the catalog it was rendered from, whatever template a
// turn runs on, and a new catalog - a compaction, a config reload, a workspace
// switch - drops it.
func TestRulesPromptLastsForOneGeneration(t *testing.T) {
	st := &session.State{ID: "t", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(nil)

	cached, gen := st.CachedRulesPrompt("inputs")
	if cached != nil {
		t.Fatalf("a fresh generation has a cached prompt: %+v", cached)
	}
	st.StoreRulesPrompt(&session.RulesPrompt{Generation: gen, Inputs: "inputs", Docs: "DOCS_V1", Rules: "RULES_V1", User: "USER_V1"})
	if cached, _ := st.CachedRulesPrompt("inputs"); cached == nil || cached.Docs != "DOCS_V1" || cached.Rules != "RULES_V1" || cached.User != "USER_V1" {
		t.Fatalf("the stored prompt is not handed back: %+v", cached)
	}
	// A configuration that names other files is not answered from the old
	// rendering, even within one generation.
	if cached, _ := st.CachedRulesPrompt("other inputs"); cached != nil {
		t.Fatalf("a rendering of other inputs was handed back: %+v", cached)
	}

	st.ReplaceRulesCatalog(nil)
	if cached, next := st.CachedRulesPrompt("inputs"); cached != nil || next == gen {
		t.Fatalf("a new catalog kept the old prompt (generation %d -> %d): %+v", gen, next, cached)
	}
	// A rendering of the generation that just ended is dropped rather than
	// taking the place of the new one.
	st.StoreRulesPrompt(&session.RulesPrompt{Generation: gen, Inputs: "inputs", Rules: "STALE"})
	if cached, _ := st.CachedRulesPrompt("inputs"); cached != nil {
		t.Fatalf("a stale rendering was stored: %+v", cached)
	}
}

// An entry that names the same file in every workspace - an absolute path, ~,
// ${CODDY_HOME} - is how several agents with configurations of their own share
// one set of instructions. When that file cannot be read the session has
// nothing to show for it, so the entry is reported with the reason; an entry
// that resolves inside the workspace is left alone while its file is merely
// absent, since one list serves workspaces that do not all carry the file.
func TestUnreadInstructionFiles(t *testing.T) {
	cwd, home, shared := t.TempDir(), t.TempDir(), t.TempDir()
	readable := write(t, shared, "house-style.md", "SHARED")
	write(t, shared, "blank.md", "  \n\n")
	if err := os.MkdirAll(filepath.Join(shared, "folder.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, home, "team.md", "TEAM")

	entries := []string{
		readable,                            // 0: read
		filepath.Join(shared, "missing.md"), // 1: absolute, absent
		filepath.Join(shared, "blank.md"),   // 2: absolute, empty
		filepath.Join(shared, "folder.md"),  // 3: absolute, a folder
		"${CODDY_HOME}/team.md",             // 4: read
		"${CODDY_HOME}/gone.md",             // 5: home, absent
		"docs/STYLE.md",                     // 6: workspace, absent here
		"${CWD}/docs/RULES.md",              // 7: workspace, absent here
		"",                                  // 8: blank, nothing to say
	}
	got := session.UnreadInstructionFiles(entries, cwd, home)
	want := map[int]string{
		1: "does not exist",
		2: "is empty",
		3: "is a folder, not a file",
		5: "does not exist",
	}
	if len(got) != len(want) {
		t.Fatalf("unread = %+v, want entries %v", got, want)
	}
	for _, u := range got {
		reason, ok := want[u.Index]
		if !ok {
			t.Fatalf("entry %d (%q) reported as unread: %v", u.Index, u.Entry, u.Err)
		}
		if u.Entry != entries[u.Index] {
			t.Fatalf("entry %d is reported as %q, want %q", u.Index, u.Entry, entries[u.Index])
		}
		if r := u.Reason(); r != reason {
			t.Fatalf("entry %d: reason %q, want %q", u.Index, r, reason)
		}
		if !filepath.IsAbs(u.Path) {
			t.Fatalf("entry %d: path %q is not absolute", u.Index, u.Path)
		}
	}
	if got[3].Path != filepath.Join(home, "gone.md") {
		t.Fatalf("the ${CODDY_HOME} entry resolves to %q", got[3].Path)
	}
}

// A file in the workspace that exists and cannot be read is a problem of the
// configuration whichever way the entry names it.
func TestUnreadInstructionFilesWorkspaceFileWithoutPermission(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny a read here")
	}
	cwd := t.TempDir()
	locked := write(t, cwd, "LOCKED.md", "SECRET")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	got := session.UnreadInstructionFiles([]string{"LOCKED.md"}, cwd, "")
	if len(got) != 1 || got[0].Reason() != "cannot be read: permission denied" {
		t.Fatalf("unread = %+v, want the locked file with permission denied", got)
	}
}

// CheckInstructionFile is what --dry-run asks of every entry: where it points,
// whether that place belongs to the workspace, and why it would not be read.
func TestCheckInstructionFile(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	write(t, cwd, "AGENTS.md", "PROJECT")
	if path, workspace, err := session.CheckInstructionFile("AGENTS.md", cwd, home); err != nil || !workspace || path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("relative entry: path %q workspace %v err %v", path, workspace, err)
	}
	if _, workspace, err := session.CheckInstructionFile("${CWD}/nope.md", cwd, home); !workspace || err == nil {
		t.Fatalf("${CWD} entry: workspace %v err %v, want a workspace file that is absent", workspace, err)
	}
	abs := filepath.Join(home, "missing.md")
	if path, workspace, err := session.CheckInstructionFile(abs, cwd, home); workspace || err == nil || path != abs {
		t.Fatalf("absolute entry: path %q workspace %v err %v", path, workspace, err)
	}
	if path, _, _ := session.CheckInstructionFile("   ", cwd, home); path != "" {
		t.Fatalf("a blank entry resolves to %q", path)
	}
}
