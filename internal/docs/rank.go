package docs

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// MaxFields is the most fields a Ranker scores a document on.
const MaxFields = 3

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// prefixWeight discounts a word the query only begins: "config" finds
	// "configuration", below a section that says "config".
	prefixWeight = 0.7
	// maxExpansions bounds the words one query term may expand to, the
	// most frequent kept.
	maxExpansions = 48
)

type posting struct {
	doc int
	tf  [MaxFields]uint16
}

// Terms is a document tokenized for a Ranker: the length of each field and
// the frequency of every word in each field. CountTerms makes one and touches
// no shared state, so documents can be counted on every core and added in
// order afterwards.
type Terms struct {
	lens  [MaxFields]int
	words []string
	tfs   [][MaxFields]uint16
}

// CountTerms tokenizes the fields of one document, in the order of the
// weights the Ranker was made with. At most MaxFields fields.
func CountTerms(fields ...string) Terms {
	if len(fields) > MaxFields {
		panic("docs: too many fields for a Ranker")
	}
	var t Terms
	size := 0
	for _, text := range fields {
		size += len(text)
	}
	// A distinct word takes about a dozen bytes of text, repeats included;
	// the map and the lists start near that count, so they rarely grow. The
	// lists are dropped once the document is added.
	hint := size/12 + 1
	slot := make(map[string]int, hint)
	t.words = make([]string, 0, hint)
	t.tfs = make([][MaxFields]uint16, 0, hint)
	for f, text := range fields {
		eachToken(text, true, func(w string) {
			t.lens[f]++
			k, ok := slot[w]
			if !ok {
				k = len(t.words)
				slot[w] = k
				t.words = append(t.words, w)
				t.tfs = append(t.tfs, [MaxFields]uint16{})
			}
			if t.tfs[k][f] < math.MaxUint16 {
				t.tfs[k][f]++
			}
		})
	}
	return t
}

// Ranker ranks documents against a query with BM25F: every document has up
// to MaxFields fields, each with its own weight and its own length
// normalisation, and a query word counts for as much as it is rare. Words
// are matched after stemming (a light English one for Latin words, Snowball
// for Russian ones, ё read as е), and a query word of three letters or more
// also finds the words it begins. One Ranker serves the documentation search
// and the search of the memory notes, so a language is treated the same
// wherever it is searched.
//
// Documents are added with Add and ranked with Rank. Once Rank has run, the
// Ranker is read-only and safe for concurrent use, and Add must not be called
// again.
type Ranker struct {
	weights  [MaxFields]float64
	fields   int
	lens     [][MaxFields]int
	postings map[string][]posting

	seal   sync.Once
	vocab  []string
	avgLen [MaxFields]float64
}

// NewRanker makes a Ranker whose documents have one field per weight, at most
// MaxFields of them.
func NewRanker(weights ...float64) *Ranker {
	if len(weights) == 0 || len(weights) > MaxFields {
		panic("docs: a Ranker has between one and MaxFields fields")
	}
	r := &Ranker{fields: len(weights), postings: map[string][]posting{}}
	copy(r.weights[:], weights)
	return r
}

// Add appends a document and returns its number, the order of the calls.
func (r *Ranker) Add(t Terms) int {
	id := len(r.lens)
	r.lens = append(r.lens, t.lens)
	for k, w := range t.words {
		r.postings[w] = append(r.postings[w], posting{doc: id, tf: t.tfs[k]})
	}
	return id
}

// finish computes what ranking needs from the documents added: the mean
// length of every field and the sorted vocabulary prefix matches look in.
func (r *Ranker) finish() {
	var total [MaxFields]int
	for _, l := range r.lens {
		for f := range total {
			total[f] += l[f]
		}
	}
	for f := range total {
		if len(r.lens) > 0 {
			r.avgLen[f] = float64(total[f]) / float64(len(r.lens))
		}
		if r.avgLen[f] == 0 {
			r.avgLen[f] = 1
		}
	}
	r.vocab = make([]string, 0, len(r.postings))
	for t := range r.postings {
		r.vocab = append(r.vocab, t)
	}
	sort.Strings(r.vocab)
}

type expansion struct {
	term   string
	weight float64
}

// expand is the index words a query term stands for: itself, and for a term
// of three letters or more every word it begins.
func (r *Ranker) expand(q string) []expansion {
	var out []expansion
	if _, ok := r.postings[q]; ok {
		out = append(out, expansion{q, 1})
	}
	if utf8.RuneCountInString(q) < 3 {
		return out
	}
	i := sort.SearchStrings(r.vocab, q)
	var more []string
	for ; i < len(r.vocab) && strings.HasPrefix(r.vocab[i], q); i++ {
		if r.vocab[i] != q {
			more = append(more, r.vocab[i])
		}
	}
	if len(more) > maxExpansions {
		sort.SliceStable(more, func(a, b int) bool { return len(r.postings[more[a]]) > len(r.postings[more[b]]) })
		more = more[:maxExpansions]
	}
	for _, t := range more {
		out = append(out, expansion{t, prefixWeight})
	}
	return out
}

// idf is the inverse document frequency of a query term over the documents
// that hold it or a word it expands to.
func (r *Ranker) idf(df int) float64 {
	n := float64(len(r.lens))
	return math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
}

// saturate is BM25's term frequency curve over the weighted, length
// normalised frequencies of the fields of one document.
func (r *Ranker) saturate(tf *[MaxFields]float64, doc int) float64 {
	var w float64
	for f := 0; f < r.fields; f++ {
		if tf[f] == 0 {
			continue
		}
		norm := 1 - bm25B + bm25B*float64(r.lens[doc][f])/r.avgLen[f]
		w += r.weights[f] * tf[f] / norm
	}
	return w / (bm25K1 + w)
}

// Scored is a document and the score a query gave it.
type Scored struct {
	Doc   int
	Score float64
}

// Rank scores the documents that hold a word of the query, best first, the
// earlier document first among equal scores. It also returns the words of
// the index the query matched, for a caller that marks them in a snippet.
// A query with no word left after the stopwords scores nothing.
func (r *Ranker) Rank(query string) ([]Scored, map[string]bool) {
	terms := dedupe(tokenize(query))
	if len(terms) == 0 {
		return nil, nil
	}
	r.seal.Do(r.finish)
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
		tfs := map[int]*[MaxFields]float64{}
		for _, e := range r.expand(q) {
			for _, p := range r.postings[e.term] {
				tf := tfs[p.doc]
				if tf == nil {
					tf = &[MaxFields]float64{}
					tfs[p.doc] = tf
				}
				for f := 0; f < MaxFields; f++ {
					tf[f] += e.weight * float64(p.tf[f])
				}
			}
			hitTerms[e.term] = true
		}
		idf := r.idf(len(tfs))
		for d, tf := range tfs {
			a := scores[d]
			if a == nil {
				a = &acc{}
				scores[d] = a
			}
			a.score += idf * r.saturate(tf, d)
			a.matched++
		}
	}
	all := make([]Scored, 0, len(scores))
	for d, a := range scores {
		// A document holding every word of the query ranks above one that
		// holds a frequent word many times.
		coverage := float64(a.matched) / float64(len(terms))
		all = append(all, Scored{d, a.score * (0.5 + 0.5*coverage*coverage)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Doc < all[j].Doc
	})
	return all, hitTerms
}

// stopwords carry no meaning for a search of the documentation: English
// function words here, the Russian ones (ruStopwords) added by init.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"can": true, "do": true, "does": true, "for": true, "from": true, "has": true, "have": true,
	"how": true, "i": true, "if": true, "in": true, "into": true, "is": true, "it": true, "its": true,
	"me": true, "my": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"their": true, "then": true, "there": true, "these": true, "this": true, "to": true,
	"was": true, "what": true, "when": true, "where": true, "which": true, "who": true,
	"will": true, "with": true, "you": true, "your": true,
}

func init() {
	for _, w := range ruStopwords {
		stopwords[w] = true
	}
}

// tokenize splits a query into search words: runs of letters, digits and
// underscores, lower-cased and stemmed, stopwords dropped. An identifier
// with underscores is kept whole and also split, so max_turns is found by
// "max_turns" and by "turns".
func tokenize(s string) []string { return tokenizeWith(s, false) }

// indexTokens splits the text of a section the way tokenize splits a query,
// and also keeps a Russian word as written next to its stem. The Snowball
// stemmer cuts some forms of a word further than others (архив is арх,
// архивы is архив; экран is экра, экраны is экран), and the written form is
// what a query stem then finds, exactly or as the beginning of a word.
func indexTokens(s string) []string { return tokenizeWith(s, true) }

func tokenizeWith(s string, forms bool) []string {
	out := make([]string, 0, len(s)/8+1)
	eachToken(s, forms, func(w string) { out = append(out, w) })
	return out
}

// eachToken calls emit with every word tokenizeWith returns, in order, so a
// caller that only counts them keeps no list of them.
func eachToken(s string, forms bool, emit func(string)) {
	start := -1
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			eachWord(s[start:i], forms, emit)
			start = -1
		}
	}
	if start >= 0 {
		eachWord(s[start:], forms, emit)
	}
}

func eachWord(w string, forms bool, emit func(string)) {
	w = lower(strings.Trim(w, "_"))
	if !isASCII(w) {
		// Russian is written with and without the dots over ё; the index
		// keeps one spelling.
		w = strings.ReplaceAll(w, "ё", "е")
	}
	if !strings.Contains(w, "_") {
		eachStem(w, forms, emit)
		return
	}
	// An identifier is a name, matched as written; its parts are words.
	emit(w)
	for _, part := range strings.Split(w, "_") {
		eachStem(part, forms, emit)
	}
}

func eachStem(w string, forms bool, emit func(string)) {
	if len(w) < 2 || stopwords[w] || (len(w) < 4 && utf8.RuneCountInString(w) < 2) {
		return
	}
	s := stem(w)
	emit(s)
	if forms && s != w && !isASCII(w) {
		emit(w)
	}
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

// stem reduces a word to its stem by its script: a Cyrillic word with the
// Snowball Russian stemmer, an English one with the S-stemmer (Harman, 1991),
// plural endings only, which is conservative enough never to merge two
// unrelated words of a technical text and leaves the rest to prefix matching.
func stem(w string) string {
	if !isASCII(w) {
		if LangOfText(w) == Russian {
			return stemRussian(w)
		}
		return w
	}
	if len(w) <= 3 {
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
