package agent

// Multi-step compaction: folding a history that no longer fits one
// summarization request.
//
// A single call was enough while the transcript stayed near the window it was
// measured against. It stops being enough exactly when compaction matters
// most: a session that ran far past its window - a model that kept reading
// large files, an automatic trigger that never fired because the window was
// unknown - arrives at /compact with a history several times the summarizer's
// window, and the one request the old path built was refused by the provider
// ("the request is too long, shorten the message history"). The session was
// then stuck: too big to send, and the only thing that could shrink it was the
// call that would not go out.
//
// So the head is folded in passes. Each pass carries the summary of everything
// folded so far plus the next run of transcript, both sized to the summarizer's
// own context window, and answers with one summary that covers both. The last
// pass's answer is the summary that goes into the transcript, so a compaction
// that took seven calls is indistinguishable in the session from one that took
// one.

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// compactionInputSharePercent is how much of the summarizer's context
	// window one pass may fill with transcript and carried summary. The rest
	// is headroom: the system prompt, the summary being written, and the
	// distance between a three-characters-per-token estimate and what the
	// provider's own tokenizer makes of source code and JSON.
	compactionInputSharePercent = 55
	// compactionMinChunkTokens is the smallest run of transcript a pass will
	// send. Below it the fold makes no progress worth the call.
	compactionMinChunkTokens = 512
	// compactionCarryShare is how much of a pass's budget the carried summary
	// may take before the rest is transcript. A carry that grew past it is
	// still sent whole - dropping what was already folded loses it for good -
	// but the transcript run shrinks to the floor instead.
	compactionCarryShare = 2
	// compactionMaxSteps caps the passes of one compaction, so a window
	// reported far smaller than it is cannot turn a compaction into an endless
	// run of calls.
	compactionMaxSteps = 64
	// compactionShrinkAttempts is how many times a pass the provider refused
	// as too large is cut down before the compaction reports the failure.
	compactionShrinkAttempts = 3
	// compactionUnrecognisedCuts is how many times a pass refused with a 400 or
	// a 413 the classifier does not recognise is cut before the pass moves on.
	// The old fold cut on any refusal, and a backend worded in a way no phrase
	// knows must not leave a session that only a compaction can save with no
	// way out; one cut is what a size problem needs shown, and a refusal that
	// is something else costs one more call.
	compactionUnrecognisedCuts = 1
	// compactionCharsPerToken is how many ASCII characters one token of
	// session.EstimateContextTokens stands for, the unit passes are sized in.
	compactionCharsPerToken = 3
	// compactionRetryAimPercent is how much of the provider's own limit a pass
	// refused with figures aims for: the system prompt, the carried summary and
	// the distance between the estimate and the provider's tokenizer take the
	// rest.
	compactionRetryAimPercent = 80
)

// compactionProgress reports a pass of a multi-step fold. step counts from 1;
// total is the passes the plan expects, which a shrink may raise.
type compactionProgress func(step, total int)

// compactionInputBudget is how many tokens of carried summary plus transcript
// one pass may send, given the summarizer's context window. Passes are sized
// with session.EstimateContextTokens, the estimate the automatic trigger
// already uses, not the four-runes-per-token one: source code, paths and
// non-ASCII text cost far more than that, and a pass built on the optimistic
// figure is the one the provider refuses.
func compactionInputBudget(window, instructionTokens int) int {
	if window <= 0 {
		window = 0
	}
	budget := window*compactionInputSharePercent/100 -
		session.EstimateContextTokens(compactionSystemPrompt) - instructionTokens
	if budget < compactionMinChunkTokens {
		return compactionMinChunkTokens
	}
	return budget
}

// compactionChunk is one pass's run of transcript: the messages it covers and
// their rendered text.
type compactionChunk struct {
	// count is how many messages of the remaining head this pass consumes;
	// always at least one, so the fold cannot stall.
	count int
	body  string
}

// nextCompactionChunk takes the longest run of msgs whose rendered text fits
// room tokens. A single message larger than room is sent alone, elided in the
// middle: its head and tail are what a summary needs, and refusing to send it
// would stall the fold on the one message compaction exists to fold away.
func nextCompactionChunk(msgs []llm.Message, room int) compactionChunk {
	if len(msgs) == 0 {
		return compactionChunk{}
	}
	if room < compactionMinChunkTokens {
		room = compactionMinChunkTokens
	}
	var b strings.Builder
	used := 0
	for i, m := range msgs {
		text := renderCompactionMessage(m)
		n := session.EstimateContextTokens(text)
		if i > 0 && used+n > room {
			return compactionChunk{count: i, body: b.String()}
		}
		if i == 0 && n > room {
			return compactionChunk{count: 1, body: elideToTokens(text, room)}
		}
		b.WriteString(text)
		used += n
	}
	return compactionChunk{count: len(msgs), body: b.String()}
}

// entryCutMarker replaces the middle of a transcript entry the summarizer
// cannot take whole; its one verb is the count of characters that went.
const entryCutMarker = "\n[... %d characters omitted: this entry alone does not fit one summarization request ...]\n"

// elideMiddle cuts the middle out of s so it fits maxChars, keeping the head
// and the tail and saying how much went. A transcript entry is summarized from
// what it starts and ends with far more often than from its middle.
func elideMiddle(s string, maxChars int) string {
	return elideMiddleMarked(s, maxChars, entryCutMarker)
}

// elideMiddleMarked is elideMiddle with the words of the cut chosen by the
// caller: marker is a format with one %d, the count of characters omitted. The
// cut says what it is, because the text it stands in is read by the model, and
// "does not fit one summarization request" is a false account of anything that
// is not a summarization request.
func elideMiddleMarked(s string, maxChars int, marker string) string {
	r := []rune(s)
	if maxChars <= 0 || len(r) <= maxChars {
		return s
	}
	head := maxChars / 2
	tail := maxChars - head
	dropped := len(r) - head - tail
	if dropped <= 0 {
		return s
	}
	return string(r[:head]) + fmt.Sprintf(marker, dropped) + string(r[len(r)-tail:])
}

// elideToTokens cuts the middle out of s until it is about tokens long by
// session.EstimateContextTokens. The cut is made in characters, and how many
// characters a token is depends on the text, so the length is corrected a few
// times until the estimate agrees.
func elideToTokens(s string, tokens int) string {
	return elideToTokensMarked(s, tokens, entryCutMarker)
}

// elideToTokensMarked is elideToTokens with the cut's marker chosen by the
// caller (see elideMiddleMarked).
func elideToTokensMarked(s string, tokens int, marker string) string {
	if tokens < 1 {
		tokens = 1
	}
	chars := tokens * compactionCharsPerToken
	out := elideMiddleMarked(s, chars, marker)
	for i := 0; i < 4; i++ {
		got := session.EstimateContextTokens(out)
		if got <= tokens || chars <= 1 {
			break
		}
		// At least one character: elideMiddleMarked takes none for "keep all".
		chars = max(chars*tokens/got, 1)
		out = elideMiddleMarked(s, chars, marker)
	}
	return out
}

// foldCompactionHead summarizes head into one summary, in as many passes as
// the summarizer's window needs. It reports every pass through progress before
// making the call, so a compaction that takes a while says what it is doing.
func (a *Agent) foldCompactionHead(
	ctx context.Context,
	chain []compactionCandidate,
	head []llm.Message,
	instructions string,
	budget int,
	progress compactionProgress,
) (summary string, modelID string, steps int, err error) {
	if len(chain) == 0 {
		return "", "", 0, fmt.Errorf("compaction model: no model configured")
	}
	rest := head
	carry := ""
	// The model that answered the last pass: what the summary row records.
	used := chain[0].modelID
	// The plan is what the estimate expects; a shrink raises it as it goes, so
	// the progress a client sees never promises a pass that will not happen.
	total := plannedCompactionSteps(head, budget)
	for len(rest) > 0 {
		if steps >= compactionMaxSteps {
			return "", "", steps, fmt.Errorf("compaction did not finish in %d passes: the summarizer's context window is too small for this history", compactionMaxSteps)
		}
		room := budget - session.EstimateContextTokens(carry)/compactionCarryShare
		if room < compactionMinChunkTokens {
			room = compactionMinChunkTokens
		}
		chunk := nextCompactionChunk(rest, room)
		steps++
		if steps > total {
			total = steps
		}
		if progress != nil {
			progress(steps, total)
		}
		out, answered, done, callErr := a.foldOnePass(ctx, chain, carry, rest, chunk, instructions)
		if callErr != nil {
			return "", "", steps, callErr
		}
		carry = out
		used = answered
		rest = rest[done:]
	}
	if strings.TrimSpace(carry) == "" {
		return "", "", steps, fmt.Errorf("compaction produced an empty summary")
	}
	return carry, used, steps, nil
}

// foldOnePass makes one summarization call and reports how many messages it
// covered. A pass the provider refuses because the request does not fit its
// window (llm.IsContextOverflow) is cut down and sent again, up to
// compactionShrinkAttempts times, because the estimate the pass was sized with
// can fall short of the provider's tokenizer and the window a provider reports
// is not always the one it enforces. A 400 or a 413 nothing recognises is cut
// once (compactionUnrecognisedCuts). Any other refusal - a key, an outage, a
// limit - fails the same way at half the size, so the pass goes to the next
// model of the chain at once.
func (a *Agent) foldOnePass(
	ctx context.Context,
	chain []compactionCandidate,
	carry string,
	rest []llm.Message,
	chunk compactionChunk,
	instructions string,
) (summary string, modelID string, covered int, err error) {
	var lastErr error
	for i, cand := range chain {
		for attempt := 0; ; attempt++ {
			req := compactionRequest(carry, chunk.body, instructions)
			resp, callErr := cand.provider.Complete(ctx, req, nil)
			if callErr == nil {
				out := strings.TrimSpace(resp.Content)
				if out == "" {
					return "", "", 0, fmt.Errorf("compaction produced an empty summary")
				}
				return out, cand.modelID, chunk.count, nil
			}
			lastErr = callErr
			if ctx.Err() != nil {
				return "", "", 0, fmt.Errorf("compaction LLM call: %w", callErr)
			}
			overflow := llm.IsContextOverflow(callErr)
			cuts := 0
			switch status := llm.UpstreamStatus(callErr); {
			case overflow:
				cuts = compactionShrinkAttempts
			case status == 400 || status == 413:
				cuts = compactionUnrecognisedCuts
			}
			if attempt >= cuts {
				break
			}
			why := "compaction pass refused as too large"
			if !overflow {
				why = "compaction pass refused with a status that can mean too large"
			}
			// Both sizes go to the log: what this fold estimated for the request
			// and what the provider says it counted, so a refusal at a size the
			// estimate called safe shows how far off the estimate was.
			detail := llm.OverflowDetailOf(callErr)
			// Cut what was actually sent, not the room it was allowed: the
			// budget can be far larger than the pass that filled it, and
			// cutting the allowance would send the identical request again.
			target := shrunkenPassTokens(session.EstimateContextTokens(chunk.body), detail)
			refused := []any{
				"model", cand.modelID, "estimatedTokens", estimateMessagesTokens(req),
				"providerTokens", detail.Prompt, "providerLimit", detail.Limit,
				"retryTokens", target, "error", callErr,
			}
			if smaller := nextCompactionChunk(rest, target); smaller.count < chunk.count {
				a.log.Warn(why+"; retrying with fewer messages",
					append(refused, "messages", chunk.count, "retryMessages", smaller.count)...)
				chunk = smaller
				continue
			}
			// One message the provider refuses even on its own: cut it further
			// rather than stop, because the alternative is a session that can
			// never be compacted again.
			body := elideToTokens(chunk.body, target)
			if len([]rune(body)) >= len([]rune(chunk.body)) {
				break
			}
			a.log.Warn(why+"; retrying with a shortened message", refused...)
			chunk.body = body
		}
		if i+1 < len(chain) {
			a.log.Warn("compaction summarizer could not fold this pass; falling back to the next model",
				"model", cand.modelID, "next", chain[i+1].modelID, "error", lastErr)
		}
	}
	return "", "", 0, fmt.Errorf("compaction LLM call: %w", lastErr)
}

// shrunkenPassTokens is how many tokens of transcript the next try of a refused
// pass may hold, given the tokens the refused one held. Half, unless the
// provider said how far over its limit the request was: then the aim is under
// that limit - compactionRetryAimPercent of it, scaled to the transcript - and
// the figure never asks for less shrinking than halving does.
func shrunkenPassTokens(bodyTokens int, detail llm.OverflowDetail) int {
	half := bodyTokens / 2
	if detail.Prompt > 0 && detail.Limit > 0 && detail.Prompt > detail.Limit {
		// In floating point: the figures are a refusal's own words, and the
		// product of three of them wraps an int64.
		aimed := int(float64(bodyTokens) * float64(detail.Limit) / float64(detail.Prompt) * compactionRetryAimPercent / 100)
		if aimed < half {
			return aimed
		}
	}
	return half
}

// estimateMessagesTokens is session.EstimateContextTokens over a whole request.
func estimateMessagesTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += session.EstimateContextTokens(m.Content)
	}
	return total
}

// plannedCompactionSteps is how many passes the estimate expects, so the first
// progress update can already say "1 of 7" instead of counting up blind.
func plannedCompactionSteps(head []llm.Message, budget int) int {
	if budget <= 0 {
		return 1
	}
	total := 0
	for _, m := range head {
		total += session.EstimateContextTokens(renderCompactionMessage(m))
	}
	steps := (total + budget - 1) / budget
	if steps < 1 {
		return 1
	}
	return steps
}

// compactionDedupMinLineLen is how long a line must be before a later exact
// copy of it is dropped. Short lines are structure - a closing brace, a bare
// number, a blank - and dropping them mangles the code a summary has to read;
// a long line repeating verbatim is a log the session pasted twice.
const compactionDedupMinLineLen = 32

// dedupeCompactionHead drops lines the head already carried verbatim. A session
// that filled its window did it with repetition - the same build log pasted
// after each attempt, the same file read a dozen times, a test runner printing
// one line per package - and the summariser learns nothing from the second copy
// while paying for it in full. The first copy of every line stays, in place;
// each entry says how many repeats went, so the model is told the history was
// thinned rather than left to wonder (issue #273).
func dedupeCompactionHead(head []llm.Message) (out []llm.Message, dropped int) {
	seen := make(map[string]struct{}, 1024)
	out = make([]llm.Message, len(head))
	for i, m := range head {
		out[i] = m
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		kept, gone := dedupeLines(m.Content, seen)
		if gone == 0 {
			continue
		}
		dropped += gone
		out[i].Content = fmt.Sprintf("%s\n[... %d repeated line(s) removed: identical to lines earlier in this conversation ...]", kept, gone)
	}
	return out, dropped
}

// dedupeLines removes from s every line long enough to carry meaning that seen
// already holds, and records the rest.
func dedupeLines(s string, seen map[string]struct{}) (kept string, dropped int) {
	lines := strings.Split(s, "\n")
	out := lines[:0:0]
	for _, line := range lines {
		key := strings.TrimSpace(line)
		if len(key) < compactionDedupMinLineLen {
			out = append(out, line)
			continue
		}
		if _, ok := seen[key]; ok {
			dropped++
			continue
		}
		seen[key] = struct{}{}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), dropped
}
