package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestArtifactMarkersSkipsUnsafeIDs(t *testing.T) {
	markers := ArtifactMarkers([]llm.Artifact{
		{ID: "safe_123-abc"},
		{ID: `unsafe"/>`},
	})
	if markers != `<coddy_file id="safe_123-abc"/>` {
		t.Fatalf("markers = %q", markers)
	}
}

// A text the output limit cut off and the message that goes on from it are one
// text, joined with nothing between. The placement markers of the files shared
// before them would split it if they closed the first part, in the middle of a
// word as often as not, so a truncated message carries them in front of its
// text, and a part that goes on from another leaves them to the message after it.

var (
	markerA = llm.Artifact{ID: "artifact-a", Name: "a.txt"}
	markerB = llm.Artifact{ID: "artifact-b", Name: "b.txt"}
)

func sharedBy(id string, a llm.Artifact) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Artifacts: []llm.Artifact{a}}
}

// TestTruncatedMessageLeavesTheMarkersToTheMessageThatCompletesIt: a truncated
// text and its continuation are one text, joined with nothing between, so the
// truncated part takes no placement markers - they would stand inside the text -
// and the message that completes the answer closes with them as any message
// does. The whitespace at the join is kept on both sides.
func TestTruncatedMessageLeavesTheMarkersToTheMessageThatCompletesIt(t *testing.T) {
	for _, tc := range []struct {
		name, head, rest, joined string
	}{
		{"cut in the middle of a word", "The cell is a butt", "on labelled Save.", "The cell is a button labelled Save."},
		// The whitespace at the end of the cut text is part of the text it is joined with.
		{"cut after a space", "The cell is a button ", "labelled Save.", "The cell is a button labelled Save."},
		{"cut after a blank line", "Press it.\n\n", "Then Save.", "Press it.\n\nThen Save."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &State{}
			st.AddMessage(sharedBy("one", markerA))
			st.AddMessage(sharedBy("two", markerB))
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: tc.head, Truncated: true})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: tc.rest})
			msgs := st.GetMessages()

			head, rest := msgs[2], msgs[3]
			if head.Content != tc.head || len(head.Artifacts) != 0 {
				t.Fatalf("the cut text is stored as %+v, want its text alone with its trailing whitespace kept: %q", head, tc.head)
			}
			markers := ArtifactMarkers([]llm.Artifact{markerA, markerB})
			if want := tc.rest + "\n\n" + markers; rest.Content != want {
				t.Fatalf("the continuation is stored as %q, want it closed with the markers: %q", rest.Content, want)
			}
			if len(rest.Artifacts) != 2 || rest.Artifacts[0].ID != markerA.ID || rest.Artifacts[1].ID != markerB.ID {
				t.Fatalf("the continuation carries artifacts %+v, want both files", rest.Artifacts)
			}
			if got := StripAssistantContent(head.Content, false, true) + StripAssistantContent(rest.Content, true, false); got != tc.joined {
				t.Fatalf("joined text = %q, want %q", got, tc.joined)
			}
		})
	}
}

// TestATruncatedMessageThatGoesOnFromAnotherPlacesNothing: files still waiting for
// a place when a truncated message goes on from another truncated one stay
// pending for the message after it, which closes with them as any message does,
// the whitespace it started with kept.
func TestATruncatedMessageThatGoesOnFromAnotherPlacesNothing(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		sharedBy("one", markerA),
		{Role: llm.RoleAssistant, Content: "head ", Truncated: true},
	}
	middle := llm.Message{Role: llm.RoleAssistant, Content: " middle ", Truncated: true}
	placePendingArtifacts(msgs, &middle)
	if middle.Content != " middle " || len(middle.Artifacts) != 0 {
		t.Fatalf("the middle part is %+v, want its text alone: nothing is placed on a part that goes on from another", middle)
	}

	msgs = append(msgs, middle)
	last := llm.Message{Role: llm.RoleAssistant, Content: " end.\n"}
	placePendingArtifacts(msgs, &last)
	if want := " end.\n\n" + ArtifactMarkers([]llm.Artifact{markerA}); last.Content != want {
		t.Fatalf("the last part is %q, want it closed with the markers and the whitespace it started with kept: %q", last.Content, want)
	}
	if len(last.Artifacts) != 1 || last.Artifacts[0].ID != markerA.ID {
		t.Fatalf("the last part carries artifacts %+v, want the file", last.Artifacts)
	}
}

func TestFollowsTruncated(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := llm.Message{Role: llm.RoleAssistant, Content: "the butt", Truncated: true}
	whole := llm.Message{Role: llm.RoleAssistant, Content: "Done."}
	empty := llm.Message{Role: llm.RoleAssistant, Reasoning: "Hmm."}
	withCall := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read"}}}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "ok"}
	for _, tc := range []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"nothing before", nil, false},
		{"a truncated message", []llm.Message{user, cut}, true},
		{"a message that was not cut off", []llm.Message{user, whole}, false},
		{"a user message after a truncated one", []llm.Message{cut, user}, false},
		{"a tool result after a truncated one", []llm.Message{cut, tool}, false},
		{"a step that came back empty is passed over", []llm.Message{user, cut, empty}, true},
		{"several empty steps are passed over", []llm.Message{user, cut, empty, empty}, true},
		{"an empty step in front of a whole message", []llm.Message{user, whole, empty}, false},
		{"a step with a call is not nothing", []llm.Message{cut, withCall, empty}, false},
		{"only empty steps", []llm.Message{empty, empty}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FollowsTruncated(tc.msgs); got != tc.want {
				t.Fatalf("FollowsTruncated = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestContinuedAnswer: the answer a turn hands over - to a parent as a report, to
// a non-streaming client as the reply, to a Stop hook - is the whole of what an
// answer the output limit cut off said, not the part after the last cut. The
// parts are joined as stored, with nothing between; only truncated messages are
// parts of it.
func TestContinuedAnswer(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	empty := llm.Message{Role: llm.RoleAssistant, Reasoning: "Hmm."}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read"}}}
	cutWithCall := llm.Message{Role: llm.RoleAssistant, Content: "reading ", Truncated: true, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read"}}}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "ok"}
	for _, tc := range []struct {
		name string
		msgs []llm.Message
		at   int
		want string
	}{
		{"a message that goes on from nothing", []llm.Message{user, text("Done.")}, 1, "Done."},
		{"the first message", []llm.Message{text("Done.")}, 0, "Done."},
		{"a cut text is only itself when nothing goes on from it", []llm.Message{user, cut("and then ")}, 1, "and then "},
		{"a text and its continuation", []llm.Message{user, cut("Press the butt"), text("on labelled Save.")}, 2, "Press the button labelled Save."},
		{"whitespace at the join is kept as stored", []llm.Message{user, cut("one "), text(" two")}, 2, "one  two"},
		{"a chain of three", []llm.Message{user, cut("a "), cut("b "), text("c")}, 3, "a b c"},
		{"an empty step between two parts is passed over", []llm.Message{user, cut("a "), empty, text("b")}, 3, "a b"},
		{"several empty steps between", []llm.Message{user, cut("a "), empty, empty, cut("b "), empty, text("c")}, 6, "a b c"},
		{"the placement markers stay as stored", []llm.Message{user, cut("<coddy_file id=\"x\"/>\n\nPress the butt"), text("on labelled Save.")}, 2, "<coddy_file id=\"x\"/>\n\nPress the button labelled Save."},
		{"a message that was not cut off ends the chain", []llm.Message{user, text("first"), text("second")}, 2, "second"},
		{"the chain stops at a message that was not cut off", []llm.Message{user, cut("a "), text("b"), text("c")}, 3, "c"},
		// An answer a provider failure cut off is not marked truncated: the step
		// run again starts the answer over as often as it goes on from it.
		{"an answer a provider failure cut off is not a part", []llm.Message{user, text("The module starts with"), text("The module starts with an import block.")}, 2, "The module starts with an import block."},
		{"a kept answer between ends the chain", []llm.Message{user, cut("a "), text("kept"), text("rest")}, 3, "rest"},
		{"a user message ends the chain", []llm.Message{cut("a "), user, text("b")}, 2, "b"},
		{"a tool result ends the chain", []llm.Message{cut("a "), tool, text("b")}, 2, "b"},
		{"a step with a call ends the chain", []llm.Message{cut("a "), call, text("b")}, 2, "b"},
		{"a truncated message that issued a call ends the chain", []llm.Message{cutWithCall, text("b")}, 1, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContinuedAnswer(tc.msgs, tc.at); got != tc.want {
				t.Fatalf("ContinuedAnswer(msgs, %d) = %q, want %q", tc.at, got, tc.want)
			}
		})
	}
}

// TestStripAssistantContent pins what is kept at the join of a truncated message
// and the one that goes on from it, and what is not.
func TestStripAssistantContent(t *testing.T) {
	marker := `<coddy_file id="x"/>`
	for _, tc := range []struct {
		name               string
		in                 string
		keepStart, keepEnd bool
		want               string
	}{
		{"trimmed on both sides", "  a b \n", false, false, "a b"},
		{"the start kept", "  a b \n", true, false, "  a b"},
		{"the end kept", "  a b \n", false, true, "a b \n"},
		{"both kept", "  a b \n", true, true, "  a b \n"},
		{"markers taken out in front", marker + "\n\n" + "a b ", false, true, "a b "},
		{"markers taken out behind", " a b\n\n" + marker, true, false, " a b"},
		{"whitespace only is nothing", " \n\t", true, true, ""},
		{"markers only is nothing", marker, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripAssistantContent(tc.in, tc.keepStart, tc.keepEnd); got != tc.want {
				t.Fatalf("StripAssistantContent(%q, %v, %v) = %q, want %q", tc.in, tc.keepStart, tc.keepEnd, got, tc.want)
			}
		})
	}
}
