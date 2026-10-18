//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The wait of a call to a model another Coddy shares for a free stream slot of
// the remote arrives as a resuming provider_usage update with the blocker
// remote_busy (internal/agent/coddy_provider.go). It is no usage limit, and
// the console must not say it is.

// Stamps of the countdown's updates: the agent's clock at sending, an RFC 3339
// UTC string the surfaces compare as a string.
const (
	busyStamp1 = "2026-09-06T17:47:12Z"
	busyStamp2 = "2026-09-06T17:47:32Z"
	busyStamp3 = "2026-09-06T17:47:52Z"
)

func remoteBusyUpdate() acp.ProviderUsageUpdate {
	return acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "lab",
		ProviderType:  "coddy",
		Model:         "terra",
		ObservedAt:    busyStamp1,
		FetchedAt:     busyStamp1,
		Blocked:       true,
		Blockers:      []string{"remote_busy"},
		Resuming:      true,
		RetryAt:       "2026-09-06T17:48:12Z",
		RetryInSec:    60,
	}
}

// remoteBusyEnd is the update that ends the countdown of remoteBusyUpdate: the
// same family (the blocker, the row and the alias), no Resuming, no Blocked, no
// retry fields and no usage data, stamped with the clock at sending.
func remoteBusyEnd(stamp string) acp.ProviderUsageUpdate {
	return acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "lab",
		ProviderType:  "coddy",
		Model:         "terra",
		ObservedAt:    stamp,
		FetchedAt:     stamp,
		Blockers:      []string{"remote_busy"},
	}
}

// remoteBusyNotice is remoteBusyUpdate at another stamp and budget.
func remoteBusyNotice(stamp string, retryInSec int) acp.ProviderUsageUpdate {
	u := remoteBusyUpdate()
	u.ObservedAt, u.FetchedAt, u.RetryInSec = stamp, stamp, retryInSec
	return u
}

// busyTimers stands in for the console's timer factory: it keeps every timer
// armed and whether it was stopped, so a test fires or inspects them.
type busyTimers struct {
	arms    []*busyArm
	stopped int
}

type busyArm struct {
	d       time.Duration
	fn      func()
	stopped bool
}

func (b *busyTimers) after(d time.Duration, fn func()) func() bool {
	arm := &busyArm{d: d, fn: fn}
	b.arms = append(b.arms, arm)
	return func() bool {
		if !arm.stopped {
			arm.stopped = true
			b.stopped++
		}
		return true
	}
}

// live is the timers not stopped.
func (b *busyTimers) live() []*busyArm {
	var out []*busyArm
	for _, arm := range b.arms {
		if !arm.stopped {
			out = append(out, arm)
		}
	}
	return out
}

// remoteBusyApp is a console on the row "lab/terra" in session s1, its timers
// replaced by busyTimers so a test sees what is armed and fires it by hand.
func remoteBusyApp(t *testing.T) (*App, *busyTimers) {
	t.Helper()
	a := newTestApp(t)
	a.modelID = "lab/terra"
	a.sessionID = "s1"
	a.foot.SetModel(a.modelID, "")
	a.mgr = &usageBackend{}
	timers := &busyTimers{}
	a.usageAfterFn = timers.after
	return a, timers
}

func TestRemoteBusyUpdateSaysTheCallWaitsForAFreeSlotOfTheRemote(t *testing.T) {
	a, timers := remoteBusyApp(t)
	u := remoteBusyUpdate()
	a.applyProviderUsage(u)

	got := a.statusMessage()
	want := "Waiting for a free slot on the remote · resumes on its own, gives up at " +
		formatResetTime(parseUsageTime(u.RetryAt), a.usageNow())
	if got != want {
		t.Fatalf("status row = %q, want %q", got, want)
	}
	if strings.Contains(strings.ToLower(got), "usage limit") {
		t.Fatalf("status row %q calls a wait for a slot a usage limit", got)
	}
	if a.foot.Usage() != nil {
		t.Fatal("the wait must not replace the footer's usage snapshot: the row has none")
	}
	if len(a.chat.Children()) != 0 {
		t.Fatalf("the wait posted %d transcript rows, want none", len(a.chat.Children()))
	}
	// No reset note and no usage timer: the one timer is the countdown's own
	// expiry, its budget after receipt plus two seconds.
	if a.usageResume != nil || a.usageTimer != nil {
		t.Fatal("the wait armed a reset note or a usage timer")
	}
	if live := timers.live(); len(live) != 1 || live[0].d != 62*time.Second {
		t.Fatalf("armed timers = %+v, want the one expiry at 62s", live)
	}

	// The countdown is re-sent while the wait lasts: the clock of the row is
	// not restarted by it, and the expiry is counted again from the new receipt.
	started := a.stepStatus.startedAt
	renewed := remoteBusyNotice(busyStamp2, 40)
	a.applyProviderUsage(renewed)
	if a.stepStatus.startedAt != started {
		t.Fatal("a re-sent countdown must not restart the status clock")
	}
	if live := timers.live(); len(live) != 1 || live[0].d != 42*time.Second {
		t.Fatalf("armed timers after a re-send = %+v, want the one expiry at 42s", live)
	}
}

func TestRemoteBusyUpdateWithoutADeadlineNamesNoTime(t *testing.T) {
	a, _ := remoteBusyApp(t)
	u := remoteBusyUpdate()
	u.RetryAt, u.RetryInSec = "", 0
	a.applyProviderUsage(u)
	if got, want := a.statusMessage(), "Waiting for a free slot on the remote · resumes on its own"; got != want {
		t.Fatalf("status row = %q, want %q", got, want)
	}
}

func TestRemoteBusyEndsWithTheEndUpdateAndTheRowGoesBackToTheModel(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.turnActive = true
	a.applyProviderUsage(remoteBusyUpdate())
	if !strings.HasPrefix(a.statusMessage(), "Waiting for a free slot") {
		t.Fatalf("status row = %q", a.statusMessage())
	}

	// The end of another row's wait says nothing about this one.
	other := remoteBusyEnd(busyStamp2)
	other.Provider = "elsewhere"
	a.applyProviderUsage(other)
	if !strings.HasPrefix(a.statusMessage(), "Waiting for a free slot") {
		t.Fatalf("an end for another row ended the wait: %q", a.statusMessage())
	}
	// So does the end of another alias's wait of the same row.
	sibling := remoteBusyEnd(busyStamp2)
	sibling.Model = "luna"
	a.applyProviderUsage(sibling)
	if !strings.HasPrefix(a.statusMessage(), "Waiting for a free slot") {
		t.Fatalf("an end for another alias ended the wait: %q", a.statusMessage())
	}

	// The call got its slot: the agent sends the end, and the row reads as
	// waiting for the model until the call streams.
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if got := a.statusMessage(); !strings.HasPrefix(got, statusWaitingModel) {
		t.Fatalf("after the wait the row reads %q, want %q", got, statusWaitingModel)
	}

	// Later ends (the call returned, the turn released) leave the row of the
	// next phase alone.
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(remoteBusyEnd(busyStamp3))
	if got := a.statusMessage(); !strings.Contains(got, "Thinking") {
		t.Fatalf("a later end changed the row to %q", got)
	}
}

func TestRemoteBusyDoesNotOutliveTheTurn(t *testing.T) {
	a, _ := remoteBusyApp(t)
	a.applyProviderUsage(remoteBusyUpdate())
	a.turnSessionID = "s1"
	a.applyLoopMessage(updateMsg{update: turnDone{sessionID: "s1"}})
	// An end that arrives in the next turn must not repaint its row.
	a.turnActive = true
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if got := a.statusMessage(); !strings.Contains(got, "Thinking") {
		t.Fatalf("an end repainted the next turn's row: %q", got)
	}
}

// A limit wait keeps its wording and its reset note, and takes over the row
// from a wait for a slot.
func TestUsageLimitWaitStillSaysUsageLimit(t *testing.T) {
	a, timers := remoteBusyApp(t)
	a.applyProviderUsage(remoteBusyUpdate())
	limit := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "neuraldeep",
		ProviderType:  "neuraldeep",
		Blocked:       true,
		Blockers:      []string{"session_exhausted"},
		Resuming:      true,
		RetryAt:       "2026-09-06T17:59:59Z",
		RetryInSec:    767,
	}
	a.applyProviderUsage(limit)
	want := "Usage limit reached · resuming at " + formatResetTime(parseUsageTime(limit.RetryAt), a.usageNow())
	if got := a.statusMessage(); got != want {
		t.Fatalf("status row = %q, want %q", got, want)
	}
	if a.usageResume == nil {
		t.Fatal("a limit wait must arm its reset note")
	}
	if live := timers.live(); len(live) != 2 {
		t.Fatalf("armed timers = %d, want the countdown's expiry and the limit wait's reset note", len(live))
	}
	// The end of the earlier wait for a slot no longer applies to the row.
	a.turnActive = true
	a.applyProviderUsage(remoteBusyEnd(busyStamp2))
	if got := a.statusMessage(); got != want {
		t.Fatalf("an end for the old wait changed the row to %q", got)
	}
}

// A snapshot that carries the blocker without resuming (no surface sends one
// today) reads right on the footer, in the notice and in /usage too.
func TestRemoteBusyBlockerReadsRightInTheFooterNoticeAndReport(t *testing.T) {
	now := time.Date(2026, 9, 6, 17, 47, 12, 0, time.UTC)
	u := remoteBusyUpdate()
	u.Resuming = false

	segs := usageFooterSegments(&u, "lab/terra", now)
	if len(segs) != 1 {
		t.Fatalf("segments = %+v, want one", segs)
	}
	if want := "waiting for a free slot on the remote (gives up at " + formatResetTime(parseUsageTime(u.RetryAt), now) + ")"; segs[0].text != want {
		t.Fatalf("footer segment = %q, want %q", segs[0].text, want)
	}
	if segs[0].role != roleWarning {
		t.Fatalf("footer role = %q, want the warning role: nothing was refused", segs[0].role)
	}
	if strings.Contains(segs[0].text, "limit") {
		t.Fatalf("footer segment %q calls the wait a limit", segs[0].text)
	}

	if got := blockedNotice(segs[0].text); !strings.HasPrefix(got, "Waiting for a free slot on the remote") {
		t.Fatalf("notice = %q", got)
	}

	report := strings.Join(usageReportLines(&u, "lab/terra", now), "\n")
	if !strings.Contains(report, "waiting for a free slot on the remote") || strings.Contains(report, "blocked: remote_busy") {
		t.Fatalf("/usage report = %q", report)
	}

	// Without a deadline the footer names none.
	u.RetryAt = ""
	segs = usageFooterSegments(&u, "lab/terra", now)
	if len(segs) != 1 || segs[0].text != "waiting for a free slot on the remote" {
		t.Fatalf("segments without a deadline = %+v", segs)
	}
}

func TestRemoteBusyNoticeIsAWarningPostedOncePerWait(t *testing.T) {
	a := newTestApp(t)
	a.modelID = "lab/terra"
	u := remoteBusyUpdate()
	u.Resuming = false
	a.noticeUsage(&u)
	// The deadline moves by a second between re-sends: still the same wait.
	u.RetryAt = "2026-09-06T17:48:13Z"
	a.noticeUsage(&u)
	text := transcriptText(a)
	if strings.Count(text, "Waiting for a free slot on the remote") != 1 {
		t.Fatalf("transcript = %q, want one notice for the wait", text)
	}
	if strings.Contains(text, "Usage") {
		t.Fatalf("transcript = %q calls the wait a usage matter", text)
	}
}
