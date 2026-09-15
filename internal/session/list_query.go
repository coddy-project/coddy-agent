package session

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// A session tag is a short, flat label an operator - or the title generation
// that already asks the model about the first message - puts on a conversation,
// so a history grown over months is narrowed instead of scrolled. The caps are
// deliberate: tags are a filter, not a description, and a row of the list has
// to stay readable next to the title it belongs to.
const (
	maxSessionTags     = 8
	maxSessionTagRunes = 32
)

// tagTrimCutset is the punctuation a model likes to leave around a tag it was
// asked to list ("- backend," / "#api."). It is trimmed from both ends, so the
// same label written three ways is stored once.
const tagTrimCutset = " \t\r\n#.,;:!?\"'`()[]{}*-_/\\"

// NormalizeTag folds one label into the single spelling everything else
// compares against: lower case, inner whitespace as a hyphen, no decoration.
// It returns "" when nothing usable is left.
func NormalizeTag(raw string) string {
	s := strings.Trim(raw, tagTrimCutset)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	// Whitespace inside a tag becomes a hyphen rather than being dropped: a
	// filter chip reads "session-manager", and splitting it into two tags would
	// invent a vocabulary the model never proposed. A comma or a line break is
	// folded the same way, and for a harder reason: they are what a query string
	// splits on, so a tag that kept one could be stored and never asked for
	// again.
	s = strings.Join(strings.FieldsFunc(s, isTagSeparator), "-")
	if rs := []rune(s); len(rs) > maxSessionTagRunes {
		s = string(rs[:maxSessionTagRunes])
	}
	return strings.Trim(s, tagTrimCutset)
}

// isTagSeparator reports whether r may not appear inside a single tag.
func isTagSeparator(r rune) bool {
	return unicode.IsSpace(r) || r == ',' || r == ';'
}

// NormalizeTags folds a list of labels, dropping what normalizes to nothing and
// keeping the first spelling of each. The order is the caller's: the model puts
// the tag it is most sure about first, and sorting that away would lose it to
// the cap. Empty input answers nil, so a session with no tags encodes to no
// field at all rather than to an empty array.
func NormalizeTags(raw []string) []string {
	return normalizeTagList(raw, maxSessionTags)
}

// NormalizeTagValues folds a list of labels without the per-session cap. It is
// what a *filter* is read with: eight is how many labels one session may carry
// and says nothing about how many alternatives a search may offer.
func NormalizeTagValues(raw []string) []string {
	return normalizeTagList(raw, 0)
}

// normalizeTagList folds raw and keeps at most limit entries; limit 0 is no cap.
func normalizeTagList(raw []string, limit int) []string {
	if len(raw) == 0 {
		return nil
	}
	capacity := len(raw)
	if limit > 0 {
		capacity = min(capacity, limit)
	}
	out := make([]string, 0, capacity)
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		tag := NormalizeTag(item)
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseTagList reads a comma separated list of tags - the shape a query string
// carries and the shape a model answers in. Only commas and line breaks split:
// a space belongs to the tag, and NormalizeTag turns it into a hyphen.
func ParseTagList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	return NormalizeTagValues(parts)
}

// ArchiveFilter selects which side of the archive a listing reports.
type ArchiveFilter string

const (
	// ArchiveExclude is the default: the working list, archived sessions left out.
	ArchiveExclude ArchiveFilter = "exclude"
	// ArchiveOnly is the archive itself.
	ArchiveOnly ArchiveFilter = "only"
	// ArchiveAll is everything stored, both sides together.
	ArchiveAll ArchiveFilter = "all"
)

// ParseArchiveFilter reads the value of the archived query parameter. An empty
// value is the default working list; an unknown one is refused rather than
// silently treated as the default, because "archived=archived" must not quietly
// answer with the sessions the caller asked to leave out.
func ParseArchiveFilter(s string) (ArchiveFilter, bool) {
	switch ArchiveFilter(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return ArchiveExclude, true
	case ArchiveExclude:
		return ArchiveExclude, true
	case ArchiveOnly:
		return ArchiveOnly, true
	case ArchiveAll:
		return ArchiveAll, true
	}
	return "", false
}

// Keeps reports whether a session with this archive flag belongs in the listing.
func (f ArchiveFilter) Keeps(archived bool) bool {
	switch f {
	case ArchiveOnly:
		return archived
	case ArchiveAll:
		return true
	default:
		return !archived
	}
}

// SortKey names the column a session listing is ordered by.
type SortKey string

const (
	SortUpdated  SortKey = "updated"
	SortCreated  SortKey = "created"
	SortTitle    SortKey = "title"
	SortMessages SortKey = "messages"
	SortTokens   SortKey = "tokens"
)

// SortOrder is the direction of that order.
type SortOrder string

const (
	SortDesc SortOrder = "desc"
	SortAsc  SortOrder = "asc"
)

// ParseSortKey reads the sort query parameter; empty means the default, the
// most recently touched session first. An unknown key is refused so a typo
// surfaces instead of silently returning a different order than was asked for.
func ParseSortKey(s string) (SortKey, bool) {
	switch SortKey(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return SortUpdated, true
	case SortUpdated:
		return SortUpdated, true
	case SortCreated:
		return SortCreated, true
	case SortTitle:
		return SortTitle, true
	case SortMessages:
		return SortMessages, true
	case SortTokens:
		return SortTokens, true
	}
	return "", false
}

// ParseSortOrder reads the order query parameter; empty means descending.
func ParseSortOrder(s string) (SortOrder, bool) {
	switch SortOrder(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return SortDesc, true
	case SortDesc:
		return SortDesc, true
	case SortAsc:
		return SortAsc, true
	}
	return "", false
}

// OriginFilter selects sessions by the surface that started them.
type OriginFilter string

const (
	// OriginAny is the default: every session, whichever surface started it.
	OriginAny OriginFilter = ""
	// OriginLocal keeps the sessions a person started on this host - the
	// console, an editor, the web UI.
	OriginLocal OriginFilter = "local"
	// OriginGateway keeps the conversations a messenger gateway is holding.
	OriginGateway OriginFilter = "gateway"
)

// gatewayOriginPrefix marks a session a messenger gateway started. The
// messenger's own name follows it, so "which gateway" survives into the
// listing without a second field, and a gateway added later needs no migration.
const gatewayOriginPrefix = "gateway"

// GatewayOrigin is the origin a gateway stamps on the sessions it creates.
func GatewayOrigin(messenger string) string {
	name := strings.ToLower(strings.TrimSpace(messenger))
	if name == "" {
		return gatewayOriginPrefix
	}
	return gatewayOriginPrefix + ":" + name
}

// IsGatewayOrigin reports whether origin names a messenger gateway.
func IsGatewayOrigin(origin string) bool {
	o := strings.ToLower(strings.TrimSpace(origin))
	return o == gatewayOriginPrefix || strings.HasPrefix(o, gatewayOriginPrefix+":")
}

// ParseOriginFilter reads the origin query parameter. Empty means every
// surface; an unknown value is refused, because the surfaces are a closed set
// and a typo must not quietly widen the answer.
func ParseOriginFilter(s string) (OriginFilter, bool) {
	switch OriginFilter(strings.ToLower(strings.TrimSpace(s))) {
	case OriginAny:
		return OriginAny, true
	case OriginLocal:
		return OriginLocal, true
	case OriginGateway:
		return OriginGateway, true
	}
	return "", false
}

// Keeps reports whether a session with this origin belongs in the listing.
func (f OriginFilter) Keeps(origin string) bool {
	switch f {
	case OriginLocal:
		return !IsGatewayOrigin(origin)
	case OriginGateway:
		return IsGatewayOrigin(origin)
	default:
		return true
	}
}

// SessionMatchesAnyTag reports whether row carries any of the wanted tags, which
// are normalized here so a chip typed as "Backend" finds what was stored as
// "backend". An empty filter keeps every session.
func SessionMatchesAnyTag(row SessionListEntry, wanted []string) bool {
	want := NormalizeTagValues(wanted)
	if len(want) == 0 {
		return true
	}
	for _, have := range row.Tags {
		for _, w := range want {
			if have == w {
				return true
			}
		}
	}
	return false
}

// SortSessionList orders rows in place. tokensOf supplies the token total for
// SortTokens - the totals live in a file of their own, so the caller reads them
// only when that column is the one being sorted by - and may be nil otherwise.
//
// Pinned sessions come first whatever the key says, and are ordered among
// themselves by it. Two further rules hold for every key. A session the key says nothing about (no
// creation stamp, no title yet) sorts last in *both* directions, because
// "unknown" is not a small value. And a tie is broken by session id, so paging
// through a listing never shows the same row twice or skips one.
func SortSessionList(rows []SessionListEntry, key SortKey, order SortOrder, tokensOf func(id string) int) {
	asc := order == SortAsc
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		// A pin means "keep this where I can see it", so it outranks the column
		// being sorted by - a pin that only worked in one order would not be one.
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.Pinned {
			// Among the pins the sort column says nothing: their order is the
			// one the operator dragged them into, and before any dragging the
			// freshest pin leads - which is where a new pin is put.
			if a.PinnedRank != b.PinnedRank {
				return a.PinnedRank < b.PinnedRank
			}
			if cmp, _ := compareOptionalTimestamps(a.PinnedAt, b.PinnedAt); cmp != 0 {
				return cmp > 0
			}
			return a.SessionID < b.SessionID
		}
		if cmp, decided := compareSessionRows(a, b, key, tokensOf); decided {
			if !asc {
				cmp = -cmp
			}
			if cmp != 0 {
				return cmp < 0
			}
		} else if cmp != 0 {
			// One side has nothing to compare: it goes last either way.
			return cmp < 0
		}
		return a.SessionID < b.SessionID
	})
}

// compareSessionRows returns the ordering of a against b for key. decided is
// false when one of the two carries no value for the key: cmp is then already
// the final answer (the empty side last) and must not be flipped by direction.
func compareSessionRows(a, b SessionListEntry, key SortKey, tokensOf func(id string) int) (cmp int, decided bool) {
	switch key {
	case SortTitle:
		return compareOptionalStrings(strings.ToLower(strings.TrimSpace(a.Title)), strings.ToLower(strings.TrimSpace(b.Title)))
	case SortCreated:
		return compareOptionalTimestamps(a.CreatedAt, b.CreatedAt)
	case SortMessages:
		return compareInts(a.MessageCount, b.MessageCount), true
	case SortTokens:
		if tokensOf == nil {
			return 0, true
		}
		return compareInts(tokensOf(a.SessionID), tokensOf(b.SessionID)), true
	default:
		return compareOptionalTimestamps(a.UpdatedAt, b.UpdatedAt)
	}
}

// compareOptionalStrings orders two values where "" means absent and sorts last.
func compareOptionalStrings(a, b string) (cmp int, decided bool) {
	switch {
	case a == "" && b == "":
		return 0, true
	case a == "":
		return 1, false
	case b == "":
		return -1, false
	case a < b:
		return -1, true
	case a > b:
		return 1, true
	}
	return 0, true
}

// compareOptionalTimestamps orders two RFC3339 stamps chronologically. A stamp
// that is empty, or that this build cannot read, is *unknown* and sorts last in
// both directions.
//
// Reading an unparseable stamp as a string and a readable one as a time would
// mix two orderings in one comparison, and the mixture is not transitive: with
// A and B a tenth of a second apart and C unreadable, A < B chronologically
// while B < C and C < A lexically. sort.SliceStable is entitled to anything at
// all from a comparison like that, and a tie-break on the id cannot repair it.
func compareOptionalTimestamps(a, b string) (cmp int, decided bool) {
	ta, okA := parseListTimestamp(a)
	tb, okB := parseListTimestamp(b)
	switch {
	case !okA && !okB:
		return 0, true
	case !okA:
		return 1, false
	case !okB:
		return -1, false
	case ta.Before(tb):
		return -1, true
	case ta.After(tb):
		return 1, true
	}
	return 0, true
}

// parseListTimestamp reads a stored stamp; ok is false for an empty or
// unreadable one.
func parseListTimestamp(s string) (time.Time, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
