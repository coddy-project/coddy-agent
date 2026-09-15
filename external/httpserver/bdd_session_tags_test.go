//go:build http

package httpserver

// Godog harness for features/session_tags_archive.feature: the tag and archive
// fields of a session over the live HTTP surface - what PATCH /coddy/sessions
// stores, what the listing filters and orders by, what the archive scope of
// bulk-delete removes, and the tags POST /coddy/describe proposes next to a
// title. The model behind describe is a stub, so the spec stays deterministic.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type sessTagsState struct {
	sessMgmtState
	describe map[string]interface{}
	// tagged remembers the positions a scenario put tags on, so the step that
	// checks the rest does not have to name them a second time - and cannot
	// quietly check the wrong session when a scenario tags another one.
	tagged map[int]bool
}

// patchSession sends one PATCH body to the nth stored session.
func (s *sessTagsState) patchSession(nth int, payload map[string]interface{}) error {
	id, err := s.idAt(nth)
	if err != nil {
		return err
	}
	status, body, err := s.request(http.MethodPatch, "/coddy/sessions/"+url.PathEscape(id), payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("patch returned %d: %v", status, body)
	}
	return nil
}

func (s *sessTagsState) tagSession(nth int, tags string) error {
	if s.tagged == nil {
		s.tagged = map[int]bool{}
	}
	s.tagged[nth] = true
	return s.patchSession(nth, map[string]interface{}{"tags": splitSpecList(tags)})
}

func (s *sessTagsState) titleSession(nth int, title string) error {
	return s.patchSession(nth, map[string]interface{}{"title": title})
}

func (s *sessTagsState) archiveSession(nth int) error {
	return s.patchSession(nth, map[string]interface{}{"archived": true})
}

// listQuery loads the listing produced by one query string into s.rows.
func (s *sessTagsState) listQuery(query string) error {
	path := "/coddy/sessions"
	if query != "" {
		path += "?" + query
	}
	status, body, err := s.request(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("session list returned %d: %v", status, body)
	}
	raw, _ := body["sessions"].([]interface{})
	s.rows = make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]interface{}); ok {
			s.rows = append(s.rows, m)
		}
	}
	return nil
}

func (s *sessTagsState) rowTags(nth int) ([]string, error) {
	if len(s.rows) == 0 {
		if err := s.listQuery("archived=all"); err != nil {
			return nil, err
		}
	}
	row, err := s.rowOf(nth)
	if err != nil {
		return nil, err
	}
	raw, _ := row["tags"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out, nil
}

func (s *sessTagsState) reportsTags(nth int, want string) error {
	if err := s.listQuery("archived=all"); err != nil {
		return err
	}
	got, err := s.rowTags(nth)
	if err != nil {
		return err
	}
	if strings.Join(got, ", ") != strings.Join(splitSpecList(want), ", ") {
		return fmt.Errorf("session %d reports tags %v, want %q", nth, got, want)
	}
	return nil
}

// othersReportNoTags checks every session a scenario did not tag.
func (s *sessTagsState) othersReportNoTags() error {
	if len(s.tagged) == 0 {
		return fmt.Errorf("no session was tagged, so there is no rest to check")
	}
	for i := range s.ids {
		if s.tagged[i+1] {
			continue
		}
		got, err := s.rowTags(i + 1)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			return fmt.Errorf("session %d carries tags %v it was never given", i+1, got)
		}
	}
	return nil
}

// startedByGateway stamps a stored session the way the messenger gateway does.
// The stamp is not settable over HTTP - the surface that creates a session
// writes it - so the spec reaches through the store the gateway shares.
func (s *sessTagsState) startedByGateway(nth int, messenger string) error {
	id, err := s.idAt(nth)
	if err != nil {
		return err
	}
	st := s.mgr.SessionByID(id)
	if st == nil {
		return fmt.Errorf("session %q not registered", id)
	}
	st.SetOrigin(session.GatewayOrigin(messenger))
	return s.store.Save(st)
}

func (s *sessTagsState) listOfGateways() error {
	return s.listQuery("origin=gateway")
}

func (s *sessTagsState) listStartedHere() error {
	return s.listQuery("origin=local")
}

func (s *sessTagsState) listTagged(tags string) error {
	return s.listQuery("tags=" + url.QueryEscape(tags))
}

func (s *sessTagsState) listSortedBy(key, direction string) error {
	order := "desc"
	if strings.EqualFold(direction, "ascending") {
		order = "asc"
	}
	return s.listQuery("sort=" + url.QueryEscape(key) + "&order=" + order)
}

// listingHolds checks the listing already loaded into s.rows against positions.
func (s *sessTagsState) listingHolds(positions ...int) error {
	want := make([]string, 0, len(positions))
	for _, nth := range positions {
		id, err := s.idAt(nth)
		if err != nil {
			return err
		}
		want = append(want, id)
	}
	got := make([]string, 0, len(s.rows))
	for _, row := range s.rows {
		id, _ := row["id"].(string)
		got = append(got, id)
	}
	if len(got) != len(want) {
		return fmt.Errorf("listing holds %d sessions, want %d: %v", len(got), len(want), got)
	}
	for _, id := range want {
		found := false
		for _, have := range got {
			if have == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("listing %v is missing %q", got, id)
		}
	}
	return nil
}

func (s *sessTagsState) defaultListingHolds(a, b int) error {
	if err := s.listQuery(""); err != nil {
		return err
	}
	return s.listingHolds(a, b)
}

func (s *sessTagsState) archivedListingHolds(nth int) error {
	if err := s.listQuery("archived=only"); err != nil {
		return err
	}
	return s.listingHolds(nth)
}

func (s *sessTagsState) fullListingHoldsEverySession() error {
	if err := s.listQuery("archived=all"); err != nil {
		return err
	}
	positions := make([]int, 0, len(s.ids))
	for i := range s.ids {
		positions = append(positions, i+1)
	}
	return s.listingHolds(positions...)
}

func (s *sessTagsState) taggedListingHolds(a, b int) error {
	return s.listingHolds(a, b)
}

func (s *sessTagsState) deleteEveryArchivedSession() error {
	return s.bulkDelete(map[string]interface{}{"scope": "archived"})
}

func (s *sessTagsState) listingReads(first, second, third string) error {
	want := []string{first, second, third}
	got := make([]string, 0, len(s.rows))
	for _, row := range s.rows {
		title, _ := row["title"].(string)
		got = append(got, title)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		return fmt.Errorf("listing reads %v, want %v", got, want)
	}
	return nil
}

// titleModelAnswers points describe at a stub speaking the two-line shape the
// prompt asks for: the phrase, then the tags.
func (s *sessTagsState) titleModelAnswers(phrase, tags string) error {
	reply := phrase + "\ntags: " + tags
	s.srv.providerFactory = func(*config.Config) (llm.Provider, error) {
		return fakeProvider{reply: reply}, nil
	}
	return nil
}

func (s *sessTagsState) askForDescription() error {
	status, body, err := s.request(http.MethodPost, "/coddy/describe", map[string]interface{}{
		"text": "Please refactor the memory tree endpoint to reject traversal and add tests.",
	})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("describe returned %d: %v", status, body)
	}
	s.describe = body
	return nil
}

func (s *sessTagsState) descriptionIs(want string) error {
	if got, _ := s.describe["short"].(string); got != want {
		return fmt.Errorf("description = %q, want %q", got, want)
	}
	return nil
}

func (s *sessTagsState) descriptionProposesTags(want string) error {
	raw, _ := s.describe["tags"].([]interface{})
	got := make([]string, 0, len(raw))
	for _, item := range raw {
		if str, ok := item.(string); ok {
			got = append(got, str)
		}
	}
	if strings.Join(got, ", ") != strings.Join(splitSpecList(want), ", ") {
		return fmt.Errorf("describe proposed tags %v, want %q", got, want)
	}
	return nil
}

// splitSpecList reads the comma separated lists the feature file writes.
func splitSpecList(s string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func initializeSessionTagsScenario(sc *godog.ScenarioContext) {
	s := &sessTagsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.describe = nil
		s.tagged = map[int]bool{}
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^(\d+) stored sessions$`, s.storedSessions)

	sc.Step(`^I tag session (\d+) with "([^"]*)"$`, s.tagSession)
	sc.Step(`^session (\d+) is tagged "([^"]*)"$`, s.tagSession)
	sc.Step(`^session (\d+) is titled "([^"]*)"$`, s.titleSession)
	sc.Step(`^I archive session (\d+)$`, s.archiveSession)
	sc.Step(`^session (\d+) is archived$`, s.archiveSession)
	sc.Step(`^I list the sessions tagged "([^"]*)"$`, s.listTagged)
	sc.Step(`^session (\d+) was started by the (\w+) gateway$`, s.startedByGateway)
	sc.Step(`^I list the sessions of the gateways$`, s.listOfGateways)
	sc.Step(`^I list the sessions started here$`, s.listStartedHere)
	sc.Step(`^I list the sessions sorted by "([^"]*)" (ascending|descending)$`, s.listSortedBy)
	sc.Step(`^I delete every archived session in one request$`, s.deleteEveryArchivedSession)
	sc.Step(`^the title model answers "([^"]*)" with tags "([^"]*)"$`, s.titleModelAnswers)
	sc.Step(`^I ask for a description of a long request$`, s.askForDescription)

	sc.Step(`^session (\d+) reports tags "([^"]*)"$`, s.reportsTags)
	sc.Step(`^the other sessions report no tags$`, s.othersReportNoTags)
	sc.Step(`^the listing holds sessions (\d+) and (\d+)$`, s.taggedListingHolds)
	sc.Step(`^the listing holds session (\d+)$`, func(nth int) error {
		return s.listingHolds(nth)
	})
	sc.Step(`^the default listing holds sessions (\d+) and (\d+)$`, s.defaultListingHolds)
	sc.Step(`^the archived listing holds session (\d+)$`, s.archivedListingHolds)
	sc.Step(`^the full listing holds every session$`, s.fullListingHoldsEverySession)
	sc.Step(`^the response reports (\d+) deleted sessions$`, s.reportsDeletedCount)
	sc.Step(`^only session (\d+) is left$`, s.onlySessionLeft)
	sc.Step(`^the listing reads "([^"]*)", "([^"]*)", "([^"]*)"$`, s.listingReads)
	sc.Step(`^the description is "([^"]*)"$`, s.descriptionIs)
	sc.Step(`^the description proposes tags "([^"]*)"$`, s.descriptionProposesTags)
}

func TestSessionTagsArchiveFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session-tags-archive",
		ScenarioInitializer: initializeSessionTagsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_tags_archive.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session tags and archive feature failed")
	}
}
