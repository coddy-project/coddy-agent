package remote

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type queueRecoveryTransport func(*http.Request) (*http.Response, error)

func (f queueRecoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each queue read waits for its own answer channel; synctest.Wait establishes
// that the hydration goroutine has finished even when a stale answer is dropped.
func queueRecoveryHandler(t *testing.T) (*Handler, *controlSender, chan chan string) {
	t.Helper()
	reads := make(chan chan string, 4)
	transport := queueRecoveryTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"sessionId":"sess_shared","turnActive":true}`
		if strings.HasSuffix(r.URL.Path, "/queue") {
			answer := make(chan string, 1)
			reads <- answer
			select {
			case body = <-answer:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	h, err := NewHandler(Options{BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: transport}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	s := &controlSender{ch: make(chan controlUpdate, 32)}
	h.SetServer(s)
	h.session("sess_shared")
	t.Cleanup(h.Close)
	return h, s, reads
}

func recoveryRead(t *testing.T, reads chan chan string) chan string {
	t.Helper()
	synctest.Wait()
	select {
	case answer := <-reads:
		return answer
	default:
		t.Fatal("hydration did not start a queue read")
		return nil
	}
}

func recoveryQueueUpdates(s *controlSender) []acp.MessageQueueUpdate {
	var out []acp.MessageQueueUpdate
	for {
		select {
		case update := <-s.ch:
			raw, _ := json.Marshal(update.body)
			var q acp.MessageQueueUpdate
			if json.Unmarshal(raw, &q) == nil && q.SessionUpdate == acp.UpdateTypeMessageQueue {
				out = append(out, q)
			}
		default:
			return out
		}
	}
}

func TestRemoteQueueRecoverySnapshotCrossedByLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		recoveryQueueUpdates(sender)
		h.applyEventFrame(sseFrame{event: "ready", data: `{}`})
		answer := recoveryRead(t, reads)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_new"}],"version":101}`})
		answer <- `{"messages":[],"version":0}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) != 1 || updates[0].Version != 101 {
			t.Fatalf("snapshot crossed by a newer live update was published: %+v", updates)
		}
	})
}

func TestRemoteQueueRecoveryLatestSnapshotWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		recoveryQueueUpdates(sender)
		h.applyEventFrame(sseFrame{event: "ready", data: `{}`})
		older := recoveryRead(t, reads)
		// A repeated ready on the same server supersedes the older read, while
		// a replayed turn_started must not itself reset queue ordering.
		h.applyEventFrame(sseFrame{event: "ready", data: `{}`})
		h.applyEventFrame(sseFrame{event: "turn_started", data: `{"sessionId":"sess_shared"}`})
		newer := recoveryRead(t, reads)
		newer <- `{"messages":[{"id":"q_new"}],"version":102}`
		synctest.Wait()
		older <- `{"messages":[{"id":"q_stale"}],"version":101}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) != 1 || updates[0].Version != 102 {
			t.Fatalf("older ready snapshot was published after the newer read: %+v", updates)
		}
	})
}

func TestRemoteQueueRecoveryKeepsACPBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, reads := queueRecoveryHandler(t)
		sender := &collectSender{}
		h.SetServer(sender)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		h.applyEventFrame(sseFrame{event: "ready", data: `{}`})
		recoveryRead(t, reads) <- `{"messages":[],"version":0}`
		synctest.Wait()
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_live"}],"version":1}`})
		sender.mu.Lock()
		defer sender.mu.Unlock()
		if len(sender.updates) != 3 {
			t.Fatalf("ACP updates = %+v, want three ordinary queue updates", sender.updates)
		}
		for i, version := range []uint64{100, 0, 1} {
			q, ok := sender.updates[i].(acp.MessageQueueUpdate)
			if !ok || q.Version != version {
				t.Fatalf("ACP received %T %+v, want public queue version %d", sender.updates[i], sender.updates[i], version)
			}
		}
	})
}
