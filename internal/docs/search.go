package docs

import (
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Hit is one section a search found.
type Hit struct {
	Slug  string
	Title string
	Group string
	// Anchor and Heading name the section; both are empty for the part of
	// a page above its first heading, whose title is the page's.
	Anchor  string
	Heading string
	Snippet []Fragment
	Score   float64
}

// Ref is the page and section of the hit, as coddy_docs_read takes it.
func (h Hit) Ref() string { return Ref(h.Slug, h.Anchor) }

// SnippetText is the snippet without its highlighting.
func (h Hit) SnippetText() string {
	var b strings.Builder
	for _, f := range h.Snippet {
		b.WriteString(f.Text)
	}
	return b.String()
}

// Fragment is a piece of a snippet; Hit marks a word the query matched.
type Fragment struct {
	Text string `json:"text"`
	Hit  bool   `json:"hit,omitempty"`
}

// The fields a section is scored on, BM25F style: the words of the page
// title, of the section heading and of the body, each with its own weight
// and length normalisation (see Ranker).
const (
	fieldTitle = iota
	fieldHeading
	fieldBody
	fieldCount
)

var fieldWeight = [fieldCount]float64{3.0, 6.0, 1.0}

// perPage caps the sections of one page in a result, so a long page does not
// push every other page out.
const perPage = 3

type unit struct {
	page    *Page
	anchor  string
	heading string
	text    string
}

type index struct {
	units []unit
	rank  *Ranker
}

func (l *Library) index() *index {
	l.indexOnce.Do(func() { l.idx = buildIndex(l.pages) })
	return l.idx
}

// unitSource is a section before it is tokenized.
type unitSource struct {
	page    *Page
	anchor  string
	heading string
	summary string
	lines   []string
}

func buildIndex(pages []*Page) *index {
	var sources []unitSource
	for _, p := range pages {
		// The part above the first section heading speaks for the whole
		// page: its heading field is the page title and the map's summary
		// joins its body.
		first := len(p.lines)
		for _, h := range p.Headings {
			if h.Level > 1 {
				first = h.Line
				break
			}
		}
		var intro []string
		for _, line := range p.lines[:first] {
			if !strings.HasPrefix(line, "# ") {
				intro = append(intro, line)
			}
		}
		sources = append(sources, unitSource{page: p, heading: "", summary: p.Summary, lines: intro})
		for i, h := range p.Headings {
			if h.Level < 2 {
				continue
			}
			end := len(p.lines)
			if i+1 < len(p.Headings) {
				end = p.Headings[i+1].Line
			}
			sources = append(sources, unitSource{page: p, anchor: h.Anchor, heading: h.Text, lines: p.lines[h.Line+1 : end]})
		}
	}

	// Tokenizing is most of the work and every section is independent, so
	// it runs on every core; the sections are added to the ranker in order
	// afterwards.
	units := make([]unit, len(sources))
	terms := make([]Terms, len(sources))
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(sources); i += workers {
				units[i], terms[i] = tokenizeUnit(sources[i])
			}
		}(w)
	}
	wg.Wait()

	idx := &index{units: units, rank: NewRanker(fieldWeight[:]...)}
	for _, t := range terms {
		idx.rank.Add(t)
	}
	return idx
}

func tokenizeUnit(src unitSource) (unit, Terms) {
	body := plainText(src.lines)
	if src.summary != "" {
		body = strings.TrimSpace(src.summary + " " + body)
	}
	heading := src.heading
	if src.anchor == "" {
		heading = src.page.Title
	}
	// The order of the fields is the order of fieldWeight.
	return unit{page: src.page, anchor: src.anchor, heading: src.heading, text: body},
		CountTerms(src.page.Title, heading, body)
}

// Search ranks the sections of the documentation against a query with
// BM25F over the page title, the section heading and the body. Words are
// matched case-insensitively after stemming (a light English one for Latin
// words, Snowball for Russian ones, ё read as е), and a query
// word of three letters or more also finds the words it begins, so a query
// typed letter by letter finds pages before it is finished. At most three
// sections of one page are returned; limit 0 means ten.
func (l *Library) Search(query string, limit int) []Hit {
	if limit <= 0 {
		limit = 10
	}
	idx := l.index()
	ranked, matched := idx.rank.Rank(query)
	var out []Hit
	seen := map[*Page]int{}
	for _, r := range ranked {
		u := &idx.units[r.Doc]
		if seen[u.page] >= perPage {
			continue
		}
		seen[u.page]++
		out = append(out, Hit{
			Slug:    u.page.Slug,
			Title:   u.page.Title,
			Group:   u.page.Group.Title,
			Anchor:  u.anchor,
			Heading: u.heading,
			Snippet: snippet(u.text, matched),
			Score:   r.Score,
		})
		if len(out) == limit {
			break
		}
	}
	return out
}

const (
	snippetWords = 32
	snippetChars = 260
)

// snippet picks the run of words of a section's text that holds the most
// matched words and marks them.
func snippet(text string, hit map[string]bool) []Fragment {
	words := strings.Fields(text)
	if len(words) == 0 {
		// A section whose text is all in its subsections: an empty snippet,
		// which a client reads as a list like any other.
		return []Fragment{}
	}
	isHit := make([]bool, len(words))
	for i, w := range words {
		for _, t := range indexTokens(w) {
			if hit[t] {
				isHit[i] = true
				break
			}
		}
	}
	start, best := 0, -1
	for s := 0; s < len(words); s++ {
		n := 0
		for i := s; i < len(words) && i < s+snippetWords; i++ {
			if isHit[i] {
				n++
			}
		}
		if n > best {
			start, best = s, n
		}
		if s+snippetWords >= len(words) {
			break
		}
	}
	// Start a little before the first hit so the words around it read.
	for back := 0; back < 4 && start > 0 && !isHit[start]; back++ {
		start--
	}
	var frags []Fragment
	if start > 0 {
		frags = append(frags, Fragment{Text: "… "})
	}
	chars := 0
	end := start
	for i := start; i < len(words) && i < start+snippetWords && chars < snippetChars; i++ {
		w := words[i]
		if i > start {
			w = " " + w
		}
		if isHit[i] {
			if i > start {
				frags = appendText(frags, " ")
				w = words[i]
			}
			// Only the word is marked, not the punctuation around it.
			lead, core, trail := splitWord(w)
			frags = appendText(frags, lead)
			frags = append(frags, Fragment{Text: core, Hit: true})
			frags = appendText(frags, trail)
		} else {
			frags = appendText(frags, w)
		}
		chars += utf8.RuneCountInString(w)
		end = i + 1
	}
	if end < len(words) {
		frags = appendText(frags, " …")
	}
	return frags
}

// splitWord cuts a whitespace-delimited word into the punctuation before
// it, the word and the punctuation after it: "(proxy)." is "(", "proxy", ").".
func splitWord(w string) (string, string, string) {
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	start := strings.IndexFunc(w, isWord)
	if start < 0 {
		return "", w, ""
	}
	end := strings.LastIndexFunc(w, isWord)
	_, size := utf8.DecodeRuneInString(w[end:])
	return w[:start], w[start : end+size], w[end+size:]
}

func appendText(frags []Fragment, s string) []Fragment {
	if s == "" {
		return frags
	}
	if n := len(frags); n > 0 && !frags[n-1].Hit {
		frags[n-1].Text += s
		return frags
	}
	return append(frags, Fragment{Text: s})
}
