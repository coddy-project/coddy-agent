package session

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// GoalCommandKind is what a /goal command asks for.
type GoalCommandKind int

const (
	GoalCommandNone GoalCommandKind = iota
	// GoalCommandShow is a bare /goal.
	GoalCommandShow
	// GoalCommandSet is /goal <objective>: set the goal and start working.
	GoalCommandSet
	GoalCommandClear
	GoalCommandPause
	// GoalCommandResume is /goal resume: active again, with a fresh
	// continuation budget, and a turn starts at once.
	GoalCommandResume
)

// GoalCommand is one parsed /goal command.
type GoalCommand struct {
	Kind      GoalCommandKind
	Objective string
}

// goalClearWords are the arguments that remove the goal, the same set Claude
// Code accepts, so muscle memory carries over.
var goalClearWords = map[string]bool{"clear": true, "stop": true, "off": true, "cancel": true, "reset": true, "none": true}

// ParseGoalCommand reads a /goal command from typed text. Anything else is
// GoalCommandNone.
func ParseGoalCommand(text string) GoalCommand {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/goal") {
		return GoalCommand{}
	}
	rest := text[len("/goal"):]
	if rest != "" && !strings.ContainsAny(rest[:1], " \t\r\n") {
		return GoalCommand{} // "/goals", "/goal-x": another command
	}
	arg := strings.TrimSpace(rest)
	word := strings.ToLower(arg)
	switch {
	case arg == "":
		return GoalCommand{Kind: GoalCommandShow}
	case goalClearWords[word]:
		return GoalCommand{Kind: GoalCommandClear}
	case word == "pause":
		return GoalCommand{Kind: GoalCommandPause}
	case word == "resume":
		return GoalCommand{Kind: GoalCommandResume}
	default:
		return GoalCommand{Kind: GoalCommandSet, Objective: arg}
	}
}

// goalCommandOf is the /goal command a prompt is: one text block and nothing
// else (an attachment makes it an ordinary message).
func goalCommandOf(prompt []acp.ContentBlock) GoalCommand {
	if len(prompt) != 1 || prompt[0].Type != acp.ContentTypeText {
		return GoalCommand{}
	}
	return ParseGoalCommand(prompt[0].Text)
}

// GoalStatusText is the answer to a bare /goal on a surface that shows text.
func GoalStatusText(g GoalState, maxContinuations int) string {
	if !g.Set() {
		return "No goal is set. /goal <objective> sets one and starts working on it."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal (%s): %s", g.Status, g.Objective)
	if r := strings.TrimSpace(g.StatusReason); r != "" {
		fmt.Fprintf(&b, "\nReason: %s", r)
	}
	fmt.Fprintf(&b, "\nContinuations: %d of %d · checks: %d", g.Continuations, maxContinuations, g.Checks)
	if g.TokensUsed > 0 {
		fmt.Fprintf(&b, " · tokens: %d", g.TokensUsed)
	}
	if c := g.LastCheck; c != nil {
		line := string(c.Verdict)
		if c.Verified {
			line += ", verified"
		}
		if r := strings.TrimSpace(c.Reason); r != "" {
			line += ": " + r
		}
		fmt.Fprintf(&b, "\nLast check: %s", line)
		for _, item := range c.Remaining {
			fmt.Fprintf(&b, "\n- %s", item)
		}
	}
	switch g.Status {
	case GoalActive:
		b.WriteString("\n/goal pause stops the automatic continuations, /goal clear removes the goal.")
	case GoalPaused, GoalBlocked, GoalLimited:
		b.WriteString("\n/goal resume continues, /goal clear removes the goal.")
	}
	return b.String()
}

// SetGoalObjective replaces the session's goal with a fresh active one. The
// turn that works on it is the caller's: a prompt through the supervisor.
func (m *Manager) SetGoalObjective(sessionID, objective string) (GoalState, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return GoalState{}, fmt.Errorf("session not found: %s", sessionID)
	}
	goal, err := NewGoal(objective)
	if err != nil {
		return GoalState{}, err
	}
	st.SetGoalWithNotice(goal, "Goal set: "+goal.Objective)
	return goal, nil
}

// ClearGoal removes the session's goal. A turn working on it finishes the
// step it is in and gets no continuation.
func (m *Manager) ClearGoal(sessionID string) error {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	m.clearGoal(st)
	return nil
}

func (m *Manager) clearGoal(st *State) string {
	g := st.GetGoal()
	if !g.Set() {
		return "No goal is set."
	}
	notice := "Goal cleared: " + g.Objective
	st.SetGoalWithNotice(GoalState{}, notice)
	return notice
}

// PauseGoal stops the automatic continuations of the session's goal and
// keeps it. A turn working on it finishes the step it is in.
func (m *Manager) PauseGoal(sessionID string) (GoalState, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return GoalState{}, fmt.Errorf("session not found: %s", sessionID)
	}
	if _, err := m.pauseGoal(st, "paused by the operator"); err != nil {
		return GoalState{}, err
	}
	return st.GetGoal(), nil
}

func (m *Manager) pauseGoal(st *State, reason string) (string, error) {
	g := st.GetGoal()
	if !g.Set() {
		return "", fmt.Errorf("no goal is set")
	}
	if g.Status == GoalPaused {
		return "Goal is already paused.", nil
	}
	notice := "Goal paused: " + g.Objective
	st.UpdateGoal(notice, func(g *GoalState) bool {
		g.Status, g.StatusReason = GoalPaused, reason
		return true
	})
	return notice, nil
}

// resumeGoal makes the goal active again with a fresh continuation and token
// budget; the caller starts the turn.
func (m *Manager) resumeGoal(st *State) error {
	g := st.GetGoal()
	if !g.Set() {
		return fmt.Errorf("no goal is set; /goal <objective> sets one")
	}
	if g.Status == GoalComplete {
		return fmt.Errorf("the goal is complete; /goal <objective> sets a new one")
	}
	st.UpdateGoal("Goal resumed: "+g.Objective, func(g *GoalState) bool {
		g.Status, g.StatusReason = GoalActive, ""
		g.Continuations = 0
		g.TokensBase = g.TokensUsed
		return true
	})
	return nil
}
