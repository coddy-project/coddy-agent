package agent

import (
	"strings"
)

// clientSystemBlock is the system-level instruction block the client that
// opened the session asked for (ACP systemPrompt, append mode).
//
// It is the editor's own instructions - a role for a review session, house
// rules the editor owns - and the specification puts them at system level, so
// they ride in the system prompt rather than in a user turn. Append is the only
// mode Coddy accepts, so the block follows the agent's own instructions and the
// template: it never takes away the tool guidance, the rules or the safety text
// the agent runs on.
//
// The block is read from the session on every turn instead of being folded into
// the history once, so a compaction cannot lose it and a client that replaces it
// between turns is followed from the next one.
func (a *Agent) clientSystemBlock() string {
	st := sessionStatePtr(a.state)
	if st == nil {
		return ""
	}
	text := strings.TrimSpace(st.GetACPSystemPrompt())
	if text == "" {
		return ""
	}
	return "## Client system instructions\n\n" +
		"The client that opened this session asked for these instructions. They are system-level and apply to every turn.\n\n" +
		text
}
