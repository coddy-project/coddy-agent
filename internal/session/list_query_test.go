package session

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTagsFoldsCaseAndDuplicates(t *testing.T) {
	got := NormalizeTags([]string{"Backend", " API ", "backend", "#UI"})
	want := []string{"backend", "api", "ui"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeTagsCollapsesInnerWhitespace(t *testing.T) {
	got := NormalizeTags([]string{"session   manager", "HTTP\tAPI"})
	want := []string{"session-manager", "http-api"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeTagsKeepsNonLatinScript(t *testing.T) {
	got := NormalizeTags([]string{"Сессии", "сессии"})
	want := []string{"сессии"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeTagsDropsEmptyAndReturnsNil(t *testing.T) {
	if got := NormalizeTags([]string{"", "   ", "#", "-"}); got != nil {
		t.Fatalf("got %v want nil", got)
	}
	if got := NormalizeTags(nil); got != nil {
		t.Fatalf("got %v want nil", got)
	}
}

func TestNormalizeTagsCapsCountAndLength(t *testing.T) {
	long := strings.Repeat("x", maxSessionTagRunes+10)
	raw := make([]string, 0, maxSessionTags+3)
	raw = append(raw, long)
	for i := 0; i < maxSessionTags+2; i++ {
		raw = append(raw, string(rune('a'+i)))
	}
	got := NormalizeTags(raw)
	if len(got) != maxSessionTags {
		t.Fatalf("kept %d tags, want %d", len(got), maxSessionTags)
	}
	if n := len([]rune(got[0])); n != maxSessionTagRunes {
		t.Fatalf("first tag is %d runes, want %d", n, maxSessionTagRunes)
	}
}

func TestParseArchiveFilterDefaultsToExclude(t *testing.T) {
	for _, in := range []string{"", "  "} {
		got, ok := ParseArchiveFilter(in)
		if !ok || got != ArchiveExclude {
			t.Fatalf("ParseArchiveFilter(%q) = %q,%v", in, got, ok)
		}
	}
	if got, ok := ParseArchiveFilter("ONLY"); !ok || got != ArchiveOnly {
		t.Fatalf("got %q,%v", got, ok)
	}
	if _, ok := ParseArchiveFilter("sometimes"); ok {
		t.Fatal("an unknown archive filter must be refused, not defaulted")
	}
}

func TestParseSortRefusesUnknownKey(t *testing.T) {
	if got, ok := ParseSortKey(""); !ok || got != SortUpdated {
		t.Fatalf("empty sort key must default to updated, got %q,%v", got, ok)
	}
	if _, ok := ParseSortKey("cost"); ok {
		t.Fatal("an unknown sort key must be refused")
	}
	if got, ok := ParseSortOrder(""); !ok || got != SortDesc {
		t.Fatalf("empty order must default to desc, got %q,%v", got, ok)
	}
	if _, ok := ParseSortOrder("sideways"); ok {
		t.Fatal("an unknown order must be refused")
	}
}

func TestSortSessionListByTitleIsCaseInsensitive(t *testing.T) {
	rows := []SessionListEntry{
		{SessionID: "sess_1", Title: "Gamma"},
		{SessionID: "sess_2", Title: "alpha"},
		{SessionID: "sess_3", Title: "Beta"},
	}
	SortSessionList(rows, SortTitle, SortAsc, nil)
	got := []string{rows[0].Title, rows[1].Title, rows[2].Title}
	want := []string{"alpha", "Beta", "Gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSortSessionListPutsMissingValuesLastInBothDirections(t *testing.T) {
	for _, order := range []SortOrder{SortAsc, SortDesc} {
		rows := []SessionListEntry{
			{SessionID: "sess_1", CreatedAt: ""},
			{SessionID: "sess_2", CreatedAt: "2026-09-01T00:00:00Z"},
			{SessionID: "sess_3", CreatedAt: "2026-09-02T00:00:00Z"},
		}
		SortSessionList(rows, SortCreated, order, nil)
		if rows[2].SessionID != "sess_1" {
			t.Fatalf("order %q: a bundle with no creation stamp must sort last, got %v", order, rows)
		}
	}
}

func TestSortSessionListBreaksTiesByIDSoPagingIsStable(t *testing.T) {
	rows := []SessionListEntry{
		{SessionID: "sess_c", UpdatedAt: "2026-09-01T00:00:00Z"},
		{SessionID: "sess_a", UpdatedAt: "2026-09-01T00:00:00Z"},
		{SessionID: "sess_b", UpdatedAt: "2026-09-01T00:00:00Z"},
	}
	SortSessionList(rows, SortUpdated, SortDesc, nil)
	got := []string{rows[0].SessionID, rows[1].SessionID, rows[2].SessionID}
	want := []string{"sess_a", "sess_b", "sess_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSortSessionListByTokensUsesTheSuppliedTotals(t *testing.T) {
	rows := []SessionListEntry{
		{SessionID: "sess_1"},
		{SessionID: "sess_2"},
		{SessionID: "sess_3"},
	}
	totals := map[string]int{"sess_1": 10, "sess_2": 900, "sess_3": 40}
	SortSessionList(rows, SortTokens, SortDesc, func(id string) int { return totals[id] })
	got := []string{rows[0].SessionID, rows[1].SessionID, rows[2].SessionID}
	want := []string{"sess_2", "sess_3", "sess_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSessionMatchesAnyTagIsAnOrOverNormalizedValues(t *testing.T) {
	row := SessionListEntry{Tags: []string{"backend", "api"}}
	if !SessionMatchesAnyTag(row, []string{"UI", "Backend"}) {
		t.Fatal("one matching tag is enough")
	}
	if SessionMatchesAnyTag(row, []string{"ui"}) {
		t.Fatal("a tag the session does not carry must not match")
	}
	if !SessionMatchesAnyTag(row, nil) {
		t.Fatal("an empty filter keeps every session")
	}
}

func TestParseOriginFilterDefaultsToAny(t *testing.T) {
	if got, ok := ParseOriginFilter(""); !ok || got != OriginAny {
		t.Fatalf("got %q,%v", got, ok)
	}
	if got, ok := ParseOriginFilter("GATEWAY"); !ok || got != OriginGateway {
		t.Fatalf("got %q,%v", got, ok)
	}
	if _, ok := ParseOriginFilter("telegram"); ok {
		t.Fatal("an unknown origin filter must be refused: the surfaces are a closed set")
	}
}

func TestOriginFilterKeeps(t *testing.T) {
	cases := []struct {
		filter OriginFilter
		origin string
		want   bool
	}{
		{OriginAny, "", true},
		{OriginAny, GatewayOrigin("telegram"), true},
		{OriginLocal, "", true},
		{OriginLocal, GatewayOrigin("telegram"), false},
		{OriginGateway, GatewayOrigin("telegram"), true},
		{OriginGateway, GatewayOrigin("slack"), true},
		{OriginGateway, "", false},
	}
	for _, tc := range cases {
		if got := tc.filter.Keeps(tc.origin); got != tc.want {
			t.Fatalf("%q.Keeps(%q) = %v, want %v", tc.filter, tc.origin, got, tc.want)
		}
	}
}

func TestGatewayOriginNamesTheMessenger(t *testing.T) {
	if got := GatewayOrigin("Telegram"); got != "gateway:telegram" {
		t.Fatalf("got %q", got)
	}
	// A messenger with no name is still a gateway, not a local session.
	if got := GatewayOrigin(""); got != "gateway" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeTagFoldsDelimitersSoAStoredTagIsAlwaysSearchable(t *testing.T) {
	// A tag is written as an array element by PATCH and read back as one item of
	// a comma separated query. A comma surviving inside the stored value would
	// make the tag impossible to ask for.
	got := NormalizeTag("backend,api")
	if strings.Contains(got, ",") {
		t.Fatalf("stored tag %q keeps a delimiter no query can express", got)
	}
	if got != "backend-api" {
		t.Fatalf("got %q, want backend-api", got)
	}
	if round := ParseTagList(got); len(round) != 1 || round[0] != got {
		t.Fatalf("round trip through a query gave %v", round)
	}
}

func TestParseTagListDoesNotCapAFilterAtTheStorageLimit(t *testing.T) {
	// Eight is how many labels one session may carry. It says nothing about how
	// many alternatives a search may offer.
	wanted := make([]string, 0, maxSessionTags+4)
	for i := 0; i < maxSessionTags+4; i++ {
		wanted = append(wanted, fmt.Sprintf("t%d", i))
	}
	got := ParseTagList(strings.Join(wanted, ","))
	if len(got) != len(wanted) {
		t.Fatalf("filter kept %d of %d alternatives", len(got), len(wanted))
	}
	last := wanted[len(wanted)-1]
	row := SessionListEntry{Tags: []string{last}}
	if !SessionMatchesAnyTag(row, got) {
		t.Fatalf("a session tagged %q is not matched by a filter that lists it", last)
	}
}

func TestSortSessionListIsTransitiveWithAnUnparseableStamp(t *testing.T) {
	// A stamp this build cannot read is unknown, not "some other position in the
	// chronology": mixing a lexical order with a chronological one makes the
	// comparison non-transitive and the listing order undefined.
	a := SessionListEntry{SessionID: "sess_a", UpdatedAt: "2026-09-01T00:00:00Z"}
	b := SessionListEntry{SessionID: "sess_b", UpdatedAt: "2026-09-01T00:00:00.1Z"}
	c := SessionListEntry{SessionID: "sess_c", UpdatedAt: "2026-09-01T00:00:00X"}

	for _, order := range []SortOrder{SortAsc, SortDesc} {
		rows := []SessionListEntry{c, b, a}
		SortSessionList(rows, SortUpdated, order, nil)
		if rows[2].SessionID != "sess_c" {
			t.Fatalf("order %q: an unreadable stamp must sort last, got %v", order, rows)
		}
		// Whatever the direction, the two readable stamps keep a chronological
		// relation to each other.
		if order == SortAsc && rows[0].SessionID != "sess_a" {
			t.Fatalf("asc: got %v", rows)
		}
		if order == SortDesc && rows[0].SessionID != "sess_b" {
			t.Fatalf("desc: got %v", rows)
		}
	}
}

func TestArchiveStateIsReadAsOnePair(t *testing.T) {
	// The flag and its stamp are one fact. Reading them under separate locks lets
	// a concurrent change be caught halfway and persisted as "archived with no
	// stamp" or "not archived, stamped".
	st := &State{ID: "sess_a"}
	st.SetArchived(true)
	archived, at := st.ArchiveState()
	if !archived || strings.TrimSpace(at) == "" {
		t.Fatalf("ArchiveState() = %v, %q", archived, at)
	}
	st.SetArchived(false)
	archived, at = st.ArchiveState()
	if archived || at != "" {
		t.Fatalf("after unarchiving: %v, %q", archived, at)
	}
}

func TestSetOriginDoesNotRelabelASessionThatHasOneRecorded(t *testing.T) {
	st := &State{ID: "sess_a"}
	st.SetOrigin(GatewayOrigin("telegram"))
	st.SetOrigin(GatewayOrigin("slack"))
	if got := st.GetOrigin(); got != GatewayOrigin("telegram") {
		t.Fatalf("origin = %q, want the first one recorded", got)
	}
}

func TestSetTagsAndSetArchivedDoNotWriteWhenNothingMoves(t *testing.T) {
	writes := 0
	st := &State{ID: "sess_a"}
	st.SetPersistHook(func() { writes++ })

	st.SetTags([]string{"backend"})
	st.SetArchived(true)
	if writes != 2 {
		t.Fatalf("two real changes produced %d writes", writes)
	}

	// The same values again: nothing moved, so nothing is written - and the
	// archive stamp keeps saying when the session was actually put aside.
	_, firstStamp := st.ArchiveState()
	st.SetTags([]string{"Backend"})
	st.SetArchived(true)
	if writes != 2 {
		t.Fatalf("re-setting the same values produced %d writes", writes)
	}
	if _, stamp := st.ArchiveState(); stamp != firstStamp {
		t.Fatalf("archiving an archived session moved its stamp: %q -> %q", firstStamp, stamp)
	}
}

func TestSortSessionListKeepsPinnedRowsOnTopOfEveryOrder(t *testing.T) {
	// A pin means "keep this where I can see it". It outranks the column being
	// sorted by, or it would only work for one of them.
	rows := []SessionListEntry{
		{SessionID: "sess_a", Title: "alpha", UpdatedAt: "2026-09-03T00:00:00Z"},
		{SessionID: "sess_b", Title: "bravo", UpdatedAt: "2026-09-02T00:00:00Z", Pinned: true},
		{SessionID: "sess_c", Title: "charlie", UpdatedAt: "2026-09-01T00:00:00Z"},
	}
	for _, key := range []SortKey{SortUpdated, SortTitle, SortCreated} {
		for _, order := range []SortOrder{SortAsc, SortDesc} {
			got := append([]SessionListEntry(nil), rows...)
			SortSessionList(got, key, order, nil)
			if got[0].SessionID != "sess_b" {
				t.Fatalf("%s %s: pinned row is at %v", key, order, got)
			}
		}
	}
}

func TestSortSessionListDoesNotLetTheColumnReorderThePins(t *testing.T) {
	// The order of the pins belongs to the operator, who dragged them into it.
	// A column sort rearranging them underneath would undo that silently, so
	// the column applies to everything below the pins and nothing above.
	rows := []SessionListEntry{
		{SessionID: "sess_a", Title: "alpha", Pinned: true, PinnedRank: 1},
		{SessionID: "sess_b", Title: "bravo", Pinned: true, PinnedRank: 2},
		{SessionID: "sess_c", Title: "charlie"},
	}
	SortSessionList(rows, SortTitle, SortDesc, nil)
	if rows[0].SessionID != "sess_a" || rows[1].SessionID != "sess_b" {
		t.Fatalf("a title sort reordered the pins: %v", rows)
	}
}

func TestSetPinnedStampsAndClears(t *testing.T) {
	writes := 0
	st := &State{ID: "sess_a"}
	st.SetPersistHook(func() { writes++ })

	st.SetPinned(true)
	pinned, at := st.PinState()
	if !pinned || strings.TrimSpace(at) == "" {
		t.Fatalf("PinState() = %v, %q", pinned, at)
	}
	st.SetPinned(true)
	if writes != 1 {
		t.Fatalf("pinning a pinned session wrote %d times", writes)
	}
	if _, again := st.PinState(); again != at {
		t.Fatalf("pinning again moved the stamp: %q -> %q", at, again)
	}

	st.SetPinned(false)
	if pinned, at = st.PinState(); pinned || at != "" {
		t.Fatalf("after unpinning: %v, %q", pinned, at)
	}
}

func TestSortSessionListOrdersPinsByTheirRank(t *testing.T) {
	// The rank is the order the operator dragged them into, so it outranks the
	// column being sorted by - inside the pins, that column says nothing.
	rows := []SessionListEntry{
		{SessionID: "sess_a", Title: "alpha", Pinned: true, PinnedRank: 2},
		{SessionID: "sess_b", Title: "bravo", Pinned: true, PinnedRank: 0},
		{SessionID: "sess_c", Title: "charlie", Pinned: true, PinnedRank: 1},
		{SessionID: "sess_d", Title: "delta"},
	}
	for _, order := range []SortOrder{SortAsc, SortDesc} {
		got := append([]SessionListEntry(nil), rows...)
		SortSessionList(got, SortTitle, order, nil)
		ids := []string{got[0].SessionID, got[1].SessionID, got[2].SessionID}
		if !reflect.DeepEqual(ids, []string{"sess_b", "sess_c", "sess_a"}) {
			t.Fatalf("order %q: pins read %v", order, ids)
		}
		if got[3].SessionID != "sess_d" {
			t.Fatalf("order %q: an unpinned row got in among the pins: %v", order, got)
		}
	}
}

func TestSortSessionListFallsBackToTheNewestPinWhenRanksTie(t *testing.T) {
	// A pin that never took part in a reorder has no rank of its own; the
	// freshest one leads, which is where a new pin is put.
	rows := []SessionListEntry{
		{SessionID: "sess_a", Pinned: true, PinnedAt: "2026-09-01T00:00:00Z"},
		{SessionID: "sess_b", Pinned: true, PinnedAt: "2026-09-03T00:00:00Z"},
		{SessionID: "sess_c", Pinned: true, PinnedAt: "2026-09-02T00:00:00Z"},
	}
	SortSessionList(rows, SortUpdated, SortDesc, nil)
	ids := []string{rows[0].SessionID, rows[1].SessionID, rows[2].SessionID}
	if !reflect.DeepEqual(ids, []string{"sess_b", "sess_c", "sess_a"}) {
		t.Fatalf("got %v", ids)
	}
}

func TestSetPinnedRankRecordsTheHandPlacedOrder(t *testing.T) {
	st := &State{ID: "sess_a"}
	st.SetPinned(true)
	st.SetPinnedRank(3)
	if _, _, rank := st.PinPlacement(); rank != 3 {
		t.Fatalf("rank = %d", rank)
	}
	// Unpinning forgets the placement: a session pinned again is a new pin and
	// goes where new pins go.
	st.SetPinned(false)
	if _, _, rank := st.PinPlacement(); rank != 0 {
		t.Fatalf("rank survived unpinning: %d", rank)
	}
}

func TestMergeTagsKeepsWhatIsThereAndAppendsTheRest(t *testing.T) {
	got := MergeTags([]string{"backend", "api"}, []string{"Session Store"}, nil)
	want := []string{"backend", "api", "session-store"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeTagsMatchesRemovalsAfterFolding(t *testing.T) {
	// A caller names a label the way it reads, not the way it is stored.
	got := MergeTags([]string{"session-store", "api"}, nil, []string{"Session Store"})
	if !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("got %v", got)
	}
}

func TestMergeTagsDropsALabelNamedOnBothSides(t *testing.T) {
	got := MergeTags([]string{"api"}, []string{"backend"}, []string{"BACKEND"})
	if !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("got %v", got)
	}
}

func TestMergeTagsSpendsTheCapOnTheLabelsAlreadyThere(t *testing.T) {
	// Eight is the per-session cap. The additions are what does not fit, so a
	// call that files one topic too many cannot quietly unfile the others.
	current := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	got := MergeTags(current, []string{"i"}, nil)
	if !reflect.DeepEqual(got, current) {
		t.Fatalf("got %v, want the eight already there", got)
	}
}

func TestMergeTagsAnswersNilWhenEverythingIsRemoved(t *testing.T) {
	if got := MergeTags([]string{"api"}, nil, []string{"api"}); got != nil {
		t.Fatalf("got %v, want nil so session.json carries no field", got)
	}
}

func TestNormalizeTitleFoldsAMultiLineTitleOntoOneRow(t *testing.T) {
	got := NormalizeTitle("  Rewrite\tthe session\n\nstore  ")
	if got != "Rewrite the session store" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeTitle("   \n "); got != "" {
		t.Fatalf("a blank title became %q, want the empty string that clears a pin", got)
	}
}

func TestTitleTooLongCountsCharactersNotBytes(t *testing.T) {
	// A Cyrillic title is not half a title: the limit is about the row, and the
	// row shows characters.
	title := strings.Repeat("я", MaxSessionTitleRunes)
	if length, tooLong := TitleTooLong(title); tooLong {
		t.Fatalf("a title of exactly the limit was refused at %d characters", length)
	}
	if length, tooLong := TitleTooLong(title + "я"); !tooLong || length != MaxSessionTitleRunes+1 {
		t.Fatalf("length = %d, tooLong = %v", length, tooLong)
	}
}
