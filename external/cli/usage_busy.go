//go:build cli

package cli

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The countdown of a call that waits for a free stream slot of a remote Coddy
// (docs/plans/remote-model-provider-phase2.md, 4.6; decided by the model of
// docs/plans/remote-model-provider-models/p2-d4-busy-notice.md) is state of its
// own on the console, beside the usage snapshot of the footer:
//
//   - an update whose blockers carry remote_busy belongs to the countdown of the
//     call that sent it, never to the snapshot; every other update is a snapshot,
//     and no snapshot and no "unsupported" answer touches the countdown;
//   - Resuming with Blocked renews it; the same update with both gone (and no
//     usage data) is its end. Both are stamped with the sender's clock in
//     FetchedAt, an RFC 3339 UTC string compared as a string;
//   - the slot holds the last accepted countdown and a tombstone, the largest
//     stamp of an end or of a countdown that was dropped. A countdown is accepted
//     iff its stamp is strictly greater than the tombstone and not older than the
//     slot's; an end iff it is not older than the slot's (it wins a tie), and it
//     then empties the slot and raises the tombstone. So a copy of a countdown
//     replayed after its end, by a relay, a mirror or a reconnect, is ignored;
//   - two nets of the surface's own, so a lost end cannot keep the countdown up:
//     the viewed session's turn ends (turnDone, the observed turn's end), and the
//     countdown's RetryInSec after receipt plus two seconds have passed on the
//     console's own clock (never against the server's absolute RetryAt);
//   - a change of the model's subject, or of the session, drops slot and
//     tombstone: they were about another call.

// remoteBusyExpiryGrace is added to a countdown's own budget before the console
// drops it on its own.
const remoteBusyExpiryGrace = 2 * time.Second

// remoteBusyExpired is the loop message of the countdown's expiry timer. gen
// names the countdown the timer was armed for: a timer that fired for a
// countdown since replaced finds another generation and does nothing.
type remoteBusyExpired struct{ gen uint64 }

// remoteBusyState is the countdown slot with its tombstone. It belongs to the
// UI goroutine.
type remoteBusyState struct {
	// held is the last accepted countdown, nil when none is on the row.
	held *acp.ProviderUsageUpdate
	// verb is the status row's text for held; the row is repainted on a drop
	// only while it still says this.
	verb string
	// tombstone is the largest stamp of an end, or of a countdown dropped.
	tombstone string
	// subject is the model selector slot and tombstone were last matched to.
	subject string
	// gen numbers the expiry timers; stopExpiry stops the armed one.
	gen        uint64
	stopExpiry func() bool
}

// isRemoteBusyEnd reports whether an update ends a countdown: the countdown's
// family without Blocked and without Resuming.
func isRemoteBusyEnd(u *acp.ProviderUsageUpdate) bool {
	return isRemoteBusy(u) && !u.Blocked && !u.Resuming
}

// sameBusySubject reports whether two updates of the countdown family are about
// one subject: the same row and the same alias, an update that names no alias (a
// remote that predates it) standing for the row's.
func sameBusySubject(a, b *acp.ProviderUsageUpdate) bool {
	return a.Provider == b.Provider && (a.Model == "" || b.Model == "" || a.Model == b.Model)
}

// laterStamp is the greater of two stamps, compared as strings; "" is the
// least.
func laterStamp(a, b string) string {
	if b > a {
		return b
	}
	return a
}

// applyRemoteBusy routes an update of the countdown family: the end, or a
// countdown (a renewal included).
func (a *App) applyRemoteBusy(u acp.ProviderUsageUpdate) {
	a.syncRemoteBusySubject()
	if isRemoteBusyEnd(&u) {
		a.endRemoteBusy(&u)
		return
	}
	a.renewRemoteBusy(&u)
}

// renewRemoteBusy accepts a countdown iff its stamp is greater than the
// tombstone and not older than the one held.
func (a *App) renewRemoteBusy(u *acp.ProviderUsageUpdate) {
	b := &a.remoteBusy
	// A tombstone is the stamp of something that ended: with none, any stamp
	// shows, an unstamped one included (a sender that predates the end update).
	if b.tombstone != "" && u.FetchedAt <= b.tombstone {
		return
	}
	if b.held != nil && u.FetchedAt < b.held.FetchedAt {
		return
	}
	held := *u
	b.held = &held
	// The turn is waiting for the remote's slot, not for a limit to lift: the
	// status row says so, nothing is armed to read again, and the reset note of
	// a limit wait that had the row before is not the row's any more.
	a.stopUsageResume()
	b.verb = remoteBusyPhrase(u.RetryAt, a.usageNow())
	a.setStatus(liveStatus{verb: b.verb, startedAt: time.Now()})
	a.armRemoteBusyExpiry(u)
}

// endRemoteBusy accepts an end iff it is not older than the countdown held; it
// then empties the slot and raises the tombstone. An end that finds no
// countdown still raises the tombstone, so an older copy that arrives after it
// stays out. The end of another subject's countdown ends nothing.
func (a *App) endRemoteBusy(u *acp.ProviderUsageUpdate) {
	b := &a.remoteBusy
	if b.held == nil {
		b.tombstone = laterStamp(b.tombstone, u.FetchedAt)
		return
	}
	if !sameBusySubject(b.held, u) || u.FetchedAt < b.held.FetchedAt {
		return
	}
	a.stopRemoteBusyExpiry()
	b.tombstone = laterStamp(b.tombstone, u.FetchedAt)
	b.held = nil
	a.releaseRemoteBusyRow()
}

// dropRemoteBusy is the nets' drop: the slot is emptied and the tombstone raised
// to the stamp of the countdown it held. It reports whether one was held and
// leaves the status row to the caller.
func (a *App) dropRemoteBusy() bool {
	b := &a.remoteBusy
	a.stopRemoteBusyExpiry()
	if b.held == nil {
		return false
	}
	b.tombstone = laterStamp(b.tombstone, b.held.FetchedAt)
	b.held = nil
	return true
}

// releaseRemoteBusyRow takes the status row back to waiting for the model after
// the countdown left, while a turn runs and only if the row still says what the
// countdown said: a row that has moved on (the first chunk, a tool, a limit
// wait) is never repainted.
func (a *App) releaseRemoteBusyRow() {
	b := &a.remoteBusy
	verb := b.verb
	b.verb = ""
	if verb == "" || a.stepStatus.verb != verb || a.stepStatus.step != "" {
		return
	}
	if a.turnActive || a.remoteTurnActive {
		a.setStatus(newWaitingStatus())
	}
}

// endRemoteBusyByTurn is the net of the viewed session's turn ending: the slot
// is dropped with the tombstone raised, and the row (which the turn's end
// clears anyway) is left alone.
func (a *App) endRemoteBusyByTurn() {
	a.dropRemoteBusy()
	a.remoteBusy.verb = ""
}

// expireRemoteBusy is the net of the countdown's own clock: its budget after
// receipt plus the grace has passed without an end.
func (a *App) expireRemoteBusy(gen uint64) {
	b := &a.remoteBusy
	if gen != b.gen || b.held == nil {
		return
	}
	a.dropRemoteBusy()
	a.releaseRemoteBusyRow()
}

// armRemoteBusyExpiry arms the expiry of the countdown just accepted, replacing
// the previous timer.
func (a *App) armRemoteBusyExpiry(u *acp.ProviderUsageUpdate) {
	b := &a.remoteBusy
	a.stopRemoteBusyExpiry()
	b.gen++
	gen, sessionID := b.gen, a.sessionID
	budget := time.Duration(max(u.RetryInSec, 0)) * time.Second
	b.stopExpiry = a.usageAfter(budget+remoteBusyExpiryGrace, func() {
		_ = a.Sender().SendSessionUpdate(sessionID, remoteBusyExpired{gen: gen})
	})
}

func (a *App) stopRemoteBusyExpiry() {
	b := &a.remoteBusy
	if b.stopExpiry != nil {
		b.stopExpiry()
		b.stopExpiry = nil
	}
}

// syncRemoteBusySubject drops slot and tombstone when the model's subject is no
// longer the one they were matched to. A running call's next countdown, newer
// than anything dropped, shows again by itself.
func (a *App) syncRemoteBusySubject() {
	b := &a.remoteBusy
	current := strings.TrimSpace(a.modelID)
	if b.subject == current {
		return
	}
	previous := b.subject
	b.subject = current
	if previous == "" {
		return
	}
	a.resetRemoteBusy()
}

// resetRemoteBusy forgets the countdown and its tombstone: another subject or
// another session is on screen.
func (a *App) resetRemoteBusy() {
	b := &a.remoteBusy
	held := a.dropRemoteBusy()
	b.tombstone = ""
	if held {
		a.releaseRemoteBusyRow()
	}
	b.verb = ""
}
