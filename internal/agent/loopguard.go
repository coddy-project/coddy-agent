package agent

// Runaway-loop protection. Two independent detectors guard a turn:
//
//   - streamRepeatDetector watches one streamed channel (answer text or
//     reasoning) and reports when the output degenerates into repeating the same
//     passage over and over, so the stream can be cancelled instead of running
//     until the user presses Stop.
//   - toolRepeatDetector reports when the model keeps requesting the identical
//     tool call (same name, same canonical arguments), which otherwise burns the
//     whole max_turns budget without producing an answer.
//
// Both are pure and allocation-light; the ReAct loop owns the policy (nudge the
// model, then stop the turn) in react.go.

import (
	"bytes"
	"encoding/json"
	"strings"
)

const (
	// streamRepeatWindow is how many normalized bytes of the streamed tail are kept
	// for periodicity checks.
	streamRepeatWindow = 4096
	// streamRepeatCheckEvery re-runs the check only once per this many new bytes.
	streamRepeatCheckEvery = 128
	// streamRepeatMinPeriod rejects trivial cycles such as ".. " or "| ".
	streamRepeatMinPeriod = 12
	// streamRepeatMaxPeriod bounds the search: a repeated unit longer than this is
	// not worth scanning for on every chunk.
	streamRepeatMaxPeriod = 512
	// streamRepeatMinDistinctRunes keeps decorative runs (a "-----" rule, a
	// "| --- | --- |" table separator) from looking like a degenerate loop.
	streamRepeatMinDistinctRunes = 4
)

// loopGuardTruncationMarker is appended to text whose repeated tail was dropped.
const loopGuardTruncationMarker = "\n\n[output truncated by the loop guard: the model repeated the same passage]"

// normalizeForRepeat lowercases ASCII and collapses whitespace runs to a single
// space so cosmetic differences between cycles do not hide a loop. The second
// return value maps each normalized byte back to the raw byte index it came from,
// which lets trimRepeatedTail cut the original text at a detected boundary.
func normalizeForRepeat(s string) ([]byte, []int) {
	norm := make([]byte, 0, len(s))
	offsets := make([]int, 0, len(s))
	lastWasSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			if lastWasSpace {
				continue
			}
			lastWasSpace = true
			norm = append(norm, ' ')
			offsets = append(offsets, i)
			continue
		}
		lastWasSpace = false
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		norm = append(norm, c)
		offsets = append(offsets, i)
	}
	return norm, offsets
}

// periodicSuffix reports the smallest period p for which the tail of norm is the
// same p bytes repeated at least minCycles times back to back. It is the core
// degeneration test: a model stuck in a loop emits exactly this shape.
func periodicSuffix(norm []byte, minCycles int) (period int, ok bool) {
	if minCycles < 2 {
		return 0, false
	}
	n := len(norm)
	for p := streamRepeatMinPeriod; p <= streamRepeatMaxPeriod; p++ {
		if p*minCycles > n {
			break
		}
		unit := norm[n-p:]
		// Cheap reject first: only the block immediately before the tail.
		if !bytes.Equal(unit, norm[n-2*p:n-p]) {
			continue
		}
		matched := true
		for k := 2; k < minCycles; k++ {
			if !bytes.Equal(unit, norm[n-(k+1)*p:n-k*p]) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if distinctRunes(unit) < streamRepeatMinDistinctRunes {
			continue
		}
		return p, true
	}
	return 0, false
}

// distinctRunes counts how many different runes appear in b.
func distinctRunes(b []byte) int {
	seen := make(map[rune]struct{}, len(b))
	for _, r := range string(b) {
		seen[r] = struct{}{}
	}
	return len(seen)
}

// trimRepeatedTail drops a degenerate repeated run from the end of s, keeping the
// useful prefix plus the first occurrence of the repeated passage and appending
// loopGuardTruncationMarker. The looped text must not survive anywhere: replaying
// it to the model would immediately re-seed the same degeneration, and it would
// otherwise bloat the transcript and the context estimate.
func trimRepeatedTail(s string, minCycles int) (string, bool) {
	norm, offsets := normalizeForRepeat(s)
	period, ok := periodicSuffix(norm, minCycles)
	if !ok {
		return s, false
	}
	n := len(norm)
	unit := norm[n-period:]
	// Count backwards in whole cycles so the entire looped run is removed, not
	// just the repetitions the detector happened to see. Whole cycles only: a
	// character-by-character walk drifts into the text before the run whenever its
	// last bytes happen to match the unit, and would then eat real content.
	cycles := 1
	for start := n - period*(cycles+1); start >= 0; start = n - period*(cycles+1) {
		if !bytes.Equal(unit, norm[start:start+period]) {
			break
		}
		cycles++
	}
	// Keep the first occurrence of the passage, drop every later repetition.
	keep := n - period*(cycles-1)
	if keep >= len(offsets) {
		return s, false
	}
	return strings.TrimRight(s[:offsets[keep]], " \t\r\n") + loopGuardTruncationMarker, true
}

// streamRepeatDetector watches one streamed channel for degeneration. A nil
// detector is inert, which is how the guard is switched off.
type streamRepeatDetector struct {
	minCycles int
	tail      []byte
	pending   int
	inSpace   bool
}

// newStreamRepeatDetector returns nil when the check is disabled (cycles < 2).
func newStreamRepeatDetector(minCycles int) *streamRepeatDetector {
	if minCycles < 2 {
		return nil
	}
	return &streamRepeatDetector{minCycles: minCycles, tail: make([]byte, 0, streamRepeatWindow)}
}

// Add feeds one streamed delta and reports whether the channel has degenerated
// into a repeating loop.
func (d *streamRepeatDetector) Add(delta string) (period int, tripped bool) {
	if d == nil || delta == "" {
		return 0, false
	}
	added := 0
	for i := 0; i < len(delta); i++ {
		c := delta[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			if d.inSpace {
				continue
			}
			d.inSpace = true
			c = ' '
		} else {
			d.inSpace = false
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
		}
		d.tail = append(d.tail, c)
		added++
	}
	if added == 0 {
		return 0, false
	}
	if len(d.tail) > streamRepeatWindow {
		d.tail = append(d.tail[:0], d.tail[len(d.tail)-streamRepeatWindow:]...)
	}
	d.pending += added
	if d.pending < streamRepeatCheckEvery {
		return 0, false
	}
	d.pending = 0
	return periodicSuffix(d.tail, d.minCycles)
}

// toolRepeatDetector counts consecutive identical tool calls. A nil detector is
// inert, which is how the check is switched off.
type toolRepeatDetector struct {
	limit int
	key   string
	count int
}

// newToolRepeatDetector returns nil when the check is disabled (limit <= 0).
func newToolRepeatDetector(limit int) *toolRepeatDetector {
	if limit <= 0 {
		return nil
	}
	return &toolRepeatDetector{limit: limit}
}

// Observe records one requested tool call and reports the consecutive count for
// it, tripping once the limit is reached and on every identical call after that.
// A different tool or different arguments resets the count, so only a genuinely
// stuck model trips.
func (d *toolRepeatDetector) Observe(name, inputJSON string) (count int, tripped bool) {
	if d == nil {
		return 0, false
	}
	key := canonicalToolCallKey(name, inputJSON)
	if d.key == key && d.count > 0 {
		d.count++
	} else {
		d.key = key
		d.count = 1
	}
	return d.count, d.count >= d.limit
}

// canonicalToolCallKey identifies a tool call by name plus canonicalized
// arguments, so semantically identical calls compare equal regardless of key
// order or spacing. The arguments are always part of the key: comparing names
// alone would flag every read of a different file as a loop.
func canonicalToolCallKey(name, inputJSON string) string {
	return name + "\x00" + canonicalJSON(inputJSON)
}

// canonicalJSON re-encodes a JSON document with sorted object keys (encoding/json
// sorts map keys on marshal). Input that does not parse falls back to
// whitespace-normalized text.
func canonicalJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return "raw:" + strings.Join(strings.Fields(trimmed), " ")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "raw:" + strings.Join(strings.Fields(trimmed), " ")
	}
	return string(b)
}
