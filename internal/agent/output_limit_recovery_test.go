package agent

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The recoveries in progress when a request is not simply the one after the
// last: the output-limit recoveries (output_limit_test.go and the files next to
// it) and the empty-answer ones leave messages that reach the model only and
// steps the request leaves out, and two things send a request that has to keep
// them. A provider failure runs the step again as the request that failed
// (issue #520), with the answer it kept and the request to go on from it at the
// end; a compaction, or the refusal of a request larger than the model's
// window, rebuilds the history from the transcript, which holds none of those
// messages, and puts back the ones that still stand at the end of the working
// history (restoreRecoveries). This file holds both, and what the transcript and
// the next turn make of an answer a provider failure kept.

var errConnectionReset = errors.New("read: connection reset by peer")

// recoveringAgent is outputLimitAgent with the pause before a provider recovery
// cut to a few milliseconds.
func recoveringAgent(t *testing.T, p llm.Provider) (*Agent, *session.State) {
	t.Helper()
	return outputLimitAgent(t, p, func(c *config.Config) { c.Agent.LLMRetryBaseMS = 1 })
}

// signedEmptyStep is an answer with signed thinking and nothing else, the kind
// of empty step that stays in the transcript and in a history rebuilt from it.
var signedEmptyStep = outputLimitStep{reasoning: "Hmm.", signature: "sig", stop: "end_turn"}

// unsignedEmptyStep is the same with unsigned thinking.
var unsignedEmptyStep = outputLimitStep{reasoning: "Hmm.", stop: "end_turn"}

// localMessages are the messages the loop puts at the end of a request and
// never stores: the recoveries', the loop guard's and the provider recovery's.
var localMessages = map[string]bool{
	outputLimitNudge:                true,
	outputLimitContinueNudge:        true,
	emptyAssistantContinuationNudge: true,
	providerRecoveryNudge:           true,
	streamLoopNudge:                 true,
	reasoningLoopNudge:              true,
}

// standingAtTheEnd is what a request ends with after its last stored message:
// the local messages that stand there, in order.
func standingAtTheEnd(msgs []llm.Message) []string {
	hist := requestHistory(msgs)
	i := len(hist)
	for i > 0 && strings.HasPrefix(hist[i-1], "user: ") && localMessages[strings.TrimPrefix(hist[i-1], "user: ")] {
		i--
	}
	return hist[i:]
}

// lastIsAssistant reports whether a request ends on an assistant message, below
// the turn context block: what a provider reads as a prefill.
func lastIsAssistant(msgs []llm.Message) bool {
	hist := requestHistory(msgs)
	return len(hist) > 0 && strings.HasPrefix(hist[len(hist)-1], "assistant: ")
}

// TestPromptCacheProviderRecoveryRunsTheFailedRequestAgain: after a provider
// failure the step runs again as the request that failed, byte for byte (issue
// #520), not as a history rebuilt from the transcript, which holds the steps
// that came back empty and none of the local-only messages. Whatever the
// recoveries before the failure did to the working history - texts the output
// limit cut off, the messages that asked for a shorter step or for the rest,
// empty steps left out, signed or not - the request run again carries exactly
// what the failed one carried, in the same order. When the failed call had
// streamed part of an answer, that part follows, as stored (the whitespace it
// starts with kept where it goes on from a text the output limit cut off), and
// the request to go on from it is last.
func TestPromptCacheProviderRecoveryRunsTheFailedRequestAgain(t *testing.T) {
	silent := outputLimitStep{err: errConnectionReset}
	failsAfter := func(text string) outputLimitStep { return outputLimitStep{text: text, err: errConnectionReset} }
	// cutWithSpace is a text the output limit cut off right after a space, which
	// is part of the text.
	cutWithSpace := outputLimitStep{text: partialText + " ", stop: "max_tokens"}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the last one fails at the provider
		// kept is the answer the failed call streamed, as the request run again
		// carries it; empty when the call streamed none.
		kept string
		// nudges are the local messages the request run again carries, by text,
		// with how many times.
		nudges map[string]int
	}{
		{"thinking cut off", []outputLimitStep{cutOff, silent}, "", map[string]int{outputLimitNudge: 1}},
		// Signed thinking is what Anthropic replays; the step is left out all the same.
		{"signed thinking cut off", []outputLimitStep{{reasoning: "Thinking.", signature: "sig", stop: "max_tokens"}, silent}, "", map[string]int{outputLimitNudge: 1}},
		{"thinking cut off twice", []outputLimitStep{cutOff, cutOff, silent}, "", map[string]int{outputLimitNudge: 2}},
		{"an empty answer", []outputLimitStep{signedEmptyStep, silent}, "", map[string]int{}},
		{"two empty answers", []outputLimitStep{signedEmptyStep, signedEmptyStep, silent}, "", map[string]int{emptyAssistantContinuationNudge: 1}},
		{"thinking cut off, then two empty answers", []outputLimitStep{cutOff, unsignedEmptyStep, unsignedEmptyStep, silent}, "", map[string]int{outputLimitNudge: 1, emptyAssistantContinuationNudge: 1}},
		// The text the user read stays; the message that asks the model to go on
		// follows it.
		{"a text cut off", []outputLimitStep{partialStep, silent}, "", map[string]int{outputLimitContinueNudge: 1}},
		{"a text cut off twice", []outputLimitStep{partialStep, partialStep, silent}, "", map[string]int{outputLimitContinueNudge: 2}},
		{"thinking, then a text", []outputLimitStep{cutOff, partialStep, silent}, "", map[string]int{outputLimitNudge: 1, outputLimitContinueNudge: 1}},
		{"a text, then thinking", []outputLimitStep{partialStep, cutOff, silent}, "", map[string]int{outputLimitContinueNudge: 1, outputLimitNudge: 1}},
		{"two empty answers, then a text", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, silent}, "", map[string]int{emptyAssistantContinuationNudge: 1, outputLimitContinueNudge: 1}},
		{"two unsigned empty answers, then a text", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep, silent}, "", map[string]int{emptyAssistantContinuationNudge: 1, outputLimitContinueNudge: 1}},
		{"an empty answer, then a text, then thinking", []outputLimitStep{signedEmptyStep, partialStep, cutOff, silent}, "", map[string]int{outputLimitContinueNudge: 1, outputLimitNudge: 1}},
		{"two empty answers, then thinking cut off", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff, silent}, "", map[string]int{emptyAssistantContinuationNudge: 1, outputLimitNudge: 1}},
		{"an answer the failure kept", []outputLimitStep{failsAfter("The notes hold ")}, "The notes hold", map[string]int{}},
		// The message the step earned stays where it was sent, before the answer
		// it led to.
		{"thinking cut off, then an answer the failure kept", []outputLimitStep{cutOff, failsAfter("The module starts with")}, "The module starts with", map[string]int{outputLimitNudge: 1}},
		// The kept part goes on from the cut text: it is not folded into that text
		// within the turn, and the message that asked for it does not move in front
		// of the text.
		{"a text cut off, then a failure in its continuation", []outputLimitStep{cutWithSpace, failsAfter(" abc")}, " abc", map[string]int{outputLimitContinueNudge: 1}},
		{"two empty answers, a text, then a failure in its continuation", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, failsAfter(" in")}, " in", map[string]int{emptyAssistantContinuationNudge: 1, outputLimitContinueNudge: 1}},
		{"two unsigned empty answers, a text, then a failure in its continuation", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep, failsAfter(" in")}, " in", map[string]int{emptyAssistantContinuationNudge: 1, outputLimitContinueNudge: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(append([]outputLimitStep(nil), tc.steps...), answered)
			p := &outputLimitProvider{steps: steps}
			ag, st := recoveringAgent(t, p)
			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(steps))
			}
			failed, rerun := withoutTurnContext(p.seen[len(p.seen)-2]), withoutTurnContext(p.seen[len(p.seen)-1])
			want := failed
			if tc.kept != "" {
				if n := len(failed); len(rerun) != n+2 || rerun[n].Role != llm.RoleAssistant || rerun[n].Content != tc.kept {
					t.Fatalf("the request run again does not carry the kept answer %q after the failed request:\n%s", tc.kept, tailLines(strings.Join(requestHistory(rerun), "\n"), 4))
				}
				want = append(append([]llm.Message(nil), failed...), rerun[len(failed)], llm.Message{Role: llm.RoleUser, Content: providerRecoveryNudge})
			}
			if !reflect.DeepEqual(rerun, want) {
				t.Fatalf("the request run again is not the one that failed:\nfailed: %s\nagain:  %s",
					strings.Join(requestHistory(failed), "\n"), strings.Join(requestHistory(rerun), "\n"))
			}
			if len(tc.nudges) > 0 && len(standingAtTheEnd(failed)) == 0 {
				t.Fatalf("the failed request ends with no local message; the chain does not exercise a recovery")
			}
			if hasEmptyAssistant(rerun) {
				t.Fatalf("the request run again carries a step that came back empty:\n%s", strings.Join(requestHistory(rerun), "\n"))
			}
			for text, want := range tc.nudges {
				if got := countNudges(rerun, text); got != want {
					t.Fatalf("the request run again carries %q %d times, want %d", text, got, want)
				}
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a stop notice: %q", notice)
			}
		})
	}
}

// TestKeptAnswerOf: the answer a provider failure kept goes after the messages
// that stood at the end of the request that failed, as a request carries an
// assistant message: the markers of shared files taken out, the whitespace it
// starts with kept only where it goes on from a text the output limit cut off.
func TestKeptAnswerOf(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := llm.Message{Role: llm.RoleAssistant, Content: "Press the butt", Truncated: true}
	empty := llm.Message{Role: llm.RoleAssistant}
	reply := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	for _, tc := range []struct {
		name       string
		stored     []llm.Message
		wantAnswer string
	}{
		{"markers are taken out", []llm.Message{user, reply("Here it is.\n\n<coddy_file id=\"a\"/>")}, "Here it is."},
		{"whitespace at the start goes after a message that was not cut off", []llm.Message{user, reply("  Here it is. ")}, "Here it is."},
		{"whitespace at the start stays after a text the output limit cut off", []llm.Message{user, cut, reply(" on labelled Save. ")}, " on labelled Save."},
		{"an empty step between does not make it a new answer", []llm.Message{user, cut, empty, reply(" on")}, " on"},
		{"a user message between ends the join", []llm.Message{cut, user, reply(" on")}, "on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := keptAnswerOf(tc.stored)
			if answer.Role != llm.RoleAssistant || answer.Content != tc.wantAnswer {
				t.Fatalf("answer = %+v, want an assistant message holding %q", answer, tc.wantAnswer)
			}
		})
	}
}

// TestPromptCacheNextTurnAfterAKeptProviderAnswerSendsOneAssistantMessage: the
// answer a provider failure cut off and the step run again after it are one
// assistant message in the next turn's history. An answer of its own is not one
// text with the step: the stream ended wherever the failure hit it and the step
// run again may go on from the last character or start the answer over, so the
// answer is stored as any other (not truncated, trimmed), the history separates
// the two with a blank line, which reads right either way, and the report of the
// run is the answer the step run again wrote. A part of an answer the output
// limit cut off and the model was asked to continue is a part of that answer:
// the step run again is asked to go on from it, so it is stored truncated, as it
// streamed, and the next turn and the report read the whole answer, the cut
// text, the kept part and the rest, joined with nothing between.
func TestPromptCacheNextTurnAfterAKeptProviderAnswerSendsOneAssistantMessage(t *testing.T) {
	type part struct {
		content   string
		truncated bool
	}
	cutText := partialText + " " // the whitespace at the end is part of the text
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the turn; the second turn is answered "ok"
		// stored are the assistant messages of the transcript after the turn.
		stored []part
		// report is what the turn hands over as its answer.
		report string
		// next is the one assistant message of the next turn's history.
		next string
	}{
		{
			name:   "an answer of its own, the step goes on from the last character",
			steps:  []outputLimitStep{{text: "The module starts with ", err: errConnectionReset}, {text: "an import block.", stop: "end_turn"}},
			stored: []part{{"The module starts with", false}, {"an import block.", false}},
			report: "an import block.",
			next:   "The module starts with\n\nan import block.",
		},
		{
			name:   "an answer of its own, the step starts it over",
			steps:  []outputLimitStep{{text: "The module sta", err: errConnectionReset}, {text: "The module starts with an import block.", stop: "end_turn"}},
			stored: []part{{"The module sta", false}, {"The module starts with an import block.", false}},
			report: "The module starts with an import block.",
			next:   "The module sta\n\nThe module starts with an import block.",
		},
		{
			name: "a part of a continued answer",
			steps: []outputLimitStep{
				{text: "REPORT: the cell is a butt", stop: "max_tokens"},
				{text: "on lab", err: errConnectionReset},
				{text: "elled Save.", stop: "end_turn"},
			},
			stored: []part{{"REPORT: the cell is a butt", true}, {"on lab", true}, {"elled Save.", false}},
			report: "REPORT: the cell is a button labelled Save.",
			next:   "REPORT: the cell is a button labelled Save.",
		},
		{
			// The whitespace at the end of the cut text and at the start of the
			// kept part is part of the one text.
			name:   "a part of a continued answer, with whitespace at the joins",
			steps:  []outputLimitStep{{text: cutText, stop: "max_tokens"}, {text: " abc", err: errConnectionReset}, answered},
			stored: []part{{cutText, true}, {" abc", true}, {"Done.", false}},
			report: cutText + " abcDone.",
			next:   cutText + " abcDone.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: append(append([]outputLimitStep(nil), tc.steps...), outputLimitStep{text: "ok", stop: "end_turn"})}
			ag, st := recoveringAgent(t, p)
			if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("first turn: stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			stored := assistantMessages(st.GetMessages())
			var got []part
			for _, m := range stored {
				got = append(got, part{m.Content, m.Truncated})
			}
			if !reflect.DeepEqual(got, tc.stored) {
				t.Fatalf("the transcript stores %+v, want %+v", got, tc.stored)
			}
			if got := lastAssistantPlainText(st.GetMessages()); got != tc.report {
				t.Fatalf("the report of the run is %q, want %q", got, tc.report)
			}
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
				t.Fatal(err)
			}
			next := p.seen[len(p.seen)-1]
			requireNoAssistantInARow(t, next)
			if got := assistantMessages(next); len(got) != 1 || got[0].Content != tc.next {
				t.Fatalf("the next turn replays %+v, want one message holding %q", got, tc.next)
			}
		})
	}
}

// TestPromptCacheRebuiltRequestEndsWithTheStandingMessagesOfTheWorkingOne: the
// same chain of recoveries is run twice, once with a compaction before the last
// request and once without. A compaction rebuilds the history from the
// transcript, which holds none of the local messages, and puts back the ones
// that still stand at the end of the working history: those sent since the last
// step that left text or a tool call there. A message sent before such a step
// was answered by it and would come after text it is not about, so it is not
// put back. The request the compaction rebuilt must end with the same local
// messages, in the same order, as the request the turn built step by step, carry
// no other local message, hold no step that came back empty with nothing signed
// in it (a signed one is replayed as the provider signed it), and never end on
// an assistant message - the chain of two empty answers and a text the output
// limit cut off included, and a provider failure that kept part of an answer,
// whose request to go on used to be lost to a compaction right after it.
func TestPromptCacheRebuiltRequestEndsWithTheStandingMessagesOfTheWorkingOne(t *testing.T) {
	text, thinking, goOn := "assistant: "+partialText, "user: "+outputLimitNudge, "user: "+outputLimitContinueNudge
	wording := "user: " + emptyAssistantContinuationNudge
	keptStart := outputLimitStep{text: "The notes hold", err: errConnectionReset}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep // the history is compacted during the last of them, before the request after it
		// tail, when set, is what the history of the rebuilt request ends with.
		tail []string
	}{
		{"thinking cut off", []outputLimitStep{cutOff}, []string{thinking}},
		{"thinking cut off twice", []outputLimitStep{cutOff, cutOff}, []string{thinking, thinking}},
		{"a text cut off", []outputLimitStep{partialStep}, []string{text, goOn}},
		{"a text cut off twice", []outputLimitStep{partialStep, partialStep}, nil},
		{"thinking, then a text", []outputLimitStep{cutOff, partialStep}, []string{text, goOn}},
		{"a text, then thinking", []outputLimitStep{partialStep, cutOff}, []string{text, goOn, thinking}},
		{"two empty answers, then a text", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep}, []string{text, goOn}},
		{"two unsigned empty answers, then a text", []outputLimitStep{unsignedEmptyStep, unsignedEmptyStep, partialStep}, nil},
		{"two empty answers, then thinking cut off", []outputLimitStep{signedEmptyStep, signedEmptyStep, cutOff}, []string{wording, thinking}},
		{"a provider failure that kept part of an answer", []outputLimitStep{keptStart}, nil},
		{"a text cut off, then a provider failure in its continuation", []outputLimitStep{partialStep, keptStart}, nil},
		{"two empty answers, a text, then a provider failure", []outputLimitStep{signedEmptyStep, signedEmptyStep, partialStep, keptStart}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(compact bool) []llm.Message {
				t.Helper()
				adjust, arm := armedCompaction()
				steps := append([]outputLimitStep(nil), tc.steps...)
				if compact {
					steps[len(steps)-1].during = arm
				}
				steps = append(steps, answered)
				p := &outputLimitProvider{steps: steps}
				ag, st := outputLimitAgent(t, p, func(c *config.Config) {
					adjust(c)
					c.Agent.LLMRetryBaseMS = 1
				})
				addEarlierTurn(st)
				stop, err := runOutputLimitTurn(ag)
				if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(steps) {
					t.Fatalf("compact=%v: stop = %q, err = %v after %d calls; want end_turn after %d", compact, stop, err, p.calls, len(steps))
				}
				last := p.seen[len(p.seen)-1]
				if summarized := compacted(last); summarized != compact {
					t.Fatalf("compact=%v: the last request was compacted = %v; the test proves nothing", compact, summarized)
				}
				return last
			}
			working, rebuilt := run(false), run(true)
			want, got := standingAtTheEnd(working), standingAtTheEnd(rebuilt)
			if len(want) == 0 {
				t.Fatalf("the working request ends with no local message; the chain does not exercise a recovery:\n%s", tailLines(strings.Join(requestHistory(working), "\n"), 4))
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("the rebuilt request ends with %q, the working one with %q", got, want)
			}
			hist := requestHistory(rebuilt)
			if n := len(tc.tail); n > 0 && (len(hist) < n || strings.Join(hist[len(hist)-n:], "\n") != strings.Join(tc.tail, "\n")) {
				t.Fatalf("the rebuilt request does not end with %q:\n%s", tc.tail, tailLines(strings.Join(hist, "\n"), n+3))
			}
			history := withoutTurnContext(rebuilt)
			for text := range localMessages {
				if all, standing := countNudges(history, text), countNudges(history[len(history)-len(got):], text); all != standing {
					t.Fatalf("the rebuilt request carries %q %d times, %d of them before what stands at its end, which a later step had answered", text, all, all-standing)
				}
			}
			for _, m := range rebuilt {
				if repliesNothing(m) {
					t.Fatalf("the compaction resurrected a step that came back empty with nothing signed:\n%s", tailLines(strings.Join(hist, "\n"), 6))
				}
			}
			if lastIsAssistant(rebuilt) {
				t.Fatalf("the rebuilt request ends on an assistant message:\n%s", tailLines(strings.Join(hist, "\n"), 3))
			}
		})
	}
}

// loopThenCompactProvider is bddLoopProvider with a hook run on its first call,
// which turns compaction on, and a summary for the compaction to write.
type loopThenCompactProvider struct {
	*bddLoopProvider
	onFirst func()
}

func (p *loopThenCompactProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "Earlier turn summary."}, nil
}

func (p *loopThenCompactProvider) Stream(ctx context.Context, msgs []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if p.calls == 0 && p.onFirst != nil {
		p.onFirst()
	}
	return p.bddLoopProvider.Stream(ctx, msgs, defs, onChunk)
}

// TestPromptCacheCompactionAfterTheLoopGuardKeepsItsNudge: the loop guard cuts
// a response that repeats itself and sends a nudge after the part it kept. A
// compaction before the next request rebuilds the history from the transcript,
// which holds that part and not the nudge; the nudge stands at the end of the
// working history and is put back, so the request does not end on the step the
// guard cut.
func TestPromptCacheCompactionAfterTheLoopGuardKeepsItsNudge(t *testing.T) {
	run := func(compact bool) []llm.Message {
		t.Helper()
		adjust, arm := armedCompaction()
		p := &loopThenCompactProvider{
			bddLoopProvider: &bddLoopProvider{channel: loopAbortText, recoverAfter: 1, realAnswer: "Validation is in place."},
			onFirst: func() {
				if compact {
					arm()
				}
			},
		}
		ag, st := outputLimitAgent(t, p, adjust)
		addEarlierTurn(st)
		stop, err := runOutputLimitTurn(ag)
		if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
			t.Fatalf("compact=%v: stop = %q, err = %v after %d calls; want end_turn after 2", compact, stop, err, p.calls)
		}
		last := p.seen[len(p.seen)-1]
		if summarized := compacted(last); summarized != compact {
			t.Fatalf("compact=%v: the last request was compacted = %v; the test proves nothing", compact, summarized)
		}
		return last
	}
	working, rebuilt := run(false), run(true)
	want := []string{"user: " + streamLoopNudge}
	if got := standingAtTheEnd(working); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the working request ends with %q, want the loop guard's nudge", got)
	}
	if got := standingAtTheEnd(rebuilt); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the request after the compaction ends with %q, want the loop guard's nudge:\n%s", got, tailLines(strings.Join(requestHistory(rebuilt), "\n"), 3))
	}
}

// How the recovery of a step the output limit cut off meets a request the
// provider refuses as larger than the model's window (context_overflow.go,
// compact_fold.go). The two work on different things - the recovery on a
// response that came back empty, the overflow handling on an error or on the
// summarizer's own calls - and these tests hold that they do not step on each
// other.

const mlxContextLimitRefusal = `provider "mlx" (http://192.0.2.10:8080/v1): openai stream: POST "http://192.0.2.10:8080/v1/chat/completions": 404 Not Found "Context limit is 49152 tokens; prompt=49182 leaves 0 output tokens, below the minimum 16"`

func withWindow(tokens int) func(*config.Config) {
	return func(c *config.Config) { c.Models[0].MaxContextTokens = tokens }
}

// The nudge that asks for a shorter step can push the request past the window.
// The refusal that answers it is a context overflow, not an empty answer to ask
// again and not a failure of the provider's lane to run after a pause: the turn
// is compacted and the same step asked again, once, with the nudge still standing
// at the end of the history. A second refusal ends the turn on the overflow
// message, and the notice of an output-limit stop is never written over it.
func TestOutputLimitRecoveryRefusedForItsSize(t *testing.T) {
	refusals := []struct {
		name string
		err  func(t *testing.T) error
	}{
		{"worded refusal of a local backend", func(*testing.T) error { return errors.New(mlxContextLimitRefusal) }},
		{"typed code in a server error", func(t *testing.T) error {
			return apiRefusal(t, http.StatusInternalServerError, `{"message":"refused","type":"invalid_request_error","code":"context_length_exceeded"}`)
		}},
	}
	nudgePersisted := func(st *session.State) bool {
		for _, m := range st.GetMessages() {
			if m.Role == llm.RoleUser && m.Content == outputLimitNudge {
				return true
			}
		}
		return false
	}
	for _, tc := range refusals {
		t.Run(tc.name+": compacted and asked again", func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {err: tc.err(t)}, answered}}
			ag, st := outputLimitAgent(t, p, withWindow(49152))

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after the cut-off step, the refusal and the answer", stop, err, p.calls)
			}
			if got := countNudges(p.seen[1], outputLimitNudge); got != 1 {
				t.Fatalf("the refused request carried the output-limit nudge %d times, want 1 (the test must be in the recovery)", got)
			}
			if got := countNudges(p.seen[2], outputLimitNudge); got != 1 {
				t.Fatalf("the request asked again after the compaction carries the nudge %d times, want 1", got)
			}
			if hasEmptyAssistant(p.seen[2]) {
				t.Fatal("the compaction resurrected the step the output limit cut off")
			}
			var rows int
			for _, m := range st.GetMessages() {
				if m.CompactionSummary {
					rows++
				}
			}
			if rows != 1 {
				t.Fatalf("the transcript holds %d summary rows, want the one compaction the refusal asked for", rows)
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a notice: %q", notice)
			}
			if nudgePersisted(st) {
				t.Fatal("the local-only nudge reached the transcript")
			}
		})

		t.Run(tc.name+": refused again, ends on the overflow message", func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{cutOff, {err: tc.err(t)}, {err: tc.err(t)}, answered}}
			ag, st := outputLimitAgent(t, p, withWindow(49152))

			stop, err := runOutputLimitTurn(ag)
			if err == nil || stop != string(acp.StopReasonRefused) {
				t.Fatalf("stop = %q, err = %v; want the turn refused with the overflow message", stop, err)
			}
			if !strings.HasPrefix(err.Error(), "context window exceeded") || !llm.IsContextOverflow(err) || !strings.Contains(err.Error(), "Coddy compacted this turn") {
				t.Fatalf("not the overflow message after a compaction: %v", err)
			}
			if p.calls != 3 {
				t.Fatalf("provider asked %d times, want 3: the cut-off step, the refusal and the one request after the compaction", p.calls)
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("an output-limit notice was written over the overflow message: %q", notice)
			}
			if nudgePersisted(st) {
				t.Fatal("the local-only nudge reached the transcript")
			}

			// The next turn starts clean: no leftover nudge, and the cut-off step
			// that stayed in the transcript is not replayed.
			stop, err = ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go on"}})
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 4 {
				t.Fatalf("next turn: stop = %q, err = %v after %d calls; want end_turn after 4", stop, err, p.calls)
			}
			if got := countNudges(p.seen[3], outputLimitNudge); got != 0 {
				t.Fatalf("the next turn carries a nudge of the one that ended: %d", got)
			}
			if hasEmptyAssistant(p.seen[3]) {
				t.Fatal("the next turn replays the step the output limit cut off")
			}
		})
	}
}

// variedProse is n words of text that never repeats a phrase, so the loop guard
// does not take a long answer for a model stuck in a loop.
func variedProse(n int) string {
	words := []string{"module", "reads", "input", "parser", "returns", "every", "token", "buffer", "writes", "header", "checks", "length", "error", "value", "line", "skips", "comment", "field", "table", "stream"}
	var b strings.Builder
	x := uint32(2463534242)
	for i := 0; i < n; i++ {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b.WriteString(words[x%uint32(len(words))])
		b.WriteByte(' ')
	}
	return b.String()
}

// TestOverflowRecoveryKeepsTheAnswerTheModelGoesOn: a request refused for its
// size while the model is asked to go on from a text the output limit cut off
// is folded around that text, never through it. Every part of the answer and
// the empty steps after it stay verbatim at the end of the history, so the model
// sees the word it goes on from and the answer the turn hands over is whole;
// the steps before it are folded. When the turn holds nothing but that answer,
// there is nothing to fold, and the refusal ends the turn on the overflow
// message instead of a request that asks the model to go on from a summary.
func TestOverflowRecoveryKeepsTheAnswerTheModelGoesOn(t *testing.T) {
	readStep := outputLimitStep{calls: []llm.ToolCall{notesRead("call_r", 2)}, stop: "tool_use"}
	// bigPart does not fit the share of the window a fold aims the request at,
	// so the fold keeps the fewest steps it may.
	bigPart := outputLimitStep{text: variedProse(9000), stop: "max_tokens"}
	refused := outputLimitStep{err: errors.New(mlxContextLimitRefusal)}
	rest := outputLimitStep{text: " and the rest.", stop: "end_turn"}
	for _, tc := range []struct {
		name  string
		steps []outputLimitStep
		// parts are the texts of the answer, in order, that the request after
		// the compaction must carry; nil when the turn ends on the refusal.
		parts []string
		nudge string
	}{
		{"a text too large to keep with the steps before it", []outputLimitStep{readStep, bigPart, refused, rest}, []string{bigPart.text}, outputLimitContinueNudge},
		{"a text, then a step that only thought", []outputLimitStep{readStep, bigPart, cutOff, refused, rest}, []string{bigPart.text}, outputLimitNudge},
		{"an answer cut off twice", []outputLimitStep{readStep, bigPart, partialStep, refused, rest}, []string{bigPart.text, partialText}, outputLimitContinueNudge},
		{"a turn that is the text alone", []outputLimitStep{partialStep, refused, rest}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: tc.steps}
			ag, st, _ := cutCallAgent(t, p, withWindow(49152))
			stop, err := runOutputLimitTurn(ag)

			if tc.parts == nil {
				if err == nil || stop != string(acp.StopReasonRefused) || !llm.IsContextOverflow(err) || p.calls != len(tc.steps)-1 {
					t.Fatalf("stop = %q, err = %v after %d calls; want the turn refused on the overflow message after %d", stop, err, p.calls, len(tc.steps)-1)
				}
				for _, m := range st.GetMessages() {
					if m.CompactionSummary {
						t.Fatal("the text the model was to go on from was folded into a summary")
					}
				}
				return
			}
			if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != len(tc.steps) {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after %d", stop, err, p.calls, len(tc.steps))
			}
			after := p.seen[len(p.seen)-1]
			if !compacted(after) {
				t.Fatal("the request after the refusal was not compacted")
			}
			if ids := callIDs(after); len(ids) != 0 {
				t.Fatalf("the steps before the answer were not folded: the request still carries calls %v", ids)
			}
			var texts []string
			for _, m := range after {
				if m.Role == llm.RoleAssistant && m.Content != "" {
					texts = append(texts, m.Content)
				}
			}
			if want := strings.Join(tc.parts, ""); strings.Join(texts, "") != want || len(texts) != 1 {
				t.Fatalf("the request after the compaction carries the assistant texts %q, want the whole answer as one message", texts)
			}
			if got := countNudges(after, tc.nudge); got != 1 {
				t.Fatalf("the request after the compaction carries %q %d times, want once", tc.nudge, got)
			}
			if got, want := lastAssistantPlainText(st.GetMessages()), strings.Join(tc.parts, "")+rest.text; got != want {
				t.Fatalf("the answer the turn hands over is %d characters, want the whole answer of %d", len(got), len(want))
			}
		})
	}
}

// foldRefusingProvider answers the summarizer's first calls with err, as a
// backend whose window is smaller than the one it was configured with does, and
// every later call, and the loop's own, as outputLimitProvider does.
type foldRefusingProvider struct {
	*outputLimitProvider
	err           error
	refusals      int
	completeCalls int
	completeSizes []int
}

func (p *foldRefusingProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.completeCalls++
	p.completeSizes = append(p.completeSizes, estimateMessagesTokens(msgs))
	if p.completeCalls <= p.refusals {
		return nil, p.err
	}
	return p.outputLimitProvider.Complete(ctx, msgs, tools)
}

// TestOutputLimitRecoveryAndACompactionTheBackendRefuses: a compaction between
// the cut-off step and its retry folds the history on the summarizer's own
// calls, and a backend whose window is smaller than the one it was configured
// with refuses them for their size. When it refuses the first pass, the fold
// cuts the pass and asks again with less (compact_fold.go); when it refuses
// every pass, the compaction fails and the loop carries on with the history it
// has. Either way the cut-off step is still asked again, as the request that
// failed would have been: the nudge once, the empty step nowhere, no notice.
func TestOutputLimitRecoveryAndACompactionTheBackendRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		refusals int
		// compacts says the compaction went through, the summary in place of the
		// folded head.
		compacts bool
	}{
		{"the first pass is refused", 1, true},
		{"every pass is refused", 1 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adjust, arm := armedCompaction()
			base := &outputLimitProvider{}
			base.steps = []outputLimitStep{
				{reasoning: cutOff.reasoning, stop: "max_tokens", during: arm},
				answered,
			}
			p := &foldRefusingProvider{outputLimitProvider: base, err: errors.New(mlxContextLimitRefusal), refusals: tc.refusals}
			ag, st := outputLimitAgent(t, p, adjust)
			for i := 0; i < 4; i++ {
				st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "Earlier question about the module " + strings.Repeat("alpha ", 40)})
				st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Earlier answer " + strings.Repeat("beta ", 40)})
			}

			stop, err := runOutputLimitTurn(ag)
			if err != nil || stop != string(acp.StopReasonEndTurn) || base.calls != 2 {
				t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, base.calls)
			}
			if got := countNudges(base.seen[1], outputLimitNudge); got != 1 {
				t.Fatalf("the request after the compaction carries the nudge %d times, want 1", got)
			}
			if hasEmptyAssistant(base.seen[1]) {
				t.Fatal("the compaction resurrected the cut-off step")
			}
			if notice := st.TakeTurnStopNotice(); notice != "" {
				t.Fatalf("a recovered turn left a notice: %q", notice)
			}
			if summarized := compacted(base.seen[1]); summarized != tc.compacts {
				t.Fatalf("the request after the compaction carries the summary = %v, want %v", summarized, tc.compacts)
			}
			if !tc.compacts {
				return
			}
			// The retry of the refused pass was smaller than the pass that was refused.
			if p.completeCalls < 2 {
				t.Fatalf("the summarizer was asked %d times; the test needs a refused pass and its retry", p.completeCalls)
			}
			if p.completeSizes[1] >= p.completeSizes[0] {
				t.Fatalf("summarizer request sizes %v: the retry did not send less", p.completeSizes)
			}
		})
	}
}
