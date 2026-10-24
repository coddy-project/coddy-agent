package acp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func strptr(s string) *string { return &s }

// syncBuffer collects what a server wrote to its client while the test reads
// it from another goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lastLine is the JSON object the server wrote last.
func (b *syncBuffer) lastLine(t *testing.T) map[string]interface{} {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("the server wrote nothing")
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &out); err != nil {
		t.Fatalf("last wire line is not JSON: %v\n%s", err, lines[len(lines)-1])
	}
	return out
}

// wireServer runs a server over a pipe: the test writes requests into it and
// reads what the server sends back.
type wireServer struct {
	srv *acp.Server
	out *syncBuffer
	pw  *io.PipeWriter
}

func startWireServer(t *testing.T, handler acp.Handler) *wireServer {
	t.Helper()
	out := &syncBuffer{}
	srv := acp.NewServerWithWriter(handler, slog.New(slog.DiscardHandler), out)
	pr, pw := io.Pipe()
	go func() { _ = srv.Run(context.Background(), pr) }()
	t.Cleanup(func() { _ = pw.Close() })
	return &wireServer{srv: srv, out: out, pw: pw}
}

// send writes one request line into the server.
func (w *wireServer) send(line string) error {
	_, err := io.WriteString(w.pw, strings.TrimSpace(line)+"\n")
	return err
}

// awaitWritten waits until the server has written something containing want.
func (w *wireServer) awaitWritten(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.out.String(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the server never wrote %q; wire so far:\n%s", want, w.out.String())
}

// answer replies to the request with the given id.
func (w *wireServer) answer(t *testing.T, id interface{}, result string) {
	t.Helper()
	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, raw, result)); err != nil {
		t.Fatal(err)
	}
}

func (w *wireServer) initialize(t *testing.T, clientCapabilities string) {
	t.Helper()
	if err := w.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":` +
		clientCapabilities + `}}`); err != nil {
		t.Fatal(err)
	}
	w.awaitWritten(t, `"protocolVersion"`)
}

// Every session capability Coddy advertises has to reach the client as the
// empty marker object the schema reserves for "this agent supports it", and a
// capability left out has to stay out: a client that sees `delete` offers a
// delete it would then be refused.
func TestFullSessionCapsMarshalsEverySupportedMethod(t *testing.T) {
	got, err := json.Marshal(acp.FullSessionCaps())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"list":{},"delete":{},"additionalDirectories":{},"resume":{}}`
	if string(got) != want {
		t.Fatalf("sessionCapabilities = %s, want %s", got, want)
	}
}

func TestSessionCapsOmitsWhatIsNotAdvertised(t *testing.T) {
	got, err := json.Marshal(&acp.SessionCaps{List: &acp.SessionListCapabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"list":{}}` {
		t.Fatalf("sessionCapabilities = %s, want the list alone", got)
	}
}

// The title is always on the wire: a rename carries the new name, and a
// session nobody names any more carries null, which is what tells a client to
// clear the name it was showing rather than leave it standing.
func TestSessionInfoUpdateAlwaysWritesATitle(t *testing.T) {
	rename := acp.SessionInfoUpdate{
		SessionUpdate: acp.UpdateTypeSessionInfoUpdate,
		Title:         strptr("Ship the release"),
	}
	got, err := json.Marshal(rename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"title":"Ship the release"`) {
		t.Fatalf("rename = %s, want the new title", got)
	}

	cleared := acp.SessionInfoUpdate{SessionUpdate: acp.UpdateTypeSessionInfoUpdate}
	got, err = json.Marshal(cleared)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"title":null`) {
		t.Fatalf("clear = %s, want a null title, which is what clears a name", got)
	}
}

// A client that said it renders form elicitation is asked through the standard
// elicitation/create; one that never said so keeps getting the Coddy-only
// session/request_question, so nothing that cannot render a form is sent one.
func TestRequestQuestionUsesElicitationOnlyWhenTheClientAdvertisedIt(t *testing.T) {
	t.Run("client renders forms", func(t *testing.T) {
		w := startWireServer(t, &mockHandler{})
		w.initialize(t, `{"elicitation":{"form":{}}}`)

		asked := make(chan *acp.QuestionResult, 1)
		go func() {
			res, _ := w.srv.RequestQuestion(context.Background(), acp.QuestionRequestParams{
				SessionID: "sess_1",
				Questions: []acp.QuestionPrompt{{
					Question: "Pick a stack",
					Options:  []acp.QuestionOption{{Label: "Go"}, {Label: "Rust"}},
					Custom:   false,
				}},
			})
			asked <- res
		}()

		w.awaitWritten(t, `"elicitation/create"`)
		id := w.out.lastLine(t)["id"]
		w.answer(t, id, `{"action":"accept","content":{"q1":"Rust"}}`)

		res := <-asked
		if res == nil || len(res.Answers) != 1 || res.Answers[0][0] != "Rust" {
			t.Fatalf("answers = %#v, want the picked label", res)
		}
	})

	t.Run("client never advertised elicitation", func(t *testing.T) {
		w := startWireServer(t, &mockHandler{})
		w.initialize(t, `{}`)

		asked := make(chan *acp.QuestionResult, 1)
		go func() {
			res, _ := w.srv.RequestQuestion(context.Background(), acp.QuestionRequestParams{
				SessionID: "sess_1",
				Questions: []acp.QuestionPrompt{{
					Question: "Pick a stack",
					Options:  []acp.QuestionOption{{Label: "Go"}},
				}},
			})
			asked <- res
		}()

		w.awaitWritten(t, `"session/request_question"`)
		if strings.Contains(w.out.String(), `"elicitation/create"`) {
			t.Fatalf("an elicitation was sent to a client that never advertised it:\n%s", w.out.String())
		}
		id := w.out.lastLine(t)["id"]
		w.answer(t, id, `{"answers":[["Go"]]}`)

		res := <-asked
		if res == nil || len(res.Answers) != 1 || res.Answers[0][0] != "Go" {
			t.Fatalf("answers = %#v, want the picked label", res)
		}
	})
}

// A question the client answers with a form value that is not one of the
// offered labels is what the user typed, which is the contract the question
// tool gives the model.
func TestElicitationAnswerThatMatchesNoLabelIsTheTypedText(t *testing.T) {
	w := startWireServer(t, &mockHandler{})
	w.initialize(t, `{"elicitation":{"form":{}}}`)

	asked := make(chan *acp.QuestionResult, 1)
	go func() {
		res, _ := w.srv.RequestQuestion(context.Background(), acp.QuestionRequestParams{
			SessionID: "sess_1",
			Questions: []acp.QuestionPrompt{{
				Question: "Pick a stack",
				Options:  []acp.QuestionOption{{Label: "Go"}, {Label: "Rust"}},
				Custom:   true,
			}},
		})
		asked <- res
	}()

	w.awaitWritten(t, `"elicitation/create"`)
	id := w.out.lastLine(t)["id"]
	w.answer(t, id, `{"action":"accept","content":{"q1":"something of my own"}}`)

	res := <-asked
	if res == nil || len(res.Answers) != 1 || res.Answers[0][0] != "something of my own" {
		t.Fatalf("answers = %#v, want the text the user typed", res)
	}
}

// A form the user dismissed answers with no picks at all rather than failing
// the tool call: nobody refused the question, there was simply no answer.
func TestDeclinedElicitationAnswersWithNothing(t *testing.T) {
	w := startWireServer(t, &mockHandler{})
	w.initialize(t, `{"elicitation":{"form":{}}}`)

	asked := make(chan *acp.QuestionResult, 1)
	go func() {
		res, _ := w.srv.RequestQuestion(context.Background(), acp.QuestionRequestParams{
			SessionID: "sess_1",
			Questions: []acp.QuestionPrompt{{Question: "Pick a stack", Options: []acp.QuestionOption{{Label: "Go"}}}},
		})
		asked <- res
	}()

	w.awaitWritten(t, `"elicitation/create"`)
	id := w.out.lastLine(t)["id"]
	w.answer(t, id, `{"action":"decline"}`)

	res := <-asked
	if res == nil || len(res.Answers) != 0 {
		t.Fatalf("answers = %#v, want none", res)
	}
}
