package agent

// Context overflow protection: prune superseded read/grep tool results from the
// LLM projection. This is a projection over the message slice sent to the model —
// the persisted session transcript is never rewritten. A read page or grep result
// survives only while it is "fresh" (inside the recent working window), was marked
// useful (keep:true on the call, or a keep_result pin), and has not been made stale
// by a later write to a file it covered. Everything else collapses to a short
// placeholder that keeps the tool_call/tool_result pairing valid for the provider.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// resultEvictionOptions configures pruneToolResults.
type resultEvictionOptions struct {
	Enabled        bool
	KeepRecent     int // most recent evictable results left intact (working window)
	MinResultBytes int // results at or below this size are never candidates
	CWD            string
}

// evictionOptions reads the effective result-eviction settings for this agent.
func (a *Agent) evictionOptions() resultEvictionOptions {
	re := &a.cfg.Compaction.ResultEviction
	return resultEvictionOptions{
		Enabled:        re.IsEnabled(),
		KeepRecent:     re.EffectiveKeepRecent(),
		MinResultBytes: re.EffectiveMinResultBytes(),
		CWD:            a.state.GetCWD(),
	}
}

// prunedForLLM applies read/grep result eviction to an LLM-visible message window.
func (a *Agent) prunedForLLM(msgs []llm.Message) []llm.Message {
	return pruneToolResults(msgs, a.evictionOptions())
}

// evReadResult is a read tool result eligible for eviction.
type evReadResult struct {
	msgIdx int
	path   string // absolute
	start  int    // effective 1-based start line
	end    int    // inclusive end line; 0 means to EOF (no limit)
	ranged bool   // the call passed offset/limit (a windowed page)
	keep   bool
}

// evGrepResult is a grep tool result eligible for eviction.
type evGrepResult struct {
	msgIdx     int
	pattern    string
	searchPath string              // absolute; the search root
	keep       bool
	outPaths   map[string]struct{} // absolute paths appearing in the output
}

type evReadPin struct {
	path  string
	start int
	end   int
	whole bool // no range: pins the whole file
}

type evGrepPin struct {
	pattern    string
	searchPath string // "" matches any search path
}

type evWrite struct {
	msgIdx int
	path   string // absolute
}

// grepLineRe captures the leading "path:line:" of a grep record. The non-greedy
// path group tolerates Windows drive letters ("C:\x:12:content").
var grepLineRe = regexp.MustCompile(`(?m)^(.+?):(\d+):`)

// pruneToolResults returns history with superseded read/grep results collapsed to
// placeholders. It never mutates the input slice (copy-on-write) and returns it
// unchanged when eviction is disabled or nothing qualifies.
func pruneToolResults(history []llm.Message, opt resultEvictionOptions) []llm.Message {
	if !opt.Enabled || len(history) == 0 {
		return history
	}

	callByID := toolCallsByID(history)

	var reads []evReadResult
	var greps []evGrepResult
	var readPins []evReadPin
	var grepPins []evGrepPin
	var writes []evWrite

	for i := range history {
		m := history[i]
		// Collect pins and write targets announced by assistant tool calls.
		for _, tc := range m.ToolCalls {
			switch {
			case tc.Name == "keep_result":
				addKeepResultPin(tc.InputJSON, opt.CWD, &readPins, &grepPins)
			case filesystemWriteTool(tc.Name):
				for _, p := range writeTargets(tc.Name, tc.InputJSON, opt.CWD) {
					writes = append(writes, evWrite{msgIdx: i, path: p})
				}
			}
		}
		if m.Role != llm.RoleTool {
			continue
		}
		call, ok := callByID[m.ToolCallID]
		if !ok {
			continue
		}
		// Skip tiny results: not worth a placeholder, and they do not consume the
		// working-window budget.
		if len(m.Content) <= opt.MinResultBytes {
			continue
		}
		switch call.Name {
		case "read":
			reads = append(reads, parseReadResult(i, call.InputJSON, opt.CWD))
		case "grep":
			greps = append(greps, parseGrepResult(i, call.InputJSON, m.Content, opt.CWD))
		}
	}

	if len(reads) == 0 && len(greps) == 0 {
		return history
	}

	// Working window: the most recent KeepRecent candidates (across both kinds) by
	// message position stay intact so the model is never forced to mark a result it
	// is still reasoning about.
	windowIdx := recentCandidateWindow(reads, greps, opt.KeepRecent)

	out := history
	cloned := false
	evict := func(msgIdx int, placeholder string) {
		if !cloned {
			out = append([]llm.Message(nil), history...)
			cloned = true
		}
		out[msgIdx].Content = placeholder
	}

	for _, r := range reads {
		if w, stale := staleReadWrite(r, writes); stale {
			evict(r.msgIdx, readStalePlaceholder(r, w, opt.CWD))
			continue
		}
		if _, ok := windowIdx[r.msgIdx]; ok {
			continue
		}
		if r.keep || readPinned(r, readPins) {
			continue
		}
		evict(r.msgIdx, readEvictedPlaceholder(r, opt.CWD))
	}
	for _, g := range greps {
		if w, stale := staleGrepWrite(g, writes); stale {
			evict(g.msgIdx, grepStalePlaceholder(g, w, opt.CWD))
			continue
		}
		if _, ok := windowIdx[g.msgIdx]; ok {
			continue
		}
		if g.keep || grepPinned(g, grepPins) {
			continue
		}
		evict(g.msgIdx, grepEvictedPlaceholder(g, opt.CWD))
	}

	return out
}

// toolCallsByID maps a tool_call_id to the assistant tool call that announced it.
func toolCallsByID(history []llm.Message) map[string]llm.ToolCall {
	out := make(map[string]llm.ToolCall)
	for i := range history {
		for _, tc := range history[i].ToolCalls {
			if strings.TrimSpace(tc.ID) != "" {
				out[tc.ID] = tc
			}
		}
	}
	return out
}

func parseReadResult(msgIdx int, argsJSON, cwd string) evReadResult {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
		Keep   bool   `json:"keep"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &a)
	start := a.Offset
	if start < 1 {
		start = 1
	}
	end := 0
	if a.Limit > 0 {
		end = start + a.Limit - 1
	}
	return evReadResult{
		msgIdx: msgIdx,
		path:   absPath(a.Path, cwd),
		start:  start,
		end:    end,
		ranged: a.Offset > 0 || a.Limit > 0,
		keep:   a.Keep,
	}
}

func parseGrepResult(msgIdx int, argsJSON, content, cwd string) evGrepResult {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Keep    bool   `json:"keep"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &a)
	searchPath := cwd
	if strings.TrimSpace(a.Path) != "" {
		searchPath = absPath(a.Path, cwd)
	}
	return evGrepResult{
		msgIdx:     msgIdx,
		pattern:    a.Pattern,
		searchPath: searchPath,
		keep:       a.Keep,
		outPaths:   grepOutputPaths(content, searchPath),
	}
}

// grepOutputPaths extracts the absolute file paths appearing as the path:line:
// prefix of grep records, used to detect a later write invalidating the results.
func grepOutputPaths(content, searchPath string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range grepLineRe.FindAllStringSubmatch(content, -1) {
		p := strings.TrimSpace(m[1])
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(searchPath, p)
		}
		out[filepath.Clean(p)] = struct{}{}
	}
	return out
}

// addKeepResultPin routes a keep_result call to the read or grep pin list.
func addKeepResultPin(argsJSON, cwd string, readPins *[]evReadPin, grepPins *[]evGrepPin) {
	var a struct {
		Path    string `json:"path"`
		Offset  int    `json:"offset"`
		Limit   int    `json:"limit"`
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &a)
	if strings.TrimSpace(a.Pattern) != "" {
		sp := ""
		if strings.TrimSpace(a.Path) != "" {
			sp = absPath(a.Path, cwd)
		}
		*grepPins = append(*grepPins, evGrepPin{pattern: a.Pattern, searchPath: sp})
		return
	}
	if strings.TrimSpace(a.Path) == "" {
		return
	}
	start := a.Offset
	if start < 1 {
		start = 1
	}
	end := 0
	if a.Limit > 0 {
		end = start + a.Limit - 1
	}
	*readPins = append(*readPins, evReadPin{
		path:  absPath(a.Path, cwd),
		start: start,
		end:   end,
		whole: a.Offset <= 0 && a.Limit <= 0,
	})
}

// writeTargets returns the absolute path(s) a filesystem write tool modifies.
// Arg shapes mirror internal/permission WriteGrantKeys.
func writeTargets(toolName, argsJSON, cwd string) []string {
	switch toolName {
	case "mv":
		var a struct {
			Src string `json:"src"`
			Dst string `json:"dst"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		var out []string
		if strings.TrimSpace(a.Src) != "" {
			out = append(out, absPath(a.Src, cwd))
		}
		if strings.TrimSpace(a.Dst) != "" {
			out = append(out, absPath(a.Dst, cwd))
		}
		return out
	default:
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &a) != nil {
			return nil
		}
		if strings.TrimSpace(a.Path) == "" {
			return nil
		}
		return []string{absPath(a.Path, cwd)}
	}
}

// recentCandidateWindow returns the message indices of the last keepRecent
// candidates (reads and greps combined), which stay intact as a working window.
func recentCandidateWindow(reads []evReadResult, greps []evGrepResult, keepRecent int) map[int]struct{} {
	window := make(map[int]struct{})
	if keepRecent <= 0 {
		return window
	}
	idx := make([]int, 0, len(reads)+len(greps))
	for _, r := range reads {
		idx = append(idx, r.msgIdx)
	}
	for _, g := range greps {
		idx = append(idx, g.msgIdx)
	}
	// idx is naturally ordered by message position within each kind but not across
	// kinds; sort to take the true most-recent set.
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && idx[j] < idx[j-1]; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	start := len(idx) - keepRecent
	if start < 0 {
		start = 0
	}
	for _, v := range idx[start:] {
		window[v] = struct{}{}
	}
	return window
}

func readPinned(r evReadResult, pins []evReadPin) bool {
	for _, p := range pins {
		if p.path != r.path {
			continue
		}
		if p.whole || rangesOverlap(r.start, r.end, p.start, p.end) {
			return true
		}
	}
	return false
}

func grepPinned(g evGrepResult, pins []evGrepPin) bool {
	for _, p := range pins {
		if p.pattern != g.pattern {
			continue
		}
		if p.searchPath == "" || p.searchPath == g.searchPath {
			return true
		}
	}
	return false
}

// rangesOverlap reports whether [aStart,aEnd] and [bStart,bEnd] overlap, treating
// end 0 as "to end of file" (unbounded).
func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	if aEnd == 0 {
		aEnd = int(^uint(0) >> 1)
	}
	if bEnd == 0 {
		bEnd = int(^uint(0) >> 1)
	}
	return aStart <= bEnd && bStart <= aEnd
}

func staleReadWrite(r evReadResult, writes []evWrite) (evWrite, bool) {
	for _, w := range writes {
		if w.msgIdx > r.msgIdx && w.path == r.path {
			return w, true
		}
	}
	return evWrite{}, false
}

func staleGrepWrite(g evGrepResult, writes []evWrite) (evWrite, bool) {
	for _, w := range writes {
		if w.msgIdx <= g.msgIdx {
			continue
		}
		if w.path == g.searchPath {
			return w, true
		}
		if _, ok := g.outPaths[w.path]; ok {
			return w, true
		}
	}
	return evWrite{}, false
}

// absPath resolves p against cwd and returns a cleaned absolute path.
func absPath(p, cwd string) string {
	resolved := tools.ResolvePath(strings.TrimSpace(p), cwd)
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

// relForDisplay renders an absolute path relative to cwd for a placeholder, falling
// back to the absolute path when that is not cleaner.
func relForDisplay(abs, cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return abs
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return abs
	}
	return filepath.ToSlash(rel)
}

func readRangeLabel(r evReadResult) string {
	if !r.ranged {
		return "contents"
	}
	if r.end > 0 {
		return fmt.Sprintf("lines %d-%d", r.start, r.end)
	}
	return fmt.Sprintf("from line %d", r.start)
}

func readEvictedPlaceholder(r evReadResult, cwd string) string {
	return fmt.Sprintf("[evicted: %s %s, not marked as useful; re-read if this range is needed again]",
		relForDisplay(r.path, cwd), readRangeLabel(r))
}

func readStalePlaceholder(r evReadResult, _ evWrite, cwd string) string {
	return fmt.Sprintf("[evicted: %s was modified after this read; re-read for current contents]",
		relForDisplay(r.path, cwd))
}

func grepEvictedPlaceholder(g evGrepResult, cwd string) string {
	return fmt.Sprintf("[evicted: grep %q in %s, not marked as useful; re-run the search if needed]",
		g.pattern, relForDisplay(g.searchPath, cwd))
}

func grepStalePlaceholder(g evGrepResult, w evWrite, cwd string) string {
	return fmt.Sprintf("[evicted: grep %q results are stale after %s was modified; re-run the search]",
		g.pattern, relForDisplay(w.path, cwd))
}
