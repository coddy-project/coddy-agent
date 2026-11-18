package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type questionErrorSender struct {
	collectSender
	result *acp.QuestionResult
	err    error
}

func (s *questionErrorSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return s.result, s.err
}

func TestQuestionErrorsAreNotEmptyAnswers(t *testing.T) {
	for _, tc := range []struct {
		name             string
		supportsError    bool
		result           *acp.QuestionResult
		err              error
		status           int
		cancelStatus     int
		contextCancelled bool
		wantPost         bool
		wantCancel       bool
		wantErr          bool
	}{
		{name: "long refusal", supportsError: true, err: errors.New(strings.Repeat("界", 2000)), wantPost: true},
		{name: "long successful answer", result: &acp.QuestionResult{Answers: [][]string{{strings.Repeat("界", 2000)}}}, wantPost: true},
		{name: "refusal", supportsError: true, err: errors.New("question refused by client"), wantPost: true},
		{name: "error overrides partial answer", supportsError: true, result: &acp.QuestionResult{Answers: [][]string{{"A"}}}, err: errors.New("question refused by client"), wantPost: true},
		{name: "nil result", supportsError: true, wantPost: true},
		{name: "cancelled context", supportsError: true, contextCancelled: true, err: errors.New("question refused by client"), wantCancel: true, wantErr: true},
		{name: "legacy nil result", wantCancel: true, wantErr: true},
		{name: "legacy server", err: errors.New("question refused by client"), wantCancel: true, wantErr: true},
		{name: "legacy cancel failure", err: errors.New("question refused by client"), cancelStatus: 500, wantCancel: true, wantErr: true},
		{name: "rejected error delivery", supportsError: true, err: errors.New("question refused by client"), status: 400, wantPost: true, wantCancel: true, wantErr: true},
		{name: "delivery unavailable", supportsError: true, err: errors.New("question refused by client"), status: 500, wantPost: true, wantCancel: true, wantErr: true},
		{name: "conflict is delivery failure", supportsError: true, err: errors.New("question refused by client"), status: 409, wantPost: true, wantCancel: true, wantErr: true},
		{name: "successful answer conflict", result: &acp.QuestionResult{Answers: [][]string{{"A"}}}, status: 409, wantPost: true, wantCancel: true, wantErr: true},
		{name: "stale refusal", supportsError: true, err: errors.New("question refused by client"), status: 404, wantPost: true},
		{name: "empty successful answer", result: &acp.QuestionResult{}, wantPost: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posted map[string]json.RawMessage
			cancelled := false
			mux := http.NewServeMux()
			mux.HandleFunc("POST /coddy/sessions/{id}/question", func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					t.Error(err)
				}
				status := tc.status
				if status == 0 {
					status = 204
				}
				w.WriteHeader(status)
			})
			mux.HandleFunc("POST /coddy/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
				cancelled = true
				status := tc.cancelStatus
				if status == 0 {
					status = 204
				}
				w.WriteHeader(status)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			h, err := NewHandler(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.contextCancelled {
				cancel()
			}
			turn := &turnStream{h: h, ctx: ctx, sessionID: "sess_test", sender: &questionErrorSender{result: tc.result, err: tc.err}}
			data := `{"sessionId":"sess_test","requestId":"q_1","questions":[],"supportsError":false}`
			if tc.supportsError {
				data = strings.Replace(data, "false", "true", 1)
			}
			err = turn.onQuestion(data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && tc.err != nil && !errors.Is(err, tc.err) {
				t.Errorf("lost original error: %v", err)
			}
			if tc.cancelStatus != 0 && (err == nil || !strings.Contains(err.Error(), "cancel remote turn after question failure")) {
				t.Errorf("lost cancellation failure: %v", err)
			}
			if cancelled != tc.wantCancel {
				t.Errorf("cancelled = %v", cancelled)
			}
			if (posted != nil) != tc.wantPost {
				t.Fatalf("posted = %s", posted)
			}
			if !tc.wantPost {
				return
			}
			if string(posted["requestId"]) != `"q_1"` {
				t.Errorf("requestId = %s", posted["requestId"])
			}
			if tc.err != nil || tc.result == nil {
				if _, ok := posted["answers"]; ok {
					t.Error("refusal must not carry answers")
				}
				if len(posted["error"]) == 0 || string(posted["error"]) == `""` {
					t.Error("refusal lost error")
				}
				var message string
				if err := json.Unmarshal(posted["error"], &message); err != nil {
					t.Fatal(err)
				}
				if len(message) > acp.MaxQuestionErrorBytes || !utf8.ValidString(message) {
					t.Fatalf("invalid diagnostic: %d bytes", len(message))
				}
				if tc.err != nil && len(tc.err.Error()) <= acp.MaxQuestionErrorBytes && message != tc.err.Error() {
					t.Fatal("short diagnostic changed")
				}
				if tc.err != nil && len(tc.err.Error()) > acp.MaxQuestionErrorBytes && !strings.HasSuffix(message, "[truncated]") {
					t.Fatal("missing truncation marker")
				}
			} else if tc.result != nil && len(tc.result.Answers) != 0 {
				want, _ := json.Marshal(tc.result.Answers)
				if string(posted["answers"]) != string(want) {
					t.Errorf("answers = %s, want %s", posted["answers"], want)
				}
			} else if string(posted["answers"]) != "[]" {
				t.Errorf("empty answer = %s", posted["answers"])
			}
		})
	}
}
