//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

// The countdown of a call that waits for a free slot of a remote Coddy is state
// of its own beside the usage snapshot (docs/plans/remote-model-provider-phase2.md,
// 4.6): ended by an explicit end update on the countdown's own fields, guarded
// by a strict tombstone and by two local nets. These tests hold the console to
// that state machine, one rule each.

// busySnapshot is a real usage snapshot of the alias the countdown belongs to.
func busySnapshot(fetchedAt string, usedPercent float64) acp.ProviderUsageUpdate {
	return acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "lab",
		ProviderType:  "coddy",
		Model:         "terra",
		ObservedAt:    fetchedAt,
		FetchedAt:     fetchedAt,
		Windows: []acp.UsageWindow{
			{ID: "session", Label: "3h", UsedPercent: usedPercent, ResetsAt: "2026-09-06T19:00:00Z", ResetInSec: 4000},
		},
	}
}

func busyRowShows(a *App, want string) bool { return strings.HasPrefix(a.statusMessage(), want) }

const waitingForSlot = "Waiting for a free slot on the remote"

// A snapshot that lands during a wait leaves the countdown on the row, and the
// end leaves the snapshot on the footer: the two never touch each other.
func TestRemoteBusyAndTheSnapshotNeverTouchEachOther(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.foot.now = func() time.Time { return usageNow }
	a.foot.SetModel(a.modelID, "")
	a.turnActive = true

	a.applyProviderUsage(busySnapshot("2026-09-06T17:40:00Z", 41))
	a.applyProviderUsage(remoteBusyUpdate())
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("status row = %q", a.statusMessage())
	}
	if u := a.foot.Usage(); u == nil || u.Windows[0].UsedPercent != 41 {
		t.Fatalf("the countdown replaced the snapshot on the footer: %+v", u)
	}

	// A snapshot with a later stamp than the countdown's: the row stays.
	a.applyProviderUsage(busySnapshot("2026-09-06T17:50:00Z", 43))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("a snapshot ended the wait: %q", a.statusMessage())
	}
	if u := a.foot.Usage(); u == nil || u.Windows[0].UsedPercent != 43 {
		t.Fatalf("the snapshot during a wait was not adopted: %+v", u)
	}

	// The end empties the countdown and leaves the snapshot where it is.
	a.applyProviderUsage(remoteBusyEnd("2026-09-06T17:51:00Z"))
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("status row after the end = %q", a.statusMessage())
	}
	if u := a.foot.Usage(); u == nil || u.Windows[0].UsedPercent != 43 {
		t.Fatalf("the end wiped the snapshot: %+v", u)
	}
	if len(a.chat.Children()) != 0 {
		t.Fatalf("the countdown posted %d transcript rows, want none", len(a.chat.Children()))
	}
}

// An answer that says the subject has no usage drops its snapshot and nothing
// else: the countdown has an end of its own.
func TestAnUnsupportedAnswerLeavesTheCountdownAlone(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.foot.SetModel(a.modelID, "")
	a.turnActive = true
	a.applyProviderUsage(busySnapshot("2026-09-06T17:40:00Z", 41))
	a.applyProviderUsage(remoteBusyUpdate())

	a.applyProviderUsage(acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: "lab", ProviderType: "coddy", Model: "terra", Unsupported: true,
	})
	if a.foot.Usage() != nil {
		t.Fatal("an unsupported answer must take the snapshot of its subject down")
	}
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("an unsupported answer ended the wait: %q", a.statusMessage())
	}
	// Without a model the answer is about the whole row, the aliases too.
	a.applyProviderUsage(busySnapshot("2026-09-06T17:41:00Z", 42))
	a.applyProviderUsage(acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: "lab", ProviderType: "coddy", Unsupported: true})
	if a.foot.Usage() != nil || !busyRowShows(a, waitingForSlot) {
		t.Fatalf("a row-wide unsupported answer: usage=%v row=%q", a.foot.Usage(), a.statusMessage())
	}
}

// An end older than the countdown it would end is ignored; one that is not
// older ends it, a tie included.
func TestAnOlderEndDoesNotEndTheCountdownAndATiedEndDoes(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	a.applyProviderUsage(remoteBusyEnd(busyStamp1))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("an end older than the countdown ended it: %q", a.statusMessage())
	}
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("an end with the countdown's own stamp must end it, row = %q", a.statusMessage())
	}
}

// An old copy of a countdown, replayed after its end by a relay, a mirror or a
// reconnect, finds the tombstone: the stamp must be strictly greater.
func TestAReplayedCountdownAfterTheEndIsIgnored(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 60))
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("row after the end = %q", a.statusMessage())
	}

	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 60))
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("a replayed countdown revived the wait: %q", a.statusMessage())
	}
	// A countdown of the same second as the end is a tie, and the end wins it.
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("a countdown tied with the end revived the wait: %q", a.statusMessage())
	}
	// The next wait of the call is newer than the end and shows again.
	a.applyProviderUsage(remoteBusyNotice(busyStamp3, 60))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("a newer countdown was refused: %q", a.statusMessage())
	}
}

// An end that arrives with no countdown held (it overtook its countdown, or the
// countdown was dropped by a net) still raises the tombstone.
func TestAnEndWithNothingHeldStillRaisesTheTombstone(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 60))
	if busyRowShows(a, waitingForSlot) {
		t.Fatalf("a countdown older than an end already seen showed: %q", a.statusMessage())
	}
}

// A countdown older than the one held is a stale copy and is ignored.
func TestACountdownOlderThanTheHeldOneIsIgnored(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	arms := len(timers.arms)
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 5))
	if len(timers.arms) != arms {
		t.Fatalf("a stale countdown re-armed the expiry: %d arms", len(timers.arms))
	}
}

// The end of the viewed session's turn drops the countdown and raises the
// tombstone to its stamp, so a copy of it that lands later stays out.
func TestTheEndOfTheTurnDropsTheCountdown(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.turnSessionID = "s1"
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	a.applyLoopMessage(updateMsg{update: turnDone{sessionID: "s1"}})
	if len(timers.live()) != 0 {
		t.Fatalf("the turn's end left %d timers armed", len(timers.live()))
	}
	a.turnActive = true
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	if busyRowShows(a, waitingForSlot) {
		t.Fatalf("a copy of the dropped countdown revived it: %q", a.statusMessage())
	}
	a.applyProviderUsage(remoteBusyNotice(busyStamp3, 60))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("the next turn's countdown was refused: %q", a.statusMessage())
	}
}

// A turn another client owns ends when the activity events say so.
func TestAnObservedTurnEndDropsTheCountdown(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.remoteTurnActive = true
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	a.applyLoopMessage(updateMsg{sessionID: "s1", update: remote.ActivityUpdate{Revision: 1, TurnActive: true}})
	if len(timers.live()) != 1 {
		t.Fatal("an activity update that keeps the turn running dropped the countdown")
	}
	a.applyLoopMessage(updateMsg{sessionID: "s1", update: remote.ActivityUpdate{Revision: 2, TurnActive: false}})
	if len(timers.live()) != 0 {
		t.Fatalf("the observed turn's end left %d timers armed", len(timers.live()))
	}
	a.remoteTurnActive = true
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	if busyRowShows(a, waitingForSlot) {
		t.Fatalf("a copy of the dropped countdown revived it: %q", a.statusMessage())
	}
}

// A countdown nobody ends (the end was lost on the way) is taken down when its
// budget after receipt plus two seconds has passed on the console's own clock,
// whatever the server's absolute retry time says.
func TestTheCountdownExpiresOnItsOwnBudgetAfterReceipt(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.turnActive = true
	u := remoteBusyNotice(busyStamp1, 30)
	u.RetryAt = "2031-01-01T00:00:00Z" // a server clock far ahead changes nothing
	a.applyProviderUsage(u)
	live := timers.live()
	if len(live) != 1 || live[0].d != 32*time.Second {
		t.Fatalf("expiry timers = %+v, want one at 32s", live)
	}

	live[0].fn()
	msg := <-a.updatesCh
	if _, ok := msg.update.(remoteBusyExpired); !ok || msg.sessionID != "s1" {
		t.Fatalf("the expiry posted %+v", msg)
	}
	a.applyLoopMessage(msg)
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("after the expiry the row reads %q", a.statusMessage())
	}
	// The dropped countdown's own stamp is behind the tombstone now.
	a.applyProviderUsage(u)
	if busyRowShows(a, waitingForSlot) {
		t.Fatalf("a replay of the expired countdown revived it: %q", a.statusMessage())
	}
}

// A renewal replaces the expiry: the old timer is stopped, and a message of a
// timer that already fired for the replaced countdown does nothing.
func TestARenewedCountdownReplacesItsExpiry(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 30))
	first := timers.arms[0]
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 30))
	if !first.stopped {
		t.Fatal("the renewal left the old expiry armed")
	}
	if live := timers.live(); len(live) != 1 {
		t.Fatalf("armed expiry timers = %d, want one", len(live))
	}
	// The old timer had fired before it was stopped: its message is stale.
	first.fn()
	a.applyLoopMessage(<-a.updatesCh)
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("a stale expiry ended the renewed countdown: %q", a.statusMessage())
	}
}

// An end stops the expiry; so does the replacement of the row by another wait.
func TestTheEndStopsTheExpiry(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 30))
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if len(timers.live()) != 0 {
		t.Fatalf("the end left %d timers armed", len(timers.live()))
	}
}

// The row is repainted only while it still shows the countdown: a row that has
// moved on (the first chunk, a tool) is never taken back to waiting.
func TestTheEndRepaintsOnlyARowThatStillShowsTheCountdown(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyUpdate())
	a.setStatus(newModelStatus("Responding"))
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if got := a.statusMessage(); !strings.Contains(got, "Responding") {
		t.Fatalf("the end repainted a row that had moved on: %q", got)
	}
}

// A change of the model's subject drops the countdown and its tombstone: they
// were about the old one.
func TestAChangeOfSubjectDropsTheCountdownAndItsTombstone(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyEnd(busyStamp3)) // raises the tombstone
	a.applyProviderUsage(remoteBusyNotice(busyStamp3, 60))
	if busyRowShows(a, waitingForSlot) {
		t.Fatalf("precondition: the tombstone must refuse the countdown, row = %q", a.statusMessage())
	}
	a.applyProviderUsage(remoteBusyNotice("2026-09-06T17:48:12Z", 60))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("precondition: a newer countdown shows, row = %q", a.statusMessage())
	}

	a.modelID = "lab/luna"
	a.refreshFooterModel()
	if len(timers.live()) != 0 {
		t.Fatalf("the change of subject left %d timers armed", len(timers.live()))
	}
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("after the change the row reads %q", a.statusMessage())
	}
	// The tombstone went with it: a stamp the old one would have refused shows.
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 60))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("the tombstone survived the change of subject: %q", a.statusMessage())
	}
}

// A switch to another session drops whatever the one on screen was counting.
func TestASessionSwitchDropsTheCountdown(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.applyProviderUsage(remoteBusyNotice(busyStamp2, 60))
	a.adoptSession("s2", nil, nil)
	if len(timers.live()) != 0 {
		t.Fatalf("the switch left %d timers armed", len(timers.live()))
	}
	a.applyProviderUsage(remoteBusyNotice(busyStamp1, 60))
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("the new session's countdown was refused: %q", a.statusMessage())
	}
}

// An update whose countdown has no stamp (a sender that predates the end
// update) still shows: the tombstone refuses only what a stamp can be compared
// with.
func TestACountdownWithoutAStampStillShows(t *testing.T) {
	a, _ := remoteBusyApp(t)
	u := remoteBusyUpdate()
	u.FetchedAt, u.ObservedAt = "", ""
	a.applyProviderUsage(u)
	if !busyRowShows(a, waitingForSlot) {
		t.Fatalf("an unstamped countdown was refused: %q", a.statusMessage())
	}
}

// The countdown of a row that has no alias in the update (a remote that
// predates the per-alias subject) is the row's: its end ends it.
func TestAnAliasLessCountdownIsEndedByAnAliasLessEnd(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	u := remoteBusyUpdate()
	u.Model = ""
	a.applyProviderUsage(u)
	end := remoteBusyEnd(busyStamp2)
	end.Model = ""
	a.applyProviderUsage(end)
	if !busyRowShows(a, statusWaitingModel) {
		t.Fatalf("the row's end left the countdown up: %q", a.statusMessage())
	}
}
