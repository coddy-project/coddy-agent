package agent

import (
	"strings"
	"testing"
)

func TestPeriodicSuffixDetectsRepeatedPassages(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		minCycles int
		want      bool
	}{
		{
			name:      "repeated sentence",
			text:      "Working on it. " + strings.Repeat("Still writing validation logic... ", 6),
			minCycles: 5,
			want:      true,
		},
		{
			name:      "repeated thought in the reasoning channel",
			text:      strings.Repeat("I should check the config file again, then re-read it. ", 5),
			minCycles: 5,
			want:      true,
		},
		{
			name:      "horizontal rule is not a loop",
			text:      "Section\n" + strings.Repeat("-", 300) + "\n",
			minCycles: 5,
			want:      false,
		},
		{
			name:      "markdown table separator is not a loop",
			text:      "| a | b |\n" + strings.Repeat("| --- ", 40) + "|\n",
			minCycles: 5,
			want:      false,
		},
		{
			name:      "period below the minimum is not a loop",
			text:      strings.Repeat(".. ", 200),
			minCycles: 5,
			want:      false,
		},
		{
			name:      "fewer cycles than required",
			text:      strings.Repeat("Still writing validation logic... ", 4),
			minCycles: 5,
			want:      false,
		},
		{
			name:      "ordinary prose",
			text:      "I read the config, found the agent section, and added the new fields with their defaults and validation.",
			minCycles: 5,
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			norm, _ := normalizeForRepeat(tc.text)
			period, ok := periodicSuffix(norm, tc.minCycles)
			if ok != tc.want {
				t.Fatalf("periodicSuffix ok = %v (period %d), want %v", ok, period, tc.want)
			}
			if ok && period < streamRepeatMinPeriod {
				t.Fatalf("period %d below the minimum %d", period, streamRepeatMinPeriod)
			}
		})
	}
}

func TestStreamRepeatDetectorTripsOnStreamedDeltas(t *testing.T) {
	d := newStreamRepeatDetector(5)
	var tripped bool
	for i := 0; i < 40 && !tripped; i++ {
		_, tripped = d.Add("Still writing validation logic... ")
	}
	if !tripped {
		t.Fatal("detector never tripped on a stream repeating one sentence")
	}

	calm := newStreamRepeatDetector(5)
	for _, delta := range []string{
		"I checked ", "internal/config/agent.go ", "and added the loop guard fields ",
		"with defaults, validation, and the JSON DTO mapping. ",
	} {
		if _, ok := calm.Add(delta); ok {
			t.Fatalf("detector tripped on ordinary prose at %q", delta)
		}
	}
}

func TestStreamRepeatDetectorDisabled(t *testing.T) {
	if d := newStreamRepeatDetector(0); d != nil {
		t.Fatal("zero cycles must disable the detector")
	}
	var d *streamRepeatDetector
	if _, ok := d.Add("anything"); ok {
		t.Fatal("nil detector must never trip")
	}
}

func TestTrimRepeatedTailDropsTheLoopedRun(t *testing.T) {
	prefix := "Here is what I did so far. "
	text := prefix + strings.Repeat("Still writing validation logic... ", 12)

	got, ok := trimRepeatedTail(text, 5)
	if !ok {
		t.Fatal("expected the repeated run to be trimmed")
	}
	if !strings.Contains(got, prefix) {
		t.Fatalf("the useful prefix was dropped: %q", got)
	}
	if !strings.Contains(got, loopGuardTruncationMarker) {
		t.Fatalf("truncation marker missing: %q", got)
	}
	if n := strings.Count(got, "Still writing validation logic..."); n != 1 {
		t.Fatalf("looped passage kept %d times, want exactly 1: %q", n, got)
	}
	if len(got) >= len(text) {
		t.Fatalf("trimmed text (%d bytes) is not shorter than the original (%d bytes)", len(got), len(text))
	}
}

func TestTrimRepeatedTailLeavesCleanTextAlone(t *testing.T) {
	text := "I added the loop guard fields to the agent config and wired the defaults."
	got, ok := trimRepeatedTail(text, 5)
	if ok || got != text {
		t.Fatalf("clean text was modified: ok=%v got=%q", ok, got)
	}
}

func TestCanonicalToolCallKey(t *testing.T) {
	a := canonicalToolCallKey("read", `{"path":"/a.go","limit":10}`)
	b := canonicalToolCallKey("read", `{"limit":10,"path":"/a.go"}`)
	if a != b {
		t.Fatalf("permuted JSON keys produced different keys:\n%q\n%q", a, b)
	}

	if c := canonicalToolCallKey("read", `{"path":"/b.go","limit":10}`); c == a {
		t.Fatal("different arguments must produce different keys")
	}
	if c := canonicalToolCallKey("glob", `{"path":"/a.go","limit":10}`); c == a {
		t.Fatal("different tool names must produce different keys")
	}

	m1 := canonicalToolCallKey("read", `{"path":`)
	m2 := canonicalToolCallKey("read", `{"path":   `)
	if m1 != m2 {
		t.Fatalf("malformed JSON fallback is not whitespace-insensitive:\n%q\n%q", m1, m2)
	}
	if m1 == a {
		t.Fatal("malformed arguments must not collide with parsed ones")
	}
}

func TestToolRepeatDetector(t *testing.T) {
	d := newToolRepeatDetector(3)

	for i := 1; i <= 2; i++ {
		if _, tripped := d.Observe("glob", `{"pattern":"**/*.go"}`); tripped {
			t.Fatalf("tripped early on call %d", i)
		}
	}
	if _, tripped := d.Observe("glob", `{"pattern":"**/*.go"}`); !tripped {
		t.Fatal("third identical call must trip the detector")
	}
	// Every identical call after the trip keeps tripping: the guard nudges the
	// model automatically, so letting the counter restart would just resume the loop.
	if _, tripped := d.Observe("glob", `{"pattern":"**/*.go"}`); !tripped {
		t.Fatal("calls after the trip must keep tripping")
	}
	// A genuinely different call clears it.
	if _, tripped := d.Observe("glob", `{"pattern":"**/*.md"}`); tripped {
		t.Fatal("a different call must reset the counter")
	}

	// Same tool, different arguments each time: never a loop.
	varying := newToolRepeatDetector(3)
	for i := 0; i < 10; i++ {
		args := `{"pattern":"**/*` + strings.Repeat("x", i) + `.go"}`
		if _, tripped := varying.Observe("glob", args); tripped {
			t.Fatalf("tripped on varying arguments at call %d", i)
		}
	}
}

func TestToolRepeatDetectorDisabled(t *testing.T) {
	if d := newToolRepeatDetector(0); d != nil {
		t.Fatal("zero limit must disable the detector")
	}
	var d *toolRepeatDetector
	for i := 0; i < 5; i++ {
		if _, tripped := d.Observe("glob", `{"pattern":"*"}`); tripped {
			t.Fatal("nil detector must never trip")
		}
	}
}
