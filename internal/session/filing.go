package session

import "strings"

// A session is filed under two things: the title it is listed by and the tags
// it is grouped by. NormalizeTags above is the vocabulary of the labels; what
// follows is the rest of that filing, shared by everything that writes it - the
// describe call that names a new chat, the operator editing a row, and the
// session_describe tool the model reaches for.

// MaxSessionTitleRunes is the longest title a caller may store. A title is a
// row of a list, not a summary: past this length it is the surrounding layout
// that decides what the reader sees, so the limit is stated here instead.
const MaxSessionTitleRunes = 120

// NormalizeTitle folds a title onto one line. A session row is a single line,
// and a title carrying a newline is either cut at it or breaks the row, so the
// break is turned into the space it reads as. Returns "" for a title that was
// only whitespace, which clears a pinned title rather than storing a blank one.
func NormalizeTitle(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}

// TitleTooLong reports whether a normalized title exceeds the stored limit, and
// by how many characters it counts. Runes, not bytes: the limit is about what
// the row shows, and a Cyrillic title is not half a title.
func TitleTooLong(title string) (length int, tooLong bool) {
	length = len([]rune(title))
	return length, length > MaxSessionTitleRunes
}

// MergeTags applies a change to the labels a session already carries: the ones
// named in remove go, the ones named in add join the end, and everything else
// stays where it was. Both sides are matched after normalization, so a caller
// may name a label the way it reads ("Session Store") rather than the way it is
// stored ("session-store").
//
// The result is normalized as a whole, so the per-session cap applies to it: the
// labels already there come first and survive, and it is the additions past the
// cap that are dropped. A label named in both add and remove is removed - a
// request that says both is a confused one, and going without a label is the
// half of it that can be undone with another call.
func MergeTags(current, add, remove []string) []string {
	drop := make(map[string]struct{}, len(remove))
	for _, tag := range NormalizeTagValues(remove) {
		drop[tag] = struct{}{}
	}
	next := make([]string, 0, len(current)+len(add))
	for _, tag := range append(append([]string{}, current...), add...) {
		normalized := NormalizeTag(tag)
		if normalized == "" {
			continue
		}
		if _, gone := drop[normalized]; gone {
			continue
		}
		next = append(next, normalized)
	}
	return NormalizeTags(next)
}
