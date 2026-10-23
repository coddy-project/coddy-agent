//go:build http

package httpserver

// Godog harness for features/todo_checklist_http.feature: the transcript read
// of a session carries its todo checklist, which the bundled UI shows under
// the newest message without asking GET /coddy/sessions/{id}/plan.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type todoChecklistHTTPState struct {
	ts   *httptest.Server
	sid  string
	read map[string]json.RawMessage
}

// todoChecklistServer starts a server with one empty session.
func todoChecklistServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mgr, srv, _ := testHTTPServerPersist(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return ts, res.SessionID
}

// putTodoPlan writes the checklist through the route other clients use.
func putTodoPlan(ts *httptest.Server, sid string, entries []acp.PlanEntry) error {
	buf, _ := json.Marshal(map[string]interface{}{"entries": entries})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/coddy/sessions/"+sid+"/plan", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT plan: status %d", res.StatusCode)
	}
	return nil
}

// readTranscript is the SPA's read of a conversation.
func readTranscript(ts *httptest.Server, sid string) (map[string]json.RawMessage, error) {
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/coddy/sessions/"+sid+"/messages?limit=50", nil)
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET messages: status %d", res.StatusCode)
	}
	var out map[string]json.RawMessage
	return out, json.NewDecoder(res.Body).Decode(&out)
}

var todoChecklistFixture = []acp.PlanEntry{
	{Content: "Read the failing test", Status: "completed"},
	{Content: "Fix the parser", Status: "in_progress"},
	{Content: "Run the whole suite", Status: "pending"},
}

func TestTodoChecklistHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "todo_checklist_http",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &todoChecklistHTTPState{}
			sc.Step(`^a session whose checklist has an item done, one in progress and one pending$`, func() error {
				s.ts, s.sid = todoChecklistServer(t)
				return putTodoPlan(s.ts, s.sid, todoChecklistFixture)
			})
			sc.Step(`^the client reads the transcript$`, func() error {
				out, err := readTranscript(s.ts, s.sid)
				s.read = out
				return err
			})
			sc.Step(`^the read carries the checklist in its order with each item's status$`, func() error {
				var got []acp.PlanEntry
				if err := json.Unmarshal(s.read["todoPlan"], &got); err != nil {
					return fmt.Errorf("todoPlan %s: %w", s.read["todoPlan"], err)
				}
				if fmt.Sprint(got) != fmt.Sprint(todoChecklistFixture) {
					return fmt.Errorf("todoPlan = %+v, want %+v", got, todoChecklistFixture)
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/todo_checklist_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("todo checklist HTTP feature failed")
	}
}

// A session without a checklist, or whose checklist was archived, has no
// todoPlan in its read: a client then shows no list.
func TestTranscriptReadOmitsAnEmptyTodoChecklist(t *testing.T) {
	ts, sid := todoChecklistServer(t)
	out, err := readTranscript(ts, sid)
	if err != nil {
		t.Fatal(err)
	}
	if raw, ok := out["todoPlan"]; ok {
		t.Fatalf("a session without a checklist reads todoPlan %s", raw)
	}

	if err := putTodoPlan(ts, sid, todoChecklistFixture); err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(ts.URL+"/coddy/sessions/"+sid+"/plan/archive", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	out, err = readTranscript(ts, sid)
	if err != nil {
		t.Fatal(err)
	}
	if raw, ok := out["todoPlan"]; ok {
		t.Fatalf("an archived checklist still reads todoPlan %s", raw)
	}
}
