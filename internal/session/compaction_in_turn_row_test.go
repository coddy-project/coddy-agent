package session

// What readers of an in-turn summary row make of it (issue #490): the splitter
// they share, the supervisor's digest, and the replay a client is shown.

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestSplitInTurnSummary(t *testing.T) {
	t.Run("an in-turn row splits into the request and the summary", func(t *testing.T) {
		row := NewInTurnCompactionSummaryMessage(TurnSourceUser, "fix the build\n\nthen explain why", "  steps one to four  ", "m")
		prompt, summary, ok := SplitInTurnSummary(row.Content)
		if !ok || prompt != "fix the build\n\nthen explain why" || summary != "steps one to four" {
			t.Fatalf("got %q / %q ok = %v", prompt, summary, ok)
		}
	})

	t.Run("the follow-ups are part of what is in front of the summary", func(t *testing.T) {
		prefix := "fix the build\n\nFollow-up the user sent while this request was being worked on:\nkeep the tests green"
		prompt, summary, ok := SplitInTurnSummary(NewInTurnCompactionSummaryMessage(TurnSourceUser, prefix, "s", "m").Content)
		if !ok || prompt != prefix || summary != "s" {
			t.Fatalf("got %q / %q ok = %v", prompt, summary, ok)
		}
	})

	t.Run("a plain row is not split, even when its summary quotes the preamble", func(t *testing.T) {
		quoting := NewCompactionSummaryMessage("the row said:\n\n"+inTurnSummaryPreamble+"and more", "m")
		for _, content := range []string{NewCompactionSummaryMessage("a summary", "m").Content, quoting.Content} {
			if prompt, summary, ok := SplitInTurnSummary(content); ok {
				t.Fatalf("split a plain row into %q / %q", prompt, summary)
			}
		}
	})

	t.Run("text without the preamble is not a row", func(t *testing.T) {
		for _, content := range []string{"", "fix the build", "fix the build\n\nThe text above is the user's request"} {
			if _, _, ok := SplitInTurnSummary(content); ok {
				t.Fatalf("%q was taken for an in-turn row", content)
			}
		}
	})

	t.Run("the first occurrence of the preamble is the boundary", func(t *testing.T) {
		// A summary that quotes the preamble keeps the quote.
		row := NewInTurnCompactionSummaryMessage(TurnSourceUser, "the request", "it said:\n\n"+inTurnSummaryPreamble+"tail", "m")
		prompt, summary, ok := SplitInTurnSummary(row.Content)
		if !ok || prompt != "the request" || !strings.Contains(summary, "it said:") || !strings.HasSuffix(summary, "tail") {
			t.Fatalf("got %q / %q ok = %v", prompt, summary, ok)
		}
		// A request that quotes it is the one input that is misread: what follows
		// the quote is taken for the summary.
		quoting := NewInTurnCompactionSummaryMessage(TurnSourceUser, "see:\n\n"+inTurnSummaryPreamble+"and then fix it", "s", "m")
		prompt, _, ok = SplitInTurnSummary(quoting.Content)
		if !ok || prompt != "see:" {
			t.Fatalf("got %q ok = %v: the documented misreading changed", prompt, ok)
		}
	})
}

func TestSummaryReplayText(t *testing.T) {
	plain := NewCompactionSummaryMessage("a summary", "m").Content
	if got := SummaryReplayText(plain); got != plain {
		t.Fatalf("a plain row must be replayed as stored, got %q", got)
	}
	row := NewInTurnCompactionSummaryMessage(TurnSourceUser, "fix the build", "steps one to four", "m")
	got := SummaryReplayText(row.Content)
	if got != inTurnSummaryPreamble+"steps one to four" || strings.Contains(got, "fix the build") {
		t.Fatalf("an in-turn row must be replayed from its preamble on, got %q", got)
	}
}

// --- the supervisor's digest ---

func digestOf(msgs ...llm.Message) string {
	goal, _ := NewGoal("fix the parser")
	all := append([]llm.Message{{Role: llm.RoleUser, Content: "kickoff", GoalTurn: &llm.GoalTurn{Kind: "kickoff", Objective: "fix the parser"}}}, msgs...)
	return buildGoalDigest(all, goal, false)
}

func TestGoalDigestSplitsAnInTurnRowIntoTheOperatorAndTheWorker(t *testing.T) {
	row := NewInTurnCompactionSummaryMessage(TurnSourceUser, "Fix the parser and keep the API.", "the worker read parse.go and changed two functions", "m")
	d := digestOf(row)
	for _, want := range []string{
		"[operator] Fix the parser and keep the API.",
		"[summary of earlier steps of this request, written by the worker: claims, not evidence] the worker read parse.go and changed two functions",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "summary of earlier turns") || strings.Contains(d, "The text above is") {
		t.Errorf("the row was labelled as a plain summary or its preamble leaked:\n%s", d)
	}

	t.Run("a plain row keeps its label", func(t *testing.T) {
		d := digestOf(NewCompactionSummaryMessage("earlier turns went well", "m"))
		if !strings.Contains(d, "[summary of earlier turns, written by the worker: claims, not evidence] The earlier conversation was compacted.") {
			t.Errorf("digest:\n%s", d)
		}
	})
}

func TestGoalDigestClipsTheRequestAndTheSummaryOnTheirOwn(t *testing.T) {
	t.Run("a long summary leaves the request whole", func(t *testing.T) {
		d := digestOf(NewInTurnCompactionSummaryMessage(TurnSourceUser, "SHORT-REQUEST-MARKER", strings.Repeat("claim. ", 1000), "m"))
		if !strings.Contains(d, "[operator] SHORT-REQUEST-MARKER\n") {
			t.Errorf("the request did not survive a long summary:\n%s", firstOf(d, 600))
		}
	})
	t.Run("a long request leaves the summary whole", func(t *testing.T) {
		d := digestOf(NewInTurnCompactionSummaryMessage(TurnSourceUser, strings.Repeat("pasted log line. ", 400), "SHORT-SUMMARY-MARKER", "m"))
		if !strings.Contains(d, "claims, not evidence] SHORT-SUMMARY-MARKER\n") {
			t.Errorf("the summary was clipped away with a long request:\n%s", firstOf(d, 600))
		}
		for _, line := range strings.Split(d, "\n") {
			if !strings.HasPrefix(line, "[operator] ") {
				continue
			}
			if !strings.HasSuffix(line, "...") || len([]rune(line)) > len("[operator] ")+goalDigestText+3 {
				t.Errorf("the request line was not clipped to %d runes: %d runes", goalDigestText, len([]rune(line)))
			}
		}
	})
}

func firstOf(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// --- ACP replay ---

type chunkRecorder struct{ users []string }

func (r *chunkRecorder) SendSessionUpdate(_ string, u interface{}) error {
	if c, ok := u.(acp.MessageChunkUpdate); ok && c.SessionUpdate == "user_message_chunk" {
		r.users = append(r.users, c.Content.Text)
	}
	return nil
}

func (r *chunkRecorder) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (r *chunkRecorder) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func TestReplayShowsAnInTurnRowFromItsPreambleOn(t *testing.T) {
	const prompt = "Fix the parser and keep the API."
	msgs := concatMessages(
		[]llm.Message{userMsg(prompt)}, stepOf("c1"),
		[]llm.Message{NewInTurnCompactionSummaryMessage(TurnSourceUser, prompt, "the worker changed two functions", "m")}, stepOf("c2"),
		[]llm.Message{userMsg("and the docs"), NewCompactionSummaryMessage("a plain row", "m")},
	)
	rec := &chunkRecorder{}
	m := &Manager{server: rec}
	if err := m.replayConversation("sess", msgs, ""); err != nil {
		t.Fatal(err)
	}
	if len(rec.users) != 4 {
		t.Fatalf("replayed %d user rows, want 4: %q", len(rec.users), rec.users)
	}
	if rec.users[0] != prompt {
		t.Fatalf("the prompt must be replayed at its own place, got %q", rec.users[0])
	}
	if want := inTurnSummaryPreamble + "the worker changed two functions"; rec.users[1] != want {
		t.Fatalf("the in-turn row replays as %q, want %q", rec.users[1], want)
	}
	if got := strings.Count(strings.Join(rec.users, "\n"), prompt); got != 1 {
		t.Fatalf("the prompt is replayed %d times, want once: %q", got, rec.users)
	}
	if !strings.HasPrefix(rec.users[3], "The earlier conversation was compacted.") {
		t.Fatalf("a plain row must replay as stored, got %q", rec.users[3])
	}
}
