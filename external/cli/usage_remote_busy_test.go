//go:build cli

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The wait of a call to a model another Coddy shares for a free stream slot of
// the remote arrives as a resuming provider_usage update with the blocker
// remote_busy (internal/agent/coddy_provider.go). It is no usage limit, and
// the console must not say it is.

func remoteBusyUpdate() acp.ProviderUsageUpdate {
	return acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "lab",
		ProviderType:  "coddy",
		Blocked:       true,
		Blockers:      []string{"remote_busy"},
		Resuming:      true,
		RetryAt:       "2026-09-06T17:48:12Z",
		RetryInSec:    60,
	}
}

func remoteBusyApp() (*App, *int) {
	a := &App{updatesCh: make(chan updateMsg, 8)}
	a.foot = newFooter(newTheme("dark"), ".")
	a.chat = &tui.Container{}
	a.modelID = "lab/terra"
	a.sessionID = "s1"
	armed := 0
	a.usageAfterFn = func(time.Duration, func()) func() bool {
		armed++
		return func() bool { return true }
	}
	return a, &armed
}

func TestRemoteBusyUpdateSaysTheCallWaitsForAFreeSlotOfTheRemote(t *testing.T) {
	a, armed := remoteBusyApp()
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
	// Nothing to read again when the budget runs out: the clearing update ends
	// the wait, so no reset note and no usage timer are armed.
	if *armed != 0 || a.usageResume != nil || a.usageTimer != nil {
		t.Fatalf("the wait armed %d timers, want none", *armed)
	}

	// The countdown is re-sent while the wait lasts: the clock of the row is
	// not restarted by it.
	started := a.stepStatus.startedAt
	u.RetryInSec = 40
	a.applyProviderUsage(u)
	if a.stepStatus.startedAt != started {
		t.Fatal("a re-sent countdown must not restart the status clock")
	}
}

func TestRemoteBusyUpdateWithoutADeadlineNamesNoTime(t *testing.T) {
	a, _ := remoteBusyApp()
	u := remoteBusyUpdate()
	u.RetryAt, u.RetryInSec = "", 0
	a.applyProviderUsage(u)
	if got, want := a.statusMessage(), "Waiting for a free slot on the remote · resumes on its own"; got != want {
		t.Fatalf("status row = %q, want %q", got, want)
	}
}

func TestRemoteBusyEndsWithTheClearingUpdateAndTheRowGoesBackToTheModel(t *testing.T) {
	a, _ := remoteBusyApp()
	a.turnActive = true
	a.applyProviderUsage(remoteBusyUpdate())
	if !strings.HasPrefix(a.statusMessage(), "Waiting for a free slot") {
		t.Fatalf("status row = %q", a.statusMessage())
	}

	// The update of another row says nothing about this wait.
	a.applyProviderUsage(acp.ProviderUsageUpdate{Provider: "elsewhere", ProviderType: "coddy", Unsupported: true})
	if !strings.HasPrefix(a.statusMessage(), "Waiting for a free slot") {
		t.Fatalf("an update for another row ended the wait: %q", a.statusMessage())
	}

	// The call got its slot: the agent says the row has no usage, and the row
	// reads as waiting for the model until the call streams.
	a.applyProviderUsage(acp.ProviderUsageUpdate{Provider: "lab", ProviderType: "coddy", Unsupported: true})
	if got := a.statusMessage(); !strings.HasPrefix(got, statusWaitingModel) {
		t.Fatalf("after the wait the row reads %q, want %q", got, statusWaitingModel)
	}

	// Later clearing updates (a turn's release publishes one for the row) leave
	// the row of the next phase alone.
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(acp.ProviderUsageUpdate{Provider: "lab", ProviderType: "coddy", Unsupported: true})
	if got := a.statusMessage(); !strings.Contains(got, "Thinking") {
		t.Fatalf("a later clearing update changed the row to %q", got)
	}
}

func TestRemoteBusyDoesNotOutliveTheTurn(t *testing.T) {
	a, _ := remoteBusyApp()
	a.applyProviderUsage(remoteBusyUpdate())
	a.turnSessionID = "s1"
	a.status = &tui.Container{}
	a.plain = true
	a.applyLoopMessage(updateMsg{update: turnDone{sessionID: "s1"}})
	// A clearing update that arrives in the next turn must not repaint its row.
	a.turnActive = true
	a.setStatus(newModelStatus("Thinking"))
	a.applyProviderUsage(acp.ProviderUsageUpdate{Provider: "lab", ProviderType: "coddy", Unsupported: true})
	if got := a.statusMessage(); !strings.Contains(got, "Thinking") {
		t.Fatalf("a clearing update repainted the next turn's row: %q", got)
	}
}

// A limit wait keeps its wording and its reset note, and takes over the row
// from a wait for a slot.
func TestUsageLimitWaitStillSaysUsageLimit(t *testing.T) {
	a, armed := remoteBusyApp()
	a.modelID = "neuraldeep/qwen3.8-27b"
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
	if *armed != 1 || a.usageResume == nil {
		t.Fatalf("a limit wait armed %d timers, want its one reset note", *armed)
	}
	// The clearing update of the earlier wait for a slot no longer applies.
	a.turnActive = true
	a.applyProviderUsage(acp.ProviderUsageUpdate{Provider: "lab", ProviderType: "coddy", Unsupported: true})
	if got := a.statusMessage(); got != want {
		t.Fatalf("a clearing update for the old wait changed the row to %q", got)
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
