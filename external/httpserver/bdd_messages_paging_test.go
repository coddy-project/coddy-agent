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

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type messagesPagingState struct {
	t     *testing.T
	srv   *Server
	sid   string
	turns int
	pages []pagedMessagesBody
	// custom writes the turns a scenario changes, see pagedSessionOf.
	custom func(st *session.State, i int) bool
}

func (s *messagesPagingState) serverRunning() error { return nil }

// storedSession names the session of the scenario; it is written when a step
// first reads it, so that a scenario can still change one of its turns.
func (s *messagesPagingState) storedSession(turns int) error {
	s.srv, s.sid, s.custom = nil, "", nil
	s.turns = turns
	s.pages = nil
	return nil
}

func (s *messagesPagingState) store() {
	if s.srv == nil {
		s.srv, s.sid = pagedSessionOf(s.t, s.turns, s.custom)
	}
}

// The two parts of an answer the output limit cut off and the model went on
// from: the first stops in the middle of a sentence, the second picks it up.
const (
	continuedHead = "The notes hold the first half of it and then "
	continuedRest = "the second half."
)

// answerCutOffAndContinued rewrites the turn of prompt n into a long one, the
// way an agent turn that hit the output limit is stored: two tool steps, the
// answer cut off (truncated), the step that went on from it - which called a
// tool of its own - and the final answer. Pages of ten messages from the end
// of this history would have their boundary between the two parts, were a page
// free to start or end on the message that goes on from a truncated one.
func (s *messagesPagingState) answerCutOffAndContinued(prompt int) error {
	if s.srv != nil {
		return fmt.Errorf("the session was written already; change its turns before reading it")
	}
	s.custom = func(st *session.State, i int) bool {
		if i != prompt {
			return false
		}
		call := func(id string) {
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_file", InputJSON: `{"path":"a.go"}`}}})
			st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "result " + id})
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt %d", i)})
		call(fmt.Sprintf("long_%d_1", i))
		call(fmt.Sprintf("long_%d_2", i))
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: continuedHead, Truncated: true})
		contID := fmt.Sprintf("long_%d_cont", i)
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: continuedRest, ToolCalls: []llm.ToolCall{{ID: contID, Name: "read_file", InputJSON: `{"path":"b.go"}`}}})
		st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: contID, Content: "result " + contID})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
		return true
	}
	s.pages = nil
	return nil
}

// noPageSplitsTheContinuedAnswer: every page that holds the first part holds the
// second right behind it, and the other way round, so a client draws the answer
// as the one text it is without another page to wait for.
func (s *messagesPagingState) noPageSplitsTheContinuedAnswer() error {
	holding := 0
	for _, page := range s.pages {
		head, rest := -1, -1
		for i, m := range page.Messages {
			switch m.Content {
			case continuedHead:
				head = i
			case continuedRest:
				rest = i
			}
		}
		switch {
		case head < 0 && rest < 0:
		case head >= 0 && rest == head+1:
			holding++
		case head >= 0 && rest < 0:
			return fmt.Errorf("the page at %d ends with the cut-off part, the part that goes on from it is on the next", page.Window.Offset)
		case head < 0:
			return fmt.Errorf("the page at %d starts with the part that goes on from a cut-off text", page.Window.Offset)
		default:
			return fmt.Errorf("the page at %d holds the two parts out of order (%d, %d)", page.Window.Offset, head, rest)
		}
	}
	if holding != 1 {
		return fmt.Errorf("%d pages hold the continued answer, want exactly one", holding)
	}
	return nil
}

func (s *messagesPagingState) read(query string) (pagedMessagesBody, error) {
	s.store()
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
	s.store()
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
	s.store()
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
			sc.Step(`^the answer to prompt (\d+) was cut off at the output limit after two tool steps and the model went on$`, s.answerCutOffAndContinued)
			sc.Step(`^no page starts or ends between the two parts of the continued answer$`, s.noPageSplitsTheContinuedAnswer)
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
