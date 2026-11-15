package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// ACP system prompt modes Coddy accepts. Append is the only one: a prompt that
// replaced the agent's own instructions would take away the tool guidance, the
// rules and the safety text the agent runs on, and the specification lets an
// agent decline override by not advertising it.
const acpSystemPromptAppend = "append"

// validateACPSystemPrompt reads the system prompt a session lifecycle request
// carries and returns the text to keep.
//
// A request that carries none answers "", which means "keep what the session
// has": the specification lets an agent hold a prompt it stored with the
// session, and a client that reopens a session without resending the block
// keeps the instructions it set. Everything the request does carry is checked
// before the session is created, loaded or resumed, and every refusal is
// invalid params - the shape of the request is what the client got wrong.
func validateACPSystemPrompt(prompt *acp.SystemPrompt) (string, error) {
	if prompt == nil {
		return "", nil
	}
	mode := strings.TrimSpace(prompt.Mode)
	if mode != acpSystemPromptAppend {
		return "", acp.InvalidParams("systemPrompt mode %q is not supported (append)", mode)
	}
	parts := make([]string, 0, len(prompt.Content))
	for _, block := range prompt.Content {
		if block.Type != acp.ContentTypeText {
			return "", acp.InvalidParams("systemPrompt accepts text blocks only, got %q", block.Type)
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", acp.InvalidParams("systemPrompt carries no text")
	}
	return strings.Join(parts, "\n\n"), nil
}

// applyACPSystemPrompt stores text on the session. An empty text leaves the
// session's block alone, which is what a request that carried no systemPrompt
// asks for; a text equal to the stored one writes nothing.
func applyACPSystemPrompt(st *State, text string) {
	if st == nil || text == "" || text == st.GetACPSystemPrompt() {
		return
	}
	st.SetACPSystemPrompt(text)
}
