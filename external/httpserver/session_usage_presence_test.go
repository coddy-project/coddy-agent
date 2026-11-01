//go:build http

package httpserver

import (
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionListUsagePresence(t *testing.T) {
	fs := &session.FileStore{Root: t.TempDir()}
	yes, no := true, false
	for _, tc := range []struct {
		name    string
		known   *bool
		value   int
		present bool
	}{
		{"unknown", &no, 12, false}, {"zero", &yes, 0, true}, {"legacy", nil, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "sess_" + tc.name
			if err := os.MkdirAll(fs.SessionPath(id), 0700); err != nil {
				t.Fatal(err)
			}
			if err := session.WriteSessionStats(fs.SessionPath(id), session.SessionStats{TokenUsageTotal: session.TokenUsageTotals{UsageKnown: tc.known, TotalTokens: tc.value}}); err != nil {
				t.Fatal(err)
			}
			got := coddySessionTokenUsage(fs, id)
			if (got != nil) != tc.present {
				t.Fatalf("got %v, want present %v", got, tc.present)
			}
		})
	}
}

// A session bundle without stats.json (a chat that never completed a model
// call) keeps the legacy zero counters, so every row of the table has the
// same shape.
func TestSessionListUsageMissingStatsFile(t *testing.T) {
	fs := &session.FileStore{Root: t.TempDir()}
	id := "sess_no_stats"
	if err := os.MkdirAll(fs.SessionPath(id), 0700); err != nil {
		t.Fatal(err)
	}
	got := coddySessionTokenUsage(fs, id)
	if got == nil {
		t.Fatal("missing stats.json must keep the legacy zero counters, got null")
	}
	for _, name := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if got[name] != 0 {
			t.Fatalf("%s = %d, want 0", name, got[name])
		}
	}
}

// A stats.json the store cannot read - corrupt JSON, or a path that is not a
// file at all - means the usage is unknown, not zero. The row must carry null
// so the table shows the same "usage unknown" the UsageKnown flag produces,
// rather than a false zero.
func TestSessionListUsageUnreadableStatsFile(t *testing.T) {
	fs := &session.FileStore{Root: t.TempDir()}

	t.Run("corrupt json", func(t *testing.T) {
		id := "sess_corrupt"
		dir := fs.SessionPath(id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stats.json"), []byte("{not json"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := coddySessionTokenUsage(fs, id); got != nil {
			t.Fatalf("corrupt stats.json reported %v, want null", got)
		}
	})

	t.Run("stats path is a directory", func(t *testing.T) {
		id := "sess_dir_stats"
		if err := os.MkdirAll(filepath.Join(fs.SessionPath(id), "stats.json"), 0700); err != nil {
			t.Fatal(err)
		}
		if got := coddySessionTokenUsage(fs, id); got != nil {
			t.Fatalf("unreadable stats.json reported %v, want null", got)
		}
	})
}
