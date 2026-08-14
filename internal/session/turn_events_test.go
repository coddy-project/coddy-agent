package session

import (
	"sync"
	"testing"
)

func collectTurnEvents(m *Manager) (*[]TurnEvent, *sync.Mutex, func()) {
	var (
		mu   sync.Mutex
		seen []TurnEvent
	)
	remove := m.AddTurnObserver(func(ev TurnEvent) {
		mu.Lock()
		seen = append(seen, ev)
		mu.Unlock()
	})
	return &seen, &mu, remove
}

func TestTurnObserverSeesOneEventPairPerTurn(t *testing.T) {
	m := &Manager{}
	seen, mu, remove := collectTurnEvents(m)
	defer remove()

	release := m.markTurnActive("sess_obs")
	release()

	mu.Lock()
	defer mu.Unlock()
	if len(*seen) != 2 {
		t.Fatalf("events %+v, want a started/ended pair", *seen)
	}
	if (*seen)[0].Phase != TurnPhaseStarted || (*seen)[0].SessionID != "sess_obs" {
		t.Fatalf("first event %+v", (*seen)[0])
	}
	if (*seen)[1].Phase != TurnPhaseEnded || (*seen)[1].SessionID != "sess_obs" {
		t.Fatalf("second event %+v", (*seen)[1])
	}
	if (*seen)[0].At.IsZero() || (*seen)[1].At.IsZero() {
		t.Fatal("events must carry a timestamp")
	}
}

// A prompt turn delegates to RunPlan, so one logical turn marks its session twice.
// Watchers must not be told the turn ended when only the inner run returned.
func TestTurnObserverIgnoresNestedTurns(t *testing.T) {
	m := &Manager{}
	seen, mu, remove := collectTurnEvents(m)
	defer remove()

	outer := m.markTurnActive("sess_nested")
	inner := m.markTurnActive("sess_nested")
	inner()

	mu.Lock()
	if len(*seen) != 1 {
		mu.Unlock()
		t.Fatalf("events %+v, want only the started edge", *seen)
	}
	mu.Unlock()

	outer()
	mu.Lock()
	defer mu.Unlock()
	if len(*seen) != 2 || (*seen)[1].Phase != TurnPhaseEnded {
		t.Fatalf("events %+v, want the ended edge on the outer release", *seen)
	}
}

func TestRemoveTurnObserverStopsDelivery(t *testing.T) {
	m := &Manager{}
	seen, mu, remove := collectTurnEvents(m)
	remove()

	m.markTurnActive("sess_gone")()

	mu.Lock()
	defer mu.Unlock()
	if len(*seen) != 0 {
		t.Fatalf("events %+v after the observer was removed", *seen)
	}
}

// Events are delivered on the turn's own goroutine precisely so a session's started
// edge can never reach a watcher after its ended edge.
func TestTurnObserverKeepsPhaseOrderUnderConcurrentTurns(t *testing.T) {
	m := &Manager{}
	var (
		mu   sync.Mutex
		seen = map[string][]TurnPhase{}
	)
	remove := m.AddTurnObserver(func(ev TurnEvent) {
		mu.Lock()
		seen[ev.SessionID] = append(seen[ev.SessionID], ev.Phase)
		mu.Unlock()
	})
	defer remove()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "sess_" + string(rune('a'+n))
			for j := 0; j < 25; j++ {
				m.markTurnActive(id)()
			}
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for id, phases := range seen {
		if len(phases)%2 != 0 {
			t.Fatalf("%s saw %d events, want pairs", id, len(phases))
		}
		for i, p := range phases {
			want := TurnPhaseStarted
			if i%2 == 1 {
				want = TurnPhaseEnded
			}
			if p != want {
				t.Fatalf("%s event %d is %q, want %q (phases %v)", id, i, p, want, phases)
			}
		}
	}
}

func TestActiveTurnSessionIDsListsRunningTurns(t *testing.T) {
	m := &Manager{}
	if got := m.ActiveTurnSessionIDs(); len(got) != 0 {
		t.Fatalf("ids %v, want none", got)
	}
	releaseA := m.markTurnActive("sess_a")
	releaseB := m.markTurnActive("sess_b")

	got := m.ActiveTurnSessionIDs()
	if len(got) != 2 {
		t.Fatalf("ids %v, want both running sessions", got)
	}
	found := map[string]bool{}
	for _, id := range got {
		found[id] = true
	}
	if !found["sess_a"] || !found["sess_b"] {
		t.Fatalf("ids %v", got)
	}

	releaseA()
	releaseB()
	if got := m.ActiveTurnSessionIDs(); len(got) != 0 {
		t.Fatalf("ids %v after both turns ended", got)
	}
}
