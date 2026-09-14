//go:build cli

package cli

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Decode the shared queue payload without requiring a private notification type.
// Both the original ACP delivery and the console-only envelope carry these fields.
func recoveryQueuePayload(update any) (acp.MessageQueueUpdate, bool) {
	raw, _ := json.Marshal(update)
	var q acp.MessageQueueUpdate
	err := json.Unmarshal(raw, &q)
	return q, err == nil && q.SessionUpdate == acp.UpdateTypeMessageQueue
}

func restartControlStand(t *testing.T) *remoteControlStand {
	t.Helper()
	f := newRemoteControlStand(t)
	// Finish the initial hydration before establishing the pre-restart state.
	f.h.HandleSessionReady(sharedControlSession)
	pumpControls(t, f.app, func(updateMsg) bool { return f.app.queue.version == 1 })
	f.mu.Lock()
	f.active = true
	f.rows = []session.QueuedMessage{{ID: "q_old", Text: "before restart"}}
	f.version = 100
	f.mu.Unlock()
	f.turnEvent(t, sharedControlSession, true)
	f.syncEvents(t, controlQueueFrame(sharedControlSession, f.rows, 100))
	return f
}

func awaitRecoveryQueue(t *testing.T, f *remoteControlStand, version uint64) {
	t.Helper()
	pumpControls(t, f.app, func(msg updateMsg) bool {
		q, ok := recoveryQueuePayload(msg.update)
		return ok && msg.sessionID == sharedControlSession && q.Version == version
	})
}

func assertRecoveryQueue(t *testing.T, f *remoteControlStand, version uint64, id string) {
	t.Helper()
	rows := f.app.queue.Rows()
	if f.app.queue.version != version || (id == "" && len(rows) != 0) || (id != "" && (len(rows) != 1 || rows[0].ID != id)) {
		t.Fatalf("queue version=%d rows=%+v; want version=%d id=%q", f.app.queue.version, rows, version, id)
	}
}

func TestRemoteControlsQueueRestartRecovery(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "idle"
		if active {
			name = "already_active"
		}
		t.Run(name, func(t *testing.T) {
			f := restartControlStand(t)
			version, id := uint64(0), ""
			var rows []session.QueuedMessage
			if active {
				version, id = 1, "q_after_restart"
				rows = []session.QueuedMessage{{ID: id, Text: "new server turn"}}
			}
			f.mu.Lock()
			f.active, f.rows, f.version = active, rows, version
			f.mu.Unlock()
			f.events <- controlFrame("ready", `{}`)
			awaitRecoveryQueue(t, f, version)
			assertRecoveryQueue(t, f, version, id)
			if f.app.remoteTurnActive != active || f.app.turnActive {
				t.Fatal("queue recovery changed server activity or claimed an owned prompt")
			}
			f.syncEvents(t, controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_live", Text: "live after restart"}}, version+1))
			assertRecoveryQueue(t, f, version+1, "q_live")
		})
	}
}

func TestRemoteControlsQueueRestartRejectsOldMutation(t *testing.T) {
	f := restartControlStand(t)
	gate, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	f.mu.Lock()
	f.postGate, f.postEntered = gate, entered
	f.mu.Unlock()
	f.app.enqueuePrompt("old server mutation")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("queue mutation did not reach the server")
	}
	f.mu.Lock()
	f.active, f.rows, f.version = false, nil, 0
	f.mu.Unlock()
	f.events <- controlFrame("ready", `{}`)
	awaitRecoveryQueue(t, f, 0)
	f.syncEvents(t, controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_live"}}, 1))
	release()
	f.app.workers.Wait()
	drainControls(f.app)
	assertRecoveryQueue(t, f, 1, "q_live")
}

// Delay an actual notification after the handler accepted it, allowing a second
// producer to deliver first. The UI must see one coherent version/epoch/row set.
type heldRecoverySender struct {
	acp.UpdateSender
	version uint64
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (s *heldRecoverySender) deliver(sid string, update any) error {
	if q, ok := recoveryQueuePayload(update); ok && sid == sharedControlSession && q.Version == s.version {
		close(s.entered)
		<-s.release
		defer close(s.done)
	}
	return s.UpdateSender.SendSessionUpdate(sid, update)
}

func (s *heldRecoverySender) SendSessionUpdate(sid string, update any) error {
	return s.deliver(sid, update)
}

func (s *heldRecoverySender) SendControlUpdate(sid string, update any) error {
	return s.deliver(sid, update)
}

func TestRemoteControlsQueueRecoveryNotificationOrder(t *testing.T) {
	for _, heldVersion := range []uint64{0, 101} {
		name := "snapshot_after_live"
		if heldVersion == 101 {
			name = "old_delivery_after_recovery"
		}
		t.Run(name, func(t *testing.T) {
			f := restartControlStand(t)
			s := &heldRecoverySender{UpdateSender: f.app.Sender(), version: heldVersion,
				entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
			var once sync.Once
			release := func() { once.Do(func() { close(s.release) }) }
			t.Cleanup(release)
			f.h.SetServer(s)
			f.mu.Lock()
			f.active, f.rows, f.version = false, nil, 0
			f.mu.Unlock()
			if heldVersion == 101 {
				f.events <- controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_delayed"}}, 101)
			} else {
				f.events <- controlFrame("ready", `{}`)
			}
			select {
			case <-s.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("notification did not reach the delivery gate")
			}
			if heldVersion == 101 {
				// The event reader is held at the sender; hydration is independent.
				f.h.RefreshSessionState(sharedControlSession)
				awaitRecoveryQueue(t, f, 0)
			} else {
				f.syncEvents(t, controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_live"}}, 1))
			}
			release()
			select {
			case <-s.done:
			case <-time.After(3 * time.Second):
				t.Fatal("held notification did not finish")
			}
			drainControls(f.app)
			if heldVersion == 101 {
				assertRecoveryQueue(t, f, 0, "")
			} else {
				assertRecoveryQueue(t, f, 1, "q_live")
			}
		})
	}
}

func TestRemoteControlsQueueRepeatedReadyKeepsHighWater(t *testing.T) {
	f := restartControlStand(t)
	for range 2 {
		f.turnEvent(t, sharedControlSession, true)
		f.events <- controlFrame("ready", `{}`)
		awaitRecoveryQueue(t, f, 100)
	}
	f.syncEvents(t, controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_stale"}}, 99))
	assertRecoveryQueue(t, f, 100, "q_old")
}

func TestRemoteControlsQueueRecoveryCrossedByLowFrame(t *testing.T) {
	f := restartControlStand(t)
	f.mu.Lock()
	locked := true
	defer func() {
		if locked {
			f.mu.Unlock()
		}
	}()
	f.rows = []session.QueuedMessage{{ID: "q_after_restart", Text: "new server"}}
	f.version = 1
	previous := f.app.remoteActivityRevision
	f.h.RefreshSessionState(sharedControlSession)
	// REST is held by the stand mutex; the event reader is independent.
	f.events <- controlQueueFrame(sharedControlSession, f.rows, 1)
	f.events <- controlFrame("turn_started", fmt.Sprintf(`{"sessionId":%q}`, sharedControlSession))
	pumpControls(t, f.app, func(msg updateMsg) bool {
		update, ok := msg.update.(remote.ActivityUpdate)
		return ok && update.Revision > previous
	})
	f.mu.Unlock()
	locked = false
	awaitRecoveryQueue(t, f, 1)
	assertRecoveryQueue(t, f, 1, "q_after_restart")
}
