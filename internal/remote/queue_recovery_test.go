package remote

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type queueRecoveryTransport func(*http.Request) (*http.Response, error)

func (f queueRecoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each queue read waits for its own answer channel; synctest.Wait establishes
// that the hydration goroutine has finished even when a stale answer is dropped.
func queueRecoveryHandler(t *testing.T, expireActivity ...bool) (*Handler, *controlSender, chan chan string) {
	t.Helper()
	reads := make(chan chan string, 4)
	transport := queueRecoveryTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"sessionId":"sess_shared","turnActive":true}`
		if len(expireActivity) > 0 && expireActivity[0] && strings.HasSuffix(r.URL.Path, "/activity") {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if strings.HasSuffix(r.URL.Path, "/queue") {
			answer := make(chan string, 1)
			reads <- answer
			select {
			case body = <-answer:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		status := http.StatusOK
		if body == "unavailable" {
			status, body = http.StatusServiceUnavailable, `{"message":"loading"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
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
		// The old snapshot stays rejected; the new read returns current state.
		retryRecoveryRead(t, reads) <- `{"messages":[{"id":"q_new"}],"version":101}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) != 2 || updates[0].Version != 101 || updates[1].Version != 101 {
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

func retryRecoveryRead(t *testing.T, reads chan chan string) chan string {
	t.Helper()
	select {
	case answer := <-reads:
		return answer
	case <-time.After(time.Second):
		t.Fatal("queue recovery was not retried")
		return nil
	}
}

func TestRemoteQueueRecoveryRetriesCrossedLowFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		recoveryQueueUpdates(sender)
		h.RefreshSessionState("sess_shared")
		first := recoveryRead(t, reads)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_new"}],"version":1}`})
		first <- `{"messages":[{"id":"q_new"}],"version":1}`
		retryRecoveryRead(t, reads) <- `{"messages":[{"id":"q_new"}],"version":1}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) != 1 || updates[0].Version != 1 || updates[0].Messages[0].ID != "q_new" {
			t.Fatalf("crossed low frame left old queue state: %+v", updates)
		}
	})
}

func TestRemoteQueueRecoveryRetriesFailedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		recoveryQueueUpdates(sender)
		h.RefreshSessionState("sess_shared")
		recoveryRead(t, reads) <- "unavailable"
		retryRecoveryRead(t, reads) <- `{"messages":[],"version":0}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) != 1 || updates[0].Version != 0 || len(updates[0].Messages) != 0 {
			t.Fatalf("failed read left old queue state: %+v", updates)
		}
	})
}

func TestRemoteQueueRecoveryRetryStopsOnCloseOrNewRead(t *testing.T) {
	for _, closeHandler := range []bool{false, true} {
		t.Run(map[bool]string{false: "superseded", true: "closed"}[closeHandler], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, _, reads := queueRecoveryHandler(t)
				h.RefreshSessionState("sess_shared")
				recoveryRead(t, reads) <- "unavailable"
				synctest.Wait()
				if closeHandler {
					h.Close()
				} else {
					h.RefreshSessionState("sess_shared")
					recoveryRead(t, reads) <- `{"messages":[],"version":0}`
				}
				// Advance the virtual clock past the retry delay.
				time.Sleep(time.Second)
				synctest.Wait()
				if len(reads) != 0 {
					t.Fatal("superseded or closed recovery issued another request")
				}
			})
		})
	}
}

func TestRemoteQueueRecoveryFailedReadsAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, reads := queueRecoveryHandler(t)
		h.RefreshSessionState("sess_shared")
		recoveryRead(t, reads) <- "unavailable"
		for range 2 {
			retryRecoveryRead(t, reads) <- "unavailable"
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reads) != 0 {
			t.Fatal("failed queue reads exceeded the retry budget")
		}
	})
}

func TestRemoteQueueRecoveryLowFrameAndOldMutationStillRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_old"}],"version":100}`})
		old := h.queueRequestFence("sess_shared")
		recoveryQueueUpdates(sender)
		h.RefreshSessionState("sess_shared")
		answer := recoveryRead(t, reads)
		h.applyEventFrame(sseFrame{event: "message_queue", data: `{"sessionId":"sess_shared","messages":[{"id":"q_new"}],"version":1}`})
		h.publishQueue("sess_shared", queueResponse{Version: 101}, old)
		answer <- `{"messages":[{"id":"q_new"}],"version":1}`
		retryRecoveryRead(t, reads) <- `{"messages":[{"id":"q_new"}],"version":1}`
		synctest.Wait()
		updates := recoveryQueueUpdates(sender)
		if len(updates) == 0 || updates[len(updates)-1].Version != 1 {
			t.Fatalf("old mutation suppressed restart recovery: %+v", updates)
		}
	})
}

func TestRemoteQueueRecoveryHasItsOwnTimeoutBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, sender, reads := queueRecoveryHandler(t, true)
		h.RefreshSessionState("sess_shared")
		select {
		case answer := <-reads:
			answer <- `{"messages":[],"version":0}`
		case <-time.After(restTimeout + time.Second):
			t.Fatal("activity timeout prevented queue hydration")
		}
		synctest.Wait()
		if updates := recoveryQueueUpdates(sender); len(updates) != 1 {
			t.Fatalf("queue inherited the expired activity context: %+v", updates)
		}
	})
}
