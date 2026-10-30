package agent

import (
	"math"
	"testing"
	"time"
)

func requireSpeed(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 0.00001 {
		t.Fatalf("speed = %v, want %g", got, want)
	}
}

func TestGenerationSpeedWindows(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	for n := 1; n <= 800; n++ {
		speed.add(n, start.Add(time.Duration(n)*100*time.Millisecond))
		switch n {
		case 199:
			if speed.current != nil {
				t.Fatalf("speed visible before window filled at %d tokens", n)
			}
		case 200, 201:
			requireSpeed(t, speed.current, 200/19.9)
		case 499:
			// The 500-token window is not full yet, so the 200-token one still speaks.
			if speed.current == nil {
				t.Fatal("speed went away between the two windows")
			}
		case 500:
			requireSpeed(t, speed.current, 500/49.9)
		case 600, 800:
			requireSpeed(t, speed.current, 10)
		}
	}
}

func TestGenerationSpeedRefreshesPartialStepAfterFiveSeconds(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(500, start.Add(5*time.Second))
	before := *speed.current
	speed.add(510, start.Add(9*time.Second))
	requireSpeed(t, speed.current, before)
	speed.add(520, start.Add(10*time.Second))
	if speed.current == nil || *speed.current >= before {
		t.Fatal("a partial 100-token step did not refresh after five seconds")
	}
}

func TestGenerationSpeedAverageExcludesToolsAndResetsWindow(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(500, start.Add(5*time.Second))
	speed.finish(600)
	speed.begin()
	if speed.current != nil {
		t.Fatal("a new model call kept the previous window")
	}
	speed.add(1, start.Add(time.Hour))
	speed.add(100, start.Add(time.Hour+5*time.Second))
	speed.finish(200)
	requireSpeed(t, speed.average(), 80)
	// Finishing twice must not count a failed/cancelled call a second time.
	speed.finish(200)
	requireSpeed(t, speed.average(), 80)
}

func TestGenerationSpeedPartialAndSingleChunk(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(100, start.Add(2*time.Second))
	speed.finish(0)
	requireSpeed(t, speed.average(), 50)
	speed.begin()
	speed.add(500, start.Add(time.Hour))
	speed.finish(500)
	requireSpeed(t, speed.average(), 50)
}

func TestGenerationSpeedIncludesTrailingOutputWithinTheSameTokenEstimate(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(2, start.Add(time.Second))
	speed.add(2, start.Add(9*time.Second))
	speed.finish(2)
	if speed.totalTime != 9*time.Second {
		t.Fatalf("generation interval = %s, want 9s", speed.totalTime)
	}
	requireSpeed(t, speed.average(), 2.0/9.0)
}

func TestGenerationSpeedInterpolationKeepsTheLastTokenBoundary(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(2, start.Add(time.Second))
	speed.add(2, start.Add(9*time.Second))
	speed.add(4, start.Add(11*time.Second))
	// The next estimated tokens span the interval since token 2, not just
	// since the last delta whose output still rounded to token 2.
	if got := speed.boundaries[3]; !got.Equal(start.Add(6 * time.Second)) {
		t.Fatalf("token 3 boundary = %v, want %v", got, start.Add(6*time.Second))
	}
	speed.finish(4)
	if speed.totalTime != 11*time.Second {
		t.Fatalf("generation interval = %s, want 11s", speed.totalTime)
	}
	requireSpeed(t, speed.average(), 4.0/11.0)
}

func TestGenerationSpeedDisplayChangesAtMostEveryFiveSeconds(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	speed.add(1, start)
	speed.add(500, start.Add(time.Second))
	before := *speed.current
	speed.add(600, start.Add(2*time.Second))
	requireSpeed(t, speed.current, before)
	speed.add(700, start.Add(6*time.Second))
	if speed.current == nil || *speed.current == before {
		t.Fatal("display did not refresh after five seconds")
	}
}

// Most model calls of an agent stop short of 500 tokens: once a call has written
// 200, its speed stays on the line for the rest of the call.
func TestGenerationSpeedStaysOnceTheFirstWindowFilled(t *testing.T) {
	var speed generationSpeed
	start := time.Unix(100, 0)
	speed.begin()
	for n := 1; n <= 520; n++ {
		speed.add(n, start.Add(time.Duration(n)*100*time.Millisecond))
		if n >= 200 && speed.current == nil {
			t.Fatalf("no speed at %d tokens", n)
		}
	}
}
