package session

// Completion for "@": one search the console, the web UI and a remote console
// share (GET /coddy/mentions), so every surface offers the same candidates in
// the same order - the files and folders of the session's workspace ranked
// against what was typed, a folder anywhere on disk browsed by the path typed
// so far, and the meta references.

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// Mention candidate kinds beyond the attachment kinds: a scheme hint row
// ("@session:") that narrows the picker to that kind.
const MentionKindScheme = "scheme"

// Default and upper bound of candidates one search returns.
const (
	DefaultMentionLimit = 50
	MaxMentionLimit     = 200
)

// mentionIndexFirstWait is how long a search waits for the first index of a
// workspace; later searches answer from the last build at once.
const mentionIndexFirstWait = 1500 * time.Millisecond

// MentionSearch is one completion request.
type MentionSearch struct {
	// SessionID names the session the draft belongs to; its workspace, rules
	// and bundle answer. Empty searches CWD with no session behind it.
	SessionID string
	CWD       string
	// Query is what follows the "@" in the draft, possibly opening with a
	// quote ("@\"my no").
	Query string
	Limit int
	// Refresh rebuilds the workspace index even when the last build is fresh:
	// what a surface asks for when the picker opens, so a file written a
	// moment ago is offered.
	Refresh bool
	// Wait bounds how long the first search of a workspace waits for its
	// index (0: mentionIndexFirstWait). The console waits little and redraws
	// when the build lands; an HTTP client has nothing to redraw with.
	Wait time.Duration
}

// MentionCandidate is one row a picker offers.
type MentionCandidate struct {
	// Kind is a mention kind (file, directory, session, rule, agent, plan) or
	// "scheme" for a hint that narrows the search.
	Kind string `json:"kind"`
	// Insert is the text that replaces "@" plus the query in the draft,
	// the "@" included: "@src/app.go", "@\"notes/my draft.md\"",
	// "@session:sess_1". Add a space after it unless Continue is set.
	Insert string `json:"insert"`
	// Label is what the row shows; Detail the dimmer second column.
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	// Continue says choosing the row keeps the picker open: a folder to look
	// into, or a scheme hint.
	Continue bool `json:"continue,omitempty"`
}

// MentionSearchResult is the answer to one completion request.
type MentionSearchResult struct {
	Items []MentionCandidate `json:"items"`
	// Total counts every match before the list was cut to the limit, so a
	// surface can say how many more there are.
	Total int `json:"total"`
	// Indexing says the workspace index is still being built: the list may
	// grow, and a surface asks again (or waits for the next keystroke).
	Indexing bool `json:"indexing,omitempty"`
	// IndexTruncated says the workspace holds more entries than the index
	// keeps (mention.MaxIndexEntries).
	IndexTruncated bool `json:"index_truncated,omitempty"`
}

// SearchMentions answers a completion request. It does not fail: a folder
// that cannot be read or a store that is not there yields fewer candidates.
// The error is there for the remote client's twin (internal/remote).
func (m *Manager) SearchMentions(ctx context.Context, req MentionSearch) (MentionSearchResult, error) {
	return m.searchMentions(ctx, req), nil
}

func (m *Manager) searchMentions(ctx context.Context, req MentionSearch) MentionSearchResult {
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultMentionLimit
	}
	if limit > MaxMentionLimit {
		limit = MaxMentionLimit
	}
	cwd := strings.TrimSpace(req.CWD)
	var st *State
	if m != nil && strings.TrimSpace(req.SessionID) != "" {
		st = m.getSession(strings.TrimSpace(req.SessionID))
	}
	if st != nil {
		cwd = st.GetCWD()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	q := strings.TrimPrefix(req.Query, `"`)
	q = strings.TrimSuffix(q, `"`)
	quoted := strings.HasPrefix(req.Query, `"`)

	if scheme, rest, ok := strings.Cut(q, ":"); ok {
		if sc, known := mention.ParseScheme(scheme); known {
			return m.searchScheme(ctx, st, cwd, sc, rest, limit)
		}
	}
	if browsesDirectory(q) {
		return browseMentions(cwd, q, quoted, limit)
	}
	wait := req.Wait
	if wait <= 0 {
		wait = mentionIndexFirstWait
	}
	return m.searchWorkspace(st, cwd, q, limit, req.Refresh, wait)
}

// browsesDirectory reports whether a query names a folder to look into
// rather than a workspace path to search: an absolute path, "~", "./", "../"
// or a Windows drive.
func browsesDirectory(q string) bool {
	switch {
	case strings.HasPrefix(q, "/"), strings.HasPrefix(q, `\`), q == "~", strings.HasPrefix(q, "~/"), strings.HasPrefix(q, `~\`),
		strings.HasPrefix(q, "./"), strings.HasPrefix(q, "../"), q == "..", strings.HasPrefix(q, `.\`), strings.HasPrefix(q, `..\`):
		return true
	}
	return len(q) >= 2 && q[1] == ':' && isDriveLetter(q[0])
}

func isDriveLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// browseMentions lists the folder the query has typed so far - everything up
// to its last separator - filtered by the name after it, the way a shell
// completes a path. Folders come first and keep the picker open.
func browseMentions(cwd, q string, quoted bool, limit int) MentionSearchResult {
	cut := strings.LastIndexAny(q, `/\`)
	dirTyped, prefix := "", q
	if cut >= 0 {
		dirTyped, prefix = q[:cut+1], q[cut+1:]
	} else if q == "~" || q == ".." {
		dirTyped, prefix = q+"/", ""
	}
	loc, ok := mention.Resolve(cwd, mention.HomeDir(), dirTyped)
	if !ok {
		return MentionSearchResult{Items: []MentionCandidate{}}
	}
	entries, err := os.ReadDir(loc.Abs)
	if err != nil {
		return MentionSearchResult{Items: []MentionCandidate{}}
	}
	if dirTyped == "~" || dirTyped == ".." {
		dirTyped += "/"
	}
	lowered := strings.ToLower(prefix)
	var dirs, files []MentionCandidate
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if lowered != "" && !strings.HasPrefix(strings.ToLower(name), lowered) {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(loc.Abs, name)); err == nil && info.IsDir() {
				isDir = true
			}
		}
		typed := dirTyped + name
		if isDir {
			typed += "/"
			dirs = append(dirs, MentionCandidate{Kind: mention.KindDirectory, Insert: insertForPath(typed, quoted), Label: typed, Continue: true})
			continue
		}
		files = append(files, MentionCandidate{Kind: mention.KindFile, Insert: insertForPath(typed, quoted), Label: typed})
	}
	items := append(dirs, files...)
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []MentionCandidate{}
	}
	return MentionSearchResult{Items: items, Total: total}
}

// insertForPath is the draft text for a path: quoted when it holds a space,
// or when the user had opened a quote.
func insertForPath(p string, quoted bool) string {
	if quoted || strings.ContainsAny(p, " \t") {
		if strings.HasSuffix(p, "/") {
			// A folder keeps the quote open so the user can go on typing in it.
			return `@"` + p
		}
		return `@"` + p + `"`
	}
	return "@" + p
}

// searchWorkspace ranks the workspace index against the query and merges in
// the rules, subagents and plans whose names match it. An empty query offers
// the scheme hints and the top of the workspace.
func (m *Manager) searchWorkspace(st *State, cwd, q string, limit int, refresh bool, wait time.Duration) MentionSearchResult {
	idx := mention.DefaultIndexes()
	var snap mention.Snapshot
	if refresh {
		snap = idx.Refresh(cwd, wait)
	} else {
		snap = idx.Get(cwd, wait)
	}
	res := MentionSearchResult{Indexing: snap.Building && len(snap.Entries) == 0, IndexTruncated: snap.Truncated}
	quoted := false
	query := strings.TrimSpace(strings.TrimPrefix(q, "./"))
	if query == "" {
		var items []MentionCandidate
		for _, sc := range mention.Schemes {
			if sc == mention.SchemeSession && !m.sessionsMentionable(st) {
				continue
			}
			items = append(items, MentionCandidate{Kind: MentionKindScheme, Insert: "@" + string(sc) + ":", Label: string(sc) + ":", Detail: schemeHint(sc), Continue: true})
		}
		var top []mention.Entry
		for _, e := range snap.Entries {
			if !strings.Contains(strings.TrimSuffix(e.Path, "/"), "/") {
				top = append(top, e)
			}
		}
		sort.SliceStable(top, func(i, j int) bool {
			if top[i].Dir != top[j].Dir {
				return top[i].Dir
			}
			return top[i].Path < top[j].Path
		})
		res.Total = len(items) + len(top)
		for _, e := range top {
			if len(items) >= limit {
				break
			}
			items = append(items, entryCandidate(e, quoted))
		}
		res.Items = items
		return res
	}

	type scored struct {
		c     MentionCandidate
		score int
		key   string
	}
	var all []scored
	ranked, total := mention.Rank(query, snap.Entries, func(e mention.Entry) string { return e.Path }, limit)
	for _, r := range ranked {
		all = append(all, scored{c: entryCandidate(r.Item, quoted), score: r.Score, key: r.Key})
	}
	res.Total = total
	for _, c := range m.metaCandidates(st, cwd) {
		s, ok := mention.Score(query, c.Label)
		if !ok {
			continue
		}
		res.Total++
		all = append(all, scored{c: c, score: s, key: c.Label})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return false
	})
	if len(all) > limit {
		all = all[:limit]
	}
	res.Items = make([]MentionCandidate, 0, len(all))
	for _, s := range all {
		res.Items = append(res.Items, s.c)
	}
	return res
}

func entryCandidate(e mention.Entry, quoted bool) MentionCandidate {
	c := MentionCandidate{Kind: mention.KindFile, Insert: insertForPath(e.Path, quoted), Label: e.Path, Detail: parentOf(e.Path)}
	if e.Dir {
		c.Kind, c.Continue = mention.KindDirectory, true
	}
	return c
}

// parentOf is the folder a path lives in, "" at the top of the workspace.
func parentOf(p string) string {
	dir := path.Dir(strings.TrimSuffix(p, "/"))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir + "/"
}

func schemeHint(sc mention.Scheme) string {
	switch sc {
	case mention.SchemeSession:
		return "another session"
	case mention.SchemeRule:
		return "a project rule"
	case mention.SchemeAgent:
		return "a subagent"
	case mention.SchemeCoddy:
		return "a page of Coddy's documentation"
	}
	return ""
}

// metaCandidates are the rules, subagents and plans a scheme-less query may
// also match by name. Sessions are listed under "session:" only: listing
// them reads every bundle, which is not a per-keystroke cost.
func (m *Manager) metaCandidates(st *State, cwd string) []MentionCandidate {
	var out []MentionCandidate
	out = append(out, m.ruleCandidates(st, cwd)...)
	out = append(out, m.agentCandidates(cwd)...)
	if st != nil {
		if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" {
			if docs, err := plans.List(sd); err == nil {
				for _, d := range docs {
					rel := plans.DirName + "/" + d.Slug + plans.FileSuffix
					out = append(out, MentionCandidate{Kind: mention.KindPlan, Insert: "@" + rel, Label: rel, Detail: d.Name})
				}
			}
		}
	}
	return out
}

func (m *Manager) ruleCandidates(st *State, cwd string) []MentionCandidate {
	var catalog []*rules.Rule
	if st != nil {
		catalog = st.GetRulesCatalog()
	} else if m != nil {
		catalog = DiscoverRules(m.Cfg(), cwd)
	}
	var out []MentionCandidate
	// A rule kept in two trees (.cursor/rules/x.mdc, .claude/rules/x.md) is
	// one name to "@rule:", which reads the first in catalog order: offer
	// that one, once.
	seen := map[string]bool{}
	for _, r := range catalog {
		if r == nil || r.ScopeDir != "" {
			continue
		}
		name := r.CanonicalName()
		if seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, MentionCandidate{Kind: mention.KindRule, Insert: "@rule:" + name, Label: name, Detail: firstLine(strings.TrimSpace(r.Description))})
	}
	return out
}

func (m *Manager) agentCandidates(cwd string) []MentionCandidate {
	if m == nil {
		return nil
	}
	agents, _ := m.mentionAgents(cwd, false)
	out := make([]MentionCandidate, 0, len(agents))
	for _, a := range agents {
		detail := strings.TrimSpace(a.Description)
		if a.Blocked != "" {
			detail = "awaiting approval"
		}
		out = append(out, MentionCandidate{Kind: mention.KindAgent, Insert: "@agent:" + a.Name, Label: a.Name, Detail: firstLine(detail)})
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// sessionsMentionable mirrors mentionScope: a messenger chat does not reach
// into other sessions.
func (m *Manager) sessionsMentionable(st *State) bool {
	if m == nil || m.store == nil {
		return false
	}
	return st == nil || !IsGatewayOrigin(st.GetOrigin())
}

// searchScheme lists one kind: "@session:", "@rule:", "@agent:", "@coddy:".
func (m *Manager) searchScheme(_ context.Context, st *State, cwd string, sc mention.Scheme, q string, limit int) MentionSearchResult {
	if sc == mention.SchemeCoddy {
		return docCandidates(q, limit)
	}
	var items []MentionCandidate
	switch sc {
	case mention.SchemeSession:
		items = m.sessionCandidates(st, cwd)
	case mention.SchemeRule:
		items = m.ruleCandidates(st, cwd)
	case mention.SchemeAgent:
		items = m.agentCandidates(cwd)
	}
	q = strings.TrimSpace(q)
	if q != "" {
		type scored struct {
			c MentionCandidate
			s int
		}
		var hits []scored
		for _, c := range items {
			// A session matches by its title or by its id.
			s1, ok1 := mention.Score(q, c.Label)
			s2, ok2 := mention.Score(q, strings.TrimPrefix(c.Insert, "@"+string(sc)+":"))
			switch {
			case ok1 && (!ok2 || s1 >= s2):
				hits = append(hits, scored{c, s1})
			case ok2:
				hits = append(hits, scored{c, s2})
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
		items = items[:0]
		for _, h := range hits {
			items = append(items, h.c)
		}
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []MentionCandidate{}
	}
	return MentionSearchResult{Items: items, Total: total}
}

// docCandidates completes "@coddy:": the pages of the built-in documentation
// in map order for an empty query; the pages whose slug or title the query
// matches, then the sections its words find; the sections of one page once
// the query names it and a "#".
func docCandidates(q string, limit int) MentionSearchResult {
	lib, err := docs.Default()
	if err != nil {
		return MentionSearchResult{Items: []MentionCandidate{}}
	}
	pageRow := func(p *docs.Page) MentionCandidate {
		return MentionCandidate{Kind: mention.KindDoc, Insert: "@coddy:" + p.Slug, Label: p.Slug, Detail: p.Title}
	}
	sectionRow := func(p *docs.Page, anchor, heading string) MentionCandidate {
		ref := docs.Ref(p.Slug, anchor)
		return MentionCandidate{Kind: mention.KindDoc, Insert: "@coddy:" + ref, Label: ref, Detail: p.Title + " › " + heading}
	}
	q = strings.TrimSpace(q)
	var items []MentionCandidate
	switch pageRef, sectionQuery, hasSection := strings.Cut(q, "#"); {
	case q == "":
		for _, p := range lib.Pages() {
			items = append(items, pageRow(p))
		}
	case hasSection:
		page, _, err := lib.Resolve(pageRef)
		if err != nil {
			break
		}
		type scored struct {
			c MentionCandidate
			s int
		}
		var hits []scored
		for _, h := range page.Sections() {
			c := sectionRow(page, h.Anchor, h.Text)
			if sectionQuery == "" {
				hits = append(hits, scored{c, 0})
				continue
			}
			s1, ok1 := mention.Score(sectionQuery, h.Anchor)
			s2, ok2 := mention.Score(sectionQuery, h.Text)
			if ok1 || ok2 {
				hits = append(hits, scored{c, max(s1, s2)})
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
		for _, h := range hits {
			items = append(items, h.c)
		}
	default:
		type scored struct {
			p *docs.Page
			s int
		}
		var pages []scored
		for _, p := range lib.Pages() {
			s1, ok1 := mention.Score(q, p.Slug)
			s2, ok2 := mention.Score(q, p.Title)
			if ok1 || ok2 {
				pages = append(pages, scored{p, max(s1, s2)})
			}
		}
		sort.SliceStable(pages, func(i, j int) bool { return pages[i].s > pages[j].s })
		seen := map[string]bool{}
		for _, h := range pages {
			items = append(items, pageRow(h.p))
			seen[h.p.Slug] = true
		}
		// The words of the query find sections too: "@coddy:proxy" offers
		// the proxy sections of the gateway and configuration pages.
		for _, h := range lib.Search(strings.NewReplacer("/", " ", "-", " ", "_", " ").Replace(q), limit) {
			ref := h.Ref()
			if seen[ref] {
				continue
			}
			seen[ref] = true
			page, _ := lib.Page(h.Slug)
			if h.Anchor == "" {
				items = append(items, pageRow(page))
				continue
			}
			items = append(items, sectionRow(page, h.Anchor, h.Heading))
		}
	}
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []MentionCandidate{}
	}
	return MentionSearchResult{Items: items, Total: total}
}

// mentionSessionCache keeps the session listing for a few seconds: a picker
// asks on every keystroke, and a listing reads every bundle.
type mentionSessionCache struct {
	mu      sync.Mutex
	at      time.Time
	entries []SessionListEntry
}

const mentionSessionCacheTTL = 5 * time.Second

func (m *Manager) listSessionsForMentions() []SessionListEntry {
	if m == nil || m.store == nil {
		return nil
	}
	c := &m.mentionSessions
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries != nil && time.Since(c.at) < mentionSessionCacheTTL {
		return c.entries
	}
	entries, err := m.store.ListSnapshotsWith(ListOptions{})
	if err != nil {
		return nil
	}
	c.entries, c.at = entries, time.Now()
	return entries
}

// sessionCandidates lists the sessions a prompt may mention, the ones of the
// same workspace first, the most recent first within each.
func (m *Manager) sessionCandidates(st *State, cwd string) []MentionCandidate {
	if !m.sessionsMentionable(st) {
		return nil
	}
	self := ""
	if st != nil {
		self = st.GetID()
	}
	entries := append([]SessionListEntry(nil), m.listSessionsForMentions()...)
	canonical := CanonicalWorkspacePath(cwd)
	sort.SliceStable(entries, func(i, j int) bool {
		ai, aj := matchesWorkspace(canonical, entries[i].CWD), matchesWorkspace(canonical, entries[j].CWD)
		if ai != aj {
			return ai
		}
		return false
	})
	out := make([]MentionCandidate, 0, len(entries))
	for _, e := range entries {
		if e.SessionID == self {
			continue
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = "(untitled)"
		}
		detail := e.SessionID
		if when := strings.TrimSpace(e.UpdatedAt); when != "" {
			if t, err := time.Parse(time.RFC3339, when); err == nil {
				detail += " · " + t.Local().Format("2006-01-02 15:04")
			}
		}
		if !matchesWorkspace(canonical, e.CWD) && e.CWD != "" {
			detail += " · " + filepath.Base(e.CWD)
		}
		out = append(out, MentionCandidate{Kind: mention.KindSession, Insert: "@session:" + e.SessionID, Label: title, Detail: detail})
	}
	return out
}
