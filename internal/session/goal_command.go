package session

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
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
	// Model and Reasoning are the --model and --reasoning options, as typed:
	// the model and the reasoning level that check this goal.
	Model     string
	Reasoning string
	// Err is what was wrong with the options; the command is answered with
	// it and the usage.
	Err string
}

// GoalUsage closes the answer to a /goal command that could not run as typed.
const GoalUsage = "Usage: /goal [-m|--model <id>] [-r|--reasoning <level>] <objective> sets a goal and starts working on it; " +
	"/goal shows it; /goal pause, /goal resume [-m <id>] [-r <level>], /goal clear. --model and --reasoning " +
	"name the model and the reasoning level that check this goal: a configured models[].model (or a part of one that " +
	"matches exactly one model) and a level that model offers."

// goalClearWords are the arguments that remove the goal, the same set Claude
// Code accepts, so muscle memory carries over.
var goalClearWords = map[string]bool{"clear": true, "stop": true, "off": true, "cancel": true, "reset": true, "none": true}

// ParseGoalCommand reads a /goal command from typed text. Anything else is
// GoalCommandNone. Options come first, like /compact's: --model <id>,
// --reasoning <level> (or --model=<id>, --reasoning=<level>); the first word
// that is not an option starts the objective or names the subcommand, so an
// objective may mention an option without being read as one.
func ParseGoalCommand(text string) GoalCommand {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/goal") {
		return GoalCommand{}
	}
	rest := text[len("/goal"):]
	if rest != "" && !strings.ContainsAny(rest[:1], " \t\r\n") {
		return GoalCommand{} // "/goals", "/goal-x": another command
	}
	var cmd GoalCommand
	arg := strings.TrimSpace(rest)
	optioned := false
	for isGoalOption(arg) {
		optioned = true
		var word string
		word, arg = cutGoalWord(arg)
		name, value, inline := strings.Cut(word, "=")
		name = goalOptionName(name)
		if !inline {
			if arg == "" || isGoalOption(arg) {
				cmd.Err = fmt.Sprintf("%s needs a value", name)
				continue
			}
			value, arg = cutGoalWord(arg)
		}
		switch name {
		case "--model":
			cmd.Model = value
		case "--reasoning":
			cmd.Reasoning = value
		default:
			cmd.Err = fmt.Sprintf("unknown option %s", name)
			continue
		}
		if strings.TrimSpace(value) == "" {
			cmd.Err = fmt.Sprintf("%s needs a value", name)
		}
	}
	word := strings.ToLower(arg)
	switch {
	case arg == "" && optioned:
		cmd.Kind, cmd.Err = GoalCommandSet, firstNonEmpty(cmd.Err, "the goal objective is empty")
	case arg == "":
		cmd.Kind = GoalCommandShow
	case goalClearWords[word]:
		cmd.Kind = GoalCommandClear
	case word == "pause":
		cmd.Kind = GoalCommandPause
	case word == "resume":
		cmd.Kind = GoalCommandResume
	default:
		cmd.Kind, cmd.Objective = GoalCommandSet, arg
	}
	if optioned && cmd.Err == "" && (cmd.Kind == GoalCommandClear || cmd.Kind == GoalCommandPause) {
		cmd.Err = "--model and --reasoning go with a new goal or /goal resume"
	}
	return cmd
}

// isGoalOption reports whether text starts with an option word: a --word, or
// the short -m and -r (alone or as -m=value). A lone dash or any other -word
// starts the objective.
func isGoalOption(text string) bool {
	if strings.HasPrefix(text, "--") {
		return true
	}
	word, _ := cutGoalWord(text)
	name, _, _ := strings.Cut(word, "=")
	return name == "-m" || name == "-r"
}

// goalOptionName spells a short option out: -m is --model, -r --reasoning.
func goalOptionName(name string) string {
	switch name {
	case "-m":
		return "--model"
	case "-r":
		return "--reasoning"
	}
	return name
}

// cutGoalWord splits s at its first run of whitespace.
func cutGoalWord(s string) (word, rest string) {
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

// resolveGoalChecker turns the --model and --reasoning of a /goal command
// into the configured model and a level it offers. An empty option keeps what
// keep holds (the goal's own pair on a resume); "default" clears the level.
func resolveGoalChecker(cfg *config.Config, st *State, cmd GoalCommand, keep GoalState) (model, reasoning string, err error) {
	model, reasoning = keep.Model, keep.Reasoning
	if m := strings.TrimSpace(cmd.Model); m != "" {
		if model, err = cfg.MatchModelID(m); err != nil {
			return "", "", err
		}
		if strings.TrimSpace(cmd.Reasoning) == "" {
			reasoning = "" // a level chosen for another model may not exist on this one
		}
	}
	level := strings.ToLower(strings.TrimSpace(cmd.Reasoning))
	if level == "" {
		return model, reasoning, nil
	}
	if level == config.ReasoningDefault {
		return model, "", nil
	}
	checker := model
	if checker == "" {
		checker = GoalCheckModelFor(cfg, st, GoalState{})
	}
	choices := cfg.ReasoningChoicesFor(cfg.FindModelEntry(checker))
	if len(choices) == 0 {
		return "", "", fmt.Errorf("model %q offers no reasoning levels", checker)
	}
	for _, c := range choices {
		if c == level {
			return model, level, nil
		}
	}
	return "", "", fmt.Errorf("reasoning %q is not offered by model %q (offered: %s, default)", level, checker, strings.Join(choices, ", "))
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
func GoalStatusText(g GoalState, maxContinuations, tokenBudget int) string {
	if !g.Set() {
		return "No goal is set. /goal <objective> sets one and starts working on it."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Goal (%s): %s", g.Status, g.Objective)
	if r := strings.TrimSpace(g.StatusReason); r != "" {
		fmt.Fprintf(&b, "\nReason: %s", r)
	}
	if g.Model != "" || g.Reasoning != "" {
		fmt.Fprintf(&b, "\nChecked by: %s", strings.TrimSpace(firstNonEmpty(g.Model, "the supervisor's model")+" "+goalReasoningNote(g.Reasoning)))
	}
	fmt.Fprintf(&b, "\nContinuations: %d of %d · checks: %d", g.Continuations, maxContinuations, g.Checks)
	// What counts against the budget: since the last resume, like every
	// other surface shows it.
	if tokens := g.BudgetTokens(); tokens > 0 || tokenBudget > 0 {
		fmt.Fprintf(&b, " · tokens: %d", tokens)
		if tokenBudget > 0 {
			fmt.Fprintf(&b, " of %d", tokenBudget)
		}
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
	if g.Status == GoalComplete {
		return "", fmt.Errorf("the goal is complete; /goal <objective> sets a new one")
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
func (m *Manager) resumeGoal(st *State, model, reasoning string) error {
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
		g.Model, g.Reasoning = model, reasoning
		return true
	})
	return nil
}

func goalReasoningNote(level string) string {
	if level == "" {
		return ""
	}
	return "(reasoning " + level + ")"
}
