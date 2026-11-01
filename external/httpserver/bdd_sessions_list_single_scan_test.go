//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func TestSessionsListSingleScanFeature(t *testing.T) {
	var fs *session.FileStore
	var srv *Server
	var scans int
	var response *httptest.ResponseRecorder
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a stored conversation and its subagent$`, func() error {
				_, server, root := testHTTPServerPersist(t)
				srv = server
				for _, f := range []struct{ dir, meta string }{
					{"sess_example", `{"version":1,"id":"sess_example","pinned":true,"updatedAt":"2026-10-20T10:00:00Z"}`},
					{filepath.Join("sess_example", "subagents", "sess_child"), `{"version":1,"id":"sess_child","subagentRun":true,"updatedAt":"2026-10-19T10:00:00Z"}`},
				} {
					dir := filepath.Join(root, f.dir)
					if err := os.MkdirAll(dir, 0755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(f.meta), 0644); err != nil {
						return err
					}
				}
				fs = &session.FileStore{Root: root}
				return nil
			})
			sc.Step(`^I list History with include_subagents set to "([^"]*)"$`, func(include string) error {
				response = httptest.NewRecorder()
				srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/coddy/sessions?include_subagents="+include, nil))
				if response.Code != http.StatusOK {
					return fmt.Errorf("status %d: %s", response.Code, response.Body.String())
				}
				scans = 0
				scan := func(o session.ListOptions) ([]session.SessionListEntry, error) {
					scans++
					return fs.ListSnapshotsWith(o)
				}
				_, _, err := listSessionRows(scan, session.ListOptions{Archived: session.ArchiveExclude, IncludeEmpty: true, IncludeSubagents: include == "true"})
				return err
			})
			sc.Step(`^the HTTP response contains "([^"]*)" and global active count 0$`, func(expected string) error {
				var body struct {
					Sessions []struct {
						ID string `json:"id"`
					} `json:"sessions"`
					Active int `json:"active_count"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					return err
				}
				var ids []string
				for _, row := range body.Sessions {
					ids = append(ids, row.ID)
				}
				if strings.Join(ids, ",") != expected || body.Active != 0 {
					return fmt.Errorf("IDs %v, active count %d", ids, body.Active)
				}
				return nil
			})
			sc.Step(`^the listing selection uses one filesystem scan$`, func() error {
				if scans != 1 {
					return fmt.Errorf("scans = %d", scans)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "progress", Paths: []string{"../../features/sessions_list_single_scan.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("feature failed")
	}
}
