//go:build cli

package cli

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// mentionListLimit is how many "@" candidates the list holds; the total the
// search counted is shown next to the scroll position.
const mentionListLimit = 50

// mentionSearchTimeout bounds a search the console waits for in-process.
const mentionSearchTimeout = 750 * time.Millisecond

// mentionSearchFunc answers the text after "@": session.Manager.SearchMentions
// in-process, the server's GET /coddy/mentions in remote mode.
type mentionSearchFunc func(ctx context.Context, query string, refresh bool) (session.MentionSearchResult, error)

// completionProvider serves slash-command and "@" mention suggestions to the
// editor. Mentions come from the same search the web UI uses, so the console
// ranks the whole workspace against what was typed, sees files written since
// it started, and completes absolute and home paths by browsing them.
type completionProvider struct {
	// commands returns the current slash catalog (server + client merged).
	commands func() []tui.AutocompleteItem
	search   mentionSearchFunc
	// async is set in remote mode: a search goes over the network, so it runs
	// on a worker and its answer reaches the editor through post.
	async bool
	// post runs fn on the UI loop; refresh asks the editor to recompute its
	// suggestions. Both may be nil (tests).
	post    func(fn func())
	refresh func()

	mu sync.Mutex
	// token is the mention being completed: where its "@" is, and the query
	// last asked for it. A new token asks the search to refresh the index.
	tokenLine, tokenCol int
	tokenOpen           bool
	query               string
	dismissed           bool
	total               int
	// cache holds the answers of the current token by query (remote mode).
	cache   map[string]session.MentionSearchResult
	pending map[string]bool
	last    []tui.AutocompleteItem

	unsubscribe func()
}

func newCompletionProvider(commands func() []tui.AutocompleteItem, search mentionSearchFunc, async bool) *completionProvider {
	return &completionProvider{commands: commands, search: search, async: async}
}

// watchIndex re-asks the search when a workspace index build lands while a
// mention is being completed, so a list opened on a stale index catches up
// without another keystroke.
func (p *completionProvider) watchIndex(post func(fn func()), refresh func()) {
	p.post, p.refresh = post, refresh
	if p.async || post == nil {
		return
	}
	p.unsubscribe = mention.DefaultIndexes().Subscribe(func(string) {
		p.mu.Lock()
		active := p.tokenOpen && !p.dismissed
		p.mu.Unlock()
		if active {
			post(func() {
				if p.refresh != nil {
					p.refresh()
				}
			})
		}
	})
}

// Close drops the index subscription.
func (p *completionProvider) Close() {
	if p.unsubscribe != nil {
		p.unsubscribe()
		p.unsubscribe = nil
	}
}

// Suggestions implements tui.AutocompleteProvider.
func (p *completionProvider) Suggestions(lines []string, cursorLine, cursorCol int, force bool) []tui.AutocompleteItem {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return nil
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]

	// Slash commands: only on the first line, message starting with "/".
	// An exact value match sorts first so enter picks it over longer
	// prefix-sharing commands (pi behavior: an exact name wins over the longer
	// names it prefixes).
	if cursorLine == 0 && strings.HasPrefix(line, "/") && !strings.Contains(before, " ") {
		p.closeToken()
		prefix := strings.ToLower(strings.TrimPrefix(before, "/"))
		var exact, rest []tui.AutocompleteItem
		for _, item := range p.commands() {
			lowered := strings.ToLower(item.Value)
			if lowered == prefix {
				exact = append(exact, item)
			} else if strings.HasPrefix(lowered, prefix) {
				rest = append(rest, item)
			}
		}
		return append(exact, rest...)
	}

	if at, query, ok := mentionTokenAt(before); ok {
		return p.mentionSuggestions(cursorLine, at, query)
	}
	p.closeToken()
	if force {
		// tab on a bare word completes it as a path, without the "@".
		tokenStart := strings.LastIndexAny(before, " \t") + 1
		items := p.mentionSuggestions(cursorLine, -1-tokenStart, before[tokenStart:])
		for i := range items {
			items[i].Value = strings.TrimPrefix(items[i].Value, "@")
		}
		return items
	}
	return nil
}

// mentionTokenAt finds the "@" mention the cursor is in: an "@" that opens
// the line or follows whitespace, an opening bracket or a quote, followed by
// no whitespace - or by an opened quote, which may hold spaces until it
// closes. It returns the offset of the "@" and the text after it.
func mentionTokenAt(before string) (int, string, bool) {
	for i := len(before) - 1; i >= 0; i-- {
		if before[i] != '@' {
			continue
		}
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(before[:i])
			if !unicode.IsSpace(prev) && !strings.ContainsRune(`([{"'`, prev) {
				continue
			}
		}
		query := before[i+1:]
		if strings.HasPrefix(query, `"`) {
			if strings.Contains(query[1:], `"`) {
				// The quote closed: the mention is finished.
				return 0, "", false
			}
			return i, query, true
		}
		if strings.ContainsAny(query, " \t") {
			return 0, "", false
		}
		if strings.Count(before[:i], "`")%2 == 1 {
			return 0, "", false
		}
		return i, query, true
	}
	return 0, "", false
}

func (p *completionProvider) closeToken() {
	p.mu.Lock()
	p.tokenOpen = false
	p.query = ""
	p.cache, p.pending, p.last = nil, nil, nil
	p.mu.Unlock()
}

func (p *completionProvider) mentionSuggestions(line, at int, query string) []tui.AutocompleteItem {
	if p.search == nil {
		return nil
	}
	p.mu.Lock()
	fresh := !p.tokenOpen || p.tokenLine != line || p.tokenCol != at
	if fresh {
		p.tokenOpen, p.tokenLine, p.tokenCol = true, line, at
		p.cache, p.pending, p.last = map[string]session.MentionSearchResult{}, map[string]bool{}, nil
		p.dismissed = false
	}
	p.query = query
	p.mu.Unlock()

	if !p.async {
		ctx, cancel := context.WithTimeout(context.Background(), mentionSearchTimeout)
		defer cancel()
		res, err := p.search(ctx, query, fresh)
		if err != nil {
			return nil
		}
		items := mentionItems(res)
		p.mu.Lock()
		p.total = res.Total
		p.last = items
		p.mu.Unlock()
		return items
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if res, ok := p.cache[query]; ok {
		items := mentionItems(res)
		p.total, p.last = res.Total, items
		return items
	}
	if !p.pending[query] {
		p.pending[query] = true
		go p.fetch(line, at, query, fresh)
	}
	// Until the answer lands the list keeps what the previous keystroke
	// showed, instead of flickering shut.
	return p.last
}

// fetch runs one remote search and hands the answer to the UI loop.
func (p *completionProvider) fetch(line, at int, query string, refresh bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := p.search(ctx, query, refresh)
	land := func() {
		p.mu.Lock()
		current := p.tokenOpen && p.tokenLine == line && p.tokenCol == at
		if current && err == nil {
			p.cache[query] = res
		}
		if current {
			delete(p.pending, query)
		}
		redraw := current && err == nil && p.query == query && !p.dismissed
		p.mu.Unlock()
		if redraw && p.refresh != nil {
			p.refresh()
		}
	}
	if p.post != nil {
		p.post(land)
	} else {
		land()
	}
	if refresh && err == nil {
		// The server answered from the index it had and rebuilds it in the
		// background; ask once more so a file written a moment ago shows up.
		time.AfterFunc(400*time.Millisecond, func() {
			p.mu.Lock()
			still := p.tokenOpen && p.tokenLine == line && p.tokenCol == at && p.query == query
			if still {
				delete(p.cache, query)
				p.pending[query] = true
			}
			p.mu.Unlock()
			if still {
				p.fetch(line, at, query, false)
			}
		})
	}
}

// mentionItems turns search candidates into list rows. The value is what
// replaces the typed token; a folder or a scheme hint ends in "/" or ":", and
// Apply leaves the list open after it.
func mentionItems(res session.MentionSearchResult) []tui.AutocompleteItem {
	items := make([]tui.AutocompleteItem, 0, len(res.Items))
	for _, c := range res.Items {
		label := tui.SanitizeText(c.Label)
		if c.Kind == session.MentionKindScheme {
			label = "@" + label
		}
		// The second column names the kind; a path's label already is the whole
		// path, so only a meta row adds its detail (a session's id and date, a
		// rule's description, a documentation page's title).
		desc := mentionKindWord(c.Kind)
		switch c.Kind {
		case mention.KindSession, mention.KindRule, mention.KindAgent, mention.KindDoc, session.MentionKindScheme:
			if d := tui.SanitizeText(c.Detail); d != "" {
				desc += " · " + d
			}
		}
		pathLike := c.Kind == mention.KindFile || c.Kind == mention.KindDirectory || c.Kind == mention.KindPlan
		items = append(items, tui.AutocompleteItem{Value: c.Insert, Label: label, Description: desc, TruncateLeft: pathLike})
	}
	return items
}

// mentionKindWord is how the console names a candidate's kind.
func mentionKindWord(kind string) string {
	switch kind {
	case mention.KindDirectory:
		return "folder"
	case mention.KindAgent:
		return "subagent"
	case session.MentionKindScheme:
		return "search"
	case mention.KindDoc:
		return "docs"
	case "":
		return "file"
	}
	return kind
}

// SuggestionsTotal implements tui.AutocompleteTotaler: how many candidates
// matched before the list was cut, shown as "(3/50 of 1204)".
func (p *completionProvider) SuggestionsTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tokenOpen {
		return 0
	}
	return p.total
}

// AutocompleteDismissed implements tui.AutocompleteDismisser: escape closed
// the list, so an answer landing late does not reopen it.
func (p *completionProvider) AutocompleteDismissed() {
	p.mu.Lock()
	p.dismissed = true
	p.mu.Unlock()
}

// Apply implements tui.AutocompleteProvider: replaces the active token.
func (p *completionProvider) Apply(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem) ([]string, int, int) {
	if cursorLine < 0 || cursorLine >= len(lines) {
		return lines, cursorLine, cursorCol
	}
	line := lines[cursorLine]
	if cursorCol > len(line) {
		cursorCol = len(line)
	}
	before := line[:cursorCol]
	after := line[cursorCol:]

	if cursorLine == 0 && strings.HasPrefix(line, "/") && !strings.Contains(before, " ") {
		newBefore := "/" + item.Value + " "
		lines[0] = newBefore + after
		return lines, 0, len(newBefore)
	}

	tokenStart := strings.LastIndexAny(before, " \t") + 1
	value := item.Value
	quotedValue := strings.HasPrefix(value, `@"`)
	if at, query, ok := mentionTokenAt(before); ok {
		tokenStart = at
		if quotedValue && strings.HasPrefix(query, `"`) {
			// The quote a folder step closed ahead of the cursor belongs to
			// this token: the new value brings its own.
			after = strings.TrimPrefix(after, `"`)
		}
	}
	newBefore := before[:tokenStart] + value
	if quotedValue && !strings.HasSuffix(value, `"`) {
		// A quoted folder is a step inside an open quote. Close it ahead of
		// the cursor, so the text names the folder even if no file follows.
		lines[cursorLine] = newBefore + `"` + after
		return lines, cursorLine, len(newBefore)
	}
	// A folder or a scheme hint is a step, not a finished mention: no space,
	// so the next keystroke goes on inside it.
	if !strings.HasSuffix(value, "/") && !strings.HasSuffix(value, `\`) && !strings.HasSuffix(value, ":") {
		if strings.HasPrefix(after, " ") {
			lines[cursorLine] = newBefore + after
			return lines, cursorLine, len(newBefore) + 1
		}
		newBefore += " "
	}
	lines[cursorLine] = newBefore + after
	return lines, cursorLine, len(newBefore)
}
