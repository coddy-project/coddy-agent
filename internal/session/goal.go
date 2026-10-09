package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// GoalStatus is where the supervisor stands with a session goal
// (acp.GoalStatus*).
type GoalStatus string

const (
	GoalActive   GoalStatus = acp.GoalStatusActive
	GoalPaused   GoalStatus = acp.GoalStatusPaused
	GoalBlocked  GoalStatus = acp.GoalStatusBlocked
	GoalComplete GoalStatus = acp.GoalStatusComplete
	GoalLimited  GoalStatus = acp.GoalStatusLimited
)

// MaxGoalObjectiveRunes bounds an objective: it is restated in every
// continuation, so a longer one belongs in a file the objective names.
const MaxGoalObjectiveRunes = 4000

// GoalVerdict is one answer of the supervisor's check.
type GoalVerdict string

const (
	GoalVerdictMet        GoalVerdict = "met"
	GoalVerdictNotMet     GoalVerdict = "not_met"
	GoalVerdictNeedsUser  GoalVerdict = "needs_user"
	GoalVerdictImpossible GoalVerdict = "impossible"
)

// GoalItem is one requirement the supervisor tracks (acp.GoalItem).
type GoalItem struct {
	Text     string `json:"text"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

// GoalCheck is the supervisor's last verdict (acp.GoalCheck).
type GoalCheck struct {
	Verdict   GoalVerdict `json:"verdict"`
	Reason    string      `json:"reason,omitempty"`
	Remaining []string    `json:"remaining,omitempty"`
	Verified  bool        `json:"verified,omitempty"`
	At        string      `json:"at,omitempty"`
	Model     string      `json:"model,omitempty"`
}

// GoalState is a session's persisted objective and the supervisor's progress
// on it. The zero value is "no goal".
type GoalState struct {
	ID            string     `json:"id,omitempty"`
	Objective     string     `json:"objective,omitempty"`
	Status        GoalStatus `json:"status,omitempty"`
	StatusReason  string     `json:"statusReason,omitempty"`
	SetAt         string     `json:"setAt,omitempty"`
	UpdatedAt     string     `json:"updatedAt,omitempty"`
	Continuations int        `json:"continuations,omitempty"`
	Checks        int        `json:"checks,omitempty"`
	ActiveMs      int64      `json:"activeMs,omitempty"`
	TokensUsed    int        `json:"tokensUsed,omitempty"`
	// TokensBase is TokensUsed when the goal was last resumed: the token
	// budget counts what was spent since.
	TokensBase int        `json:"tokensBase,omitempty"`
	LastCheck  *GoalCheck `json:"lastCheck,omitempty"`
	Checklist  []GoalItem `json:"checklist,omitempty"`
	// Model and Reasoning are the model and the reasoning level that check
	// this goal (/goal --model, --reasoning); empty falls back to
	// supervisor.model, then the session's model, and to its default level.
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// Set reports whether the state holds a goal.
func (g GoalState) Set() bool { return strings.TrimSpace(g.Objective) != "" }

// Active reports whether the supervisor checks and continues this goal.
func (g GoalState) Active() bool { return g.Set() && g.Status == GoalActive }

// BudgetTokens is what the goal has spent against its token budget.
func (g GoalState) BudgetTokens() int { return max(g.TokensUsed-g.TokensBase, 0) }

// clone copies the slices, so a caller never shares them with the state.
func (g GoalState) clone() GoalState {
	if g.LastCheck != nil {
		c := *g.LastCheck
		c.Remaining = append([]string(nil), c.Remaining...)
		g.LastCheck = &c
	}
	g.Checklist = append([]GoalItem(nil), g.Checklist...)
	return g
}

// NewGoal is a fresh active goal for objective. The objective is trimmed;
// an empty or overlong one is an error the operator reads.
func NewGoal(objective string) (GoalState, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return GoalState{}, fmt.Errorf("the goal objective is empty")
	}
	if n := utf8.RuneCountInString(objective); n > MaxGoalObjectiveRunes {
		return GoalState{}, fmt.Errorf("a goal objective is limited to %d characters (got %d); put the details in a file and name it in the goal", MaxGoalObjectiveRunes, n)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return GoalState{ID: newGoalID(), Objective: objective, Status: GoalActive, SetAt: now, UpdatedAt: now}, nil
}

func newGoalID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "goal_" + hex.EncodeToString(b[:])
}

// GetGoal returns a copy of the session's goal.
func (s *State) GetGoal() GoalState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.goal.clone()
}

// SetGoal replaces the goal, persists it and tells the surfaces. The zero
// value clears it.
func (s *State) SetGoal(goal GoalState) {
	s.replaceGoal(goal, "")
}

// SetGoalWithNotice is SetGoal with a one-line note for the surfaces.
func (s *State) SetGoalWithNotice(goal GoalState, notice string) {
	s.replaceGoal(goal, notice)
}

func (s *State) replaceGoal(goal GoalState, notice string) {
	s.updateGoal(notice, &goal, func(*GoalState) bool { return true })
}

// UpdateGoal changes the goal in place under the state lock: fn edits it and
// says whether anything changed. A change is persisted and published, with
// notice for the surfaces that show one.
func (s *State) UpdateGoal(notice string, fn func(*GoalState) bool) bool {
	return s.updateGoal(notice, nil, fn)
}

func (s *State) updateGoal(notice string, replace *GoalState, fn func(*GoalState) bool) bool {
	s.mu.Lock()
	next := s.goal.clone()
	if replace != nil {
		next = replace.clone()
	}
	if !fn(&next) {
		s.mu.Unlock()
		return false
	}
	next.Objective = strings.TrimSpace(next.Objective)
	if next.Set() {
		next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		next = GoalState{}
	}
	s.goal = next
	notify := s.goalNotify
	s.mu.Unlock()
	s.touchPersist()
	if notify != nil {
		notify(notice)
	}
	return true
}

// RestoreGoalWithoutPersist restores a goal from a saved bundle.
func (s *State) RestoreGoalWithoutPersist(goal GoalState) {
	if !goal.Set() {
		return
	}
	s.mu.Lock()
	s.goal = goal.clone()
	s.mu.Unlock()
}

// SetGoalNotifier installs what a goal change calls once the state lock is
// released: the manager publishes the snapshot (manager.go).
func (s *State) SetGoalNotifier(fn func(notice string)) {
	s.mu.Lock()
	s.goalNotify = fn
	s.mu.Unlock()
}

// attachGoalNotifier makes every change of st's goal publish itself, whoever
// made it: a command, the supervisor, an HTTP request.
func (m *Manager) attachGoalNotifier(st *State) {
	st.SetGoalNotifier(func(notice string) { m.PublishSessionGoal(st, notice) })
}

// goalVersionSeq numbers goal snapshots across the process, like the
// settings and the message queue: a session rebuilt from disk never reuses a
// version a client has already seen.
var goalVersionSeq atomic.Uint64

// GoalSnapshot is the goal as the surfaces show it, nil without one.
func GoalSnapshot(g GoalState, cfg *config.Config) *acp.SessionGoal {
	if !g.Set() {
		return nil
	}
	var sup config.Supervisor
	if cfg != nil {
		sup = cfg.Supervisor
	}
	out := &acp.SessionGoal{
		ID:               g.ID,
		Objective:        g.Objective,
		Status:           string(g.Status),
		StatusReason:     g.StatusReason,
		SetAt:            g.SetAt,
		UpdatedAt:        g.UpdatedAt,
		Continuations:    g.Continuations,
		MaxContinuations: sup.ContinuationLimit(),
		Checks:           g.Checks,
		ActiveMs:         g.ActiveMs,
		TokensUsed:       g.BudgetTokens(),
		TokenBudget:      sup.EffectiveTokenBudget(),
		Model:            g.Model,
		Reasoning:        g.Reasoning,
	}
	if c := g.LastCheck; c != nil {
		out.LastCheck = &acp.GoalCheck{
			Verdict:   string(c.Verdict),
			Reason:    c.Reason,
			Remaining: append([]string(nil), c.Remaining...),
			Verified:  c.Verified,
			At:        c.At,
			Model:     c.Model,
		}
	}
	for _, it := range g.Checklist {
		out.Checklist = append(out.Checklist, acp.GoalItem{Text: it.Text, Status: it.Status, Evidence: it.Evidence})
	}
	return out
}

// goalObservers is the manager's registry of goal observers.
type goalObservers struct {
	mu  sync.Mutex
	seq int
	fns map[int]func(acp.SessionGoalUpdate)
}

// AddSessionGoalObserver registers fn for every change of any session's goal
// and returns the function that removes it. The HTTP server puts the change on
// GET /coddy/events, so a browser that is not reading the turn's stream still
// mirrors it. fn runs on the goroutine that made the change and MUST NOT block.
func (m *Manager) AddSessionGoalObserver(fn func(acp.SessionGoalUpdate)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	o := &m.goalObs
	o.mu.Lock()
	if o.fns == nil {
		o.fns = make(map[int]func(acp.SessionGoalUpdate))
	}
	o.seq++
	id := o.seq
	o.fns[id] = fn
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.fns, id)
			o.mu.Unlock()
		})
	}
}

// SessionGoal returns the goal snapshot of a live session and its version.
func (m *Manager) SessionGoal(sessionID string) (acp.SessionGoalUpdate, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return acp.SessionGoalUpdate{}, fmt.Errorf("session not found: %s", sessionID)
	}
	return m.goalUpdate(st, ""), nil
}

func (m *Manager) goalUpdate(st *State, notice string) acp.SessionGoalUpdate {
	cfg := m.activeCfg()
	g := st.GetGoal()
	snap := GoalSnapshot(g, cfg)
	if snap != nil && cfg != nil {
		// The checker the next check runs on, resolved here because it may be
		// the session's own model.
		checker := GoalCheckerFor(cfg, st, g)
		snap.CheckModel, snap.CheckReasoning = checker.Model, checker.Reasoning
	}
	return acp.SessionGoalUpdate{
		SessionUpdate: acp.UpdateTypeSessionGoal,
		SessionID:     st.ID,
		Goal:          snap,
		Version:       goalVersionSeq.Add(1),
		Notice:        notice,
	}
}

// PublishSessionGoal tells everyone watching the session what its goal is
// now: the running turn's sender (or the manager's own between turns), and
// every observer.
func (m *Manager) PublishSessionGoal(st *State, notice string) acp.SessionGoalUpdate {
	update := m.goalUpdate(st, notice)
	sender := st.TurnSender()
	if sender == nil {
		sender = m.server
	}
	if sender != nil {
		_ = sender.SendSessionUpdate(st.ID, update)
	}
	o := &m.goalObs
	o.mu.Lock()
	fns := make([]func(acp.SessionGoalUpdate), 0, len(o.fns))
	for _, fn := range o.fns {
		fns = append(fns, fn)
	}
	o.mu.Unlock()
	for _, fn := range fns {
		fn(update)
	}
	return update
}

// GoalTurnUpdate is the session update that stands for a goal turn's first
// message on the wire.
func GoalTurnUpdate(t *llm.GoalTurn) acp.GoalTurnUpdate {
	u := acp.GoalTurnUpdate{SessionUpdate: acp.UpdateTypeGoalTurn}
	if t == nil {
		return u
	}
	u.Kind, u.Index, u.Limit = t.Kind, t.Index, t.Limit
	u.Objective, u.Reason = t.Objective, t.Reason
	u.Remaining = append([]string(nil), t.Remaining...)
	return u
}

// GoalTurnNote is the one-line text of a goal turn, for a surface that can
// only show text - a chat, an editor that renders no Coddy update.
func GoalTurnNote(u acp.GoalTurnUpdate) string {
	reason := strings.TrimSpace(u.Reason)
	switch u.Kind {
	case acp.GoalTurnKickoff:
		return "Goal set: " + u.Objective
	case acp.GoalTurnResume:
		return "Goal resumed: " + u.Objective
	case acp.GoalTurnWrapUp:
		return joinNote("Goal budget used up, wrapping up", reason)
	case acp.GoalTurnRecover:
		return joinNote(fmt.Sprintf("Goal recovery %d of %d", u.Index, u.Limit), reason)
	default:
		return joinNote(fmt.Sprintf("Goal continuation %d of %d", u.Index, u.Limit), reason)
	}
}

func joinNote(head, reason string) string {
	if reason == "" {
		return head
	}
	return head + ": " + reason
}

// SetTurnGoal hands the marker of the goal turn about to run to the agent,
// which puts it on the turn's first message (TakeTurnGoal); nil for a turn
// somebody typed.
func (s *State) SetTurnGoal(t *llm.GoalTurn) {
	s.mu.Lock()
	s.turnGoal = t
	s.mu.Unlock()
}

// TakeTurnGoal returns the marker SetTurnGoal left, once.
func (s *State) TakeTurnGoal() *llm.GoalTurn {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turnGoal
	s.turnGoal = nil
	return t
}

// setGoalCommandNotice keeps the answer of a /goal command for the prompt's
// result (HandleSessionPromptWithSender).
func (s *State) setGoalCommandNotice(notice string) {
	s.mu.Lock()
	s.goalCommandNotice = notice
	s.mu.Unlock()
}

func (s *State) takeGoalCommandNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.goalCommandNotice
	s.goalCommandNotice = ""
	return n
}

// GoalEndNote is the line a chat posts for a goal update: how the goal ended
// or where the supervisor left it. Empty for an update that says nothing new
// there - a plain resend, a goal set or resumed (its turn's note says so),
// the progress of a check.
func GoalEndNote(u acp.SessionGoalUpdate) string {
	n := strings.TrimSpace(u.Notice)
	for _, prefix := range []string{"Goal set:", "Goal resumed:", "Goal check:", "Goal verification:"} {
		if strings.HasPrefix(n, prefix) {
			return ""
		}
	}
	return n
}
