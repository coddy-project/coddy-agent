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
		{Role: "user", CompactionSummary: true, Content: session.NewInTurnCompactionSummaryMessage(prompt, "the worker read two files", "m").Content},
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
