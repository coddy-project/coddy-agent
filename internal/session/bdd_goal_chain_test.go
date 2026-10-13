package session_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

type goalChainWorld struct {
	t *testing.T
	h *chainHarness
}

func (w *goalChainWorld) workerWrites(text, rel string) error {
	w.h = newChainHarness(w.t)
	w.h.writesInFirstTurn(rel, text)
	return nil
}

func (w *goalChainWorld) send(text string) error {
	w.h.prompt(w.h.sid, text)
	return nil
}

// second is the session the first one's goal continued in, once its first
// turn has ended.
func (w *goalChainWorld) second() (*session.State, error) {
	id := w.h.waitStarted()
	if next := w.h.st(w.h.sid).GetGoal().ContinuedIn; next != id {
		return nil, fmt.Errorf("the first goal continued in %q, the chained turn ran in %q", next, id)
	}
	st := w.h.st(id)
	if st == nil {
		return nil, fmt.Errorf("the chained session %s is not live", id)
	}
	return st, nil
}

func (w *goalChainWorld) newSessionWorksOn(objective string) error {
	st, err := w.second()
	if err != nil {
		return err
	}
	if st.ID == w.h.sid || st.GetCWD() != w.h.st(w.h.sid).GetCWD() {
		return fmt.Errorf("chained session %s in %q, want a new session in %q", st.ID, st.GetCWD(), w.h.cwd)
	}
	if g := st.GetGoal(); g.Objective != objective || g.ChainIndex != 1 {
		return fmt.Errorf("chained goal = %+v, want %q at chain index 1", g, objective)
	}
	return nil
}

func (w *goalChainWorld) keepsFollowUp(rel string) error {
	next := w.h.st(w.h.st(w.h.sid).GetGoal().ContinuedIn)
	if got := next.GetGoal().ThenFile; got != rel {
		return fmt.Errorf("the chained goal's follow-up file = %q, want %q", got, rel)
	}
	return nil
}

func (w *goalChainWorld) firstIsKickoff() error {
	next := w.h.st(w.h.sid).GetGoal().ContinuedIn
	runs := w.h.runsIn(next)
	if len(runs) == 0 || runs[0].marker == nil || runs[0].marker.Kind != acp.GoalTurnKickoff || runs[0].marker.Objective != "write the release notes" {
		return fmt.Errorf("first run of the chained session = %+v, want the goal kickoff", runs)
	}
	if !strings.Contains(runs[0].prompt, "[Goal set]") {
		return fmt.Errorf("kickoff text = %q", runs[0].prompt)
	}
	return nil
}

func (w *goalChainWorld) renamed(rel string) error {
	if w.h.exists(rel) {
		return fmt.Errorf("%s is still there", rel)
	}
	if copies := w.h.startedCopies(rel); len(copies) != 1 {
		return fmt.Errorf("consumed copies of %s = %v, want one", rel, copies)
	}
	return nil
}

func (w *goalChainWorld) linked() error {
	first := w.h.st(w.h.sid)
	nextID := first.GetGoal().ContinuedIn
	next := w.h.st(nextID)
	if from := next.GetGoal().ContinuedFrom; from != w.h.sid {
		return fmt.Errorf("the chained goal continued from %q, want %q", from, w.h.sid)
	}
	if !hasNotice(first, "continued in session "+nextID) {
		return fmt.Errorf("the finished session's notices %q do not name %s", notices(first), nextID)
	}
	if !hasNotice(next, "continued from session "+w.h.sid) {
		return fmt.Errorf("the chained session's notices %q do not name %s", notices(next), w.h.sid)
	}
	return nil
}

func (w *goalChainWorld) secondComplete() error {
	st, err := w.second()
	if err != nil {
		return err
	}
	if g := st.GetGoal(); g.Status != session.GoalComplete {
		return fmt.Errorf("the second goal = %+v, want complete", g)
	}
	return nil
}

func (w *goalChainWorld) noThird() error {
	next := w.h.st(w.h.st(w.h.sid).GetGoal().ContinuedIn)
	if g := next.GetGoal(); g.ContinuedIn != "" {
		return fmt.Errorf("the second goal continued in %q", g.ContinuedIn)
	}
	if got := w.h.sessionsRun(); len(got) != 2 {
		return fmt.Errorf("turns ran in %d sessions, want 2: %v", len(got), got)
	}
	return nil
}

func (w *goalChainWorld) chainEnded(rel string) error {
	next := w.h.st(w.h.st(w.h.sid).GetGoal().ContinuedIn)
	if !hasNotice(next, "Goal chain ended: "+rel+" does not exist") {
		return fmt.Errorf("the second session's notices = %q", notices(next))
	}
	return nil
}

func TestGoalChainFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "goal-chain",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &goalChainWorld{t: t}
			sc.Step(`^a session whose worker writes "([^"]*)" to "([^"]*)" in its first turn$`, w.workerWrites)
			sc.Step(`^the operator sends "([^"]*)"$`, w.send)
			sc.Step(`^a new session in the same workspace works on the goal "([^"]*)"$`, w.newSessionWorksOn)
			sc.Step(`^its goal keeps the follow-up file "([^"]*)"$`, w.keepsFollowUp)
			sc.Step(`^its first message is the goal kickoff$`, w.firstIsKickoff)
			sc.Step(`^"([^"]*)" was renamed so it never runs twice$`, w.renamed)
			sc.Step(`^each session names the other one$`, w.linked)
			sc.Step(`^the goal of the second session is complete$`, w.secondComplete)
			sc.Step(`^no third session was opened$`, w.noThird)
			sc.Step(`^the second session says the chain ended because "([^"]*)" does not exist$`, w.chainEnded)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/goal_chain.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("goal chain feature failed")
	}
}
