//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func TestWorkspaceViewerFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:    "workspace-viewer",
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/workspace_viewer.feature"}, TestingT: t, Strict: true},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &sessionChangesState{}
			var data []byte
			var status int
			var media string
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			request := func(method, path, body string, headers map[string]string) error {
				req, err := http.NewRequest(method, s.ts.URL+"/coddy/sessions/"+s.sessionID+"/workspace/"+path, strings.NewReader(body))
				if err != nil {
					return err
				}
				for key, value := range headers {
					req.Header.Set(key, value)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					return err
				}
				defer func() { _ = res.Body.Close() }()
				status = res.StatusCode
				data, err = io.ReadAll(res.Body)
				return err
			}
			sc.Step(`^a file viewer server with a workspace$`, s.startServer)
			sc.Step(`^a workspace file "([^"]+)" containing "([^"]*)"$`, func(path, content string) error {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(s.workspace, path)), 0o755); err != nil {
					return err
				}
				return s.workspaceContains(path, content)
			})
			sc.Step(`^I browse the directory "([^"]+)"$`, func(path string) error { return request("GET", "tree?path_rel="+url.QueryEscape(path), "", nil) })
			sc.Step(`^the directory lists the file "([^"]+)"$`, func(name string) error {
				var tree struct {
					Entries []workspaceEntry `json:"entries"`
				}
				if status != 200 {
					return fmt.Errorf("tree status %d: %s", status, data)
				}
				if err := json.Unmarshal(data, &tree); err != nil {
					return err
				}
				if len(tree.Entries) != 1 || tree.Entries[0].Name != name || tree.Entries[0].Kind != "file" {
					return fmt.Errorf("unexpected tree: %s", data)
				}
				return nil
			})
			sc.Step(`^a large workspace text file$`, func() error { return s.workspaceContains("large.txt", strings.Repeat("hello world\n", 100000)) })
			sc.Step(`^I read the large file from line offset (\d+)$`, func(offset int) error {
				return request("GET", fmt.Sprintf("text?path_rel=large.txt&offset=%d&max_lines=1", offset), "", nil)
			})
			sc.Step(`^the next line offset is (\d+) and more lines remain$`, func(offset int) error {
				var page struct {
					Next int  `json:"next_offset"`
					More bool `json:"has_more"`
				}
				if status != 200 {
					return fmt.Errorf("text status %d: %s", status, data)
				}
				if err := json.Unmarshal(data, &page); err != nil {
					return err
				}
				if page.Next != offset || !page.More {
					return fmt.Errorf("unexpected page: %s", data)
				}
				return nil
			})
			sc.Step(`^I mint a media URL for "([^"]+)" using API authentication$`, func(path string) error {
				s.srv.SetExtraAuthTokens([]string{"viewer-test-secret"})
				body, err := json.Marshal(map[string]string{"path_rel": path})
				if err != nil {
					return err
				}
				if err := request("POST", "media-token", string(body), map[string]string{"Authorization": "Bearer viewer-test-secret"}); err != nil {
					return err
				}
				if status != 200 {
					return fmt.Errorf("token status %d: %s", status, data)
				}
				var token struct {
					Token string `json:"token"`
				}
				if err := json.Unmarshal(data, &token); err != nil {
					return err
				}
				media = "raw?path_rel=" + url.QueryEscape(path) + "&access_token=" + url.QueryEscape(token.Token)
				return nil
			})
			sc.Step(`^I read the first five bytes using only that URL$`, func() error { return request("GET", media, "", map[string]string{"Range": "bytes=0-4"}) })
			sc.Step(`^the response is a partial range containing "([^"]*)"$`, func(want string) error {
				if status != 206 || string(data) != want {
					return fmt.Errorf("range %d: %s", status, data)
				}
				return nil
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("workspace viewer feature suite failed")
	}
}
