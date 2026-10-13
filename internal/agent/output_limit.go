package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// What the loop does with a step the model's output limit (finish_reason
// "length", stop reason "max_tokens") ended before the model was done. The
// bound, the budget and the step-cap rule are those of maxOutputLimitRecoveries
// (react.go); this file holds the three kinds of cut and the words each one is
// answered with:
//
//   - nothing the agent can act on (no tool call, no visible text): outputLimitNudge;
//   - visible text that stops partway: outputLimitContinueNudge, the text stays;
//   - a last tool call whose arguments stop partway: cutOffToolCallNudge, the
//     call is dropped and never run.

// outputLimitContinueNudge is injected into the LLM-facing message slice only
// (never persisted to the transcript) after a step the output limit stopped in
// the middle of visible text. The text stays in the transcript and in the
// history, the user having read it, and the model is asked to go on from its
// last word.
const outputLimitContinueNudge = "Your previous reply hit the output limit (max_tokens) and stopped partway through. Continue exactly where it stopped, without repeating what you already wrote, and keep this step short: split a large file write or edit into several smaller tool calls."

// cutOffToolCallResult is what the row of a tool call the output limit cut off
// says, and what the call's record in the tool-call store holds. The call is
// not part of the conversation, so this text never reaches the model.
const cutOffToolCallResult = "not run: the call was cut off at the model's output limit (max_tokens) before its arguments were complete"

// cutOffToolCallNudge is injected after a step whose last tool call the output
// limit cut off. The call is not in the history (a provider that parses the
// arguments of the calls it is sent would refuse half a JSON document, and it
// was never run), so the message names the tool: it is all the model has left
// of the call. The name comes from the provider, and a user message is an
// instruction channel, so a name that is not a plain identifier is left out.
func cutOffToolCallNudge(name string) string {
	call := "its last tool call"
	if plainToolName(name) {
		call += " (" + name + ")"
	}
	return fmt.Sprintf("Your previous step hit the output limit (max_tokens) in the middle of %s, so that call was cut off and was not run. Make it again in several smaller tool calls (write or edit a large file in parts) and keep your reasoning short.", call)
}

// plainToolName reports whether name looks like the identifier of a tool:
// letters, digits and the separators tool names use, at most 64 bytes.
func plainToolName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-', r == '.', r == ':':
		default:
			return false
		}
	}
	return true
}

// cutOffToolCall reports the last tool call of a response when the output
// limit cut it off: the step stopped at max_tokens and the call has arguments
// that are not a whole JSON document. Generation stops where it stops, so only
// the last call can be cut; an earlier call that does not parse is a model
// error, not a truncation, and runs (and fails) as it always did. Arguments that
// parse are whole, whatever stopped the step. Empty arguments say nothing about
// where the step stopped: the Anthropic adapter hands over a call that takes no
// arguments as an empty string, and an OpenAI-compatible server may do the same,
// so such a call is taken as written whole and runs. One the limit cut right
// after its name runs with no arguments, as every call at the limit did before,
// and the tool answers with what is missing.
func cutOffToolCall(response *llm.Response) (llm.ToolCall, bool) {
	if response == nil || response.StopReason != "max_tokens" || len(response.ToolCalls) == 0 {
		return llm.ToolCall{}, false
	}
	last := response.ToolCalls[len(response.ToolCalls)-1]
	if !argumentsStopShort(last.InputJSON) {
		return llm.ToolCall{}, false
	}
	return last, true
}

// argumentsStopShort says whether a tool call has arguments and they are not a
// whole JSON document.
func argumentsStopShort(args string) bool {
	args = strings.TrimSpace(args)
	return args != "" && !json.Valid([]byte(args))
}

// outputLimitStepNudge picks the words a cut-off step is answered with. A cut
// tool call comes first: the text before it, if any, was complete, and what
// the model has to do again is the call.
func outputLimitStepNudge(content string, cutCall *llm.ToolCall) string {
	switch {
	case cutCall != nil:
		return cutOffToolCallNudge(cutCall.Name)
	case strings.TrimSpace(content) == "":
		return outputLimitNudge
	default:
		return outputLimitContinueNudge
	}
}

// dropCutOffToolCall closes the row of a tool call the output limit cut off.
// The stream announced the call while its arguments were still coming in, so
// clients show a pending row for it, and the tool-call store holds a pending
// record; neither may stay pending for a call that will never run. The call
// itself is left out of the assistant message the loop stores, so it never
// takes part in the tool-call pairing of the transcript.
func (a *Agent) dropCutOffToolCall(tc llm.ToolCall) {
	sessionDir := ""
	if st := sessionStatePtr(a.state); st != nil {
		sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
	}
	a.finishToolCall(sessionDir, a.state.GetID(), tc, cutOffToolCallResult, nil, "failed")
}

// stopsPartway says whether a step's text stops partway because the output limit
// cut it off: the step stopped at max_tokens with text and no tool call, cut or
// whole, to go with it. The step is then stored as Truncated, and what goes on
// from it joins it with nothing between.
func stopsPartway(response *llm.Response, cutCall *llm.ToolCall) bool {
	return response.StopReason == "max_tokens" && cutCall == nil && len(response.ToolCalls) == 0 &&
		strings.TrimSpace(response.Content) != ""
}

// foldSplitAnswers joins the assistant messages of a history that follow each
// other. Three come out of the loop's recoveries: a text the output limit cut
// off and the step that went on from it, the text before a tool call the output
// limit cut off with the step that answered the message about it, and an answer
// a provider failure cut off with the step run again after it. The requests of
// the turn have a local message between them; a history built from the
// transcript for a later turn has none, and chat templates that insist on
// alternation refuse two assistant messages in a row for the rest of the
// session. A message goes on from a truncated one with nothing between,
// whitespace at the join as it streamed; otherwise the two texts are separated
// by a blank line, which also keeps an answer a provider failure cut off apart
// from the step that started it over. The result carries the later message's
// calls and reasoning. A step with signed reasoning keeps its message, since a
// provider that signs replays it as it is, and so does a step with a tool call
// before what follows it.
func foldSplitAnswers(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if n := len(out); n > 0 && foldsInto(out[n-1], m) {
			out[n-1] = joinAnswers(out[n-1], m)
			continue
		}
		out = append(out, m)
	}
	return out
}

func foldsInto(prev, next llm.Message) bool {
	return prev.Role == llm.RoleAssistant && next.Role == llm.RoleAssistant &&
		len(prev.ToolCalls) == 0 &&
		prev.ReasoningSignature == "" && next.ReasoningSignature == "" &&
		prev.PlanDocument == nil && next.PlanDocument == nil &&
		len(prev.ImageParts) == 0 && len(next.ImageParts) == 0
}

func joinAnswers(prev, next llm.Message) llm.Message {
	joined := next
	switch {
	case prev.Truncated:
		joined.Content = prev.Content + next.Content
	case strings.TrimSpace(next.Content) == "":
		joined.Content = prev.Content
	case strings.TrimSpace(prev.Content) == "":
		joined.Content = next.Content
	default:
		joined.Content = prev.Content + "\n\n" + next.Content
	}
	return joined
}
