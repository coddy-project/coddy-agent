//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// postQueue queues body on the running turn of s and decodes the answer.
func postQueue(t *testing.T, s *queueHTTPState, body string) (int, map[string]interface{}) {
	t.Helper()
	res, err := http.Post(s.queueURL(""), "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out := map[string]interface{}{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func runningQueueStand(t *testing.T, textOnly bool) *queueHTTPState {
	t.Helper()
	s := &queueHTTPState{}
	s.reset()
	s.textOnlyModel = textOnly
	t.Cleanup(s.close)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.haveSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.turnIsRunning(); err != nil {
		t.Fatal(err)
	}
	return s
}

// A model that reads no images gets none, the rule POST /v1/responses applies
// to an ordinary prompt: the text is queued without the image, and an image
// alone leaves nothing to queue.
func TestQueueHTTPDropsImagesForATextOnlyModel(t *testing.T) {
	s := runningQueueStand(t, true)
	status, out := postQueue(t, s, `{"text":"inspect","inline_files":[{"name":"pixel.png","data_url":"`+queueFeatureImage+`"}]}`)
	msg, _ := out["message"].(map[string]interface{})
	if status != http.StatusCreated || msg["text"] != "inspect" || msg["imageParts"] != nil {
		t.Fatalf("text with an image for a text-only model: %d %v", status, out)
	}
	rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
	if err != nil || len(rows) != 1 || len(rows[0].ImageParts) != 0 {
		t.Fatalf("queued rows = %+v, %v; want the text without the image", rows, err)
	}
	if status, out := postQueue(t, s, `{"text":"","inline_files":[{"name":"pixel.png","data_url":"`+queueFeatureImage+`"}]}`); status != http.StatusBadRequest {
		t.Fatalf("an image alone for a text-only model answered %d %v, want 400", status, out)
	}
}

// Only an image carried in the request is taken: a URL the server or a browser
// would have to fetch, or a data URI of another type, is refused.
func TestQueueHTTPRefusesAnInlineFileThatIsNotAnImageDataURI(t *testing.T) {
	s := runningQueueStand(t, false)
	for _, dataURL := range []string{
		"https://example.com/pixel.png",
		"data:text/plain;base64,YQ==",
		"data:image/png,not-base64",
		"data:image/png;base64,",
	} {
		status, out := postQueue(t, s, `{"text":"inspect","inline_files":[{"name":"x","data_url":"`+dataURL+`"}]}`)
		errObj, _ := out["error"].(map[string]interface{})
		if status != http.StatusBadRequest || errObj["code"] != "invalid_request" {
			t.Fatalf("data_url %q answered %d %v, want 400 invalid_request", dataURL, status, out)
		}
	}
	if rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID); err != nil || len(rows) != 0 {
		t.Fatalf("refused files were queued: %+v, %v", rows, err)
	}
}

func TestQueueResponseUsesOneCurrentSnapshot(t *testing.T) {
	st := &session.State{ID: "sess_queue_snapshot"}
	st.OpenMessageQueue()
	msg, err := st.EnqueueMessage("withdraw before the response")
	if err != nil {
		t.Fatal(err)
	}
	if !st.CancelQueuedMessage(msg.ID) {
		t.Fatal("queued message was not removed")
	}

	w := httptest.NewRecorder()
	writeQueue(w, http.StatusCreated, st.GetID(), st, msg.Wire())
	var got struct {
		Messages []session.QueuedMessage `json:"messages"`
		Message  session.QueuedMessage   `json:"message"`
		Version  uint64                  `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 0 || got.Version != st.QueueVersion() {
		t.Fatalf("queue response mixed snapshots: %s; current version %d has no messages", w.Body.String(), st.QueueVersion())
	}
	if got.Message.ID != msg.ID {
		t.Fatalf("the enqueue receipt disappeared after the message was withdrawn: %s", w.Body.String())
	}
}

func TestQueueResponsesRemainConsistentDuringMutations(t *testing.T) {
	st := &session.State{ID: "sess_queue_concurrent"}
	var snapshots sync.Map
	st.SetQueueNotifier(func() {
		messages, version := st.QueueSnapshot()
		snapshots.Store(version, messages)
	})
	st.OpenMessageQueue()
	finished := make(chan struct{})
	var writerErr error
	go func() {
		defer close(finished)
		for i := 0; i < 1024; i++ {
			if _, err := st.EnqueueMessage("follow-up"); err != nil {
				writerErr = err
				return
			}
			st.ClearQueuedMessages()
		}
	}()
	t.Cleanup(func() { <-finished })

	type response struct {
		Messages []session.QueuedMessage `json:"messages"`
		Version  uint64                  `json:"version"`
	}
	responses := make([]response, 2048)
	for i := range responses {
		w := httptest.NewRecorder()
		writeQueue(w, http.StatusOK, st.GetID(), st, nil)
		if err := json.Unmarshal(w.Body.Bytes(), &responses[i]); err != nil {
			t.Fatal(err)
		}
	}
	<-finished
	if writerErr != nil {
		t.Fatal(writerErr)
	}
	for _, got := range responses {
		want, ok := snapshots.Load(got.Version)
		if !ok || !slices.EqualFunc(got.Messages, want.([]session.QueuedMessage), func(a, b session.QueuedMessage) bool { return reflect.DeepEqual(a, b) }) {
			t.Fatalf("version %d: response messages %+v, recorded snapshot %+v", got.Version, got.Messages, want)
		}
	}
}
