package agent

import (
	"io"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestSubagentUnknownUsageNotifiesEvenWithoutCounterChange(t *testing.T) {
	s := newSubagentSender(io.Discard, nil)
	type update struct {
		input, output int
		incomplete    bool
	}
	var updates []update
	s.onUsage = func(in, out int, incomplete bool) { updates = append(updates, update{in, out, incomplete}) }
	yes, no := true, false
	_ = s.SendSessionUpdate("child", acp.TokenUsageUpdate{UsageKnown: &yes, InputTokens: 12})
	_ = s.SendSessionUpdate("child", acp.TokenUsageUpdate{UsageKnown: &no})
	if len(updates) != 2 || updates[0].incomplete || !updates[1].incomplete {
		t.Fatalf("usage notifications = %+v, want known then incomplete", updates)
	}
	_ = s.SendSessionUpdate("child", acp.TurnProgressUpdate{StartedAt: "turn", OutputTokens: 25, Estimated: true})
	_ = s.SendSessionUpdate("child", acp.TokenUsageUpdate{UsageKnown: &yes, InputTokens: 3})
	last := updates[len(updates)-1]
	if last != (update{15, 25, true}) {
		t.Fatalf("later usage = %+v, want estimate retained and incomplete", last)
	}
}

func TestSubagentIgnoresUnknownInputUsage(t *testing.T) {
	s := newSubagentSender(io.Discard, nil)
	known := false
	if err := s.SendSessionUpdate("child", acp.TokenUsageUpdate{UsageKnown: &known, InputTokens: 42}); err != nil {
		t.Fatal(err)
	}
	if s.usage.input != 0 {
		t.Fatalf("unknown input counted as exact: %d", s.usage.input)
	}
	if err := s.SendSessionUpdate("child", acp.TokenUsageUpdate{InputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if s.usage.input != 5 {
		t.Fatalf("legacy input lost: %d", s.usage.input)
	}
}
