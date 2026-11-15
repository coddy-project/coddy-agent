package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// askOverWire asks one question of a client that advertised no elicitation,
// so it goes out as session/request_question, and answers it with reply (the
// JSON-RPC member after the id: a result or an error).
func askOverWire(t *testing.T, reply string) (*acp.QuestionResult, error) {
	t.Helper()
	w := startWireServer(t, &mockHandler{})
	w.initialize(t, `{}`)
	type outcome struct {
		res *acp.QuestionResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := w.srv.RequestQuestion(context.Background(), acp.QuestionRequestParams{
			SessionID: "s1",
			Questions: []acp.QuestionPrompt{{Question: "Which format?", Options: []acp.QuestionOption{{Label: "JSON"}}}},
		})
		done <- outcome{res, err}
	}()
	w.awaitWritten(t, `"session/request_question"`)
	var id json.RawMessage
	scanner := bufio.NewScanner(strings.NewReader(w.out.String()))
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.Method == "session/request_question" {
			id = msg.ID
		}
	}
	if id == nil {
		t.Fatalf("no request id on the wire:\n%s", w.out.String())
	}
	if err := w.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, id, reply)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		return got.res, got.err
	case <-time.After(2 * time.Second):
		t.Fatal("the question was never answered")
		return nil, nil
	}
}

// A client that does not implement session/request_question answers it with
// an error. That is no answer from the user: the question tool reports it to
// the model instead of handing it an empty set of answers to read as one.
func TestQuestionRefusedByTheClientIsAnError(t *testing.T) {
	res, err := askOverWire(t, `"error":{"code":-32601,"message":"Method not found"}`)
	if err == nil {
		t.Fatalf("a refused question read as answered: %+v", res)
	}
	if !strings.Contains(err.Error(), "-32601 Method not found") {
		t.Fatalf("error = %q, want the client's refusal named", err)
	}
}

func TestQuestionAnswerThatDoesNotReadIsAnError(t *testing.T) {
	if res, err := askOverWire(t, `"result":"not an answer"`); err == nil {
		t.Fatalf("an unreadable answer read as answered: %+v", res)
	}
}

func TestQuestionAnswerRequiresAnAnswersField(t *testing.T) {
	for _, reply := range []string{`"result":null`, `"result":{}`} {
		t.Run(reply, func(t *testing.T) {
			if res, err := askOverWire(t, reply); err == nil {
				t.Fatalf("missing answers read as answered: %+v", res)
			}
		})
	}
}

func TestQuestionEmptyAnswersRemainValid(t *testing.T) {
	for _, reply := range []string{`"result":{"answers":[]}`, `"result":{"answers":null}`} {
		t.Run(reply, func(t *testing.T) {
			if _, err := askOverWire(t, reply); err != nil {
				t.Fatalf("empty answers rejected: %v", err)
			}
		})
	}
}

func TestQuestionAnsweredByTheClientIsReturned(t *testing.T) {
	res, err := askOverWire(t, `"result":{"answers":[["JSON"]]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(res)
	if !strings.Contains(string(got), "JSON") {
		t.Fatalf("answers = %s, want the client's pick", got)
	}
}

func TestQuestionClientErrorMessageIsBounded(t *testing.T) {
	for _, message := range []string{"refused", strings.Repeat("x", 4096), strings.Repeat("x", 4097), strings.Repeat("界", 2000)} {
		raw, _ := json.Marshal(message)
		_, err := askOverWire(t, `"error":{"code":-32601,"message":`+string(raw)+`}`)
		if err == nil {
			t.Fatal("missing refusal")
		}
		got := err.Error()
		prefix, suffix := "the client could not ask the user (-32601 ", "); continue without an answer"
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
			t.Fatalf("lost code or guidance: %q", got)
		}
		detail := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix)
		if len(detail) > 4096 || !utf8.ValidString(detail) {
			t.Fatalf("invalid diagnostic: %d bytes, valid UTF-8 %v", len(detail), utf8.ValidString(detail))
		}
		if len(message) <= 4096 && detail != message {
			t.Fatal("short diagnostic changed")
		}
		if len(message) > 4096 && !strings.HasSuffix(detail, "[truncated]") {
			t.Fatal("missing truncation marker")
		}
	}
}

func TestBoundQuestionError(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"short", "request refused", "request refused"},
		{"boundary", strings.Repeat("x", 4096), strings.Repeat("x", 4096)},
		{"over boundary", strings.Repeat("x", 4097), strings.Repeat("x", 4084) + " [truncated]"},
		{"UTF-8 boundary", strings.Repeat("界", 2000), strings.Repeat("界", 1361) + " [truncated]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := acp.BoundQuestionError(tc.input)
			if got != tc.want {
				t.Fatalf("bounded error differs: got %d bytes, want %d", len(got), len(tc.want))
			}
			if acp.BoundQuestionError(got) != got {
				t.Fatal("bounding twice changed the diagnostic")
			}
		})
	}
}
