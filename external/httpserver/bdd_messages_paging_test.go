//go:build http

package httpserver

// Godog harness for features/session_messages_paging.feature: reads a long
// session through the paged GET /coddy/sessions/{id}/messages and
// GET /coddy/sessions/{id}/tool-calls, and rewinds it at an index taken from a
// page's window, the way the web UI does.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type messagesPagingState struct {
	t     *testing.T
	srv   *Server
	sid   string
	turns int
	pages []pagedMessagesBody
}

func (s *messagesPagingState) serverRunning() error { return nil }

func (s *messagesPagingState) storedSession(turns int) error {
	s.srv, s.sid = pagedSession(s.t, turns)
	s.turns = turns
	s.pages = nil
	return nil
}

func (s *messagesPagingState) read(query string) (pagedMessagesBody, error) {
	rec, body := getPaged(s.t, s.srv, "/coddy/sessions/"+s.sid+"/messages"+query)
	if rec.Code != http.StatusOK {
		return body, fmt.Errorf("GET messages%s: status %d: %s", query, rec.Code, rec.Body.String())
	}
	return body, nil
}

func (s *messagesPagingState) readNewest(limit int) error {
	page, err := s.read(fmt.Sprintf("?limit=%d", limit))
	if err != nil {
		return err
	}
	s.pages = []pagedMessagesBody{page}
	return nil
}

func (s *messagesPagingState) readOlderPages(limit int) error {
	for guard := 0; ; guard++ {
		oldest := s.pages[len(s.pages)-1]
		if oldest.Window.Offset == 0 {
			return nil
		}
		if guard > s.turns*4 {
			return fmt.Errorf("paging never reached the first message")
		}
		page, err := s.read(fmt.Sprintf("?limit=%d&before=%d", limit, oldest.Window.Offset))
		if err != nil {
			return err
		}
		if page.Window.Offset+len(page.Messages) != oldest.Window.Offset {
			return fmt.Errorf("page at %d with %d messages does not end at %d", page.Window.Offset, len(page.Messages), oldest.Window.Offset)
		}
		s.pages = append(s.pages, page)
	}
}

func (s *messagesPagingState) pageEndsWithNewest() error {
	page := s.pages[0]
	if page.Window.Offset+len(page.Messages) != page.Window.Total {
		return fmt.Errorf("page [%d,+%d) does not reach the end %d", page.Window.Offset, len(page.Messages), page.Window.Total)
	}
	if last := page.Messages[len(page.Messages)-1]; last.Content != fmt.Sprintf("answer %d", s.turns) {
		return fmt.Errorf("last message %q", last.Content)
	}
	return nil
}

func (s *messagesPagingState) pageOpensWithPrompt() error {
	if first := s.pages[0].Messages[0]; first.Role != "user" || !strings.HasPrefix(first.Content, "prompt ") {
		return fmt.Errorf("page opens with %s %q", first.Role, first.Content)
	}
	return nil
}

func (s *messagesPagingState) windowCountsPrompts() error {
	page := s.pages[0]
	var n int
	if _, err := fmt.Sscanf(page.Messages[0].Content, "prompt %d", &n); err != nil {
		return err
	}
	if page.Window.TurnsBefore != n-1 || page.Window.UserRowsBefore != n-1 {
		return fmt.Errorf("page opens with prompt %d, window %+v", n, page.Window)
	}
	return nil
}

func (s *messagesPagingState) everyMessageOnce() error {
	var all []string
	for i := len(s.pages) - 1; i >= 0; i-- {
		for _, m := range s.pages[i].Messages {
			all = append(all, m.Role+":"+m.Content+m.ToolCallID)
		}
	}
	full, err := s.read("")
	if err != nil {
		return err
	}
	if len(all) != len(full.Messages) {
		return fmt.Errorf("pages hold %d messages, the history %d", len(all), len(full.Messages))
	}
	for i, m := range full.Messages {
		if all[i] != m.Role+":"+m.Content+m.ToolCallID {
			return fmt.Errorf("message %d: pages %q, history %q", i, all[i], m.Role+":"+m.Content)
		}
	}
	return nil
}

func (s *messagesPagingState) toolCallsPerPage() error {
	for _, page := range s.pages {
		from := page.Window.Offset
		to := from + len(page.Messages)
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/coddy/sessions/%s/tool-calls?from=%d&to=%d", s.sid, from, to), nil)
		rec := httptest.NewRecorder()
		s.srv.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("tool-calls [%d,%d): status %d", from, to, rec.Code)
		}
		var body struct {
			ToolCalls []struct {
				ToolCallID string `json:"toolCallId"`
			} `json:"toolCalls"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			return err
		}
		want := map[string]bool{}
		for _, m := range page.Messages {
			if m.ToolCallID != "" {
				want[m.ToolCallID] = true
			}
		}
		if len(body.ToolCalls) != len(want) {
			return fmt.Errorf("tool-calls [%d,%d) lists %d calls, the page issued %d", from, to, len(body.ToolCalls), len(want))
		}
		for _, c := range body.ToolCalls {
			if !want[c.ToolCallID] {
				return fmt.Errorf("tool-calls [%d,%d) lists %s from another page", from, to, c.ToolCallID)
			}
		}
	}
	return nil
}

func (s *messagesPagingState) rewindAtFirstPrompt() error {
	page := s.pages[0]
	// The first prompt of the page is the page's prompt number 0.
	body, _ := json.Marshal(map[string]int{"userMessageIndex": page.Window.TurnsBefore})
	req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+s.sid+"/rewind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("rewind: status %d: %s", rec.Code, rec.Body.String())
	}
	return nil
}

func (s *messagesPagingState) historyEndsBeforePrompt() error {
	full, err := s.read("")
	if err != nil {
		return err
	}
	want := s.pages[0].Window.Offset
	if len(full.Messages) != want {
		return fmt.Errorf("history holds %d messages after the rewind, want %d", len(full.Messages), want)
	}
	var n int
	if _, err := fmt.Sscanf(s.pages[0].Messages[0].Content, "prompt %d", &n); err != nil {
		return err
	}
	if last := full.Messages[len(full.Messages)-1]; last.Content != fmt.Sprintf("answer %d", n-1) {
		return fmt.Errorf("history ends with %q, want the answer before prompt %d", last.Content, n)
	}
	return nil
}

func TestSessionMessagesPagingFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "session_messages_paging",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &messagesPagingState{t: t}
			sc.Step(`^a running coddy HTTP server$`, s.serverRunning)
			sc.Step(`^a stored session of (\d+) turns, each a prompt, a tool step and an answer$`, s.storedSession)
			sc.Step(`^I read the newest (\d+) messages of the session$`, s.readNewest)
			sc.Step(`^I read older pages of (\d+) messages until the first message$`, s.readOlderPages)
			sc.Step(`^the page ends with the newest message$`, s.pageEndsWithNewest)
			sc.Step(`^the page opens with the prompt of its turn$`, s.pageOpensWithPrompt)
			sc.Step(`^the window counts the prompts before the page$`, s.windowCountsPrompts)
			sc.Step(`^every message of the history was read exactly once$`, s.everyMessageOnce)
			sc.Step(`^every page of tool calls lists the calls of its own messages only$`, s.toolCallsPerPage)
			sc.Step(`^I rewind the session at the first prompt of that page$`, s.rewindAtFirstPrompt)
			sc.Step(`^the history ends right before that prompt$`, s.historyEndsBeforePrompt)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_messages_paging.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session messages paging feature failed")
	}
}
