//go:build http

package httpserver

// Edge and error cases of the tag, archive and ordering query surface of
// GET /coddy/sessions, of the tags and archived fields of PATCH, of the
// archived scope of bulk delete, and of the tags POST /coddy/describe parses
// out of a model answer. The happy paths are the godog spec
// features/session_tags_archive.feature.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// getSessions performs one listing request and returns its status and body.
func getSessions(t *testing.T, srv *Server, query string) (int, map[string]interface{}) {
	t.Helper()
	path := "/coddy/sessions"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

// patchSessionJSON sends one PATCH body to a session.
func patchSessionJSON(t *testing.T, srv *Server, id string, payload interface{}) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/coddy/sessions/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

// listedIDs reads the session ids out of a listing body.
func listedIDs(body map[string]interface{}) []string {
	raw, _ := body["sessions"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]interface{}); ok {
			id, _ := m["id"].(string)
			out = append(out, id)
		}
	}
	return out
}

func TestSessionListRefusesUnknownQueryValues(t *testing.T) {
	srv, _, _ := bulkDeleteServer(t)
	for _, query := range []string{
		"archived=archived",
		"sort=cost",
		"order=sideways",
		"origin=telegram",
	} {
		t.Run(query, func(t *testing.T) {
			code, body := getSessions(t, srv, query)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %v)", code, body)
			}
		})
	}
}

func TestSessionListSortsTheWholeListingNotThePage(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	// Written oldest first, so the default order (newest updatedAt first) is
	// delta, charlie, bravo, alpha - the reverse of the title order. A page
	// taken before the sort would therefore hold delta and charlie, and only a
	// listing sorted as a whole answers with alpha and bravo.
	for _, title := range []string{"alpha", "bravo", "charlie", "delta"} {
		id := storeSession(t, mgr, store, "question "+title)
		if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"title": title}); code != http.StatusOK {
			t.Fatalf("patch title: status %d body %v", code, body)
		}
	}

	code, body := getSessions(t, srv, "sort=title&order=asc&limit=2")
	if code != http.StatusOK {
		t.Fatalf("status %d body %v", code, body)
	}
	raw, _ := body["sessions"].([]interface{})
	if len(raw) != 2 {
		t.Fatalf("page holds %d rows, want 2", len(raw))
	}
	got := make([]string, 0, 2)
	for _, item := range raw {
		m, _ := item.(map[string]interface{})
		title, _ := m["title"].(string)
		got = append(got, title)
	}
	if got[0] != "alpha" || got[1] != "bravo" {
		t.Fatalf("first page reads %v, want [alpha bravo]", got)
	}
	if hasMore, _ := body["hasMore"].(bool); !hasMore {
		t.Fatal("a listing of four with a page of two must report more")
	}

	// The second page continues the sorted listing rather than restarting it.
	cursor, _ := body["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("a listing with more rows must hand back a cursor")
	}
	code, body = getSessions(t, srv, "sort=title&order=asc&limit=2&cursor="+cursor)
	if code != http.StatusOK {
		t.Fatalf("status %d body %v", code, body)
	}
	raw, _ = body["sessions"].([]interface{})
	got = got[:0]
	for _, item := range raw {
		m, _ := item.(map[string]interface{})
		title, _ := m["title"].(string)
		got = append(got, title)
	}
	if len(got) != 2 || got[0] != "charlie" || got[1] != "delta" {
		t.Fatalf("second page reads %v, want [charlie delta]", got)
	}
}

func TestPatchClearsTagsWithAnEmptyArrayAndLeavesThemOnOmission(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "question")

	if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"tags": []string{"Backend", "api"}}); code != http.StatusOK {
		t.Fatalf("set tags: status %d body %v", code, body)
	}
	// An unrelated patch must not touch them.
	if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"title": "Something"}); code != http.StatusOK {
		t.Fatalf("patch title: status %d body %v", code, body)
	}
	snap, err := store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Meta.Tags) != 2 {
		t.Fatalf("tags after an unrelated patch = %v, want both kept", snap.Meta.Tags)
	}

	code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"tags": []string{}})
	if code != http.StatusOK {
		t.Fatalf("clear tags: status %d body %v", code, body)
	}
	if tags, ok := body["tags"].([]interface{}); !ok || len(tags) != 0 {
		t.Fatalf("clearing tags answered %v, want an empty array", body["tags"])
	}
	snap, err = store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Meta.Tags) != 0 {
		t.Fatalf("tags = %v after being cleared", snap.Meta.Tags)
	}
}

func TestPatchUnarchivingDropsTheArchiveStamp(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "question")

	if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"archived": true}); code != http.StatusOK {
		t.Fatalf("archive: status %d body %v", code, body)
	}
	snap, err := store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Meta.Archived || snap.Meta.ArchivedAt == "" {
		t.Fatalf("meta after archiving = %+v", snap.Meta)
	}

	if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"archived": false}); code != http.StatusOK {
		t.Fatalf("unarchive: status %d body %v", code, body)
	}
	snap, err = store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Archived || snap.Meta.ArchivedAt != "" {
		t.Fatalf("a session taken out of the archive kept its stamp: %+v", snap.Meta)
	}
}

func TestPatchWithNoUnderstoodFieldIsRefused(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "question")
	if code, _ := patchSessionJSON(t, srv, id, map[string]interface{}{"nothing": true}); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestBulkDeleteArchivedScopeTouchesOnlyTheArchive(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	kept := storeSession(t, mgr, store, "kept")
	filed := storeSession(t, mgr, store, "filed")
	if code, body := patchSessionJSON(t, srv, filed, map[string]interface{}{"archived": true}); code != http.StatusOK {
		t.Fatalf("archive: status %d body %v", code, body)
	}

	code, body := postBulkDelete(t, srv, map[string]interface{}{"scope": "archived"})
	if code != http.StatusOK {
		t.Fatalf("status %d body %v", code, body)
	}
	deleted, _ := body["deleted"].([]interface{})
	if len(deleted) != 1 {
		t.Fatalf("deleted %v, want only the archived session", deleted)
	}

	_, listing := getSessions(t, srv, "archived=all")
	if ids := listedIDs(listing); len(ids) != 1 || ids[0] != kept {
		t.Fatalf("sessions left = %v, want only %q", ids, kept)
	}
}

func TestBulkDeleteArchivedScopeOnAnEmptyArchiveDeletesNothing(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	kept := storeSession(t, mgr, store, "kept")

	code, body := postBulkDelete(t, srv, map[string]interface{}{"scope": "archived"})
	if code != http.StatusOK {
		t.Fatalf("status %d body %v", code, body)
	}
	if deleted, _ := body["deleted"].([]interface{}); len(deleted) != 0 {
		t.Fatalf("deleted %v over an empty archive", deleted)
	}
	_, listing := getSessions(t, srv, "")
	if ids := listedIDs(listing); len(ids) != 1 || ids[0] != kept {
		t.Fatalf("sessions left = %v, want only %q", ids, kept)
	}
}

func TestBulkDeleteAllScopeReachesIntoTheArchive(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	_ = storeSession(t, mgr, store, "plain")
	filed := storeSession(t, mgr, store, "filed")
	if code, body := patchSessionJSON(t, srv, filed, map[string]interface{}{"archived": true}); code != http.StatusOK {
		t.Fatalf("archive: status %d body %v", code, body)
	}

	code, body := postBulkDelete(t, srv, map[string]interface{}{"scope": "all"})
	if code != http.StatusOK {
		t.Fatalf("status %d body %v", code, body)
	}
	if deleted, _ := body["deleted"].([]interface{}); len(deleted) != 2 {
		t.Fatalf("deleted %v, want the whole history including the archive", deleted)
	}
}

func TestBulkDeleteRefusesArchivedScopeWithIDs(t *testing.T) {
	srv, _, _ := bulkDeleteServer(t)
	if code, _ := postBulkDelete(t, srv, map[string]interface{}{"scope": "archived", "ids": []string{"sess_abc"}}); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

func TestDescribeSplitTagsLine(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantRest string
		wantTags []string
	}{
		{"no tag line at all", "Refactor memory API", "Refactor memory API", nil},
		{"tags after the phrase", "Refactor memory API\ntags: backend, memory", "Refactor memory API", []string{"backend", "memory"}},
		{"decorated tag line", "Refactor memory API\n**Tags:** Backend, Memory, backend", "Refactor memory API", []string{"backend", "memory"}},
		{"tags before the phrase", "tags: ui\nRefactor memory API", "Refactor memory API", []string{"ui"}},
		{"an empty tag line proposes nothing", "Refactor memory API\ntags:", "Refactor memory API", nil},
		// Lower casing can change how many bytes a rune takes (Ⱥ is two, ⱥ is
		// three), so an offset measured on the lowered copy can run off the
		// front of the original string.
		{"a tag line whose lower case is longer", "Refactor memory API\ntags: ȺȺȺȺȺȺ", "Refactor memory API", []string{"ⱥⱥⱥⱥⱥⱥ"}},
		{"a tag line in another script", "Refactor memory API\nTAGS: Память, Бэкенд", "Refactor memory API", []string{"память", "бэкенд"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, tags := describeSplitTagsLine(tc.raw)
			if rest != tc.wantRest {
				t.Fatalf("rest = %q, want %q", rest, tc.wantRest)
			}
			if len(tags) != len(tc.wantTags) {
				t.Fatalf("tags = %v, want %v", tags, tc.wantTags)
			}
			for i := range tags {
				if tags[i] != tc.wantTags[i] {
					t.Fatalf("tags = %v, want %v", tags, tc.wantTags)
				}
			}
		})
	}
}

func TestDescribeStillNamesAChatWhenTheModelIgnoresTags(t *testing.T) {
	_, srv, _ := testHTTPServerPersist(t)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: "Refactor memory API"}, nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/coddy/describe", "application/json",
		bytes.NewReader([]byte(`{"text":"Please refactor the memory tree endpoint to reject traversal and add tests."}`)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Short string   `json:"short"`
		Tags  []string `json:"tags"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Short != "Refactor memory API" {
		t.Fatalf("short = %q", out.Short)
	}
	if out.Tags == nil {
		t.Fatal("tags must be an empty array rather than null, so a client can assign it")
	}
	if len(out.Tags) != 0 {
		t.Fatalf("tags = %v, want none", out.Tags)
	}
}

func TestSessionListReportsTagsAndArchiveOnTheRow(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "question")
	if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{
		"tags":     []string{"Backend"},
		"archived": true,
	}); code != http.StatusOK {
		t.Fatalf("patch: status %d body %v", code, body)
	}

	_, body := getSessions(t, srv, "archived=only")
	raw, _ := body["sessions"].([]interface{})
	if len(raw) != 1 {
		t.Fatalf("archived listing holds %d rows, want 1", len(raw))
	}
	row, _ := raw[0].(map[string]interface{})
	tags, _ := row["tags"].([]interface{})
	if len(tags) != 1 || tags[0] != "backend" {
		t.Fatalf("row tags = %v, want [backend]", tags)
	}
	if archived, _ := row["archived"].(bool); !archived {
		t.Fatalf("row does not report the archive: %v", row)
	}
	if at, _ := row["archivedAt"].(string); at == "" {
		t.Fatalf("row does not report when it was archived: %v", row)
	}
}

func TestSessionListTagFilterIsOrOverNormalizedValues(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	a := storeSession(t, mgr, store, "first")
	b := storeSession(t, mgr, store, "second")
	_ = storeSession(t, mgr, store, "third")
	if code, _ := patchSessionJSON(t, srv, a, map[string]interface{}{"tags": []string{"backend"}}); code != http.StatusOK {
		t.Fatal("patch a")
	}
	if code, _ := patchSessionJSON(t, srv, b, map[string]interface{}{"tags": []string{"ui"}}); code != http.StatusOK {
		t.Fatal("patch b")
	}

	_, body := getSessions(t, srv, "tags=Backend,UI")
	ids := listedIDs(body)
	if len(ids) != 2 {
		t.Fatalf("tag filter kept %v, want both tagged sessions", ids)
	}

	_, body = getSessions(t, srv, "tags=docs")
	if ids := listedIDs(body); len(ids) != 0 {
		t.Fatalf("a tag nobody carries kept %v", ids)
	}
}

func TestSessionListReportsTheOriginOfAGatewayChat(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	local := storeSession(t, mgr, store, "opened here")
	chat := storeSession(t, mgr, store, "telegram chat")
	st := mgr.SessionByID(chat)
	if st == nil {
		t.Fatalf("session %q not registered", chat)
	}
	st.SetOrigin(session.GatewayOrigin("telegram"))
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	_, body := getSessions(t, srv, "origin=gateway")
	raw, _ := body["sessions"].([]interface{})
	if len(raw) != 1 {
		t.Fatalf("gateway listing holds %d rows, want 1", len(raw))
	}
	row, _ := raw[0].(map[string]interface{})
	if got, _ := row["origin"].(string); got != "gateway:telegram" {
		t.Fatalf("row origin = %q, want gateway:telegram", got)
	}

	_, body = getSessions(t, srv, "origin=local")
	if ids := listedIDs(body); len(ids) != 1 || ids[0] != local {
		t.Fatalf("local listing = %v, want only %q", ids, local)
	}

	// A session keeps where it came from: a second surface must not relabel it.
	st.SetOrigin("")
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	_, body = getSessions(t, srv, "origin=gateway")
	if ids := listedIDs(body); len(ids) != 1 {
		t.Fatalf("the origin stamp was overwritten: gateway listing = %v", ids)
	}
}

func TestPinnedSessionsLeadEveryOrder(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	var ids []string
	for _, title := range []string{"alpha", "bravo", "charlie"} {
		id := storeSession(t, mgr, store, "question "+title)
		if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"title": title}); code != http.StatusOK {
			t.Fatalf("patch title: status %d body %v", code, body)
		}
		ids = append(ids, id)
	}
	// The oldest and alphabetically first: last in a newest-first listing, and
	// first in a title one - so a pin on it is visible in neither order by luck.
	if code, body := patchSessionJSON(t, srv, ids[0], map[string]interface{}{"pinned": true}); code != http.StatusOK {
		t.Fatalf("pin: status %d body %v", code, body)
	}

	for _, query := range []string{"", "sort=title&order=desc", "sort=created&order=asc"} {
		_, body := getSessions(t, srv, query)
		listed := listedIDs(body)
		if len(listed) == 0 || listed[0] != ids[0] {
			t.Fatalf("query %q: pinned row is not first: %v", query, listed)
		}
	}

	_, body := getSessions(t, srv, "")
	raw, _ := body["sessions"].([]interface{})
	row, _ := raw[0].(map[string]interface{})
	if pinned, _ := row["pinned"].(bool); !pinned {
		t.Fatalf("row does not report the pin: %v", row)
	}
	if at, _ := row["pinnedAt"].(string); at == "" {
		t.Fatalf("row does not report when it was pinned: %v", row)
	}

	if code, _ := patchSessionJSON(t, srv, ids[0], map[string]interface{}{"pinned": false}); code != http.StatusOK {
		t.Fatal("unpin")
	}
	// Checked against a title order rather than the default one: unpinning is a
	// write, so it leaves the session the most recently touched and it would
	// lead a newest-first listing on its own merits.
	_, body = getSessions(t, srv, "sort=title&order=desc")
	listed := listedIDs(body)
	if listed[len(listed)-1] != ids[0] {
		t.Fatalf("an unpinned row did not fall back into the order: %v", listed)
	}
}

// postPinOrder sends one reorder request.
func postPinOrder(t *testing.T, srv *Server, ids []string) (int, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/pins/reorder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var parsed map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	return rec.Code, parsed
}

func TestANewPinGoesOnTopOfTheOnesAlreadyThere(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, storeSession(t, mgr, store, "question"))
	}
	for _, id := range ids {
		if code, body := patchSessionJSON(t, srv, id, map[string]interface{}{"pinned": true}); code != http.StatusOK {
			t.Fatalf("pin: status %d body %v", code, body)
		}
	}
	_, body := getSessions(t, srv, "")
	// Pinned in order, so the last one pinned leads and the first one trails.
	want := []string{ids[2], ids[1], ids[0]}
	if got := listedIDs(body); len(got) < 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("pins read %v, want the newest first: %v", got, want)
	}
}

func TestReorderPinsPutsThemWhereTheyWereDragged(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	var ids []string
	for i := 0; i < 3; i++ {
		id := storeSession(t, mgr, store, "question")
		if code, _ := patchSessionJSON(t, srv, id, map[string]interface{}{"pinned": true}); code != http.StatusOK {
			t.Fatal("pin")
		}
		ids = append(ids, id)
	}

	order := []string{ids[1], ids[2], ids[0]}
	if code, body := postPinOrder(t, srv, order); code != http.StatusOK {
		t.Fatalf("reorder: status %d body %v", code, body)
	}
	_, body := getSessions(t, srv, "")
	got := listedIDs(body)
	for i, want := range order {
		if got[i] != want {
			t.Fatalf("after the drag the pins read %v, want %v", got[:3], order)
		}
	}

	// And the order survives a sort the operator picks for everything below.
	_, body = getSessions(t, srv, "sort=title&order=asc")
	got = listedIDs(body)
	for i, want := range order {
		if got[i] != want {
			t.Fatalf("a title sort reordered the pins: %v", got[:3])
		}
	}
}

func TestReorderPinsRefusesWhatIsNotAPin(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	pinned := storeSession(t, mgr, store, "pinned")
	plain := storeSession(t, mgr, store, "plain")
	if code, _ := patchSessionJSON(t, srv, pinned, map[string]interface{}{"pinned": true}); code != http.StatusOK {
		t.Fatal("pin")
	}

	cases := map[string][]string{
		"a session that is not pinned":  {pinned, plain},
		"a session that does not exist": {pinned, "sess_deadbeefdeadbeefdeadbeef"},
		"a malformed id":                {"../../etc"},
		"nothing at all":                {},
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			if code, _ := postPinOrder(t, srv, ids); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
		})
	}
	// Nothing was written: the one real pin kept its place.
	_, body := getSessions(t, srv, "")
	if got := listedIDs(body); got[0] != pinned {
		t.Fatalf("a refused reorder moved something: %v", got)
	}
}
