//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

func TestQuestionAnswerRejectsInvalidOutcomeWithoutSettling(t *testing.T) {
	for _, body := range []string{
		`{"requestId":"q"}`,
		`{"requestId":"q","error":""}`,
		`{"requestId":"q","error":"  "}`,
		`{"requestId":"q","answers":[],"error":"refused"}`,
		`{"requestId":"q","answers":null}`,
		`{"requestId":"q","answers":null,"error":"refused"}`,
		`{"requestId":"q","answers":[],"error":null}`,
		`{"requestId":"q","error":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			registerQuestionWait("sess_test", "q")
			defer unregisterQuestionWait("sess_test", "q")
			req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/sess_test/question", strings.NewReader(body))
			req.SetPathValue("id", "sess_test")
			rec := httptest.NewRecorder()
			(&Server{}).coddySessionQuestionPost(rec, req)
			if rec.Code != 400 {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if !QuestionPending("sess_test") {
				t.Fatal("invalid body settled the question")
			}
		})
	}
}

func TestQuestionErrorPostReachesWaitingSender(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"refused", `{"requestId":"q","error":"question refused by client"}`, true},
		{"empty answer", `{"requestId":"q","answers":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			sender := NewSender(&config.Config{}, &syncBuffer{}, true, "")
			pending := make(chan struct{}, 1)
			sender.SetQuestionPendingCallback(func(_ string, p bool) {
				if p {
					pending <- struct{}{}
				}
			})
			done := make(chan error, 1)
			go func() {
				_, err := sender.RequestQuestion(ctx, acp.QuestionRequestParams{SessionID: "sess_test", RequestID: "q"})
				done <- err
			}()
			select {
			case <-pending:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/sess_test/question", strings.NewReader(tc.body))
			req.SetPathValue("id", "sess_test")
			rec := httptest.NewRecorder()
			(&Server{}).coddySessionQuestionPost(rec, req)
			if rec.Code != 204 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			select {
			case err := <-done:
				if (err != nil) != tc.wantErr {
					t.Fatalf("err = %v", err)
				}
				if tc.wantErr && !strings.Contains(err.Error(), "question refused by client") {
					t.Fatalf("lost refusal: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("question did not settle")
			}
			if QuestionPending("sess_test") {
				t.Fatal("settled question remains pending")
			}
			stale := httptest.NewRequest(http.MethodPost, "/coddy/sessions/sess_test/question", strings.NewReader(tc.body))
			stale.SetPathValue("id", "sess_test")
			again := httptest.NewRecorder()
			(&Server{}).coddySessionQuestionPost(again, stale)
			if again.Code != 404 {
				t.Fatalf("stale status %d", again.Code)
			}
		})
	}
}

type refusingRemoteQuestionSender struct{ noopSender }

func (refusingRemoteQuestionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, errors.New("question refused by client")
}

// Exercise both sides over real HTTP, including capability advertisement.
func TestRemoteQuestionRefusalRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sender := NewSender(&config.Config{}, w, true, "")
		_, err := sender.RequestQuestion(ctx, acp.QuestionRequestParams{SessionID: "sess_test", RequestID: "q"})
		observed <- err
		_ = sender.FinishStream()
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/question", (&Server{}).coddySessionQuestionPost)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	h, err := remote.NewHandler(remote.Options{BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := refusingRemoteQuestionSender{}
	_, err = h.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{SessionID: "sess_test", Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}}}, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-observed:
		if err == nil || !strings.Contains(err.Error(), "question refused by client") {
			t.Fatalf("server got %v, want refusal", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestQuestionOutcomeBoundsOnlyErrorText(t *testing.T) {
	for _, message := range []string{"refused", strings.Repeat("x", 4096), strings.Repeat("x", 4097), strings.Repeat("界", 2000)} {
		for _, failure := range []bool{true, false} {
			ch := registerQuestionWait("sess_test", "q")
			body := map[string]interface{}{"requestId": "q"}
			if failure {
				body["error"] = message
			} else {
				body["answers"] = [][]string{{message}}
			}
			raw, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/coddy/sessions/sess_test/question", strings.NewReader(string(raw)))
			req.SetPathValue("id", "sess_test")
			rec := httptest.NewRecorder()
			(&Server{}).coddySessionQuestionPost(rec, req)
			if rec.Code != 204 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			reply := <-ch
			if !failure {
				if reply.err != nil || reply.result.Answers[0][0] != message {
					t.Fatal("successful answer changed")
				}
				continue
			}
			if reply.err == nil {
				t.Fatal("missing error")
			}
			detail := reply.err.Error()
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
}
