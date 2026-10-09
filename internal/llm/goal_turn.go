package llm

// GoalTurn is the marker of a user-role message the session supervisor wrote
// to start a goal turn (session.Manager.runSupervisedTurn).
type GoalTurn struct {
	// Kind is kickoff, continue, recover, resume or wrapup (acp.GoalTurn*).
	Kind string `json:"kind"`
	// Index numbers the automatic continuation, out of Limit; 0 for a
	// kickoff and a resume.
	Index int `json:"index,omitempty"`
	Limit int `json:"limit,omitempty"`
	// Objective is the goal the turn works toward, as the operator set it.
	Objective string `json:"objective"`
	// Reason is what the supervisor found: the check's verdict reason, the
	// stall or the error the turn recovers from.
	Reason    string   `json:"reason,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
}
