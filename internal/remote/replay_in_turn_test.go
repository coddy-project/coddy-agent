package remote

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A session loaded over REST replays a summary row written inside a turn from
// its preamble on: the request in front of it is already on screen at its own
// place, and a second copy would show the prompt twice. A plain row replays as
// stored.
func TestReplayShowsAnInTurnSummaryFromItsPreambleOn(t *testing.T) {
	h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	const prompt = "Fix the parser and keep the API."
	h.replayMessages("sess_1", []messageRow{
		{Role: "user", Content: prompt},
		{Role: "assistant", Content: "reading"},
		{Role: "user", CompactionSummary: true, Content: session.NewInTurnCompactionSummaryMessage(session.TurnSourceUser, prompt, "the worker read two files", "m").Content},
		{Role: "assistant", Content: "continuing"},
		{Role: "user", CompactionSummary: true, Content: session.NewCompactionSummaryMessage("an older turn", "m").Content},
	})
	sender.mu.Lock()
	defer sender.mu.Unlock()
	var users []string
	for _, u := range sender.updates {
		if v, ok := u.(acp.MessageChunkUpdate); ok && v.SessionUpdate == acp.UpdateTypeUserMessageChunk {
			users = append(users, v.Content.Text)
		}
	}
	if len(users) != 3 || users[0] != prompt {
		t.Fatalf("user rows = %q, want the prompt, the in-turn row and the plain row", users)
	}
	if !strings.HasPrefix(users[1], "The text above is the user's request") || !strings.HasSuffix(users[1], "the worker read two files") {
		t.Fatalf("the in-turn row replays as %q, want it from the preamble on", users[1])
	}
	if got := strings.Count(strings.Join(users, "\n"), prompt); got != 1 {
		t.Fatalf("the prompt is replayed %d times, want once: %q", got, users)
	}
	if !strings.HasPrefix(users[2], "The earlier conversation was compacted.") {
		t.Fatalf("a plain row must replay as stored, got %q", users[2])
	}
}

// The same holds for a row whose turn a supervisor or a finished background
// task opened: the replay drops the opening message, which is on screen as a
// row of its own, and keeps the preamble that names who wrote it.
func TestReplayShowsAnInTurnSummaryOfAnotherSourceFromItsPreambleOn(t *testing.T) {
	for name, tc := range map[string]struct {
		source session.TurnSource
		opens  string
	}{
		"goal supervisor": {session.TurnSourceSupervisor, "The text above is the instruction the session's goal supervisor started this turn with"},
		"background wake": {session.TurnSourceBackground, "The text above is the notice that woke this turn when a background task finished"},
	} {
		t.Run(name, func(t *testing.T) {
			h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:1"})
			if err != nil {
				t.Fatal(err)
			}
			sender := &collectSender{}
			h.SetServer(sender)
			const opening = "SUPERVISOR-OR-WAKE-TEXT"
			h.replayMessages("sess_1", []messageRow{
				{Role: "user", Content: opening},
				{Role: "assistant", Content: "reading"},
				{Role: "user", CompactionSummary: true, Content: session.NewInTurnCompactionSummaryMessage(tc.source, opening, "the worker read two files", "m").Content},
			})
			sender.mu.Lock()
			defer sender.mu.Unlock()
			var users []string
			for _, u := range sender.updates {
				if v, ok := u.(acp.MessageChunkUpdate); ok && v.SessionUpdate == acp.UpdateTypeUserMessageChunk {
					users = append(users, v.Content.Text)
				}
			}
			if len(users) != 2 || users[0] != opening {
				t.Fatalf("user rows = %q, want the opening message and the row", users)
			}
			if !strings.HasPrefix(users[1], tc.opens) || !strings.HasSuffix(users[1], "the worker read two files") || strings.Contains(users[1], opening) {
				t.Fatalf("the row replays as %q, want it from its preamble on", users[1])
			}
		})
	}
}
