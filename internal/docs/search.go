package docs

import (
	"math"
	"runtime"
	"sort"
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
// and length normalisation.
const (
	fieldTitle = iota
	fieldHeading
	fieldBody
	fieldCount
)

var fieldWeight = [fieldCount]float64{3.0, 6.0, 1.0}

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// prefixWeight discounts a word the query only begins: "config" finds
	// "configuration", below a section that says "config".
	prefixWeight = 0.7
	// maxExpansions bounds the words one query term may expand to, the
	// most frequent kept.
	maxExpansions = 48
	// perPage caps the sections of one page in a result, so a long page
	// does not push every other page out.
	perPage = 3
)

type unit struct {
	page    *Page
	anchor  string
	heading string
	text    string
	lens    [fieldCount]int
}

type posting struct {
	unit int
	tf   [fieldCount]uint16
}

type index struct {
	units    []unit
	postings map[string][]posting
	vocab    []string
	avgLen   [fieldCount]float64
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

// unitTerms is a tokenized section: its field lengths and the frequency of
// every word in each field.
type unitTerms struct {
	lens  [fieldCount]int
	terms []string
	tfs   [][fieldCount]uint16
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
	// it runs on every core; the postings are merged in order afterwards.
	units := make([]unit, len(sources))
	terms := make([]unitTerms, len(sources))
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

	idx := &index{units: units, postings: map[string][]posting{}}
	var total [fieldCount]int
	for id, ut := range terms {
		for f := range total {
			total[f] += ut.lens[f]
		}
		for k, t := range ut.terms {
			idx.postings[t] = append(idx.postings[t], posting{unit: id, tf: ut.tfs[k]})
		}
	}
	for f := range total {
		if len(idx.units) > 0 {
			idx.avgLen[f] = float64(total[f]) / float64(len(idx.units))
		}
		if idx.avgLen[f] == 0 {
			idx.avgLen[f] = 1
		}
	}
	idx.vocab = make([]string, 0, len(idx.postings))
	for t := range idx.postings {
		idx.vocab = append(idx.vocab, t)
	}
	sort.Strings(idx.vocab)
	return idx
}

func tokenizeUnit(src unitSource) (unit, unitTerms) {
	body := plainText(src.lines)
	if src.summary != "" {
		body = strings.TrimSpace(src.summary + " " + body)
	}
	heading := src.heading
	if src.anchor == "" {
		heading = src.page.Title
	}
	fields := [fieldCount][]string{tokenize(src.page.Title), tokenize(heading), tokenize(body)}
	u := unit{page: src.page, anchor: src.anchor, heading: src.heading, text: body}
	var ut unitTerms
	slot := map[string]int{}
	for f, toks := range fields {
		u.lens[f] = len(toks)
		ut.lens[f] = len(toks)
		for _, t := range toks {
			k, ok := slot[t]
			if !ok {
				k = len(ut.terms)
				slot[t] = k
				ut.terms = append(ut.terms, t)
				ut.tfs = append(ut.tfs, [fieldCount]uint16{})
			}
			if ut.tfs[k][f] < math.MaxUint16 {
				ut.tfs[k][f]++
			}
		}
	}
	return u, ut
}

type expansion struct {
	term   string
	weight float64
}

// expand is the index words a query term stands for: itself, and for a term
// of three letters or more every word it begins.
func (idx *index) expand(q string) []expansion {
	var out []expansion
	if _, ok := idx.postings[q]; ok {
		out = append(out, expansion{q, 1})
	}
	if utf8.RuneCountInString(q) < 3 {
		return out
	}
	i := sort.SearchStrings(idx.vocab, q)
	var more []string
	for ; i < len(idx.vocab) && strings.HasPrefix(idx.vocab[i], q); i++ {
		if idx.vocab[i] != q {
			more = append(more, idx.vocab[i])
		}
	}
	if len(more) > maxExpansions {
		sort.SliceStable(more, func(a, b int) bool { return len(idx.postings[more[a]]) > len(idx.postings[more[b]]) })
		more = more[:maxExpansions]
	}
	for _, t := range more {
		out = append(out, expansion{t, prefixWeight})
	}
	return out
}

// idf is the inverse document frequency of a query term over the sections
// that hold it or a word it expands to.
func (idx *index) idf(df int) float64 {
	n := float64(len(idx.units))
	return math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
}

// saturate is BM25's term frequency curve over the weighted, length
// normalised frequencies of the three fields.
func (idx *index) saturate(tf *[fieldCount]float64, u *unit) float64 {
	var w float64
	for f := 0; f < fieldCount; f++ {
		if tf[f] == 0 {
			continue
		}
		norm := 1 - bm25B + bm25B*float64(u.lens[f])/idx.avgLen[f]
		w += fieldWeight[f] * tf[f] / norm
	}
	return w / (bm25K1 + w)
}

// Search ranks the sections of the documentation against a query with
// BM25F over the page title, the section heading and the body. Words are
// matched case-insensitively after a light English stemming, and a query
// word of three letters or more also finds the words it begins, so a query
// typed letter by letter finds pages before it is finished. At most three
// sections of one page are returned; limit 0 means ten.
func (l *Library) Search(query string, limit int) []Hit {
	if limit <= 0 {
		limit = 10
	}
	terms := dedupe(tokenize(query))
	if len(terms) == 0 {
		return nil
	}
	idx := l.index()
	type acc struct {
		score   float64
		matched int
	}
	scores := map[int]*acc{}
	hitTerms := map[string]bool{}
	for _, q := range terms {
		// A term and the words it begins count as one word: their
		// frequencies add up (the begun ones discounted) and they share one
		// document frequency, so a rare word the query merely starts cannot
		// outrank the common word it spells out.
		tfs := map[int]*[fieldCount]float64{}
		for _, e := range idx.expand(q) {
			for _, p := range idx.postings[e.term] {
				tf := tfs[p.unit]
				if tf == nil {
					tf = &[fieldCount]float64{}
					tfs[p.unit] = tf
				}
				for f := 0; f < fieldCount; f++ {
					tf[f] += e.weight * float64(p.tf[f])
				}
			}
			hitTerms[e.term] = true
		}
		idf := idx.idf(len(tfs))
		for u, tf := range tfs {
			a := scores[u]
			if a == nil {
				a = &acc{}
				scores[u] = a
			}
			a.score += idf * idx.saturate(tf, &idx.units[u])
			a.matched++
		}
	}
	type ranked struct {
		unit  int
		score float64
	}
	var all []ranked
	for u, a := range scores {
		// A section holding every word of the query ranks above one that
		// holds a frequent word many times.
		coverage := float64(a.matched) / float64(len(terms))
		all = append(all, ranked{u, a.score * (0.5 + 0.5*coverage*coverage)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].unit < all[j].unit
	})
	var out []Hit
	seen := map[*Page]int{}
	for _, r := range all {
		u := &idx.units[r.unit]
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
			Snippet: snippet(u.text, hitTerms),
			Score:   r.score,
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
		return nil
	}
	isHit := make([]bool, len(words))
	for i, w := range words {
		for _, t := range tokenize(w) {
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
		chars += len(w)
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

// stopwords carry no meaning for a search of the documentation.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"can": true, "do": true, "does": true, "for": true, "from": true, "has": true, "have": true,
	"how": true, "i": true, "if": true, "in": true, "into": true, "is": true, "it": true, "its": true,
	"me": true, "my": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"their": true, "then": true, "there": true, "these": true, "this": true, "to": true,
	"was": true, "what": true, "when": true, "where": true, "which": true, "who": true,
	"will": true, "with": true, "you": true, "your": true,
}

// tokenize splits text into search words: runs of letters, digits and
// underscores, lower-cased and stemmed, stopwords dropped. An identifier
// with underscores is kept whole and also split, so max_turns is found by
// "max_turns" and by "turns".
func tokenize(s string) []string {
	out := make([]string, 0, len(s)/8+1)
	start := -1
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = appendWord(out, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = appendWord(out, s[start:])
	}
	return out
}

func appendWord(out []string, w string) []string {
	w = lower(strings.Trim(w, "_"))
	if !strings.Contains(w, "_") {
		return appendToken(out, w)
	}
	// An identifier is a name, matched as written; its parts are words.
	out = append(out, w)
	for _, part := range strings.Split(w, "_") {
		out = appendToken(out, part)
	}
	return out
}

func appendToken(out []string, w string) []string {
	if len(w) < 2 || stopwords[w] || (len(w) < 4 && utf8.RuneCountInString(w) < 2) {
		return out
	}
	return append(out, stem(w))
}

// lower is strings.ToLower without the allocation for a word that is
// already lower-case ASCII, which most words of the documentation are.
func lower(w string) string {
	for i := 0; i < len(w); i++ {
		c := w[i]
		if c >= utf8.RuneSelf || (c >= 'A' && c <= 'Z') {
			return strings.ToLower(w)
		}
	}
	return w
}

// stem is the S-stemmer (Harman, 1991): plural endings only, which is
// conservative enough never to merge two unrelated words of a technical
// text, and leaves the rest to prefix matching.
func stem(w string) string {
	if len(w) <= 3 || !isASCII(w) {
		return w
	}
	switch {
	case strings.HasSuffix(w, "ies") && !strings.HasSuffix(w, "eies") && !strings.HasSuffix(w, "aies"):
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "es") && !strings.HasSuffix(w, "aes") && !strings.HasSuffix(w, "ees") && !strings.HasSuffix(w, "oes"):
		return w[:len(w)-1]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
