package agent

import "time"

const speedRefreshInterval = 5 * time.Second

// generationSpeed measures output between the first and last delta of each
// model call. The fixed ring retains just enough token boundaries for a
// 500-token window, including its starting boundary. Deltas containing several
// tokens are spread across the interval since the preceding delta.
type generationSpeed struct {
	boundaries  [501]time.Time
	callTokens  int
	first, last time.Time
	refreshed   time.Time
	windowEnd   int
	active      bool
	current     *float64
	windowSpeed *float64
	totalTokens int
	totalTime   time.Duration
}

func (s *generationSpeed) begin() {
	s.callTokens = 0
	s.first, s.last, s.refreshed = time.Time{}, time.Time{}, time.Time{}
	s.windowEnd = 0
	s.active = true
	s.current = nil
	s.windowSpeed = nil
}

func (s *generationSpeed) add(tokens int, now time.Time) {
	if !s.active || tokens <= s.callTokens {
		return
	}
	previous := s.last
	if s.first.IsZero() {
		s.first, previous = now, now
		s.boundaries[0] = now
		s.refreshed = now
	}
	// A single large delta still writes at most one ring's worth of entries.
	from := max(s.callTokens+1, tokens-500)
	for n := from; n <= tokens; n++ {
		fraction := float64(n-s.callTokens) / float64(tokens-s.callTokens)
		s.boundaries[n%len(s.boundaries)] = previous.Add(time.Duration(float64(now.Sub(previous)) * fraction))
	}
	s.callTokens, s.last = tokens, now
	window, step := 200, 50
	if tokens > 200 {
		window, step = 500, 100
	}
	if tokens < window {
		s.current = nil
		s.windowEnd = 0
		return
	}
	if s.windowEnd != 0 && tokens-s.windowEnd < step && now.Sub(s.refreshed) < speedRefreshInterval {
		return
	}
	elapsed := now.Sub(s.boundaries[(tokens-window)%len(s.boundaries)])
	if elapsed > 0 {
		value := float64(window) / elapsed.Seconds()
		s.windowSpeed = &value
		s.windowEnd = tokens
		if s.current == nil || now.Sub(s.refreshed) >= speedRefreshInterval {
			s.current, s.refreshed = s.windowSpeed, now
		}
	}
}

func (s *generationSpeed) finish(reported int) {
	if !s.active {
		return
	}
	s.active = false
	if elapsed := s.last.Sub(s.first); !s.first.IsZero() && elapsed > 0 {
		if reported <= 0 {
			reported = s.callTokens
		}
		s.totalTokens += reported
		s.totalTime += elapsed
	}
}

func (s *generationSpeed) average() *float64 {
	if s.totalTime <= 0 {
		return nil
	}
	value := float64(s.totalTokens) / s.totalTime.Seconds()
	return &value
}
