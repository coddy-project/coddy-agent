package acp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
