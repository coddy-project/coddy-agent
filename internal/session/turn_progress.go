package session

import "time"

// TurnProgress is how far the turn a session is running has come: when it was
// admitted and how many tokens the model has generated in it so far. The agent
// loop writes it, and a client that joins the turn late reads it, because the
// updates that carried it are not replayed once the transcript holds what they
// described.
type TurnProgress struct {
	StartedAt time.Time
	// OutputTokens is what the provider reported for the turn's completed
	// calls plus an estimate of what the call in flight has streamed.
	OutputTokens int
	// Estimated says an estimate is part of OutputTokens.
	Estimated        bool
	TokensPerSecond  *float64
	GenerationTokens int
	GenerationTime   time.Duration
}

// SetTurnGeneration records the progress and generation measurement together,
// so an activity read cannot pair a new count with an older speed.
func (s *State) SetTurnGeneration(tokens int, estimated bool, speed *float64, generationTokens int, generationTime time.Duration) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if !s.progressSet {
		return
	}
	s.progress.OutputTokens, s.progress.Estimated = tokens, estimated
	s.progress.TokensPerSecond = speed
	s.progress.GenerationTokens, s.progress.GenerationTime = generationTokens, generationTime
}

// BeginTurnProgress opens the progress of a turn admitted at startedAt. A
// prompt that delegates to RunPlan is admitted twice for one turn and hands in
// the same start both times, so the second call keeps what the first recorded.
func (s *State) BeginTurnProgress(startedAt time.Time) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progressSet && s.progress.StartedAt.Equal(startedAt) {
		return
	}
	s.progress = TurnProgress{StartedAt: startedAt}
	s.progressSet = true
}

// SetTurnOutputTokens records the turn's token count so far. It is a no-op
// outside a turn.
func (s *State) SetTurnOutputTokens(tokens int, estimated bool) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if !s.progressSet {
		return
	}
	s.progress.OutputTokens = tokens
	s.progress.Estimated = estimated
}

// TurnProgress returns the progress of the running turn; ok is false when the
// session is not running one.
func (s *State) TurnProgress() (TurnProgress, bool) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.progress, s.progressSet
}

// EndTurnProgress closes the progress of the turn admitted at startedAt. A
// turn that was admitted while this one was releasing has a start of its own
// and is left alone.
func (s *State) EndTurnProgress(startedAt time.Time) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progressSet && s.progress.StartedAt.Equal(startedAt) {
		s.progress = TurnProgress{}
		s.progressSet = false
	}
}
