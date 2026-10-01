package session

import "strings"

// GoalStatus is the supervisor's current verdict for a session goal.
type GoalStatus string

const (
	GoalActive   GoalStatus = "active"
	GoalComplete GoalStatus = "complete"
	GoalStopped  GoalStatus = "stopped"
)

// GoalState is the persisted objective and bounded supervisor progress.
type GoalState struct {
	Text          string     `json:"text,omitempty"`
	Status        GoalStatus `json:"status,omitempty"`
	Remaining     string     `json:"remaining,omitempty"`
	Continuations int        `json:"continuations,omitempty"`
}

// GetGoal returns a copy of the session's objective.
func (s *State) GetGoal() GoalState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.goal
}

// SetGoal replaces the objective and persists it with the session metadata.
func (s *State) SetGoal(goal GoalState) {
	goal.Text = strings.TrimSpace(goal.Text)
	goal.Remaining = strings.TrimSpace(goal.Remaining)
	s.mu.Lock()
	s.goal = goal
	s.mu.Unlock()
	s.touchPersist()
}

// RestoreGoalWithoutPersist restores an objective from a saved bundle.
func (s *State) RestoreGoalWithoutPersist(goal GoalState) {
	s.mu.Lock()
	s.goal = goal
	s.mu.Unlock()
}
