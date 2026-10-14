package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A goal chain (/goal --then-file, issue #512): when a goal with a follow-up
// file is met, the text of that file becomes the goal of a new session in the
// same workspace, on the same model, reasoning level and permission mode, and
// the file is renamed so it runs once. The worker of a chained goal is told
// to write the next task there (goalChainBlock), so the chain goes on for as
// long as work remains and ends when no file is left.

// goalChainReadLimit bounds what is read of a follow-up file: enough for the
// longest objective in any encoding, plus one byte to tell it was longer.
const goalChainReadLimit = MaxGoalObjectiveRunes*4 + 1

// GoalChainStarter runs the first turn of a session a goal chain opened: the
// prompt, with the options given, through HandleSessionPromptWithSender under
// the session's turn lock, on a surface where it can be followed. It returns
// once the turn has ended, with session.ErrSessionTurnBusy when the lock was
// held, which the chain tries again.
type GoalChainStarter func(ctx context.Context, params acp.SessionPromptParams, opts *PromptRunOpts) error

// SetGoalChainStarter installs what runs a chained session's first turn.
// coddy serve installs one; a process without it (a local console, a local
// coddy acp) refuses /goal --then-file, since nothing there could run a
// session nobody opened.
func (m *Manager) SetGoalChainStarter(start GoalChainStarter) { m.goalChainStart = start }

// goalChainRetry is the schedule on which a chained first turn is tried again
// while the new session's turn lock is held: first, doubling up to most, for
// at most giveUp in all. Zero values take the defaults.
type goalChainRetry struct {
	first, most, giveUp time.Duration
}

func (r goalChainRetry) withDefaults() goalChainRetry {
	if r.first <= 0 {
		r.first = time.Second
	}
	if r.most <= 0 {
		r.most = 15 * time.Second
	}
	if r.giveUp <= 0 {
		r.giveUp = 2 * time.Minute
	}
	return r
}

// continueGoalChain hands the next task of a met goal to a new session. done
// is the goal as it closed; verifyRan says its check asked the verifier (what
// the configuration says now may differ from what the check did), and
// unverified is why the verifier could not confirm it. Every way the chain
// stops leaves a notice in the finished session; the file is consumed only for
// a session that was created.
func (m *Manager) continueGoalChain(st *State, done GoalState, verifyRan bool, unverified string) {
	cfg := m.activeCfg()
	verified := done.LastCheck != nil && done.LastCheck.Verified
	switch {
	case verifyRan && !verified:
		why := firstNonEmpty(unverified, "the verifier did not confirm it")
		m.goalChainNotice(st, fmt.Sprintf("Goal chain stopped: the goal was not verified (%s)", why))
		return
	case cfg.Supervisor.ChainSessionLimit() == 0:
		m.goalChainNotice(st, "Goal chain stopped: goal chains are off (supervisor.max_chain_sessions: 0)")
		return
	case done.ChainIndex >= cfg.Supervisor.ChainSessionLimit():
		// A backstop: what ends a chain is a follow-up file nobody wrote.
		m.goalChainNotice(st, fmt.Sprintf("Goal chain stopped: it opened %d sessions (supervisor.max_chain_sessions)", cfg.Supervisor.ChainSessionLimit()))
		return
	case m.goalChainStart == nil:
		m.goalChainNotice(st, "Goal chain stopped: this process cannot start a session on its own; the chain continues under coddy serve")
		return
	}

	typed := strings.TrimSpace(done.ThenFile)
	path := goalChainPath(st.GetCWD(), typed)
	text, err := readGoalChainFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		m.goalChainNotice(st, "Goal chain ended: "+typed+" does not exist")
		return
	case err != nil:
		m.goalChainNotice(st, "Goal chain stopped: "+err.Error())
		return
	case strings.TrimSpace(text) == "":
		m.goalChainNotice(st, "Goal chain ended: "+typed+" is empty")
		return
	}
	next, err := NewGoal(text)
	if err != nil {
		m.goalChainNotice(st, fmt.Sprintf("Goal chain stopped: %s: %v", typed, err))
		return
	}
	next.Model, next.Reasoning = done.Model, done.Reasoning
	next.ThenFile, next.ChainIndex, next.ContinuedFrom = done.ThenFile, done.ChainIndex+1, st.ID

	// Consumed before the session exists: a crash in between runs the task
	// at most once, and its text stays in the renamed file.
	consumed, err := consumeGoalChainFile(path)
	if err != nil {
		m.goalChainNotice(st, "Goal chain stopped: "+err.Error())
		return
	}
	res, err := m.newSession(context.Background(), acp.SessionNewParams{CWD: st.GetCWD()}, "", "")
	var ns *State
	if err == nil {
		if ns = m.getSession(res.SessionID); ns == nil {
			err = fmt.Errorf("session %s is not live", res.SessionID)
		}
	}
	if err != nil {
		restoreGoalChainFile(consumed, path, m)
		m.goalChainNotice(st, "Goal chain stopped: the new session could not be created: "+err.Error())
		return
	}

	// The settings of the finished session, as its own: an inherited
	// permission mode is not the operator's latest choice
	// (rememberPermissionMode), and a session keeps it across a restart.
	ns.SetSelectedModelID(st.GetSelectedModelID())
	ns.SetSelectedReasoning(st.GetSelectedReasoning())
	ns.SetSelectedToolset(st.GetSelectedToolset())
	ns.SetPermissionMode(st.GetPermissionMode())
	ns.SetGoal(next)
	ns.AppendUILogNotice(0, fmt.Sprintf("Goal chain: continued from session %s (link %d of %d)", st.ID, next.ChainIndex, cfg.Supervisor.ChainSessionLimit()))

	link := "Goal chain: continued in session " + ns.ID
	st.UpdateGoal(link, func(g *GoalState) bool {
		if g.ID != done.ID {
			return false
		}
		g.ContinuedIn = ns.ID
		return true
	})
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), link)
	m.log.Info("goal chain continued", "from", st.ID, "to", ns.ID, "link", next.ChainIndex, "file", consumed)

	params := acp.SessionPromptParams{SessionID: ns.ID, Prompt: goalPrompt(goalKickoffText(next, cfg))}
	opts := &PromptRunOpts{SkipUsagePublish: true, goalKickoff: &chainKickoff{goalID: next.ID, marker: &llm.GoalTurn{
		Kind: acp.GoalTurnKickoff, Objective: next.Objective, Limit: cfg.Supervisor.ContinuationLimit(),
	}}}
	go m.startChainedTurn(ns, next.ID, params, opts)
}

// startChainedTurn runs the chained session's first turn through the starter,
// again while the session's turn lock is held and nobody has worked on its
// goal yet. A person's prompt that got there first ran under the supervisor
// on that goal, so the kickoff is dropped then.
func (m *Manager) startChainedTurn(st *State, goalID string, params acp.SessionPromptParams, opts *PromptRunOpts) {
	retry := m.goalChainRetry.withDefaults()
	delay, deadline := retry.first, time.Now().Add(retry.giveUp)
	for {
		err := m.goalChainStart(context.Background(), params, opts)
		if err == nil {
			return
		}
		pending := opts.goalKickoff != nil && m.chainKickoffPending(st, goalID)
		if errors.Is(err, ErrSessionTurnBusy) && pending && time.Now().Before(deadline) {
			time.Sleep(delay)
			delay = min(delay*2, retry.most)
			continue
		}
		m.log.Warn("goal chain: the first turn of the new session", "session", st.ID, "error", err)
		// Only a turn that never started leaves a notice: a turn that ran
		// recorded its own error, and a notice on a session being deleted
		// would write its bundle again.
		if pending && !errors.Is(err, ErrSessionGone) && !errors.Is(err, ErrSessionDeleting) {
			st.AppendUILogNotice(0, "Goal chain: the first turn did not start ("+err.Error()+"); /goal resume starts it")
		}
		return
	}
}

// chainKickoffPending reports whether the chained session is still waiting
// for its first turn: live, no turn of anybody's yet, and its goal still the
// chain's and active.
func (m *Manager) chainKickoffPending(st *State, goalID string) bool {
	if m.getSession(st.ID) != st || CountUserTurns(st.GetMessages()) > 0 {
		return false
	}
	g := st.GetGoal()
	return g.ID == goalID && g.Active()
}

// goalChainNotice tells the surfaces of the finished session how its chain
// stopped: a row in its transcript, and the notice of a goal update, which a
// messenger chat posts.
func (m *Manager) goalChainNotice(st *State, notice string) {
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), notice)
	m.PublishSessionGoal(st, notice)
	m.log.Info("goal chain stopped", "session", st.ID, "reason", notice)
}

// goalChainPath resolves the follow-up file as the operator typed it: an
// absolute path as it is, a leading ~ against the home folder, anything else
// against the session's workspace.
func goalChainPath(cwd, typed string) string {
	p := strings.TrimSpace(typed)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// readGoalChainFile reads a follow-up file, which must be a regular file: a
// FIFO would block the finished session's turn for good, and the open of one
// cannot be cancelled. At most goalChainReadLimit bytes are read; NewGoal
// refuses the text when it is longer than an objective may be.
func readGoalChainFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return "", fmt.Errorf("%s changed while it was read", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, goalChainReadLimit))
	if err != nil {
		return "", err
	}
	if len(data) >= goalChainReadLimit {
		return "", fmt.Errorf("%s is longer than a goal objective may be (%d characters)", path, MaxGoalObjectiveRunes)
	}
	return string(data), nil
}

// consumeGoalChainFile renames <stem><ext> to <stem>.started-<UTC><ext> next
// to it, never over a file that is already there, and returns the new path.
func consumeGoalChainFile(path string) (string, error) {
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext) + ".started-" + time.Now().UTC().Format("20060102T150405Z")
	for n := 1; n <= 100; n++ {
		target := stem + ext
		if n > 1 {
			target = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Rename(path, target); err != nil {
			return "", err
		}
		return target, nil
	}
	return "", fmt.Errorf("no free name to rename %s to", path)
}

// restoreGoalChainFile puts a consumed file back when its session could not
// be created, unless a new follow-up file took its place meanwhile.
func restoreGoalChainFile(consumed, path string, m *Manager) {
	if _, err := os.Lstat(path); err == nil {
		m.log.Warn("goal chain: a new follow-up file appeared; the consumed one stays renamed", "file", consumed)
		return
	}
	if err := os.Rename(consumed, path); err != nil {
		m.log.Warn("goal chain: could not restore the follow-up file", "file", consumed, "error", err)
	}
}
