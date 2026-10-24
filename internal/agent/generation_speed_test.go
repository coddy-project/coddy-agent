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
		case 199, 201, 499:
			if speed.current != nil {
				t.Fatalf("speed visible before window filled at %d tokens", n)
			}
		case 200:
			requireSpeed(t, speed.current, 200/19.9)
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
