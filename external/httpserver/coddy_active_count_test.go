//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestCoddySessionsListCountsActiveSubagentTurns(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		started <- st.GetID()
		<-release
		return string(acp.StopReasonEndTurn), nil
	}
	mgr, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	ctx := context.Background()
	parent, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	childID := session.NewSessionID()
	if _, err := mgr.CreateSubagentSession(ctx, session.SubagentSpec{
		ID:              childID,
		ParentSessionID: parent.SessionID,
		Name:            "reviewer",
		TaskID:          "bg_1",
		CWD:             t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		_, _ = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: parent.SessionID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}})
	}()
	go func() {
		defer wg.Done()
		_, _ = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: other.SessionID, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}})
	}()
	go func() {
		defer wg.Done()
		_, _ = mgr.RunSubagentTurn(ctx, childID, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hold"}}, noopSender{})
	}()
	for n := 0; n < 3; n++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("an expected session turn did not start")
		}
	}
	t.Cleanup(func() {
		close(release)
		wg.Wait()
	})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/coddy/sessions?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		ActiveCount int `json:"active_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ActiveCount != 3 {
		t.Fatalf("active_count = %d, want 3 including the subagent turn", body.ActiveCount)
	}
}
