package session_test

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// An assistant message the output limit cut off, and the message that goes on
// from it, are stored as they streamed at the join: the whitespace there is part
// of the text, and trimming it ("and then" + "the second half") would run the
// words together wherever the two are joined. Every other assistant message keeps
// being stored without the whitespace around it, an answer a provider failure cut
// off included: it is not marked truncated, since the step run again may start it
// over instead of going on from its last character.

func assistantContents(st *session.State) []string {
	var out []string
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleAssistant {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestAddMessageStoresTheJoinOfATruncatedMessageAsStreamed(t *testing.T) {
	assistant := func(content string, truncated bool) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, Content: content, Truncated: truncated}
	}
	for _, tc := range []struct {
		name string
		in   []llm.Message
		want []string
	}{
		{
			name: "the end of the cut text and the start of the continuation",
			in:   []llm.Message{assistant("  The module and then ", true), assistant(" the second half.\n", false)},
			want: []string{"The module and then ", " the second half."},
		},
		{
			name: "a chain",
			in:   []llm.Message{assistant("a ", true), assistant(" b ", true), assistant(" c\n", false)},
			want: []string{"a ", " b ", " c"},
		},
		{
			name: "a message after one that was not cut",
			in:   []llm.Message{assistant("  a  ", false), assistant("  b  ", false)},
			want: []string{"a", "b"},
		},
		{
			name: "an answer a provider failure cut off and the step run again after it",
			in:   []llm.Message{assistant("The module starts with ", false), assistant(" an import block.", false)},
			want: []string{"The module starts with", "an import block."},
		},
		{
			name: "a placement marker is still taken out",
			in:   []llm.Message{assistant("a <coddy_file id=\"x\"/>", true), assistant(" b<coddy_file id=\"y\"/>", false)},
			want: []string{"a ", " b"},
		},
		{
			name: "an assistant message after a user message is trimmed at its start",
			in:   []llm.Message{assistant("a ", true), {Role: llm.RoleUser, Content: "go"}, assistant("  b", false)},
			want: []string{"a ", "b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &session.State{ID: "sess_truncated"}
			for _, m := range tc.in {
				st.AddMessage(m)
			}
			got := assistantContents(st)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("stored %q, want %q", got, tc.want)
			}
		})
	}
}
