package mention

import (
	"container/heap"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Score ranks how well query matches candidate, higher is better; ok is false
// when it does not match at all. The query is split on spaces and every word
// must match (all of "comp test" is found in "ui/chat/Composer.test.tsx").
// Matching ignores case. A word is matched against the last path segment
// first, then against the whole path, then as a subsequence of the path, and
// the score says which one hit:
//
//	the whole name ("app.go", or "app" for "app.go")   exact
//	the start of the name ("app" in "app_test.go")      prefix
//	inside the name ("pp" in "app.go")                  substring
//	a segment of the path ("cli/app" in "external/cli/app.go")
//	anywhere in the path ("ternal/c")
//	letters in order ("eca" in "external/cli/app.go")
//
// and shorter, shallower paths win a tie, so "app.go" comes before
// "external/cli/app.go" for "app".
func Score(query, candidate string) (int, bool) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return 0, true
	}
	lowered := strings.ToLower(strings.TrimSuffix(candidate, "/"))
	base := path.Base(lowered)
	total := 0
	for _, w := range words {
		s, ok := scoreWord(w, lowered, base)
		if !ok {
			return 0, false
		}
		total += s
	}
	// Shorter and shallower paths first among equals.
	total -= strings.Count(lowered, "/") * 3
	total -= utf8.RuneCountInString(lowered) / 4
	return total, true
}

func scoreWord(w, full, base string) (int, bool) {
	stem := strings.TrimSuffix(base, path.Ext(base))
	switch {
	case base == w || stem == w:
		return 1000, true
	case strings.HasPrefix(base, w):
		return 800, true
	}
	if i := strings.Index(base, w); i >= 0 {
		bonus := 0
		if boundaryBefore(base, i) {
			bonus = 60
		}
		return 600 + bonus - min(i, 50), true
	}
	if strings.Contains(w, "/") {
		// A word with a separator is a piece of path: "cli/app".
		if i := strings.Index(full, w); i >= 0 {
			if boundaryBefore(full, i) {
				return 700 - min(i/4, 50), true
			}
			return 500 - min(i/4, 50), true
		}
	}
	if i := strings.Index(full, w); i >= 0 {
		if boundaryBefore(full, i) {
			return 450 - min(i/4, 50), true
		}
		return 350 - min(i/4, 50), true
	}
	if s, ok := subsequenceScore(w, full); ok {
		return s, true
	}
	return 0, false
}

// boundaryBefore reports whether offset i starts a word of s: the start of
// the string or after a separator such as "/", "_", "-", "." or a space.
func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !unicode.IsLetter(r) && !unicode.IsNumber(r)
}

// subsequenceScore matches w as letters in order inside s ("eca" in
// "external/cli/app.go"). Letters that open a word and letters that follow
// each other score more; a match spread thin over a long path scores little.
func subsequenceScore(w, s string) (int, bool) {
	wr := []rune(w)
	if len(wr) == 0 {
		return 0, true
	}
	score := 200
	wi := 0
	prevMatch := -2
	idx := 0
	for i, r := range s {
		if wi >= len(wr) {
			break
		}
		if r == wr[wi] {
			if boundaryBefore(s, i) {
				score += 8
			}
			if prevMatch == idx-1 {
				score += 5
			} else if prevMatch >= 0 {
				score -= min(idx-prevMatch, 10)
			}
			prevMatch = idx
			wi++
		}
		idx++
	}
	if wi < len(wr) {
		return 0, false
	}
	return max(score, 1), true
}

// Ranked is one scored candidate.
type Ranked[T any] struct {
	Item  T
	Score int
	Key   string
}

// Rank scores items by key(item) against query and returns the best limit of
// them, best first, together with how many matched in total - the count a
// surface shows when it cuts the list ("12 of 340"). Equal scores keep a
// stable order by key. With an empty query every item matches with score 0.
func Rank[T any](query string, items []T, key func(T) string, limit int) ([]Ranked[T], int) {
	if limit <= 0 {
		limit = 1
	}
	h := &rankHeap[T]{}
	total := 0
	for _, it := range items {
		k := key(it)
		s, ok := Score(query, k)
		if !ok {
			continue
		}
		total++
		r := Ranked[T]{Item: it, Score: s, Key: k}
		if h.Len() < limit {
			heap.Push(h, r)
			continue
		}
		if better(r, (*h)[0]) {
			(*h)[0] = r
			heap.Fix(h, 0)
		}
	}
	out := make([]Ranked[T], h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(Ranked[T])
	}
	return out, total
}

// better orders by score, then by the shorter key, then alphabetically.
func better[T any](a, b Ranked[T]) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if len(a.Key) != len(b.Key) {
		return len(a.Key) < len(b.Key)
	}
	return a.Key < b.Key
}

// rankHeap is a min-heap by better: the root is the worst of the kept items.
type rankHeap[T any] []Ranked[T]

func (h rankHeap[T]) Len() int           { return len(h) }
func (h rankHeap[T]) Less(i, j int) bool { return better(h[j], h[i]) }
func (h rankHeap[T]) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *rankHeap[T]) Push(x any)        { *h = append(*h, x.(Ranked[T])) }
func (h *rankHeap[T]) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
