package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestBackgroundWakeNoteSaysWhatEndedAndHow(t *testing.T) {
	two := 2
	for name, tc := range map[string]struct {
		tasks []acp.BackgroundWakeTask
		want  string
	}{
		"one failed command": {
			tasks: []acp.BackgroundWakeTask{{ID: "bg_3", Kind: "command", Label: "make test", Status: "failed", ExitCode: &two, DurationMs: 90_000}},
			want:  "Woken by a finished background task: bg_3 make test, failed, exit 2, 1m 30s",
		},
		"an agent run leaves the synthetic exit code out": {
			tasks: []acp.BackgroundWakeTask{{ID: "bg_4", Kind: "agent", Agent: "reviewer", Label: "agent reviewer: check the diff", Status: "succeeded", ExitCode: new(int), DurationMs: 45_000}},
			want:  "Woken by a finished background task: bg_4 agent reviewer: check the diff, succeeded, 45s",
		},
		"several tasks get a line each": {
			tasks: []acp.BackgroundWakeTask{
				{ID: "bg_1", Kind: "command", Label: "go build ./...", Status: "succeeded", ExitCode: new(int), DurationMs: 12_000},
				{ID: "bg_2", Kind: "command", Label: "sleep 7200", Status: "timed_out", DurationMs: 3_600_000},
			},
			want: "Woken by 2 finished background tasks:\n- bg_1 go build ./..., succeeded, exit 0, 12s\n- bg_2 sleep 7200, timed out, 1h",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := BackgroundWakeNote(acp.BackgroundWakeUpdate{SessionUpdate: acp.UpdateTypeBackgroundWake, Tasks: tc.tasks})
			if got != tc.want {
				t.Fatalf("note =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestWakeDurationReadsLikeAClock(t *testing.T) {
	for ms, want := range map[int64]string{
		0:          "0s",
		999:        "1s",
		45_000:     "45s",
		60_000:     "1m",
		90_000:     "1m 30s",
		3_600_000:  "1h",
		3_900_000:  "1h 5m",
		86_400_000: "24h",
	} {
		if got := wakeDuration(ms); got != want {
			t.Errorf("wakeDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}
