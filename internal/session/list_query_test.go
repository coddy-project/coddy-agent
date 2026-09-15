package session

import (
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
