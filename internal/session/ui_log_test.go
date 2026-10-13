package session

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestCountUserTurns(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
		{Role: llm.RoleUser, Content: "c"},
	}
	if n := CountUserTurns(msgs); n != 2 {
		t.Fatalf("got %d", n)
	}
	if n := CountUserTurns(nil); n != 0 {
		t.Fatalf("nil: got %d", n)
	}
}

// pageHistory builds a history from one letter per message: U a typed user
// message, W the user message a background wake opened a turn with, C a
// compaction summary, A an assistant answer, X an answer the output limit cut
// off (Truncated), E a step that came back empty, S an assistant message issuing
// one tool call, T that call's result.
func pageHistory(t *testing.T, spec string) []llm.Message {
	t.Helper()
	var out []llm.Message
	call := 0
	for _, r := range spec {
		switch r {
		case ' ':
			continue
		case 'U':
			out = append(out, llm.Message{Role: llm.RoleUser, Content: "task"})
		case 'W':
			out = append(out, llm.Message{Role: llm.RoleUser, Content: "wake", BackgroundWake: &llm.BackgroundWake{}})
		case 'C':
			out = append(out, llm.Message{Role: llm.RoleUser, Content: "summary", CompactionSummary: true})
		case 'A':
			out = append(out, llm.Message{Role: llm.RoleAssistant, Content: "answer"})
		case 'X':
			out = append(out, llm.Message{Role: llm.RoleAssistant, Content: "the first half and ", Truncated: true})
		case 'E':
			out = append(out, llm.Message{Role: llm.RoleAssistant, Reasoning: "thinking"})
		case 'S':
			call++
			out = append(out, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c" + string(rune('0'+call%10)), Name: "read_file"}}})
		case 'T':
			out = append(out, llm.Message{Role: llm.RoleTool, Content: "result", ToolCallID: "c" + string(rune('0'+call%10))})
		default:
			t.Fatalf("unknown message letter %q", r)
		}
	}
	return out
}

func unsetPage() MessagePageQuery { return MessagePageQuery{From: -1, Before: -1} }

func TestPageMessages(t *testing.T) {
	cases := []struct {
		name  string
		spec  string
		query MessagePageQuery
		want  MessagePage
	}{
		{
			name:  "no window reads the whole history",
			spec:  "U A U S T A",
			query: unsetPage(),
			want:  MessagePage{Offset: 0, End: 6, Total: 6},
		},
		{
			name:  "a tail page that lands on a prompt starts there",
			spec:  "U A U S T T A U S T A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 4},
			want:  MessagePage{Offset: 7, End: 11, Total: 11, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "a tail page grows back to a prompt within half its size",
			spec:  "U S T S T S T A U S T S T A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 5},
			want:  MessagePage{Offset: 8, End: 14, Total: 14, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "far from any prompt the page starts at the step the cut falls in",
			spec:  "U S T S T S T S T S T A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 4},
			want:  MessagePage{Offset: 7, End: 12, Total: 12, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "a tool result never opens a page without its call",
			spec:  "U A U S T T T A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 3},
			want:  MessagePage{Offset: 3, End: 8, Total: 8, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "a page is never empty while messages remain before its end",
			spec:  "U S T",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 1},
			want:  MessagePage{Offset: 1, End: 3, Total: 3, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "an older page ends where the window it joins starts",
			spec:  "U A U S T A U S T A",
			query: MessagePageQuery{From: -1, Before: 6, Limit: 3},
			want:  MessagePage{Offset: 2, End: 6, Total: 10, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "an end on a tool result moves back to its call",
			spec:  "U S T T U A",
			query: MessagePageQuery{From: -1, Before: 3, Limit: 10},
			want:  MessagePage{Offset: 0, End: 1, Total: 6},
		},
		{
			name:  "from re-reads a window to the end",
			spec:  "U A U S T A U A",
			query: MessagePageQuery{From: 2, Before: -1},
			want:  MessagePage{Offset: 2, End: 8, Total: 8, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "from on a tool result moves back to its call",
			spec:  "U A U S T T A",
			query: MessagePageQuery{From: 5, Before: -1},
			want:  MessagePage{Offset: 3, End: 7, Total: 7, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "positions past the history are clamped",
			spec:  "U A U A",
			query: MessagePageQuery{From: 9, Before: 20},
			want:  MessagePage{Offset: 4, End: 4, Total: 4, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "a limit larger than the history reads all of it",
			spec:  "U A U A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 100},
			want:  MessagePage{Offset: 0, End: 4, Total: 4},
		},
		{
			name:  "compaction summaries are rows but not turns, wakes are both",
			spec:  "U A C A W A U A",
			query: MessagePageQuery{From: 6, Before: -1},
			want:  MessagePage{Offset: 6, End: 8, Total: 8, TurnsBefore: 2, UserRowsBefore: 3},
		},
		{
			name:  "a continued answer never opens a page in its middle",
			spec:  "U A U X X A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 1},
			want:  MessagePage{Offset: 3, End: 6, Total: 6, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "an empty step between a cut text and its continuation stays with them",
			spec:  "U X E A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 1},
			want:  MessagePage{Offset: 1, End: 4, Total: 4, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "an end inside a continued answer moves back to where it begins",
			spec:  "U X A U A",
			query: MessagePageQuery{From: -1, Before: 2, Limit: 10},
			want:  MessagePage{Offset: 0, End: 1, Total: 5},
		},
		{
			name:  "a continuation that issues a call keeps the text it goes on from",
			spec:  "U X S T A",
			query: MessagePageQuery{From: 3, Before: -1},
			want:  MessagePage{Offset: 1, End: 5, Total: 5, TurnsBefore: 1, UserRowsBefore: 1},
		},
		{
			name:  "an answer after a cut text and a prompt is not a continuation",
			spec:  "U X U A",
			query: MessagePageQuery{From: 3, Before: -1},
			want:  MessagePage{Offset: 3, End: 4, Total: 4, TurnsBefore: 2, UserRowsBefore: 2},
		},
		{
			name:  "a compaction summary does not count as a prompt to start a page at",
			spec:  "U S T S T C S T A",
			query: MessagePageQuery{From: -1, Before: -1, Limit: 4},
			want:  MessagePage{Offset: 5, End: 9, Total: 9, TurnsBefore: 1, UserRowsBefore: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PageMessages(pageHistory(t, tc.spec), tc.query)
			if got != tc.want {
				t.Fatalf("PageMessages(%q, %+v) = %+v, want %+v", tc.spec, tc.query, got, tc.want)
			}
		})
	}
}

// Walking a long history page by page from its end covers every message once:
// each older page ends exactly where the window it joins starts.
func TestPageMessagesJoinWithoutGapOrOverlap(t *testing.T) {
	msgs := pageHistory(t, "U S T S T A U A U S T T S T T S T A W S T A U X X A U S T S T X E S T S T S T S T X A C A U A")
	for _, limit := range []int{1, 2, 3, 5, 8, 13} {
		page := PageMessages(msgs, MessagePageQuery{From: -1, Before: -1, Limit: limit})
		if page.End != len(msgs) {
			t.Fatalf("limit %d: tail page ends at %d, want %d", limit, page.End, len(msgs))
		}
		for steps := 0; page.Offset > 0; steps++ {
			if steps > len(msgs) {
				t.Fatalf("limit %d: paging never reached the first message", limit)
			}
			if msgs[page.Offset].Role == llm.RoleTool {
				t.Fatalf("limit %d: page at %d opens with a tool result", limit, page.Offset)
			}
			if continuesTruncated(msgs, page.Offset) {
				t.Fatalf("limit %d: page at %d opens in the middle of a continued answer", limit, page.Offset)
			}
			older := PageMessages(msgs, MessagePageQuery{From: -1, Before: page.Offset, Limit: limit})
			if older.End != page.Offset || older.Offset >= older.End {
				t.Fatalf("limit %d: older page [%d,%d) does not join [%d,%d)", limit, older.Offset, older.End, page.Offset, page.End)
			}
			page = older
		}
	}
}

// A session saved before only the agent's own settings changes were noted
// holds a notice of every change, with no source on it. A transcript shows the
// ones the agent could have made and nothing else of the kind; every other
// row, and a notice that carries its source, is shown as it is.
func TestVisibleUILogHidesTheOperatorsSettingsNotices(t *testing.T) {
	// Turn 1 ran without a switch; in turn 2 the model called switch_model.
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "review this"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "switch to fake/b"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "sw", Name: "switch_model", InputJSON: `{"model":"fake/b"}`}}},
		{Role: llm.RoleTool, ToolCallID: "sw", Content: "Switched for the rest of the session: model fake/b, reasoning none offered."},
		{Role: llm.RoleAssistant, Content: "now on b"},
	}
	notice := func(turn int, message string) UILogEntry {
		return UILogEntry{Level: UILogLevelNotice, UserTurnIndex: turn, Message: message}
	}
	cases := []struct {
		name  string
		entry UILogEntry
		shown bool
	}{
		{"the model a session was started with", notice(1, "Model: codex/gpt-5.6-sol for this session"), false},
		{"the mode a session was started with", notice(1, "Mode: agent for this session"), false},
		{"a permission mode", notice(2, "Permission mode: bypass for this session"), false},
		{"a model for a number of turns", notice(2, "Model: stub/qwen3.8-27b for the next 2 turns"), false},
		{"a reasoning level for the next turn", notice(1, "Reasoning: high for the next turn"), false},
		{"a model and a mode together", notice(2, "Model: fake/b for this session; Mode: plan for this session"), false},
		{"a model for the session in a turn without a switch", notice(1, "Model: fake/b for this session"), false},
		{"the model the agent switched to for the session", notice(2, "Model: fake/b for this session"), true},
		{"a model and a level the agent switched to", notice(2, "Model: fake/b for this session; Reasoning: off for this session"), true},
		{"a change for the rest of a turn", notice(1, "Model: fake/b for the rest of this turn"), true},
		{"a notice with its source", UILogEntry{Level: UILogLevelNotice, UserTurnIndex: 1, Message: "Model: fake/b for this session", Source: SettingsSourceModel}, true},
		{"an error", UILogEntry{Level: UILogLevelError, UserTurnIndex: 1, Message: "Model: fake/b for this session"}, true},
		{"a hook's message", notice(1, "Hook (Stop): Mode: agent for this session"), true},
		{"a stop notice", notice(1, "Stopped after 40 steps, the step limit set by agent.max_turns."), true},
		{"another notice that names a model", notice(1, "Model: fake/b did not answer"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := VisibleUILog(msgs, []UILogEntry{tc.entry})
			if shown := len(got) == 1; shown != tc.shown {
				t.Fatalf("%q shown = %v, want %v", tc.entry.Message, shown, tc.shown)
			}
		})
	}
	// Order and the rows between are kept.
	log := []UILogEntry{
		{ID: "flag", Level: UILogLevelNotice, UserTurnIndex: 1, Message: "Mode: agent for this session"},
		{ID: "err", Level: UILogLevelError, UserTurnIndex: 1, Message: "Request failed"},
		{ID: "switch", Level: UILogLevelNotice, UserTurnIndex: 2, Message: "Model: fake/b for this session", Source: SettingsSourceModel},
	}
	var ids []string
	for _, e := range VisibleUILog(msgs, log) {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "err,switch" {
		t.Fatalf("visible rows %v, want err,switch", ids)
	}
}

func TestMessagePageUILog(t *testing.T) {
	msgs := pageHistory(t, "U A U A U A")
	log := []UILogEntry{
		{ID: "legacy", UserTurnIndex: 0},
		{ID: "t1", UserTurnIndex: 1},
		{ID: "t2", UserTurnIndex: 2},
		{ID: "t3", UserTurnIndex: 3},
	}
	ids := func(rows []UILogEntry) string {
		out := ""
		for _, r := range rows {
			out += r.ID + " "
		}
		return out
	}
	cases := []struct {
		name string
		page MessagePage
		want string
	}{
		{"the whole history keeps every notice", MessagePage{Offset: 0, End: 6, Total: 6}, "legacy t1 t2 t3 "},
		{"a page opening with a prompt shows the notice that ended the turn before it", MessagePage{Offset: 2, End: 6, Total: 6, TurnsBefore: 1, UserRowsBefore: 1}, "legacy t1 t2 t3 "},
		{"an older page leaves the notice on its end to the page after it", MessagePage{Offset: 0, End: 2, Total: 6}, ""},
		{"a page opening mid-turn keeps the notice ending that turn", MessagePage{Offset: 3, End: 6, Total: 6, TurnsBefore: 2, UserRowsBefore: 2}, "t2 t3 "},
		{"a page in the middle keeps only its own notices", MessagePage{Offset: 2, End: 4, Total: 6, TurnsBefore: 1, UserRowsBefore: 1}, "legacy t1 "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(tc.page.UILog(msgs, log)); got != tc.want {
				t.Fatalf("UILog = %q, want %q", got, tc.want)
			}
		})
	}
	// A turn that failed before anything was kept still shows its error.
	empty := PageMessages(nil, unsetPage())
	if got := ids(empty.UILog(nil, []UILogEntry{{ID: "t1", UserTurnIndex: 1}})); got != "t1 " {
		t.Fatalf("empty history UILog = %q", got)
	}
}
