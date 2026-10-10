package agent

// Result eviction of listing tools (glob, print_tree, websearch, webfetch):
// whole steps are the unit of the working window, a write under a listing's root
// makes it stale, and the projection keeps the tool-call pairing intact. The
// read/grep rules these sit beside are in result_eviction_test.go.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func tcGlob(id, pattern, path string) llm.ToolCall {
	args := map[string]interface{}{"pattern": pattern}
	if path != "" {
		args["path"] = path
	}
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: "glob", InputJSON: string(b)}
}

func tcTree(id, path string, depth int) llm.ToolCall {
	args := map[string]interface{}{}
	if path != "" {
		args["path"] = path
	}
	if depth > 0 {
		args["depth"] = depth
	}
	b, _ := json.Marshal(args)
	return llm.ToolCall{ID: id, Name: "print_tree", InputJSON: string(b)}
}

func tcSearch(id, query string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"query": query, "max_results": 10})
	return llm.ToolCall{ID: id, Name: "websearch", InputJSON: string(b)}
}

func tcFetch(id, url string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"url": url, "max_chars": 20000})
	return llm.ToolCall{ID: id, Name: "webfetch", InputJSON: string(b)}
}

// asstStep is one assistant message carrying a whole batch of parallel calls.
func asstStep(calls ...llm.ToolCall) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}
}

func userPrompt() llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: "resume"}
}

// linesBody is a result of exactly n lines, the first one carrying marker.
func linesBody(marker string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i == 0 {
			fmt.Fprintf(&b, "%s entry %03d\n", marker, i)
			continue
		}
		fmt.Fprintf(&b, "listing entry %03d\n", i)
	}
	return b.String()
}

func listingTools() map[string]bool {
	m := map[string]bool{}
	for _, name := range config.ResultEvictionListingTools {
		m[name] = true
	}
	return m
}

func listingOpts(keepSteps int) resultEvictionOptions {
	return resultEvictionOptions{
		Enabled:         true,
		KeepRecent:      0,
		MinResultBytes:  10,
		CWD:             testCWD,
		ListingTools:    listingTools(),
		KeepRecentSteps: keepSteps,
	}
}

// globStep is one step with a single glob, returning its two messages.
func globStep(id, pattern, path string, lines int) []llm.Message {
	return []llm.Message{
		asstStep(tcGlob(id, pattern, path)),
		toolResult(id, linesBody("MARK-"+id, lines)),
	}
}

func historyOf(parts ...[]llm.Message) []llm.Message {
	out := []llm.Message{userPrompt()}
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// tailStep is a later listing step: under a window of one it takes the only
// kept place, so every listing before it goes.
func tailStep() []llm.Message {
	return globStep("tail", "*.tail", "tail", 40)
}

func TestPruneListingOutsideLastStepsEvictedInsideKept(t *testing.T) {
	in := historyOf(
		globStep("g1", "*.go", "pkg", 40),
		globStep("g2", "*.md", "docs", 40),
		globStep("g3", "*.yaml", "cfg", 40),
	)
	out := pruneToolResults(in, listingOpts(2))

	if want := `[evicted: glob "*.go" in pkg (40 lines); re-run if needed]`; contentByID(out, "g1") != want {
		t.Fatalf("g1 = %q, want %q", contentByID(out, "g1"), want)
	}
	for _, id := range []string{"g2", "g3"} {
		if !strings.Contains(contentByID(out, id), "MARK-"+id) {
			t.Fatalf("%s is inside the last 2 steps and must stay verbatim: %q", id, contentByID(out, id))
		}
	}
}

// Ten parallel calls are one step to the model: the window counts steps, not
// results.
func TestPruneListingFanOutCountsAsOneStep(t *testing.T) {
	fan := []llm.Message{}
	var calls []llm.ToolCall
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("f%d", i)
		if i%2 == 0 {
			calls = append(calls, tcGlob(id, "*.go", fmt.Sprintf("pkg%d", i)))
		} else {
			calls = append(calls, tcTree(id, fmt.Sprintf("pkg%d", i), 2))
		}
	}
	fan = append(fan, asstStep(calls...))
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("f%d", i)
		fan = append(fan, toolResult(id, linesBody("MARK-"+id, 30)))
	}
	later := globStep("late", "*.go", "last", 30)

	t.Run("one kept step is the latest one and the whole fan-out goes", func(t *testing.T) {
		out := pruneToolResults(historyOf(fan, later), listingOpts(1))
		for i := 0; i < 10; i++ {
			if id := fmt.Sprintf("f%d", i); !evicted(contentByID(out, id)) {
				t.Fatalf("%s should be evicted: %q", id, contentByID(out, id))
			}
		}
		if !strings.Contains(contentByID(out, "late"), "MARK-late") {
			t.Fatalf("the latest step must stay verbatim: %q", contentByID(out, "late"))
		}
	})

	t.Run("two kept steps keep the whole fan-out", func(t *testing.T) {
		out := pruneToolResults(historyOf(fan, later), listingOpts(2))
		for i := 0; i < 10; i++ {
			if id := fmt.Sprintf("f%d", i); !strings.Contains(contentByID(out, id), "MARK-"+id) {
				t.Fatalf("%s lies in the second step from the end and must stay: %q", id, contentByID(out, id))
			}
		}
	})

	t.Run("a fan-out that is the latest step stays whole under a window of one", func(t *testing.T) {
		out := pruneToolResults(historyOf(globStep("old", "*.go", "old", 30), fan), listingOpts(1))
		if !evicted(contentByID(out, "old")) {
			t.Fatalf("the older step should be evicted: %q", contentByID(out, "old"))
		}
		for i := 0; i < 10; i++ {
			if id := fmt.Sprintf("f%d", i); !strings.Contains(contentByID(out, id), "MARK-"+id) {
				t.Fatalf("%s is part of the last step and must stay: %q", id, contentByID(out, id))
			}
		}
	})
}

// Options built in code can carry a window below one. It behaves as one: the
// latest step that holds a listing keeps its results, so a listing the model has
// just asked for reaches it instead of being collapsed in the very next request
// and asked for again.
func TestPruneListingKeepRecentStepsBelowOneBehavesAsOne(t *testing.T) {
	in := historyOf(globStep("g1", "*.go", "pkg", 40), globStep("g2", "*.md", "docs", 40))
	want := pruneToolResults(in, listingOpts(1))
	for _, steps := range []int{0, -1} {
		out := pruneToolResults(in, listingOpts(steps))
		if !reflect.DeepEqual(out, want) {
			t.Fatalf("keep_recent_steps %d must behave as 1: g1=%q g2=%q", steps, contentByID(out, "g1"), contentByID(out, "g2"))
		}
		if !evicted(contentByID(out, "g1")) {
			t.Fatalf("keep_recent_steps %d: the older step must be evicted: %q", steps, contentByID(out, "g1"))
		}
		if !strings.Contains(contentByID(out, "g2"), "MARK-g2") {
			t.Fatalf("keep_recent_steps %d: the latest step must stay whole: %q", steps, contentByID(out, "g2"))
		}
	}

	// The case that motivated the floor: one listing, nothing after it.
	only := historyOf(globStep("only", "*.go", "pkg", 40))
	for _, steps := range []int{0, -1} {
		out := pruneToolResults(only, listingOpts(steps))
		if !strings.Contains(contentByID(out, "only"), "MARK-only") {
			t.Fatalf("keep_recent_steps %d collapsed the listing that was just asked for: %q", steps, contentByID(out, "only"))
		}
	}
}

func TestPruneListingNoToolsConfiguredLeavesListingsAlone(t *testing.T) {
	in := historyOf(
		globStep("g1", "*.go", "pkg", 40),
		[]llm.Message{asstStep(tcTree("t1", "pkg", 2)), toolResult("t1", linesBody("TREE", 40))},
		[]llm.Message{asstStep(tcSearch("s1", "coddy")), toolResult("s1", linesBody("SEARCH", 40))},
		[]llm.Message{asstStep(tcFetch("w1", "https://example.com")), toolResult("w1", linesBody("FETCH", 40))},
	)
	opts := listingOpts(1)
	opts.ListingTools = nil
	out := pruneToolResults(in, opts)
	if len(out) != len(in) || &out[0] != &in[0] {
		t.Fatal("with no listing tools and no read/grep the history must come back as the same slice")
	}
	for id, marker := range map[string]string{"g1": "MARK-g1", "t1": "TREE", "s1": "SEARCH", "w1": "FETCH"} {
		if !strings.Contains(contentByID(out, id), marker) {
			t.Fatalf("%s was evicted although its tool is not configured: %q", id, contentByID(out, id))
		}
	}

	// A tool left off the list is left alone while the others still go.
	opts.ListingTools = map[string]bool{"glob": true}
	out = pruneToolResults(append(in, tailStep()...), opts)
	if !evicted(contentByID(out, "g1")) {
		t.Fatalf("glob is configured and must be evicted: %q", contentByID(out, "g1"))
	}
	for id, marker := range map[string]string{"t1": "TREE", "s1": "SEARCH", "w1": "FETCH"} {
		if !strings.Contains(contentByID(out, id), marker) {
			t.Fatalf("%s was evicted although its tool is not configured: %q", id, contentByID(out, id))
		}
	}
}

// A result at or below min_result_bytes is no candidate: it is never evicted and
// its step does not take a place in the window.
func TestPruneListingSmallResultIsNoCandidateAndTakesNoWindowPlace(t *testing.T) {
	opts := listingOpts(1)
	opts.MinResultBytes = 100
	exact := strings.Repeat("x", 100)
	in := historyOf(
		globStep("g1", "*.go", "a", 40),
		globStep("g2", "*.go", "b", 40),
		[]llm.Message{asstStep(tcGlob("tiny", "*.go", "c")), toolResult("tiny", exact)},
		[]llm.Message{asstStep(tcGlob("tiny2", "*.go", "d")), toolResult("tiny2", "none")},
	)
	out := pruneToolResults(in, opts)
	if contentByID(out, "tiny") != exact || contentByID(out, "tiny2") != "none" {
		t.Fatalf("results at or below the limit must be untouched: %q / %q", contentByID(out, "tiny"), contentByID(out, "tiny2"))
	}
	if !evicted(contentByID(out, "g1")) {
		t.Fatalf("g1 is outside the one kept step: %q", contentByID(out, "g1"))
	}
	if !strings.Contains(contentByID(out, "g2"), "MARK-g2") {
		t.Fatalf("g2 is the last step that holds a candidate and must stay: %q", contentByID(out, "g2"))
	}
}

func TestPruneListingStaleAfterWriteUnderItsRoot(t *testing.T) {
	cases := []struct {
		name  string
		step  llm.Message
		want  string
		write llm.Message
	}{
		{
			name:  "glob, a new file below its root",
			step:  asstStep(tcGlob("l", "*.go", "pkg")),
			write: asstWrite("w", "write", filepath.Join("pkg", "sub", "new.go")),
			want:  `[evicted: glob "*.go" in pkg is stale after pkg/sub/new.go was modified; re-run if needed]`,
		},
		{
			name:  "print_tree, a new file below its root",
			step:  asstStep(tcTree("l", "pkg", 3)),
			write: asstWrite("w", "touch", filepath.Join("pkg", "new.go")),
			want:  `[evicted: print_tree of pkg is stale after pkg/new.go was modified; re-run if needed]`,
		},
		{
			name:  "glob over the workspace, any write inside it",
			step:  asstStep(tcGlob("l", "**/*.go", "")),
			write: asstWrite("w", "edit", filepath.Join("deep", "er", "file.go")),
			want:  `[evicted: glob "**/*.go" in . is stale after deep/er/file.go was modified; re-run if needed]`,
		},
		{
			name:  "print_tree, the root itself moved",
			step:  asstStep(tcTree("l", "pkg", 0)),
			write: asstMove("w", "pkg", "renamed"),
			want:  `[evicted: print_tree of pkg is stale after pkg was modified; re-run if needed]`,
		},
		{
			name:  "glob, a parent of its root removed",
			step:  asstStep(tcGlob("l", "*.go", filepath.Join("pkg", "sub"))),
			write: asstWrite("w", "rmdir", "pkg"),
			want:  `[evicted: glob "*.go" in pkg/sub is stale after pkg was modified; re-run if needed]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := historyOf(
				[]llm.Message{tc.step, toolResult("l", linesBody("LIST", 40))},
				[]llm.Message{tc.write, toolResult("w", "written")},
			)
			// Three kept steps: the listing is well inside the window, and the
			// write still evicts it.
			out := pruneToolResults(in, listingOpts(3))
			if got := contentByID(out, "l"); got != tc.want {
				t.Fatalf("stale listing = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPruneListingNotStaleAfterWriteOutsideItsRoot(t *testing.T) {
	in := historyOf(
		[]llm.Message{asstStep(tcGlob("g", "*.go", "pkg")), toolResult("g", linesBody("GLOB", 40))},
		[]llm.Message{asstStep(tcTree("t", "pkg", 2)), toolResult("t", linesBody("TREE", 40))},
		[]llm.Message{asstWrite("w", "write", filepath.Join("other", "unrelated.go")), toolResult("w", "written")},
		// A sibling whose name merely starts like the root is not below it.
		[]llm.Message{asstWrite("w2", "write", filepath.Join("pkgextra", "x.go")), toolResult("w2", "written")},
	)
	out := pruneToolResults(in, listingOpts(3))
	for id, marker := range map[string]string{"g": "GLOB", "t": "TREE"} {
		if !strings.Contains(contentByID(out, id), marker) {
			t.Fatalf("%s must not be invalidated by a write outside its root: %q", id, contentByID(out, id))
		}
	}
}

// asstPatch is an apply_patch call: one file and the patch applied to it.
func asstPatch(id, path, patch string) llm.Message {
	b, _ := json.Marshal(map[string]interface{}{"path": path, "patch": patch})
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "apply_patch", InputJSON: string(b)}}}
}

const (
	v4aUpdatePatch = "*** Begin Patch\n*** Update File: pkg/a.go\n@@\n-old line\n+new line\n*** End Patch"
	v4aBarePatch   = "@@ func main\n-old line\n+new line"
	unifiedUpdate  = "--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1,2 +1,2 @@\n context\n-old line\n+new line\n"
	v4aAddPatch    = "*** Begin Patch\n*** Add File: pkg/new.go\n+package pkg\n*** End Patch"
	v4aDeletePatch = "*** Begin Patch\n*** Delete File: pkg/old.go\n*** End Patch"
	v4aMovePatch   = "*** Begin Patch\n*** Update File: pkg/a.go\n*** Move to: pkg/b.go\n@@\n-old line\n+new line\n*** End Patch"
	unifiedNewFile = "--- /dev/null\n+++ b/pkg/new.go\n@@ -0,0 +1 @@\n+package pkg\n"
	unifiedRemoval = "--- a/pkg/old.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package pkg\n"
)

// A print_tree result is names only, sorted by name, so a write that leaves every
// name where it was cannot make it wrong: an edit, or an apply_patch that updates
// a file. A write that can create, remove or move an entry still does. A glob
// result is sorted by modification time, so any write under its root changes it.
func TestPruneListingPrintTreeStalenessByKindOfWrite(t *testing.T) {
	inPkg := filepath.Join("pkg", "a.go")
	rawPatch := func(input string) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "w", Name: "apply_patch", InputJSON: input}}}
	}
	cases := []struct {
		name      string
		write     llm.Message
		treeStale bool
	}{
		{"edit of a file below the root", asstWrite("w", "edit", inPkg), false},
		{"edit of a file at the root's top", asstWrite("w", "edit", filepath.Join("pkg", "top.go")), false},
		{"apply_patch updating a file (Codex format)", asstPatch("w", inPkg, v4aUpdatePatch), false},
		{"apply_patch updating a file (hunk without headers)", asstPatch("w", inPkg, v4aBarePatch), false},
		{"apply_patch updating a file (unified diff)", asstPatch("w", inPkg, unifiedUpdate), false},
		{"apply_patch with the legacy diff key", rawPatch(`{"path":"pkg/a.go","diff":"@@\n-old line\n+new line"}`), false},
		{"write", asstWrite("w", "write", inPkg), true},
		{"mkdir", asstWrite("w", "mkdir", filepath.Join("pkg", "sub")), true},
		{"rmdir", asstWrite("w", "rmdir", filepath.Join("pkg", "sub")), true},
		{"touch", asstWrite("w", "touch", filepath.Join("pkg", "new.go")), true},
		{"rm", asstWrite("w", "rm", inPkg), true},
		{"mv out of the root", asstMove("w", inPkg, "elsewhere.go"), true},
		{"mv into the root", asstMove("w", "elsewhere.go", filepath.Join("pkg", "in.go")), true},
		{"apply_patch adding a file", asstPatch("w", filepath.Join("pkg", "new.go"), v4aAddPatch), true},
		{"apply_patch deleting a file", asstPatch("w", filepath.Join("pkg", "old.go"), v4aDeletePatch), true},
		{"apply_patch moving a file", asstPatch("w", inPkg, v4aMovePatch), true},
		{"apply_patch with a unified diff from /dev/null", asstPatch("w", filepath.Join("pkg", "new.go"), unifiedNewFile), true},
		{"apply_patch with a unified diff to /dev/null", asstPatch("w", filepath.Join("pkg", "old.go"), unifiedRemoval), true},
		{"apply_patch whose patch is missing", rawPatch(`{"path":"pkg/a.go"}`), true},
		{"apply_patch whose patch cannot be read", rawPatch(`{"path":"pkg/a.go","patch":123}`), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := historyOf(
				[]llm.Message{asstStep(tcTree("t", "pkg", 3)), toolResult("t", linesBody("TREE", 40))},
				[]llm.Message{asstStep(tcGlob("g", "*.go", "pkg")), toolResult("g", linesBody("GLOB", 40))},
				[]llm.Message{tc.write, toolResult("w", "written")},
			)
			// Both listings are inside the window: only the write can evict them.
			out := pruneToolResults(in, listingOpts(3))
			if got := evicted(contentByID(out, "t")); got != tc.treeStale {
				t.Fatalf("print_tree stale = %v, want %v: %q", got, tc.treeStale, contentByID(out, "t"))
			}
			if !strings.Contains(contentByID(out, "g"), "is stale after") {
				t.Fatalf("a glob result is sorted by modification time and any write under its root makes it stale: %q", contentByID(out, "g"))
			}
		})
	}
}

// The fields a stale listing is judged by are recorded on the write itself.
func TestWriteStructuralKinds(t *testing.T) {
	cases := []struct {
		tool, input string
		structural  bool
	}{
		{"write", `{"path":"a.go"}`, true},
		{"edit", `{"path":"a.go"}`, false},
		{"mkdir", `{"path":"d"}`, true},
		{"rmdir", `{"path":"d"}`, true},
		{"touch", `{"path":"a.go"}`, true},
		{"rm", `{"path":"a.go"}`, true},
		{"mv", `{"src":"a.go","dst":"b.go"}`, true},
		{"apply_patch", `{"path":"a.go","patch":"@@\n-a\n+b"}`, false},
		{"apply_patch", `{"path":"a.go","patch":"*** Begin Patch\r\n*** Add File: b.go\r\n+x\r\n*** End Patch"}`, true},
		{"apply_patch", `{"path":"a.go","patch":"  *** Delete File: b.go"}`, true},
		{"apply_patch", `{"path":"a.go","patch":"--- a/a.go\t2026-01-01\n+++ /dev/null\t2026-01-01\n@@ -1 +0,0 @@\n-a"}`, true},
		{"apply_patch", `{"path":"a.go","patch":"   ","diff":""}`, true},
		{"apply_patch", `{"path":"a.go","patch":["x"]}`, true},
		{"apply_patch", `{"path":"a.go"}`, true},
	}
	for _, tc := range cases {
		if got := writeStructural(tc.tool, tc.input); got != tc.structural {
			t.Errorf("writeStructural(%s, %s) = %v, want %v", tc.tool, tc.input, got, tc.structural)
		}
	}
}

// A write that came before the listing cannot have made it stale.
func TestPruneListingNotStaleAfterEarlierWrite(t *testing.T) {
	in := historyOf(
		[]llm.Message{asstWrite("w", "write", filepath.Join("pkg", "new.go")), toolResult("w", "written")},
		[]llm.Message{asstStep(tcGlob("g", "*.go", "pkg")), toolResult("g", linesBody("GLOB", 40))},
	)
	out := pruneToolResults(in, listingOpts(1))
	if !strings.Contains(contentByID(out, "g"), "GLOB") {
		t.Fatalf("a listing taken after the write is current: %q", contentByID(out, "g"))
	}
}

func TestPruneListingFailedWriteDoesNotMakeItStale(t *testing.T) {
	for _, result := range []string{
		"error: write: permission denied",
		"permission denied by user",
		toolLoopNudge,
		toolLoopSkippedResult,
	} {
		in := historyOf(
			[]llm.Message{asstStep(tcGlob("g", "*.go", "pkg")), toolResult("g", linesBody("GLOB", 40))},
			[]llm.Message{asstWrite("w", "write", filepath.Join("pkg", "new.go")), toolResult("w", result)},
		)
		out := pruneToolResults(in, listingOpts(2))
		if evicted(contentByID(out, "g")) {
			t.Fatalf("an unsuccessful write result %q made the listing stale", result)
		}
	}
}

// A web result describes the web, not the workspace: no write can stale it. Out
// of the window it goes like any other listing.
func TestPruneListingWebToolsNeverStale(t *testing.T) {
	in := historyOf(
		[]llm.Message{asstStep(tcSearch("s", "coddy agent")), toolResult("s", linesBody("SEARCH", 40))},
		[]llm.Message{asstStep(tcFetch("f", "https://example.com/doc")), toolResult("f", linesBody("FETCH", 40))},
		[]llm.Message{asstWrite("w", "write", "anything.go"), toolResult("w", "written")},
	)
	out := pruneToolResults(in, listingOpts(2))
	for id, marker := range map[string]string{"s": "SEARCH", "f": "FETCH"} {
		if !strings.Contains(contentByID(out, id), marker) {
			t.Fatalf("%s is inside the window and a write cannot stale a web result: %q", id, contentByID(out, id))
		}
	}

	out = pruneToolResults(append(in, tailStep()...), listingOpts(1))
	if got, want := contentByID(out, "s"), `[evicted: websearch "coddy agent" (40 lines); re-run if needed]`; got != want {
		t.Fatalf("websearch placeholder = %q, want %q", got, want)
	}
	if got, want := contentByID(out, "f"), `[evicted: webfetch https://example.com/doc (40 lines); re-fetch if needed]`; got != want {
		t.Fatalf("webfetch placeholder = %q, want %q", got, want)
	}
}

func TestPruneListingPlaceholderTexts(t *testing.T) {
	in := historyOf(
		[]llm.Message{asstStep(tcGlob("g", "**/*.go", filepath.Join("internal", "agent"))), toolResult("g", linesBody("G", 312))},
		[]llm.Message{asstStep(tcTree("t", "", 3)), toolResult("t", linesBody("T", 123))},
		[]llm.Message{asstStep(tcTree("t2", filepath.Join("docs", "features"), 0)), toolResult("t2", linesBody("T2", 7))},
		[]llm.Message{asstStep(tcSearch("s", "go \"quoted\" query")), toolResult("s", linesBody("S", 21))},
		[]llm.Message{asstStep(tcFetch("f", "https://example.com/a?b=c")), toolResult("f", linesBody("F", 1+99))},
		tailStep(),
	)
	out := pruneToolResults(in, listingOpts(1))
	for id, want := range map[string]string{
		"g":  `[evicted: glob "**/*.go" in internal/agent (312 lines); re-run if needed]`,
		"t":  `[evicted: print_tree of . (123 lines); re-run if needed]`,
		"t2": `[evicted: print_tree of docs/features (7 lines); re-run if needed]`,
		"s":  `[evicted: websearch "go \"quoted\" query" (21 lines); re-run if needed]`,
		"f":  `[evicted: webfetch https://example.com/a?b=c (100 lines); re-fetch if needed]`,
	} {
		if got := contentByID(out, id); got != want {
			t.Errorf("%s placeholder = %q, want %q", id, got, want)
		}
	}
}

// A call can come without the pattern, the query or the URL a placeholder
// repeats (the arguments did not parse, the field is empty or only spaces). The
// placeholder then leaves the slot out instead of printing empty quotes or a
// double space.
func TestPruneListingPlaceholderWithoutALabel(t *testing.T) {
	raw := func(id, tool, input string) []llm.Message {
		return []llm.Message{
			asstStep(llm.ToolCall{ID: id, Name: tool, InputJSON: input}),
			toolResult(id, linesBody("BODY-"+id, 40)),
		}
	}
	calls := []struct {
		id, tool, input string
		evicted, stale  string
	}{
		{"fetch-bad", "webfetch", `not json`,
			`[evicted: webfetch result (40 lines); re-fetch if needed]`, ""},
		{"fetch-empty", "webfetch", `{"url":"   "}`,
			`[evicted: webfetch result (40 lines); re-fetch if needed]`, ""},
		{"search-none", "websearch", `{"max_results":5}`,
			`[evicted: websearch result (40 lines); re-run if needed]`, ""},
		{"search-blank", "websearch", `{"query":"  "}`,
			`[evicted: websearch result (40 lines); re-run if needed]`, ""},
		{"glob-none", "glob", `{"path":"pkg"}`,
			`[evicted: glob in pkg (40 lines); re-run if needed]`,
			`[evicted: glob in pkg is stale after pkg/new.go was modified; re-run if needed]`},
		{"glob-blank", "glob", `{"pattern":"   ","path":"pkg"}`,
			`[evicted: glob in pkg (40 lines); re-run if needed]`,
			`[evicted: glob in pkg is stale after pkg/new.go was modified; re-run if needed]`},
		{"glob-bad", "glob", `not json`,
			`[evicted: glob in . (40 lines); re-run if needed]`,
			`[evicted: glob in . is stale after pkg/new.go was modified; re-run if needed]`},
	}

	var evictedParts [][]llm.Message
	for _, c := range calls {
		evictedParts = append(evictedParts, raw(c.id, c.tool, c.input))
	}
	evictedParts = append(evictedParts, tailStep())
	out := pruneToolResults(historyOf(evictedParts...), listingOpts(1))
	for _, c := range calls {
		if got := contentByID(out, c.id); got != c.evicted {
			t.Errorf("%s placeholder = %q, want %q", c.id, got, c.evicted)
		}
	}

	// The stale placeholder names the glob the same way. Only the calls with a
	// folder can go stale.
	for _, c := range calls {
		if c.stale == "" {
			continue
		}
		in := historyOf(raw(c.id, c.tool, c.input), []llm.Message{
			asstWrite("w", "write", filepath.Join("pkg", "new.go")), toolResult("w", "written"),
		})
		if got := contentByID(pruneToolResults(in, listingOpts(3)), c.id); got != c.stale {
			t.Errorf("%s stale placeholder = %q, want %q", c.id, got, c.stale)
		}
	}
}

// A pattern, a query or a URL can be as long as the model likes; the placeholder
// that replaces a result must not become a result of its own.
func TestPruneListingPlaceholderCapsLongArguments(t *testing.T) {
	long := strings.Repeat("a", 500)
	in := historyOf(
		[]llm.Message{asstStep(tcGlob("g", long, "pkg")), toolResult("g", linesBody("G", 40))},
		[]llm.Message{asstStep(tcSearch("s", long)), toolResult("s", linesBody("S", 40))},
		[]llm.Message{asstStep(tcFetch("f", "https://example.com/"+long)), toolResult("f", linesBody("F", 40))},
		tailStep(),
	)
	out := pruneToolResults(in, listingOpts(1))
	for _, id := range []string{"g", "s", "f"} {
		got := contentByID(out, id)
		if !evicted(got) || len(got) > 200 {
			t.Fatalf("%s placeholder was not capped (%d bytes): %q", id, len(got), got)
		}
		if strings.Contains(got, long) {
			t.Fatalf("%s placeholder carries the whole argument: %q", id, got)
		}
	}
	// Capping is deterministic: the same history gives the same bytes.
	again := pruneToolResults(in, listingOpts(1))
	if !reflect.DeepEqual(out, again) {
		t.Fatal("eviction of the same history is not deterministic")
	}
}

// keep:true and keep_result belong to the read/grep pairs they were written for.
func TestPruneListingHasNoPins(t *testing.T) {
	args, _ := json.Marshal(map[string]interface{}{"pattern": "*.go", "path": "pkg", "keep": true})
	in := historyOf(
		[]llm.Message{asstStep(llm.ToolCall{ID: "g", Name: "glob", InputJSON: string(args)}), toolResult("g", linesBody("GLOB", 40))},
		[]llm.Message{asstKeepResult("k", map[string]interface{}{"pattern": "*.go", "path": "pkg"}), toolResult("k", "marked")},
		[]llm.Message{asstKeepResult("k2", map[string]interface{}{"path": "pkg"}), toolResult("k2", "marked")},
		tailStep(),
	)
	out := pruneToolResults(in, listingOpts(1))
	if !evicted(contentByID(out, "g")) {
		t.Fatalf("a listing cannot be pinned: %q", contentByID(out, "g"))
	}
}

// The read/grep window counts read and grep results only; a listing neither
// takes a place in it nor is judged by it.
func TestPruneListingDoesNotChangeTheReadGrepWindow(t *testing.T) {
	readGrep := func() []llm.Message {
		return historyOf(
			[]llm.Message{asstRead("r1", "a.go", 1, 500, false), toolResult("r1", bigBody("PAGE-1"))},
			[]llm.Message{asstGrep("g1", "needle", "", false), toolResult("g1", grepBody("needle", "a.go"))},
			[]llm.Message{asstRead("r2", "b.go", 1, 500, false), toolResult("r2", bigBody("PAGE-2"))},
		)
	}
	base := readGrep()

	mixed := historyOf(
		[]llm.Message{asstRead("r1", "a.go", 1, 500, false), toolResult("r1", bigBody("PAGE-1"))},
		globStep("l1", "*.go", "pkg", 40),
		[]llm.Message{asstGrep("g1", "needle", "", false), toolResult("g1", grepBody("needle", "a.go"))},
		globStep("l2", "*.md", "docs", 40),
		[]llm.Message{asstRead("r2", "b.go", 1, 500, false), toolResult("r2", bigBody("PAGE-2"))},
		globStep("l3", "*.yaml", "cfg", 40),
	)

	for _, keepRecent := range []int{0, 1, 2, 3} {
		opts := listingOpts(5) // every listing step is inside the window
		opts.KeepRecent = keepRecent
		got := pruneToolResults(mixed, opts)

		plain := listingOpts(5)
		plain.KeepRecent = keepRecent
		plain.ListingTools = nil
		want := pruneToolResults(base, plain)

		for _, id := range []string{"r1", "g1", "r2"} {
			if contentByID(got, id) != contentByID(want, id) {
				t.Fatalf("keep_recent=%d: %s changed once listings were mixed in:\n got: %q\nwant: %q",
					keepRecent, id, contentByID(got, id), contentByID(want, id))
			}
		}
		for _, id := range []string{"l1", "l2", "l3"} {
			if evicted(contentByID(got, id)) {
				t.Fatalf("keep_recent=%d: %s is inside the step window and must stay: %q", keepRecent, id, contentByID(got, id))
			}
		}
	}
}

// And the other way round: read and grep steps are no listing steps, so they do
// not push a listing out of the step window.
func TestPruneListingStepWindowIgnoresReadAndGrepSteps(t *testing.T) {
	in := historyOf(
		globStep("l1", "*.go", "pkg", 40),
		[]llm.Message{asstRead("r1", "a.go", 1, 500, false), toolResult("r1", bigBody("PAGE-1"))},
		[]llm.Message{asstGrep("g1", "needle", "", false), toolResult("g1", grepBody("needle", "a.go"))},
		[]llm.Message{asstRead("r2", "b.go", 1, 500, false), toolResult("r2", bigBody("PAGE-2"))},
	)
	opts := listingOpts(1)
	opts.KeepRecent = 10
	out := pruneToolResults(in, opts)
	if !strings.Contains(contentByID(out, "l1"), "MARK-l1") {
		t.Fatalf("the only listing step is the last one and must stay: %q", contentByID(out, "l1"))
	}
}

// A provider that reuses a tool-call ID on a later step must not relabel the
// earlier result: each result belongs to the call that was open when it arrived.
func TestPruneListingReusedCallIDsKeepTheirOwnStep(t *testing.T) {
	in := historyOf(
		globStep("same", "*.go", "first", 40),
		globStep("same", "*.md", "second", 40),
	)
	out := pruneToolResults(in, listingOpts(1))
	var contents []string
	for _, m := range out {
		if m.Role == llm.RoleTool {
			contents = append(contents, m.Content)
		}
	}
	if len(contents) != 2 || !evicted(contents[0]) || evicted(contents[1]) {
		t.Fatalf("want the first result evicted and the second kept, got %q", contents)
	}
	if !strings.Contains(contents[0], `"*.go" in first`) {
		t.Fatalf("the first result took the second call's label: %q", contents[0])
	}
}

func TestPruneListingToolResultsWithoutACallAreLeftAlone(t *testing.T) {
	in := historyOf(
		[]llm.Message{toolResult("orphan", linesBody("ORPHAN", 40))},
		globStep("g", "*.go", "pkg", 40),
	)
	out := pruneToolResults(in, listingOpts(1))
	if !strings.Contains(contentByID(out, "orphan"), "ORPHAN") {
		t.Fatalf("a result no call announced must not be touched: %q", contentByID(out, "orphan"))
	}
}

// fanOutHistory builds a seeded history of steps, each a parallel batch of
// listing, read, grep and write calls, with a result per call.
func fanOutHistory(rng *rand.Rand, steps int, withWrites bool) (msgs []llm.Message, listingStep map[string]int) {
	msgs = []llm.Message{userPrompt()}
	listingStep = map[string]int{}
	for s := 0; s < steps; s++ {
		width := 1 + rng.Intn(10)
		var calls []llm.ToolCall
		results := map[string]string{}
		for c := 0; c < width; c++ {
			id := fmt.Sprintf("s%dc%d", s, c)
			switch rng.Intn(7) {
			case 0:
				calls = append(calls, tcGlob(id, "*.go", fmt.Sprintf("d%d", rng.Intn(4))))
				results[id] = linesBody("GLOB", 20+rng.Intn(40))
				listingStep[id] = s
			case 1:
				calls = append(calls, tcTree(id, fmt.Sprintf("d%d", rng.Intn(4)), 2))
				results[id] = linesBody("TREE", 20+rng.Intn(40))
				listingStep[id] = s
			case 2:
				calls = append(calls, tcSearch(id, fmt.Sprintf("query %d", rng.Intn(5))))
				results[id] = linesBody("SEARCH", 20+rng.Intn(40))
				listingStep[id] = s
			case 3:
				calls = append(calls, tcFetch(id, fmt.Sprintf("https://example.com/%d", rng.Intn(5))))
				results[id] = linesBody("FETCH", 20+rng.Intn(40))
				listingStep[id] = s
			case 4:
				calls = append(calls, llm.ToolCall{ID: id, Name: "read", InputJSON: fmt.Sprintf(`{"path":"d%d/f.go"}`, rng.Intn(4))})
				results[id] = bigBody("READ")
			case 5:
				calls = append(calls, llm.ToolCall{ID: id, Name: "grep", InputJSON: `{"pattern":"needle"}`})
				results[id] = grepBody("needle", "d0/f.go")
			default:
				if withWrites {
					calls = append(calls, llm.ToolCall{ID: id, Name: "write", InputJSON: fmt.Sprintf(`{"path":"d%d/new.go"}`, rng.Intn(4))})
					results[id] = "written"
				} else {
					calls = append(calls, tcGlob(id, "*.md", "docs"))
					results[id] = linesBody("GLOB", 20+rng.Intn(40))
					listingStep[id] = s
				}
			}
		}
		msgs = append(msgs, asstStep(calls...))
		for _, tc := range calls {
			msgs = append(msgs, toolResult(tc.ID, results[tc.ID]))
		}
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	return msgs, listingStep
}

func cloneMessages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	for i, m := range in {
		out[i] = m
		out[i].ToolCalls = append([]llm.ToolCall(nil), m.ToolCalls...)
	}
	return out
}

// The projection only ever swaps the text of a tool result: roles, call ids and
// order are the history's, the pairing the provider checks still holds, and the
// caller's slice is never written to.
func TestPruneListingKeepsPairingOverGeneratedFanOutHistories(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		for _, withWrites := range []bool{false, true} {
			for _, keepSteps := range []int{0, 1, 2, 3, 6} {
				rng := rand.New(rand.NewSource(seed))
				in, _ := fanOutHistory(rng, 1+rng.Intn(14), withWrites)
				if issues := session.ValidateToolPairing(in); len(issues) != 0 {
					t.Fatalf("seed %d: generated history is not paired: %v", seed, issues)
				}
				before := cloneMessages(in)

				opts := listingOpts(keepSteps)
				opts.KeepRecent = int(seed % 3)
				out := pruneToolResults(in, opts)

				name := fmt.Sprintf("seed=%d writes=%v keep=%d", seed, withWrites, keepSteps)
				if issues := session.ValidateToolPairing(out); len(issues) != 0 {
					t.Fatalf("%s: projection broke the pairing: %v", name, issues)
				}
				if !reflect.DeepEqual(in, before) {
					t.Fatalf("%s: the input slice was mutated", name)
				}
				if len(out) != len(in) {
					t.Fatalf("%s: length %d, want %d", name, len(out), len(in))
				}
				for i := range in {
					if out[i].Role != in[i].Role || out[i].ToolCallID != in[i].ToolCallID {
						t.Fatalf("%s: message %d changed role or tool call id", name, i)
					}
					if !reflect.DeepEqual(out[i].ToolCalls, in[i].ToolCalls) {
						t.Fatalf("%s: message %d changed its tool calls", name, i)
					}
					if out[i].Content != in[i].Content {
						if out[i].Role != llm.RoleTool || !evicted(out[i].Content) {
							t.Fatalf("%s: message %d changed to something that is no placeholder: %q", name, i, out[i].Content)
						}
					}
				}
			}
		}
	}
}

// Without writes nothing is stale, so the window is exact: of the steps that
// hold a listing, the last keepSteps (at least one) keep every listing result and
// every earlier step loses all of them.
func TestPruneListingKeepsExactlyTheLastStepsOverGeneratedFanOutHistories(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		for _, keepSteps := range []int{0, 1, 2, 3, 6} {
			rng := rand.New(rand.NewSource(seed))
			in, listingStep := fanOutHistory(rng, 1+rng.Intn(14), false)
			out := pruneToolResults(in, listingOpts(keepSteps))

			var steps []int
			seen := map[int]bool{}
			for _, m := range in {
				if m.Role != llm.RoleTool {
					continue
				}
				if s, ok := listingStep[m.ToolCallID]; ok && !seen[s] {
					seen[s] = true
					steps = append(steps, s)
				}
			}
			// A window below one is a window of one.
			window := keepSteps
			if window < 1 {
				window = 1
			}
			kept := map[int]bool{}
			for i := len(steps) - 1; i >= 0 && len(kept) < window; i-- {
				kept[steps[i]] = true
			}
			for id, s := range listingStep {
				got := evicted(contentByID(out, id))
				if want := !kept[s]; got != want {
					t.Fatalf("seed=%d keep=%d: %s (step %d) evicted=%v, want %v", seed, keepSteps, id, s, got, want)
				}
			}
		}
	}
}

// The agent reads the two settings off its configuration.
func TestEvictionOptionsCarryTheListingSettings(t *testing.T) {
	build := func(re config.ResultEviction) resultEvictionOptions {
		cfg := &config.Config{Compaction: config.Compaction{ResultEviction: re}}
		ag := NewAgent(cfg, &session.State{ID: "sess_opts", CWD: testCWD}, nil, nil)
		return ag.evictionOptions()
	}

	def := build(config.ResultEviction{})
	if def.KeepRecentSteps != 3 {
		t.Fatalf("default keep_recent_steps = %d, want 3", def.KeepRecentSteps)
	}
	for _, name := range []string{"glob", "print_tree", "websearch", "webfetch"} {
		if !def.ListingTools[name] {
			t.Fatalf("default listing tools lack %s: %v", name, def.ListingTools)
		}
	}
	if len(def.ListingTools) != 4 {
		t.Fatalf("default listing tools = %v, want exactly four", def.ListingTools)
	}

	empty := []string{}
	one := 1
	none := build(config.ResultEviction{Tools: &empty, KeepRecentSteps: &one})
	if len(none.ListingTools) != 0 || none.KeepRecentSteps != 1 {
		t.Fatalf("explicit empty list and 1 not honoured: %+v", none)
	}

	two := []string{"webfetch", "glob"}
	some := build(config.ResultEviction{Tools: &two})
	if len(some.ListingTools) != 2 || !some.ListingTools["webfetch"] || !some.ListingTools["glob"] {
		t.Fatalf("configured list not honoured: %v", some.ListingTools)
	}
}
