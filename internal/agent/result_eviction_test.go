package agent

import (
	"encoding/json"
	"fmt"
	// Seeded generators make reproducible test histories; nothing here is secret.
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// testCWD is a real absolute directory so read/grep/write paths resolve through
// the same absPath logic as production (filepath.Abs adds the OS drive/root).
var testCWD = func() string { d, _ := filepath.Abs("testproj"); return d }()

func bigBody(marker string) string {
	return marker + "\n" + strings.Repeat("padding line\n", 100)
}

func asstRead(id, path string, offset, limit int, keep bool) llm.Message {
	args := map[string]interface{}{"path": path}
	if offset > 0 {
		args["offset"] = offset
	}
	if limit > 0 {
		args["limit"] = limit
	}
	if keep {
		args["keep"] = true
	}
	b, _ := json.Marshal(args)
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: string(b)}}}
}

func asstGrep(id, pattern, path string, keep bool) llm.Message {
	args := map[string]interface{}{"pattern": pattern}
	if path != "" {
		args["path"] = path
	}
	if keep {
		args["keep"] = true
	}
	b, _ := json.Marshal(args)
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "grep", InputJSON: string(b)}}}
}

func asstWrite(id, tool, path string) llm.Message {
	b, _ := json.Marshal(map[string]interface{}{"path": path})
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: tool, InputJSON: string(b)}}}
}

func asstMove(id, src, dst string) llm.Message {
	b, _ := json.Marshal(map[string]interface{}{"src": src, "dst": dst})
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "mv", InputJSON: string(b)}}}
}

func asstKeepResult(id string, args map[string]interface{}) llm.Message {
	b, _ := json.Marshal(args)
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "keep_result", InputJSON: string(b)}}}
}

func toolResult(id, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: content}
}

func grepBody(pattern string, files ...string) string {
	var b strings.Builder
	b.WriteString("results for " + pattern + "\n")
	for i, f := range files {
		// Emit the same absolute path the search would have produced.
		fmt.Fprintf(&b, "%s:%d:match %d\n", absPath(f, testCWD), i+10, i)
	}
	// Pad so it clears MinResultBytes.
	b.WriteString(strings.Repeat("x", 200))
	return b.String()
}

func defaultOpts() resultEvictionOptions {
	return resultEvictionOptions{Enabled: true, KeepRecent: 1, MinResultBytes: 10, CWD: testCWD}
}

func evicted(content string) bool { return strings.HasPrefix(content, "[evicted:") }

// contentByID returns the tool-result content for a tool_call_id.
func contentByID(msgs []llm.Message, id string) string {
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

func TestPruneUnmarkedKeepsOnlyWorkingWindow(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstRead("b", "big.go", 501, 500, false),
		toolResult("b", bigBody("PAGE-B")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !evicted(contentByID(out, "a")) {
		t.Fatalf("page A should be evicted: %q", contentByID(out, "a"))
	}
	if !strings.Contains(contentByID(out, "a"), "big.go") || !strings.Contains(contentByID(out, "a"), "lines 1-500") {
		t.Fatalf("page A placeholder missing range: %q", contentByID(out, "a"))
	}
	if !strings.Contains(contentByID(out, "b"), "PAGE-B") {
		t.Fatalf("page B (working window) must survive: %q", contentByID(out, "b"))
	}
}

func TestPruneKeepTrueSurvives(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, true), // marked
		toolResult("a", bigBody("PAGE-A")),
		asstRead("b", "other.go", 1, 500, false),
		toolResult("b", bigBody("PAGE-B")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !strings.Contains(contentByID(out, "a"), "PAGE-A") {
		t.Fatalf("keep:true page must survive: %q", contentByID(out, "a"))
	}
}

func TestPruneKeepResultPinOverlappingRange(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstKeepResult("k", map[string]interface{}{"path": "big.go", "offset": 100, "limit": 50}),
		toolResult("k", "marked as useful"),
		asstRead("b", "other.go", 1, 500, false),
		toolResult("b", bigBody("PAGE-B")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !strings.Contains(contentByID(out, "a"), "PAGE-A") {
		t.Fatalf("pinned overlapping page must survive: %q", contentByID(out, "a"))
	}
}

func TestPruneKeepResultDoesNotPinFutureReads(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstKeepResult("k", map[string]interface{}{"path": "big.go", "offset": 1, "limit": 500}),
		toolResult("k", "marked as useful"),
		asstRead("b", "big.go", 1, 500, false),
		toolResult("b", bigBody("PAGE-B")),
		asstRead("c", "other.go", 1, 500, false),
		toolResult("c", bigBody("PAGE-C")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !strings.Contains(contentByID(out, "a"), "PAGE-A") {
		t.Fatalf("the earlier explicitly pinned page must survive: %q", contentByID(out, "a"))
	}
	if !evicted(contentByID(out, "b")) {
		t.Fatalf("a pin must not retain a later matching read: %q", contentByID(out, "b"))
	}
}

func TestPruneTwoPinsOneFile(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 100, false),
		toolResult("a", bigBody("PAGE-A")),
		asstRead("b", "big.go", 200, 100, false),
		toolResult("b", bigBody("PAGE-B")),
		asstKeepResult("k1", map[string]interface{}{"path": "big.go", "offset": 1, "limit": 100}),
		toolResult("k1", "ok"),
		asstKeepResult("k2", map[string]interface{}{"path": "big.go", "offset": 200, "limit": 100}),
		toolResult("k2", "ok"),
		asstRead("c", "other.go", 1, 10, false),
		toolResult("c", bigBody("PAGE-C")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !strings.Contains(contentByID(out, "a"), "PAGE-A") || !strings.Contains(contentByID(out, "b"), "PAGE-B") {
		t.Fatalf("both pinned pages must survive: a=%q b=%q", contentByID(out, "a"), contentByID(out, "b"))
	}
}

func TestPruneWriteInvalidatesPinnedRead(t *testing.T) {
	for _, tool := range []string{"edit", "write", "apply_patch"} {
		in := []llm.Message{
			asstRead("a", "big.go", 1, 500, true), // pinned via keep:true
			toolResult("a", bigBody("PAGE-A")),
			asstWrite("w", tool, "big.go"),
			toolResult("w", "written"),
		}
		out := pruneToolResults(in, defaultOpts())
		got := contentByID(out, "a")
		if !evicted(got) || !strings.Contains(got, "modified after this read") {
			t.Fatalf("%s: stale read must be evicted even when pinned: %q", tool, got)
		}
	}
}

func TestPruneMoveInvalidatesRead(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, true),
		toolResult("a", bigBody("PAGE-A")),
		asstMove("m", "big.go", "renamed.go"),
		toolResult("m", "moved"),
	}
	out := pruneToolResults(in, defaultOpts())
	if !evicted(contentByID(out, "a")) {
		t.Fatalf("read of moved file must be evicted: %q", contentByID(out, "a"))
	}
}

func TestPruneDirectoryMutationInvalidatesRelatedReads(t *testing.T) {
	tests := []struct {
		name  string
		read  llm.Message
		write llm.Message
	}{
		{
			name:  "child write invalidates directory listing",
			read:  asstRead("a", "pkg", 0, 0, true),
			write: asstWrite("w", "write", filepath.Join("pkg", "new.go")),
		},
		{
			name:  "directory move invalidates child read",
			read:  asstRead("a", filepath.Join("pkg", "file.go"), 1, 500, true),
			write: asstMove("w", "pkg", "renamed"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := defaultOpts()
			opts.KeepRecent = 0
			in := []llm.Message{
				tc.read,
				toolResult("a", bigBody("PAGE-A")),
				tc.write,
				toolResult("w", "written"),
			}
			out := pruneToolResults(in, opts)
			if !evicted(contentByID(out, "a")) {
				t.Fatalf("related filesystem mutation must invalidate the read: %q", contentByID(out, "a"))
			}
		})
	}
}

func TestPruneWindowsPathCaseDoesNotHideWrite(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path comparison")
	}
	opts := defaultOpts()
	opts.KeepRecent = 0
	in := []llm.Message{
		asstRead("a", filepath.Join("PKG", "File.go"), 1, 500, true),
		toolResult("a", bigBody("PAGE-A")),
		asstWrite("w", "write", filepath.Join("pkg", "file.go")),
		toolResult("w", "written"),
	}
	out := pruneToolResults(in, opts)
	if !evicted(contentByID(out, "a")) {
		t.Fatalf("case-only path differences must not hide a write: %q", contentByID(out, "a"))
	}
}

func TestPruneFailedWriteDoesNotInvalidateRead(t *testing.T) {
	opts := defaultOpts()
	opts.KeepRecent = 0
	for _, result := range []string{
		"error: edit: old_string not found",
		"permission denied by user",
		toolLoopNudge,
		toolLoopSkippedResult,
	} {
		in := []llm.Message{
			asstRead("a", "big.go", 1, 500, true),
			toolResult("a", bigBody("PAGE-A")),
			asstWrite("w", "edit", "big.go"),
			toolResult("w", result),
		}
		out := pruneToolResults(in, opts)
		if evicted(contentByID(out, "a")) {
			t.Fatalf("unsuccessful write result %q invalidated the read", result)
		}
	}
}

func TestPruneGrepUnmarkedEvictedKeepSurvives(t *testing.T) {
	in := []llm.Message{
		asstGrep("g1", "handleFoo", "", false),
		toolResult("g1", grepBody("handleFoo", "a.go", "b.go")),
		asstGrep("g2", "handleBar", "", true), // marked
		toolResult("g2", grepBody("handleBar", "c.go")),
		asstRead("r", "x.go", 1, 10, false),
		toolResult("r", bigBody("PAGE")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !evicted(contentByID(out, "g1")) {
		t.Fatalf("unmarked grep must be evicted: %q", contentByID(out, "g1"))
	}
	if !strings.Contains(contentByID(out, "g1"), `grep "handleFoo"`) {
		t.Fatalf("grep placeholder missing pattern: %q", contentByID(out, "g1"))
	}
	if !strings.Contains(contentByID(out, "g2"), "handleBar") {
		t.Fatalf("marked grep must survive: %q", contentByID(out, "g2"))
	}
}

func TestPruneGrepStaleAfterWriteToMatchedFile(t *testing.T) {
	in := []llm.Message{
		asstGrep("g", "handleFoo", "", true), // even pinned
		toolResult("g", grepBody("handleFoo", "a.go", "b.go")),
		asstWrite("w", "edit", "b.go"),
		toolResult("w", "written"),
	}
	out := pruneToolResults(in, defaultOpts())
	got := contentByID(out, "g")
	if !evicted(got) || !strings.Contains(got, "stale after") {
		t.Fatalf("grep must be evicted stale after writing a matched file: %q", got)
	}
}

func TestPruneGrepStaleAfterWriteBelowSearchRoot(t *testing.T) {
	opts := defaultOpts()
	opts.KeepRecent = 0
	in := []llm.Message{
		asstGrep("g", "handleFoo", "pkg", true),
		toolResult("g", grepBody("handleFoo", filepath.Join("pkg", "existing.go"))),
		asstWrite("w", "write", filepath.Join("pkg", "new.go")),
		toolResult("w", "written"),
	}
	out := pruneToolResults(in, opts)
	got := contentByID(out, "g")
	if !evicted(got) || !strings.Contains(got, "stale after") {
		t.Fatalf("a write below the grep root can change its matches: %q", got)
	}
}

func TestPruneGrepNotStaleAfterUnrelatedWrite(t *testing.T) {
	// keep_recent 0 so the grep is not shielded by the working window; only
	// staleness or a mark could evict it, and neither applies here (unrelated
	// write + keep:true).
	opts := defaultOpts()
	opts.KeepRecent = 0
	in := []llm.Message{
		asstGrep("g", "handleFoo", "pkg", true),
		toolResult("g", grepBody("handleFoo", filepath.Join("pkg", "a.go"))),
		asstWrite("w", "edit", filepath.Join("other", "unrelated.go")),
		toolResult("w", "written"),
	}
	out := pruneToolResults(in, opts)
	if evicted(contentByID(out, "g")) {
		t.Fatalf("grep must not be invalidated by an unrelated write: %q", contentByID(out, "g"))
	}
}

func TestPruneSmallResultNeverEvictedAndNoBudget(t *testing.T) {
	opts := defaultOpts()
	opts.MinResultBytes = 1000 // the small read below is under this
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")), // large candidate (old)
		asstRead("s", "tiny.go", 1, 5, false),
		toolResult("s", "small"), // under MinResultBytes -> not a candidate
		asstRead("b", "big2.go", 1, 500, false),
		toolResult("b", bigBody("PAGE-B")), // large candidate (new)
	}
	out := pruneToolResults(in, opts)
	if !evicted(contentByID(out, "a")) {
		t.Fatalf("old large read should be evicted with keep_recent=1: %q", contentByID(out, "a"))
	}
	if contentByID(out, "s") != "small" {
		t.Fatalf("small result must be untouched: %q", contentByID(out, "s"))
	}
	if !strings.Contains(contentByID(out, "b"), "PAGE-B") {
		t.Fatalf("newest large read must survive the working window: %q", contentByID(out, "b"))
	}
}

func TestPruneDisabledReturnsInputUnchanged(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstRead("b", "big.go", 501, 500, false),
		toolResult("b", bigBody("PAGE-B")),
	}
	opts := defaultOpts()
	opts.Enabled = false
	out := pruneToolResults(in, opts)
	if evicted(contentByID(out, "a")) {
		t.Fatalf("disabled eviction must not modify content")
	}
}

func TestPruneSharedWindowAcrossReadAndGrep(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstGrep("g", "handleFoo", "", false),
		toolResult("g", grepBody("handleFoo", "a.go")),
	}
	out := pruneToolResults(in, defaultOpts())
	if !evicted(contentByID(out, "a")) {
		t.Fatalf("older read must be evicted (grep took the single window slot): %q", contentByID(out, "a"))
	}
	if evicted(contentByID(out, "g")) {
		t.Fatalf("newest candidate (grep) must survive: %q", contentByID(out, "g"))
	}
}

func TestPrunePreservesRoleAndToolCallIDAndDoesNotMutateInput(t *testing.T) {
	in := []llm.Message{
		asstRead("a", "big.go", 1, 500, false),
		toolResult("a", bigBody("PAGE-A")),
		asstRead("b", "big.go", 501, 500, false),
		toolResult("b", bigBody("PAGE-B")),
	}
	origA := in[1].Content
	out := pruneToolResults(in, defaultOpts())
	// Evicted message keeps its structure.
	var evictedMsg *llm.Message
	for i := range out {
		if out[i].Role == llm.RoleTool && out[i].ToolCallID == "a" {
			evictedMsg = &out[i]
		}
	}
	if evictedMsg == nil || evictedMsg.Role != llm.RoleTool || evictedMsg.ToolCallID != "a" {
		t.Fatal("evicted message lost its role or tool_call_id")
	}
	// Input slice untouched (copy-on-write).
	if in[1].Content != origA {
		t.Fatalf("input slice was mutated: %q", in[1].Content)
	}
}

// TestPruneAlternatingReadGrepWindowSize documents the working-window trade-off
// on a workload that alternates between two large results. With keep_recent=1 the
// newest result evicts the other one, so a model that needs both is invited (by
// the placeholder) to re-fetch it; keep_recent=2 keeps the pair live.
func TestPruneAlternatingReadGrepWindowSize(t *testing.T) {
	in := []llm.Message{
		asstRead("r1", "big.go", 1, 400, false),
		toolResult("r1", bigBody("READ-PAGE")),
		asstGrep("g1", "filler", "", false),
		toolResult("g1", grepBody("filler", "big.go")),
	}

	t.Run("keep_recent=1 evicts the older half of the pair", func(t *testing.T) {
		opts := defaultOpts()
		opts.KeepRecent = 1
		out := pruneToolResults(in, opts)
		if !evicted(contentByID(out, "r1")) {
			t.Fatalf("read should be evicted when the grep takes the only window slot: %q", contentByID(out, "r1"))
		}
		if evicted(contentByID(out, "g1")) {
			t.Fatalf("newest result must stay live: %q", contentByID(out, "g1"))
		}
	})

	t.Run("keep_recent=2 keeps both live", func(t *testing.T) {
		opts := defaultOpts()
		opts.KeepRecent = 2
		out := pruneToolResults(in, opts)
		if evicted(contentByID(out, "r1")) || evicted(contentByID(out, "g1")) {
			t.Fatalf("a window of 2 must keep the read+grep pair live: read=%q grep=%q",
				contentByID(out, "r1"), contentByID(out, "g1"))
		}
	})
}

// --- listing results: glob, print_tree, websearch, webfetch --------------------
//
// Whole steps are the unit of the listings' working window, any successful write
// under a listing's root makes it stale, the projection keeps the tool-call
// pairing intact, and the listings wait for their own gate: the share of the
// window the session works with.

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

// listingOpts evicts the listing tools (the fixed set the agent uses) with a
// window of keepSteps steps, and no read/grep working window.
func listingOpts(keepSteps int) resultEvictionOptions {
	return resultEvictionOptions{
		Enabled:         true,
		KeepRecent:      0,
		MinResultBytes:  10,
		CWD:             testCWD,
		ListingTools:    listingTools,
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
			t.Fatalf("KeepRecentSteps %d must behave as 1: g1=%q g2=%q", steps, contentByID(out, "g1"), contentByID(out, "g2"))
		}
		if !evicted(contentByID(out, "g1")) {
			t.Fatalf("KeepRecentSteps %d: the older step must be evicted: %q", steps, contentByID(out, "g1"))
		}
		if !strings.Contains(contentByID(out, "g2"), "MARK-g2") {
			t.Fatalf("KeepRecentSteps %d: the latest step must stay whole: %q", steps, contentByID(out, "g2"))
		}
	}

	// The case that motivated the floor: one listing, nothing after it.
	only := historyOf(globStep("only", "*.go", "pkg", 40))
	for _, steps := range []int{0, -1} {
		out := pruneToolResults(only, listingOpts(steps))
		if !strings.Contains(contentByID(out, "only"), "MARK-only") {
			t.Fatalf("KeepRecentSteps %d collapsed the listing that was just asked for: %q", steps, contentByID(out, "only"))
		}
	}
}

// Options without listing tools - what prunedForLLM passes while the listing
// gate is closed - leave every listing alone, and a tool left out of the set is
// left alone while the others still go.
func TestPruneListingWithoutListingToolsLeavesListingsAlone(t *testing.T) {
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
			t.Fatalf("%s was evicted although no listing tool is in the set: %q", id, contentByID(out, id))
		}
	}

	opts.ListingTools = map[string]bool{"glob": true}
	out = pruneToolResults(append(in, tailStep()...), opts)
	if !evicted(contentByID(out, "g1")) {
		t.Fatalf("glob is in the set and must be evicted: %q", contentByID(out, "g1"))
	}
	for id, marker := range map[string]string{"t1": "TREE", "s1": "SEARCH", "w1": "FETCH"} {
		if !strings.Contains(contentByID(out, id), marker) {
			t.Fatalf("%s was evicted although its tool is not in the set: %q", id, contentByID(out, id))
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
			name:  "print_tree, an edit of a file below its root",
			step:  asstStep(tcTree("l", "pkg", 3)),
			write: asstWrite("w", "edit", filepath.Join("pkg", "a.go")),
			want:  `[evicted: print_tree of pkg is stale after pkg/a.go was modified; re-run if needed]`,
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

// Any successful write under the folder a listing covered makes it stale,
// whatever kind of write it is: a glob result is sorted by modification time, and
// a print_tree result is judged the same way, an edit or an apply_patch that only
// updates a file included. Nothing is read off the patch to tell the kinds apart.
func TestPruneListingStaleAfterAnyKindOfWriteUnderItsRoot(t *testing.T) {
	inPkg := filepath.Join("pkg", "a.go")
	const (
		v4aUpdatePatch = "*** Begin Patch\n*** Update File: pkg/a.go\n@@\n-old line\n+new line\n*** End Patch"
		unifiedUpdate  = "--- a/pkg/a.go\n+++ b/pkg/a.go\n@@ -1,2 +1,2 @@\n context\n-old line\n+new line\n"
		v4aAddPatch    = "*** Begin Patch\n*** Add File: pkg/new.go\n+package pkg\n*** End Patch"
	)
	cases := []struct {
		name  string
		write llm.Message
	}{
		{"edit of a file below the root", asstWrite("w", "edit", inPkg)},
		{"edit of a file at the root's top", asstWrite("w", "edit", filepath.Join("pkg", "top.go"))},
		{"apply_patch updating a file (Codex format)", asstPatch("w", inPkg, v4aUpdatePatch)},
		{"apply_patch updating a file (unified diff)", asstPatch("w", inPkg, unifiedUpdate)},
		{"apply_patch adding a file", asstPatch("w", filepath.Join("pkg", "new.go"), v4aAddPatch)},
		{"write", asstWrite("w", "write", inPkg)},
		{"mkdir", asstWrite("w", "mkdir", filepath.Join("pkg", "sub"))},
		{"rmdir", asstWrite("w", "rmdir", filepath.Join("pkg", "sub"))},
		{"touch", asstWrite("w", "touch", filepath.Join("pkg", "new.go"))},
		{"rm", asstWrite("w", "rm", inPkg)},
		{"mv out of the root", asstMove("w", inPkg, "elsewhere.go")},
		{"mv into the root", asstMove("w", "elsewhere.go", filepath.Join("pkg", "in.go"))},
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
			for _, id := range []string{"t", "g"} {
				if got := contentByID(out, id); !strings.Contains(got, "is stale after") {
					t.Fatalf("%s must be stale after the write: %q", id, got)
				}
			}
		})
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
	base := historyOf(
		[]llm.Message{asstRead("r1", "a.go", 1, 500, false), toolResult("r1", bigBody("PAGE-1"))},
		[]llm.Message{asstGrep("g1", "needle", "", false), toolResult("g1", grepBody("needle", "a.go"))},
		[]llm.Message{asstRead("r2", "b.go", 1, 500, false), toolResult("r2", bigBody("PAGE-2"))},
	)
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

// The agent's options carry the fixed listing set and the fixed step window: no
// setting moves either.
func TestEvictionOptionsCarryTheListingSettings(t *testing.T) {
	cfg := &config.Config{}
	ag := NewAgent(cfg, &session.State{ID: "sess_opts", CWD: testCWD}, nil, nil)
	opts := ag.evictionOptions()
	if opts.KeepRecentSteps != listingKeepRecentSteps || listingKeepRecentSteps != 3 {
		t.Fatalf("listing step window = %d (constant %d), want 3", opts.KeepRecentSteps, listingKeepRecentSteps)
	}
	for _, name := range []string{"glob", "print_tree", "websearch", "webfetch"} {
		if !opts.ListingTools[name] {
			t.Fatalf("listing tools lack %s: %v", name, opts.ListingTools)
		}
	}
	if len(opts.ListingTools) != 4 {
		t.Fatalf("listing tools = %v, want exactly four", opts.ListingTools)
	}
	for _, name := range []string{"read", "grep", "run_command"} {
		if opts.ListingTools[name] {
			t.Fatalf("%s is no listing tool: %v", name, opts.ListingTools)
		}
	}
}

// --- the listing gate ----------------------------------------------------------

// listingGateHistory is a turn of five glob steps of about 300 lines each, then
// three reads of about 3000 characters: the listings are large enough to be
// candidates and the reads clear min_result_bytes.
func listingGateHistory() []llm.Message {
	hist := []llm.Message{{Role: llm.RoleUser, Content: "map the repo"}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("g%d", i)
		hist = append(hist,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "glob", InputJSON: `{"pattern":"**/*.go"}`}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: strings.Repeat("pkg/file.go\n", 300)})
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("r%d", i)
		hist = append(hist,
			asstRead(id, fmt.Sprintf("file_%d.go", i), 0, 0, false),
			toolResult(id, strings.Repeat("source line of the file\n", 125)))
	}
	return hist
}

// The listings wait for start_percent of the window the session works with: the
// model entry's max_context_tokens, else the provider's, else the default of
// 128000. A model entry without max_context_tokens must not collapse them from
// the first request on - that rewrites the middle of the history at nearly every
// step - while read and grep keep their old gate, which evicts from the first
// result when the entry names no window.
func TestPrunedForLLMGatesListingsByTheResolvedWindow(t *testing.T) {
	hist := listingGateHistory()

	t.Run("an entry without max_context_tokens: the default window holds the listings", func(t *testing.T) {
		st := &session.State{ID: "sess_ungated", CWD: t.TempDir(), Mode: session.ModeAgent}
		ag := compactTestAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t})
		if window, _ := ag.contextWindow(); window != config.DefaultContextWindowTokens {
			t.Fatalf("fixture: window = %d, want the default %d", window, config.DefaultContextWindowTokens)
		}
		out := ag.prunedForLLM(hist)
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("g%d", i)
			if got := contentByID(out, id); got != contentByID(hist, id) {
				t.Fatalf("%s was collapsed below start_percent of the default window: %q", id, got)
			}
		}
		// Read and grep go by the old gate: no max_context_tokens, eviction from
		// the first result, a working window of two.
		if !evicted(contentByID(out, "r0")) {
			t.Fatalf("r0 is outside the read/grep working window and must be evicted: %q", firstChars(contentByID(out, "r0"), 80))
		}
		for _, id := range []string{"r1", "r2"} {
			if evicted(contentByID(out, id)) {
				t.Fatalf("%s is inside the working window: %q", id, contentByID(out, id))
			}
		}
	})

	t.Run("a conversation over start_percent of a small window collapses the older listing steps", func(t *testing.T) {
		st := &session.State{ID: "sess_gated", CWD: t.TempDir(), Mode: session.ModeAgent}
		ag := compactTestAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t})
		window := 8000
		if conv := conversationTokens(hist, false); conv*100 < config.ResultEvictionDefaultStartPercent*window {
			t.Fatalf("fixture: %d tokens of conversation are under the gate of a %d window", conv, window)
		}
		ag.cfg.Models[0].MaxContextTokens = window
		out := ag.prunedForLLM(hist)
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("g%d", i)
			collapsed := evicted(contentByID(out, id))
			if want := i < 5-listingKeepRecentSteps; collapsed != want {
				t.Fatalf("%s collapsed = %v, want %v: the last %d listing steps stay", id, collapsed, want, listingKeepRecentSteps)
			}
		}
	})

	t.Run("a refused request opens the listing gate whatever the window", func(t *testing.T) {
		st := &session.State{ID: "sess_forced", CWD: t.TempDir(), Mode: session.ModeAgent}
		ag := compactTestAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t})
		ag.evictionForced = true
		out := ag.prunedForLLM(hist)
		if !evicted(contentByID(out, "g0")) || evicted(contentByID(out, "g4")) {
			t.Fatalf("forced eviction must collapse the older listing steps and keep the latest: g0=%q g4=%q",
				firstChars(contentByID(out, "g0"), 60), firstChars(contentByID(out, "g4"), 60))
		}
	})
}

// The send boundary prunes the request as it goes out, its system message in
// front (buildMessages), and the overhead the gate adds already counts the system
// prompt. A request with a large system prompt that is below start_percent must
// keep its listings, measured on the history or on the request alike.
func TestListingGateCountsTheSystemPromptOnce(t *testing.T) {
	half, one, on := 50, 1, true
	comp := config.Compaction{ResultEviction: config.ResultEviction{Enabled: &on, StartPercent: &half, MinResultBytes: &one}}
	f := newRecoveryFixture(t, &recoveryProvider{}, comp) // window 49152, gate 24576
	f.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "list the tree"})
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("g%d", i)
		f.st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "glob", InputJSON: fmt.Sprintf(`{"pattern":"pkg%d/**/*.go"}`, i)}}})
		var b strings.Builder
		for k := 0; k < 160; k++ {
			fmt.Fprintf(&b, "pkg%d/sub/file_%03d.go\n", i, k)
		}
		f.st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: b.String()})
	}
	sysText := strings.Repeat("You are a coding agent working in a repo. ", 700)
	hist := session.MessagesForLLM(f.st.GetMessages())
	conv, sysTok := conversationTokens(hist, false), session.EstimateContextTokens(sysText)
	f.st.SetLastContextBreakdown(&session.ContextBreakdown{SystemPrompt: sysTok, Conversation: conv, EstimatedTotal: sysTok + conv})
	window, _ := f.ag.contextWindow()
	if (sysTok+conv)*100 >= half*window || (2*sysTok+conv)*100 < half*window {
		t.Fatalf("fixture: system %d + conversation %d must sit under the gate of %d%% of %d, and over it with the system prompt counted twice", sysTok, conv, half, window)
	}
	if f.ag.listingEvictionDue(hist) {
		t.Fatal("the history alone is over the gate")
	}
	req := f.ag.buildMessages(sysText)
	if f.ag.listingEvictionDue(req) {
		t.Fatal("the request with its system message in front opens the gate: the system prompt is counted twice")
	}
	out := f.ag.prunedForLLM(req)
	for i := range req {
		if out[i].Content != req[i].Content {
			t.Fatalf("message %d was rewritten below start_percent: %q", i, firstChars(out[i].Content, 80))
		}
	}
}
