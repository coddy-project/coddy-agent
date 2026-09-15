package web

import (
	"strings"
	"unicode"
)

// decoyMinRows is the smallest batch the relevance gate judges. The decoy an
// engine serves is a full result page - every batch measured was ten rows - so
// the threshold sits well above a short answer, where a genuine result set can
// plausibly miss every query word by naming only synonyms.
const decoyMinRows = 5

// queryStopwords are words too common to carry the subject of a query. They are
// removed before matching so "how to" in the query does not make a page titled
// "How to bake bread" look like an answer about Go.
var queryStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"how": true, "what": true, "why": true, "when": true, "where": true,
	"can": true, "does": true, "are": true, "was": true, "were": true,
	"this": true, "that": true, "into": true, "about": true, "you": true,
	"your": true, "its": true, "not": true, "use": true, "using": true,
	"как": true, "что": true, "это": true, "для": true, "или": true, "так": true,
}

// searchTokens splits text into comparable words: letters and digits in any
// script, lowercased, short and common words dropped. CJK text has no spaces to
// split on, so each ideograph is kept as its own token.
func searchTokens(text string) map[string]bool {
	out := make(map[string]bool)
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		w := strings.ToLower(cur.String())
		cur.Reset()
		if len([]rune(w)) < 3 || queryStopwords[w] {
			return
		}
		out[stem(w)] = true
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			flush()
			out[string(r)] = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// stemSuffixes are the endings folded away before two words are compared, in
// the order they are tried. An ending that leaves a different stem than the
// bare word is mapped rather than dropped: "queries" has to become "query",
// not "quer", or it never meets the word it came from.
var stemSuffixes = []struct{ suffix, replacement string }{
	{"ation", ""},
	{"ings", ""},
	{"ing", ""},
	{"ies", "y"},
	{"ed", ""},
	// Only the "s" is taken off a plural, never the "es": "routines" has to
	// meet "routine", and stripping both letters leaves it one short.
	{"s", ""},
}

// stem trims the endings that separate a query word from the same word on the
// page: "cancellation" against "cancel", "running" against "run", "queries"
// against "query". It is deliberately blunt - the gate needs two forms of a
// word to collide, not a correct lemma - and it never shortens a word below
// three runes, where unrelated words start colliding instead.
func stem(w string) string {
	r := []rune(w)
	for _, s := range stemSuffixes {
		sr := []rune(s.suffix)
		if len(r) < len(sr)+3 || string(r[len(r)-len(sr):]) != s.suffix {
			continue
		}
		return collapseDoubledTail(string(r[:len(r)-len(sr)]) + s.replacement)
	}
	return w
}

// collapseDoubledTail folds the consonant a suffix doubled back to one, so the
// stem of "cancellation" meets "cancel" and the stem of "running" meets "run".
func collapseDoubledTail(w string) string {
	r := []rune(w)
	n := len(r)
	if n < 4 || r[n-1] != r[n-2] {
		return w
	}
	switch r[n-1] {
	case 'a', 'e', 'i', 'o', 'u', 'а', 'е', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return w
	}
	return string(r[:n-1])
}

// decoyReason reports whether a batch of rows is an answer to a different
// question. Bing serves a client it dislikes a structurally perfect result page
// whose title echoes the query and whose ten rows are about something else
// entirely - pizza toppings for a Go query, a tourism site for a GitHub one -
// and rotates that decoy between requests. Nothing downstream can tell such a
// row from a real one, so the batch is judged as a whole: when an engine
// returns several rows and not one of them shares a single word with the query,
// the engine did not answer the query.
//
// The judgement is deliberately batch-wide. A single row that misses every
// query word is ordinary (a page titled "Terminating programs" answers a
// question about killing a process), and dropping rows one at a time would
// quietly filter genuine answers. A whole batch missing is not ordinary.
func decoyReason(query string, rows []Result) (string, bool) {
	if len(rows) < decoyMinRows {
		return "", false
	}
	qt := searchTokens(query)
	if len(qt) == 0 {
		return "", false
	}
	for _, r := range rows {
		if rowAnswersQuery(qt, r) {
			return "", false
		}
	}
	return "decoy: none of the results share a word with the query", true
}

// rowAnswersQuery reports whether one result has anything to do with the query.
// A query word counts when it appears as a word of the row, and also when it
// appears inside one: a question about "OOM" is answered by a page titled
// "OOMKilled containers", and a question about "cancel" by one about
// "cancellation". Matching inside words makes the gate more forgiving, which is
// the right direction for something whose mistake would be discarding a real
// answer - the decoys it exists for share nothing with the query by any
// measure.
func rowAnswersQuery(queryTokens map[string]bool, r Result) bool {
	text := r.Title + " " + r.URL + " " + r.Snippet
	rowTokens := searchTokens(text)
	lower := strings.ToLower(text)
	for t := range queryTokens {
		if rowTokens[t] {
			return true
		}
		if len([]rune(t)) >= 3 && strings.Contains(lower, t) {
			return true
		}
	}
	return false
}
