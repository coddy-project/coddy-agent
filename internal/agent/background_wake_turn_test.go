package agent

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// wakeOrderSender records the wake update and what the transcript held at the
// moment it was sent; onWake, when set, looks at anything else at that moment.
type wakeOrderSender struct {
	state  *session.State
	onWake func()

	mu             sync.Mutex
	wakes          []acp.BackgroundWakeUpdate
	messagesAtWake int
}

func (s *wakeOrderSender) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.BackgroundWakeUpdate); ok {
		s.mu.Lock()
		s.wakes = append(s.wakes, u)
		s.messagesAtWake = len(s.state.GetMessages())
		s.mu.Unlock()
		if s.onWake != nil {
			s.onWake()
		}
	}
	return nil
}

func (s *wakeOrderSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (s *wakeOrderSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func wakeTurnConfig() *config.Config {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	return cfg
}

// A turn finished background tasks started opens with the wake: the clients are
// told first, then the turn's first message is persisted with the marker - the
// order every message frame follows, so a client that reloads between the two
// reads the marked message and is not shown the wake a second time.
func TestWokenTurnAnnouncesTheWakeBeforeItRecordsTheMessage(t *testing.T) {
	st := &session.State{ID: "sess_woken", CWD: t.TempDir(), Mode: session.ModeAgent}
	sender := &wakeOrderSender{state: st}
	ag := NewAgent(wakeTurnConfig(), st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return scripted(answerStep("The tests failed.")), nil }

	code := 2
	wake := &llm.BackgroundWake{Tasks: []llm.BackgroundWakeTask{{
		ID: "bg_3", Kind: "command", Label: "make test", Status: "failed", ExitCode: &code, DurationMs: 90_000,
	}}}
	st.SetTurnWake(wake)
	instruction := "A background task you started has finished."
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: instruction}}); err != nil {
		t.Fatal(err)
	}

	sender.mu.Lock()
	wakes, before := sender.wakes, sender.messagesAtWake
	sender.mu.Unlock()
	if len(wakes) != 1 || wakes[0].SessionUpdate != acp.UpdateTypeBackgroundWake || len(wakes[0].Tasks) != 1 || wakes[0].Tasks[0].ID != "bg_3" {
		t.Fatalf("wake updates = %+v, want one naming bg_3", wakes)
	}
	if before != 0 {
		t.Fatalf("the transcript held %d messages when the wake was announced, want the message persisted after it", before)
	}

	msgs := st.GetMessages()
	if len(msgs) < 2 || msgs[0].Role != llm.RoleUser {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[0].Content != instruction || msgs[0].BackgroundWake != wake {
		t.Fatalf("first message = %+v, want the instruction marked as the wake", msgs[0])
	}
	if got := st.TakeTurnWake(); got != nil {
		t.Fatalf("the wake was not taken by the turn: %+v", got)
	}

	// The next turn somebody types is an ordinary message.
	ag2 := NewAgent(wakeTurnConfig(), st, sender, nil)
	ag2.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return scripted(answerStep("ok")), nil }
	if _, err := ag2.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "fix it"}}); err != nil {
		t.Fatal(err)
	}
	msgs = st.GetMessages()
	for _, m := range msgs[2:] {
		if m.BackgroundWake != nil {
			t.Fatalf("a typed message came out marked as a wake: %+v", m)
		}
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.wakes) != 1 {
		t.Fatalf("a typed turn announced a wake: %+v", sender.wakes)
	}
}

// The turn a finished task started marks that task in the pool as having woken
// the agent, and does it before the clients hear of the wake: a surface that
// reads its task list again on the wake already finds the mark, which is what
// keeps the bell on the task's card once it has ended.
func TestWokenTurnMarksTheTasksThatWokeIt(t *testing.T) {
	const sessionID = "sess_woken_mark"
	pool := bgtask.Default()
	t.Cleanup(func() { pool.ReleaseSession(sessionID) })
	handle := &subagentHandle{cancel: func() {}, done: make(chan struct{})}
	close(handle.done)
	snap, err := pool.Launch(bgtask.Spec{SessionID: sessionID, Kind: bgtask.KindAgent, Label: "agent general: review",
		NotifyOnFinish: true, Agent: &bgtask.AgentInfo{Name: "general"}},
		func(string, io.Writer) (bgtask.Handle, error) { return handle, nil })
	if err != nil {
		t.Fatalf("Launch(): %v", err)
	}
	if _, err := pool.Wait(context.Background(), sessionID, snap.ID, 3*time.Second); err != nil {
		t.Fatalf("Wait(): %v", err)
	}

	st := &session.State{ID: sessionID, CWD: t.TempDir(), Mode: session.ModeAgent}
	markedAtWake := false
	sender := &wakeOrderSender{state: st, onWake: func() {
		got, err := pool.Get(sessionID, snap.ID)
		markedAtWake = err == nil && got.WokeAgent
	}}
	ag := NewAgent(wakeTurnConfig(), st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return scripted(answerStep("The review is in.")), nil }
	st.SetTurnWake(&llm.BackgroundWake{Tasks: []llm.BackgroundWakeTask{{ID: snap.ID, Kind: "agent", Agent: "general", Status: "succeeded"}}})
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "A background task you started has finished."}}); err != nil {
		t.Fatal(err)
	}
	if !markedAtWake {
		t.Fatal("the task was not marked as having woken the agent by the time the wake was announced")
	}
}
