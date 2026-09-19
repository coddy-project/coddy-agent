package serve

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// fakeWakeSurface records the wakes it was offered and handles those it owns.
type fakeWakeSurface struct {
	name   string
	owns   func(sessionID string) bool
	mu     sync.Mutex
	offers []string
	ran    []string
	order  *[]string
}

func (f *fakeWakeSurface) RunBackgroundWake(_ context.Context, wake agent.Wake) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offers = append(f.offers, wake.SessionID)
	if f.order != nil {
		*f.order = append(*f.order, f.name)
	}
	if f.owns != nil && !f.owns(wake.SessionID) {
		return false, nil
	}
	f.ran = append(f.ran, wake.SessionID)
	return true, nil
}

func (f *fakeWakeSurface) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

func wakeFor(sessionID string) agent.Wake {
	end := time.Now()
	code := 2
	return agent.Wake{SessionID: sessionID, Tasks: []bgtask.Snapshot{{
		ID: "bg_1", SessionID: sessionID, Kind: bgtask.KindCommand, Label: "make test",
		Status: bgtask.StatusFailed, ExitCode: &code, StartedAt: end.Add(-time.Minute), FinishedAt: &end, NotifyOnFinish: true,
	}}}
}

// A chat bound to the session is where its person reads: the surface that owns
// the conversation runs the woken turn, whatever order the surfaces came up in,
// and a host is asked only when no owner takes it.
func TestRuntimeOffersAWakeToTheConversationOwnerFirst(t *testing.T) {
	rt := &Runtime{}
	var order []string
	host := &fakeWakeSurface{name: "http", order: &order}
	chat := &fakeWakeSurface{name: "telegram", order: &order, owns: func(id string) bool { return id == "sess_chat" }}
	rt.AddWakeSurface(host, agent.WakeHost)
	withdrawChat := rt.AddWakeSurface(chat, agent.WakeOwner)

	if err := rt.runBackgroundWake(context.Background(), wakeFor("sess_chat")); err != nil {
		t.Fatal(err)
	}
	if got := chat.runs(); len(got) != 1 || got[0] != "sess_chat" {
		t.Fatalf("the chat ran %v, want its own session", got)
	}
	if got := host.runs(); len(got) != 0 {
		t.Fatalf("the host ran %v while the chat owned the session", got)
	}
	if strings.Join(order, ",") != "telegram" {
		t.Fatalf("asked in order %v, want the owner alone", order)
	}

	// A session no chat holds goes to the host, after the owner declined.
	order = nil
	if err := rt.runBackgroundWake(context.Background(), wakeFor("sess_web")); err != nil {
		t.Fatal(err)
	}
	if got := host.runs(); len(got) != 1 || got[0] != "sess_web" {
		t.Fatalf("the host ran %v, want the web session", got)
	}
	if strings.Join(order, ",") != "telegram,http" {
		t.Fatalf("asked in order %v, want the owner, then the host", order)
	}

	// A withdrawn surface is not asked again; withdrawing twice is harmless.
	withdrawChat()
	withdrawChat()
	order = nil
	if err := rt.runBackgroundWake(context.Background(), wakeFor("sess_chat")); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "http" {
		t.Fatalf("asked in order %v after the chat went away, want the host alone", order)
	}
	if rt.AddWakeSurface(nil, agent.WakeHost) == nil {
		t.Fatal("offering nil must still return a callable withdraw")
	}
}

// With no surface up that could show the session - a `coddy serve` running
// only its scheduler, or a bot whose chat moved to another session - the
// runtime still runs the turn it promised, through the manager, and the only
// possible answer to a permission prompt is the operator's standing one: no,
// unless the permission mode is bypass.
func TestRuntimeRunsAnUnclaimedWakeItselfAndRefusesWhatNobodyCanApprove(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: filepath.Join(root, "home"), CWD: root}}

	type seen struct {
		wake     *llm.BackgroundWake
		approved *acp.PermissionResult
	}
	turns := make(chan seen, 2)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		res, _ := snd.RequestPermission(ctx, acp.PermissionRequestParams{
			SessionID: st.GetID(),
			ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: "Run: rm -rf build"},
		})
		turns <- seen{wake: st.TakeTurnWake(), approved: res}
		return string(acp.StopReasonEndTurn), nil
	}
	rt := &Runtime{cfg: cfg, Log: slog.Default(), Store: store}
	rt.Mgr = session.NewManager(cfg, &defaultSender{live: rt.Cfg}, runner, slog.Default(), root, store)
	res, err := rt.Mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.runBackgroundWake(context.Background(), wakeFor(res.SessionID)); err != nil {
		t.Fatal(err)
	}
	got := <-turns
	if got.wake == nil || len(got.wake.Tasks) != 1 || got.wake.Tasks[0].ID != "bg_1" {
		t.Fatalf("the turn ran without its wake: %+v", got.wake)
	}
	if got.approved == nil || got.approved.OptionID != "reject" {
		t.Fatalf("a prompt nobody can see was answered %+v, want a refusal", got.approved)
	}
}
