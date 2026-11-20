//go:build http

package httpserver

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func (s *wsFeatureState) createBranch(branch string) error {
	return s.postWorkspace(map[string]interface{}{"branch": branch, "create": true})
}

func (s *wsFeatureState) requestGitGraph() error {
	req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/workspace/graph", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	return s.do(req)
}

func (s *wsFeatureState) graphShowsHEAD(branch string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("graph status %d: %v", s.status, s.body)
	}
	rows, _ := s.body["commits"].([]interface{})
	if len(rows) == 0 {
		return fmt.Errorf("empty graph")
	}
	row := rows[0].(map[string]interface{})
	refs := fmt.Sprint(row["refs"])
	if !stringsContainsRef(row["refs"], "HEAD") || !stringsContainsRef(row["refs"], branch) {
		return fmt.Errorf("refs = %s", refs)
	}
	return nil
}

func stringsContainsRef(raw interface{}, want string) bool {
	refs, _ := raw.([]interface{})
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

func TestWorkspaceCreateBranchRefusesLockedAndExistingBranches(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main, feature/login"))
	mustStep(t, s.sessionRootedAt("repo"))
	mustStep(t, s.createBranch("main"))
	if s.status != http.StatusConflict {
		t.Fatalf("existing status = %d", s.status)
	}
	mustStep(t, s.sessionHasUserMessage())
	mustStep(t, s.createBranch("feature/new"))
	if s.status != http.StatusConflict {
		t.Fatalf("locked status = %d", s.status)
	}
	mustStep(t, s.requestGitGraph())
	mustStep(t, s.graphShowsHEAD("main"))
}

func TestWorkspaceDraftCreatesBranchImmediately(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main"))
	target := s.ts.URL + "/coddy/workspace/branches?path=" + url.QueryEscape(s.folders["repo"])
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewBufferString(`{"branch":"draft/new"}`))
	mustStep(t, err)
	mustStep(t, s.do(req))
	if s.status != http.StatusOK || s.body["branch"] != "draft/new" {
		t.Fatalf("draft = %d %v", s.status, s.body)
	}
	for _, query := range []string{"limit=0", "limit=501", "offset=-1", "offset=oops"} {
		req, err := http.NewRequest(http.MethodGet, s.ts.URL+"/coddy/workspace/graph?path="+url.QueryEscape(s.folders["repo"])+"&"+query, nil)
		mustStep(t, err)
		mustStep(t, s.do(req))
		if s.status != http.StatusBadRequest {
			t.Fatalf("%s = %d", query, s.status)
		}
	}
}

func TestWorkspaceGraphTimesOut(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main"))
	mustStep(t, s.sessionRootedAt("repo"))
	saved := workspaceGraphTimeout
	workspaceGraphTimeout = time.Nanosecond
	t.Cleanup(func() { workspaceGraphTimeout = saved })
	mustStep(t, s.requestGitGraph())
	if s.status != http.StatusBadRequest {
		t.Fatalf("timeout status = %d", s.status)
	}
	message, _ := s.body["error"].(map[string]interface{})["message"].(string)
	if message != "reading the git history timed out" {
		t.Fatalf("timeout message = %q", message)
	}
}
