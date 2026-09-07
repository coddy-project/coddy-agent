package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
