package session

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type contextUsageCapture struct {
	updates []interface{}
}

func (c *contextUsageCapture) SendSessionUpdate(_ string, update interface{}) error {
	c.updates = append(c.updates, update)
	return nil
}

func (*contextUsageCapture) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (*contextUsageCapture) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func TestRestoreContextBreakdownPublishesUsageUpdate(t *testing.T) {
	dir := t.TempDir()
	want := &ContextBreakdown{
		SystemPrompt:   120,
		Conversation:   300,
		EstimatedTotal: 420,
	}
	if err := WriteSessionStats(dir, SessionStats{ContextBreakdown: want}); err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         "session-with-compacted-context",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	restoreContextBreakdown(st)

	gotBreakdown := st.GetLastContextBreakdown()
	if gotBreakdown == nil || gotBreakdown.EstimatedTotal != want.EstimatedTotal {
		t.Fatalf("restored context = %+v, want %+v", gotBreakdown, want)
	}

	sender := &contextUsageCapture{}
	cfg := &config.Config{
		Models: []config.ModelEntry{{
			Model:            "test/model",
			MaxContextTokens: 128000,
		}},
		Agent: config.Agent{Model: "test/model"},
	}
	manager := NewManager(cfg, sender, nil, slog.Default(), "", nil)
	manager.sendContextUsageUpdate(st.ID, st)

	if len(sender.updates) != 1 {
		t.Fatalf("usage updates = %d, want 1", len(sender.updates))
	}
	update, ok := sender.updates[0].(acp.UsageUpdate)
	if !ok {
		t.Fatalf("update type = %T, want acp.UsageUpdate", sender.updates[0])
	}
	if update.SessionUpdate != acp.UpdateTypeUsage || update.Used != 420 || update.Size != 128000 {
		t.Fatalf("usage update = %+v", update)
	}
}

// A model without max_context_tokens still publishes its context usage,
// sized by the window its provider reports - the size the web UI and the
// console draw against.
func TestSendContextUsageUpdateSizesByTheResolvedWindow(t *testing.T) {
	cfg := windowTestConfig(t.TempDir())
	listing := &windowListing{windows: map[string]int{"reported": 262144}}
	m := newWindowTestManager(t, cfg, listing, nil)
	sender := m.server.(*contextUsageCapture)

	st := &State{ID: "sess_usage_window", Mode: ModeAgent, contextWindows: m}
	st.SetLastContextBreakdown(&ContextBreakdown{SystemPrompt: 100, EstimatedTotal: 100})

	m.sendContextUsageUpdate(st.ID, st)
	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/reported"}, time.Second)
	m.sendContextUsageUpdate(st.ID, st)

	if len(sender.updates) != 2 {
		t.Fatalf("usage updates = %d, want 2", len(sender.updates))
	}
	for i, want := range []int{config.DefaultContextWindowTokens, 262144} {
		update, ok := sender.updates[i].(acp.UsageUpdate)
		if !ok || update.Size != want || update.Used != 100 {
			t.Fatalf("update %d = %+v, want size %d", i, sender.updates[i], want)
		}
	}
}
