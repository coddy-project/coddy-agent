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

func TestQueueHTTPAcceptsModeAndImageAndAllowsSwitch(t *testing.T) {
	s := &queueHTTPState{}
	s.reset()
	defer s.close()
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	if err := s.haveSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.turnIsRunning(); err != nil {
		t.Fatal(err)
	}
	post, err := http.Post(s.queueURL(""), "application/json", bytes.NewBufferString(`{"text":"inspect","mode":"after_turn","inline_files":[{"name":"pixel.png","data_url":"data:image/png;base64,YQ=="}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var added struct {
		Message session.QueuedMessage `json:"message"`
	}
	if err := json.NewDecoder(post.Body).Decode(&added); err != nil {
		t.Fatal(err)
	}
	_ = post.Body.Close()
	if post.StatusCode != http.StatusCreated || added.Message.Mode != session.QueueModeAfterTurn || len(added.Message.ImageParts) != 1 {
		t.Fatalf("queued image = %+v, status %d", added.Message, post.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPatch, s.queueURL("/"+added.Message.ID), bytes.NewBufferString(`{"mode":"steer"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d", res.StatusCode)
	}
	rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
	if err != nil || len(rows) != 1 || rows[0].Mode != session.QueueModeSteer {
		t.Fatalf("switched rows = %+v, %v", rows, err)
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
	writeQueue(w, http.StatusCreated, st.GetID(), st, &msg)
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
