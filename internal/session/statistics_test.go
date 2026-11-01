package session

import (
	"log/slog"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func TestStatisticsImportIsIdempotentAndSkipsLiveTurns(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home}}
	fs := &FileStore{Root: cfg.ResolvedSessionsRoot()}
	dir, err := fs.EnsureLayout("sess_history")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC()
	live := time.Now().Add(-time.Hour).UTC()
	st := &State{ID: "sess_history", CWD: home, Mode: ModeAgent, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "old prompt", CreatedAt: old.Format(time.RFC3339Nano)})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer", CreatedAt: old.Add(time.Minute).Format(time.RFC3339Nano)})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "live prompt", CreatedAt: live.Format(time.RFC3339Nano)})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	m := NewManager(cfg, nil, nil, slog.New(slog.DiscardHandler), home, fs)
	if err := m.Statistics().Append(statistics.Event{ID: "live", Session: st.ID, At: live, Kind: "turn", Requests: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := m.StartStatisticsImport(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			settings, err := m.StatisticsImportStatus()
			if err != nil {
				t.Fatal(err)
			}
			if settings.Import.State == "complete" {
				if settings.Import.Total != 1 || settings.Import.Done != 1 {
					t.Fatalf("progress: %+v", settings.Import)
				}
				break
			}
			if settings.Import.State == "error" || time.Now().After(deadline) {
				t.Fatalf("import: %+v", settings.Import)
			}
			time.Sleep(time.Millisecond * 5)
		}
	}
	report, err := m.Statistics().Query(statistics.Filter{})
	if err != nil || report.Totals.Requests != 2 || report.Totals.Total != nil || report.Totals.LongestMS != nil {
		t.Fatalf("import invented or duplicated data: %+v %v", report, err)
	}
}
