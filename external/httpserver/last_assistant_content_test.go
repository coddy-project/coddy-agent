//go:build http

package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// An answer the output limit cut off and the model went on from is several
// assistant messages of the transcript. The reply of a non-streaming request is
// all of it, not the part after the last cut.

func TestLastAssistantContentIsTheWholeContinuedAnswer(t *testing.T) {
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	empty := llm.Message{Role: llm.RoleAssistant, Reasoning: "Hmm."}
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	for _, tc := range []struct {
		name string
		msgs []llm.Message
		want string
	}{
		{"no assistant message", []llm.Message{user}, ""},
		{"a plain answer", []llm.Message{user, text("Done.")}, "Done."},
		{"a text and its continuation", []llm.Message{user, cut("Press the butt"), text("on labelled Save.")}, "Press the button labelled Save."},
		{"a chain of three", []llm.Message{user, cut("a "), cut("b "), text("c")}, "a b c"},
		{"an empty step between the parts", []llm.Message{user, cut("a "), empty, text("b")}, "a b"},
		{"an answer after a plain one is its own", []llm.Message{user, text("first"), text("second")}, "second"},
		// The placement markers of shared files are for the web UI: in front of
		// a truncated text, at the end of any other.
		{"markers stay out of the reply", []llm.Message{user, cut("<coddy_file id=\"a\"/>\n\nPress the butt"), text("on labelled Save.\n\n<coddy_file id=\"b\"/>")}, "Press the button labelled Save."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &session.State{ID: "sess_last_answer", Messages: tc.msgs}
			if got := lastAssistantContent(st); got != tc.want {
				t.Fatalf("lastAssistantContent = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNonStreamingReplyIsTheWholeContinuedAnswer: a client that asks for the
// answer in one JSON body gets the whole sentence, from POST /v1/chat/completions
// and from POST /v1/responses alike.
func TestNonStreamingReplyIsTheWholeContinuedAnswer(t *testing.T) {
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The notes hold the butt", Truncated: true})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "on labelled Save."})
		return string(acp.StopReasonEndTurn), nil
	}
	_, srv, _ := testHTTPServerPersistWithRunner(t, runner)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	const want = "The notes hold the button labelled Save."

	for _, tc := range []struct {
		name, path, body, field string
	}{
		{"chat completions", "/v1/chat/completions", `{"model":"agent","messages":[{"role":"user","content":"hi"}],"stream":false}`, "choices.0.message.content"},
		{"responses", "/v1/responses", `{"model":"agent","input":"hi","stream":false}`, "output.0.text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sn, err := srv.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, ts.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Coddy-Session-ID", sn.SessionID)
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", res.StatusCode, body)
			}
			if got := gjson.GetBytes(body, tc.field).String(); got != want {
				t.Fatalf("%s = %q, want the whole answer %q in %s", tc.field, got, want, body)
			}
		})
	}
}
