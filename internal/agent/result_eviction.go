package agent

// Context overflow protection: prune superseded tool results from the LLM
// projection. This is a projection over the message slice sent to the model -
// the persisted session transcript is never rewritten. Two families go through
// it:
//
//   - read pages and grep results survive only while they are "fresh" (inside the
//     recent working window of results), were marked useful (keep:true on the
//     call, or a keep_result pin), and have not been made stale by a later write
//     to a file they covered;
//   - the listings of glob, print_tree, websearch and webfetch (the tools named
//     by compaction.result_eviction.tools) survive only inside the last
//     keep_recent_steps steps that hold one (never fewer than the latest) - a
//     step is the assistant message that issued a batch of calls with every
//     result of that batch, so a fan-out of ten parallel calls is one step -
//     and, for glob and print_tree, until a write lands under the folder they
//     listed. They have no pins.
//
// Everything else collapses to a short placeholder that keeps the
// tool_call/tool_result pairing valid for the provider: role, tool call id and
// position of every message stay as they were, only the text of the result moves.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// resultEvictionOptions configures pruneToolResults.
type resultEvictionOptions struct {
	Enabled        bool
	KeepRecent     int // most recent evictable read/grep results left intact (working window)
	MinResultBytes int // results at or below this size are never candidates
	CWD            string
	// ListingTools names the read-only listing tools evicted besides read and
	// grep (glob, print_tree, websearch, webfetch). Empty evicts read and grep only.
	ListingTools map[string]bool
	// KeepRecentSteps is how many of the latest steps that hold a listing
	// result keep all of those results intact. A value below 1 behaves as 1: the
	// latest such step always keeps its results, so a listing the model has just
	// asked for reaches it instead of being collapsed in the next request and
	// asked for again (the configuration refuses 0; this is the same floor for
	// options built in code).
	KeepRecentSteps int
}

// evictionOptions reads the effective result-eviction settings for this agent.
func (a *Agent) evictionOptions() resultEvictionOptions {
	re := &a.cfg.Compaction.ResultEviction
	listing := make(map[string]bool)
	for _, name := range re.EffectiveTools() {
		listing[name] = true
	}
	return resultEvictionOptions{
		Enabled:         re.IsEnabled(),
		KeepRecent:      re.EffectiveKeepRecent(),
		MinResultBytes:  re.EffectiveMinResultBytes(),
		CWD:             a.state.GetCWD(),
		ListingTools:    listing,
		KeepRecentSteps: re.EffectiveKeepRecentSteps(),
	}
}

// prunedForLLM applies tool result eviction (read, grep and the listing tools)
// to an LLM-visible message window, but only once the conversation is big enough
// to need it.
//
// Every placeholder this writes lands in the middle of the replayed history, and
// a provider caches a request by its prefix: one rewritten result throws away
// the cached copy of everything after it. With a sliding working window that
// happens on almost every step, which is a whole transcript reprocessed to save
// a few thousand tokens nobody was short of yet. So below
// compaction.result_eviction.start_percent of the model's context window the
// history is sent exactly as it was last time.
func (a *Agent) prunedForLLM(msgs []llm.Message) []llm.Message {
	if !a.evictionDue(msgs) {
		return msgs
	}
	return pruneToolResults(msgs, a.evictionOptions())
}

// evictionDue reports whether the conversation has grown far enough into the
// model's context window for eviction to be worth the cache it costs. It is
// measured on the unpruned messages, so the answer only ever moves one way
// within a session: pruning cannot push the estimate back under the mark and
// start the projection flapping between two shapes.
func (a *Agent) evictionDue(msgs []llm.Message) bool {
	re := &a.cfg.Compaction.ResultEviction
	if !re.IsEnabled() {
		return false
	}
	start := re.EffectiveStartPercent()
	if start <= 0 {
		return true
	}
	ent := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	if ent == nil || ent.MaxContextTokens <= 0 {
		// Nothing to measure against: keep the projection that protects the
		// window, since overflowing it is the worse failure.
		return true
	}
	// Everything the request carries besides the conversation - the system
	// message, the tool definitions, the rules - read off the last estimate.
	// It does not move with eviction, so it cannot make this decision flap.
	overhead := 0
	if rs, ok := a.state.(rulesState); ok {
		if b := rs.GetLastContextBreakdown(); b != nil && b.EstimatedTotal > b.Conversation {
			overhead = b.EstimatedTotal - b.Conversation
		}
	}
	total := overhead + conversationTokens(msgs, a.modelReadsImages())
	return total*100 >= start*ent.MaxContextTokens
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

// evListing is a glob, print_tree, websearch or webfetch result eligible for
// eviction.
type evListing struct {
	msgIdx int
	step   int    // position of the assistant message that issued the call
	tool   string // the tool's name
	// root is the absolute folder a glob or print_tree listed; a write at or
	// around it makes the listing stale. Empty for the web tools: what a search
	// or a page said does not change with the workspace.
	root  string
	label string // glob pattern, search query or URL, for the placeholder
	lines int    // lines in the original result
}

// pendingCall is a tool call whose result has not been read yet, with the
// position of the assistant message that issued it: that message is its step.
type pendingCall struct {
	call llm.ToolCall
	step int
}

// listingLabelMax caps the pattern, query or URL a placeholder repeats, so
// replacing a result never costs a result of its own.
const listingLabelMax = 80

// evGrepResult is a grep tool result eligible for eviction.
type evGrepResult struct {
	msgIdx     int
	pattern    string
	searchPath string // absolute; the search root
	keep       bool
	outPaths   map[string]struct{} // absolute paths appearing in the output
}

type evReadPin struct {
	msgIdx int
	path   string
	start  int
	end    int
	whole  bool // no range: pins the whole file
}

type evGrepPin struct {
	msgIdx     int
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

// pruneToolResults returns history with superseded read, grep and listing results
// collapsed to placeholders. It never mutates the input slice (copy-on-write) and
// returns it unchanged when eviction is disabled or nothing qualifies.
func pruneToolResults(history []llm.Message, opt resultEvictionOptions) []llm.Message {
	if !opt.Enabled || len(history) == 0 {
		return history
	}

	var reads []evReadResult
	var greps []evGrepResult
	var listings []evListing
	var readPins []evReadPin
	var grepPins []evGrepPin
	var writes []evWrite
	pendingCalls := make(map[string]pendingCall)

	for i := range history {
		m := history[i]
		// Track calls in message order so providers that reuse IDs on later turns
		// cannot relabel an earlier result during projection.
		for _, tc := range m.ToolCalls {
			if id := strings.TrimSpace(tc.ID); id != "" {
				pendingCalls[id] = pendingCall{call: tc, step: i}
			}
			if tc.Name == "keep_result" {
				addKeepResultPin(i, tc.InputJSON, opt.CWD, &readPins, &grepPins)
			}
		}
		if m.Role != llm.RoleTool {
			continue
		}
		pending, ok := pendingCalls[m.ToolCallID]
		if !ok {
			continue
		}
		call := pending.call
		delete(pendingCalls, m.ToolCallID)
		// Only completed mutations make prior observations stale. Permission
		// denials, tool errors, and loop-guard placeholders leave files untouched.
		if filesystemWriteTool(call.Name) && writeResultSucceeded(m.Content) {
			for _, p := range writeTargets(call.Name, call.InputJSON, opt.CWD) {
				writes = append(writes, evWrite{msgIdx: i, path: p})
			}
		}
		// Skip tiny results: not worth a placeholder, and they do not consume the
		// working-window budget. A picture a read showed counts with its bytes:
		// its text is one line, and the picture is what fills the context.
		if resultBytes(m) <= opt.MinResultBytes {
			continue
		}
		switch call.Name {
		case "read":
			reads = append(reads, parseReadResult(i, call.InputJSON, opt.CWD))
		case "grep":
			greps = append(greps, parseGrepResult(i, call.InputJSON, m.Content, opt.CWD))
		default:
			if opt.ListingTools[call.Name] {
				listings = append(listings, parseListingResult(i, pending.step, call, m.Content, opt.CWD))
			}
		}
	}

	if len(reads) == 0 && len(greps) == 0 && len(listings) == 0 {
		return history
	}

	// Working window: the most recent KeepRecent candidates (across both kinds) by
	// message position stay intact so the model is never forced to mark a result it
	// is still reasoning about. Listings are not part of it: they are judged by the
	// step window below.
	windowIdx := recentCandidateWindow(reads, greps, opt.KeepRecent)
	stepWindow := listingStepWindow(listings, max(opt.KeepRecentSteps, 1))

	out := history
	cloned := false
	evict := func(msgIdx int, placeholder string) {
		if !cloned {
			out = append([]llm.Message(nil), history...)
			cloned = true
		}
		out[msgIdx].Content = placeholder
		// A collapsed read no longer shows the model its picture either.
		out[msgIdx].ImageParts = nil
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
	for _, l := range listings {
		if w, stale := staleListingWrite(l, writes); stale {
			evict(l.msgIdx, listingStalePlaceholder(l, w, opt.CWD))
			continue
		}
		if _, ok := stepWindow[l.step]; ok {
			continue
		}
		evict(l.msgIdx, listingEvictedPlaceholder(l, opt.CWD))
	}

	return out
}

// resultBytes is what a tool result costs the request: its text plus the
// pictures it carries.
func resultBytes(m llm.Message) int {
	n := len(m.Content)
	for _, p := range m.ImageParts {
		n += partBytes(p)
	}
	return n
}

func writeResultSucceeded(content string) bool {
	trimmed := strings.TrimSpace(content)
	switch trimmed {
	case "", permissionDeniedByUser, toolLoopNudge, toolLoopSkippedResult:
		return false
	}
	// A gate refused for any other reason - a detached subagent's prompt that
	// reached nobody - is still a write that never happened.
	if strings.HasPrefix(trimmed, permissionNotGrantedPrefix) {
		return false
	}
	return !strings.HasPrefix(trimmed, "error:")
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

// parseListingResult reads what a placeholder and the staleness check need off a
// listing call. Arguments that do not parse leave the fields empty: the result
// is still evicted, only its label is thinner.
func parseListingResult(msgIdx, step int, call llm.ToolCall, content, cwd string) evListing {
	l := evListing{msgIdx: msgIdx, step: step, tool: call.Name, lines: countLines(content)}
	switch call.Name {
	case "glob":
		var a struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		_ = json.Unmarshal([]byte(call.InputJSON), &a)
		l.label = a.Pattern
		l.root = absPath(a.Path, cwd)
	case "print_tree":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal([]byte(call.InputJSON), &a)
		l.root = absPath(a.Path, cwd)
	case "websearch":
		var a struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal([]byte(call.InputJSON), &a)
		l.label = strings.TrimSpace(a.Query)
	case "webfetch":
		var a struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal([]byte(call.InputJSON), &a)
		l.label = strings.Join(strings.Fields(a.URL), " ")
	}
	return l
}

// countLines is the number of lines in a tool result, not counting the newline
// that ends the last one.
func countLines(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// listingStepWindow returns the steps whose listings stay intact: the last
// keepSteps distinct steps that hold a listing candidate. A step is the
// assistant message that issued the call, so a batch of parallel calls takes one
// place however many results it brings.
func listingStepWindow(listings []evListing, keepSteps int) map[int]struct{} {
	window := make(map[int]struct{})
	if keepSteps <= 0 || len(listings) == 0 {
		return window
	}
	seen := make(map[int]struct{}, len(listings))
	steps := make([]int, 0, len(listings))
	for _, l := range listings {
		if _, ok := seen[l.step]; ok {
			continue
		}
		seen[l.step] = struct{}{}
		steps = append(steps, l.step)
	}
	sort.Ints(steps)
	start := len(steps) - keepSteps
	if start < 0 {
		start = 0
	}
	for _, step := range steps[start:] {
		window[step] = struct{}{}
	}
	return window
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
func addKeepResultPin(msgIdx int, argsJSON, cwd string, readPins *[]evReadPin, grepPins *[]evGrepPin) {
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
		*grepPins = append(*grepPins, evGrepPin{msgIdx: msgIdx, pattern: a.Pattern, searchPath: sp})
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
		msgIdx: msgIdx,
		path:   absPath(a.Path, cwd),
		start:  start,
		end:    end,
		whole:  a.Offset <= 0 && a.Limit <= 0,
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
		if p.msgIdx <= r.msgIdx || !pathsEqual(p.path, r.path) {
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
		if p.msgIdx <= g.msgIdx || p.pattern != g.pattern {
			continue
		}
		if p.searchPath == "" || pathsEqual(p.searchPath, g.searchPath) {
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
		if w.msgIdx > r.msgIdx && pathsRelated(w.path, r.path) {
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
		if pathsRelated(w.path, g.searchPath) {
			return w, true
		}
		for p := range g.outPaths {
			if pathsRelated(w.path, p) {
				return w, true
			}
		}
	}
	return evWrite{}, false
}

// staleListingWrite reports the first successful write after the listing that
// touched its folder, or a folder around it: a created, removed or moved entry
// changes what glob and print_tree would answer now. The web tools have no root
// and are never stale.
func staleListingWrite(l evListing, writes []evWrite) (evWrite, bool) {
	if l.root == "" {
		return evWrite{}, false
	}
	for _, w := range writes {
		if w.msgIdx > l.msgIdx && pathsRelated(w.path, l.root) {
			return w, true
		}
	}
	return evWrite{}, false
}

func pathsEqual(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathsRelated reports whether two mutation/read targets are equal or one is
// nested below the other. This invalidates directory listings and child reads
// when a directory is created, removed, or moved.
func pathsRelated(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}

func pathWithin(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
		dir = strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
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

func listingEvictedPlaceholder(l evListing, cwd string) string {
	switch l.tool {
	case "glob":
		return fmt.Sprintf("[evicted: glob %q in %s (%d lines); re-run if needed]",
			capRunes(l.label, listingLabelMax), relForDisplay(l.root, cwd), l.lines)
	case "print_tree":
		return fmt.Sprintf("[evicted: print_tree of %s (%d lines); re-run if needed]",
			relForDisplay(l.root, cwd), l.lines)
	case "websearch":
		return fmt.Sprintf("[evicted: websearch %q (%d lines); re-run if needed]",
			capRunes(l.label, listingLabelMax), l.lines)
	case "webfetch":
		return fmt.Sprintf("[evicted: webfetch %s (%d lines); re-fetch if needed]",
			capRunes(l.label, listingLabelMax), l.lines)
	default:
		return fmt.Sprintf("[evicted: %s result (%d lines); re-run if needed]", l.tool, l.lines)
	}
}

// listingStalePlaceholder is for the two tools that have a root: only a folder
// listing can go stale.
func listingStalePlaceholder(l evListing, w evWrite, cwd string) string {
	if l.tool == "glob" {
		return fmt.Sprintf("[evicted: glob %q in %s is stale after %s was modified; re-run if needed]",
			capRunes(l.label, listingLabelMax), relForDisplay(l.root, cwd), relForDisplay(w.path, cwd))
	}
	return fmt.Sprintf("[evicted: %s of %s is stale after %s was modified; re-run if needed]",
		l.tool, relForDisplay(l.root, cwd), relForDisplay(w.path, cwd))
}
