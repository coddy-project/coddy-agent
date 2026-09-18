package session_test

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func exitCode(n int) *int { return &n }

func sampleWake() *llm.BackgroundWake {
	return &llm.BackgroundWake{Tasks: []llm.BackgroundWakeTask{{
		ID: "bg_3", Kind: "command", Label: "make test", Status: "failed",
		ExitCode: exitCode(2), DurationMs: 90_000, Error: "exit status 2",
	}}}
}

// A turn finished background tasks started holds the wake for exactly that
// turn: the agent can take it while it builds the first message, the next turn
// on the session does not see it, and the observers hear that the turn holding
// the session is a wake - between its start and its end, dated at the turn's
// start like the started edge. The manager answers for the wake of the turn
// that is running, for a client that attaches after the event went out.
func TestBackgroundWakeLastsOneTurnAndIsAnnounced(t *testing.T) {
	seen := make(chan *llm.BackgroundWake, 4)
	held := make(chan *llm.BackgroundWake, 4)
	var m *session.Manager
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		st.AddMessage(acpToLLM(prompt))
		seen <- st.TakeTurnWake()
		// Taken by the agent, still known to the manager for the turn.
		held <- m.TurnWake(st.GetID())
		return string(acp.StopReasonEndTurn), nil
	}
	m, _, root := newSubagentTestManagerWithRunner(t, runner)
	parent := newParent(t, m, root)

	var (
		mu     sync.Mutex
		events []session.TurnEvent
	)
	remove := m.AddTurnObserver(func(ev session.TurnEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	defer remove()

	wake := sampleWake()
	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "A background task you asked to be notified about has finished."}}
	if _, err := m.HandleSessionPromptWithSender(context.Background(),
		acp.SessionPromptParams{SessionID: parent.ID, Prompt: prompt}, noopSender{},
		&session.PromptRunOpts{BackgroundWake: wake}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != wake {
		t.Fatalf("the turn ran without its wake: %+v", got)
	}
	if got := <-held; got != wake {
		t.Fatalf("the manager did not know the running turn's wake: %+v", got)
	}
	if got := parent.TakeTurnWake(); got != nil {
		t.Fatalf("the wake outlived its turn: %+v", got)
	}
	if got := m.TurnWake(parent.ID); got != nil {
		t.Fatalf("the manager kept the wake after the turn: %+v", got)
	}

	mu.Lock()
	phases := make([]session.TurnPhase, 0, len(events))
	for _, ev := range events {
		phases = append(phases, ev.Phase)
	}
	started, woken := events[0], events[1]
	mu.Unlock()
	want := []session.TurnPhase{session.TurnPhaseStarted, session.TurnPhaseWoken, session.TurnPhaseEnded}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	if woken.SessionID != parent.ID || woken.Wake != wake || woken.At.IsZero() {
		t.Fatalf("woken event = %+v", woken)
	}
	// Both edges name the turn by its start, which is how a client that hears
	// of the wake twice - live, then in a reconnect's snapshot - knows it is
	// the same turn.
	if !woken.At.Equal(started.At) {
		t.Fatalf("woken at %v, the turn started at %v", woken.At, started.At)
	}

	// A turn somebody typed is not a wake, and nothing says it is.
	if _, err := m.HandleSessionPromptWithSender(context.Background(),
		acp.SessionPromptParams{SessionID: parent.ID, Prompt: prompt}, noopSender{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != nil {
		t.Fatalf("a typed turn carried a wake: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range events[3:] {
		if ev.Phase == session.TurnPhaseWoken {
			t.Fatalf("a typed turn was announced as a wake: %+v", ev)
		}
	}
}

// updateRecorder keeps every session update a replay sends.
type updateRecorder struct {
	mu      sync.Mutex
	updates []interface{}
}

func (r *updateRecorder) SendSessionUpdate(_ string, u interface{}) error {
	r.mu.Lock()
	r.updates = append(r.updates, u)
	r.mu.Unlock()
	return nil
}

func (r *updateRecorder) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (r *updateRecorder) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// The marker is part of the transcript: it survives the bundle, and a session
// loaded again replays the woken turn's first message as the wake it was - the
// same update the turn sent live - never as a message from the user.
func TestReloadedSessionReplaysAWakeAsTheWake(t *testing.T) {
	m, store, root := newSubagentTestManagerWithRunner(t, func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	parent := newParent(t, m, root)
	wake := sampleWake()
	parent.AddMessage(llm.Message{Role: llm.RoleUser, Content: "build it"})
	parent.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "started bg_3"})
	parent.AddMessage(llm.Message{Role: llm.RoleUser, Content: "A background task you asked to be notified about has finished.", BackgroundWake: wake})
	parent.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The tests failed."})
	if err := store.Save(parent); err != nil {
		t.Fatal(err)
	}

	snap, err := store.ReadSnapshot(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Messages[2].BackgroundWake; got == nil || !reflect.DeepEqual(got, wake) {
		t.Fatalf("the bundle lost the marker: %+v", got)
	}
	if snap.Messages[0].BackgroundWake != nil {
		t.Fatal("a typed message came back marked as a wake")
	}

	rec := &updateRecorder{}
	m.SetServer(rec)
	m.ForgetLiveSession(parent.ID)
	if _, err := m.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: parent.ID, CWD: root}); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var users []string
	var wakes []acp.BackgroundWakeUpdate
	for _, u := range rec.updates {
		switch v := u.(type) {
		case acp.MessageChunkUpdate:
			if v.SessionUpdate == acp.UpdateTypeUserMessageChunk {
				users = append(users, v.Content.Text)
			}
		case acp.BackgroundWakeUpdate:
			wakes = append(wakes, v)
		}
	}
	if !reflect.DeepEqual(users, []string{"build it"}) {
		t.Fatalf("user messages replayed = %q, want only the typed one", users)
	}
	if len(wakes) != 1 {
		t.Fatalf("wakes replayed = %+v, want one", wakes)
	}
	got := wakes[0]
	if got.SessionUpdate != acp.UpdateTypeBackgroundWake || len(got.Tasks) != 1 {
		t.Fatalf("wake update = %+v", got)
	}
	task := got.Tasks[0]
	if task.ID != "bg_3" || task.Status != "failed" || task.Label != "make test" || task.ExitCode == nil || *task.ExitCode != 2 || task.DurationMs != 90_000 {
		t.Fatalf("wake task = %+v", task)
	}
}
