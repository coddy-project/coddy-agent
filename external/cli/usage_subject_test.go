//go:build cli

package cli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The usage of a model another Coddy shares is read per alias (the update's
// Model, docs/plans/remote-model-provider-phase2.md 4.4): the footer keeps one
// snapshot per subject, orders the snapshots of one subject by the time they
// were read, and asks the backend for the alias, not for the row.

func coddySnapshot(alias, fetchedAt string, usedPercent float64) *acp.ProviderUsageUpdate {
	u := busySnapshot(fetchedAt, usedPercent)
	u.Model = alias
	return &u
}

func TestFooterKeepsOneSnapshotPerAliasOfACoddyRow(t *testing.T) {
	f := newFooter(newTheme("dark"), ".")
	f.now = func() time.Time { return usageNow }
	f.SetUsage(coddySnapshot("terra", "2026-09-06T17:40:00Z", 41))
	f.SetUsage(coddySnapshot("luna", "2026-09-06T17:41:00Z", 70))

	f.SetModel("lab/terra", "")
	if u := f.Usage(); u == nil || u.Windows[0].UsedPercent != 41 {
		t.Fatalf("terra shows %+v, want its own 41%%", u)
	}
	if lines := f.Render(120); len(lines) != 3 || !strings.Contains(plain(lines[2]), "3h 41%") {
		t.Fatalf("terra footer = %q", lines)
	}
	f.SetModel("lab/luna", "")
	if u := f.Usage(); u == nil || u.Windows[0].UsedPercent != 70 {
		t.Fatalf("luna shows %+v, want its own 70%%", u)
	}
	// An alias nothing was read for shows no line, and no other alias's.
	f.SetModel("lab/sol", "")
	if f.Usage() != nil {
		t.Fatalf("an alias with no snapshot shows %+v", f.Usage())
	}
	// A row that keys by the row alone (every other type) is unchanged.
	f.SetUsage(usageFixtureUpdate())
	f.SetModel("neuraldeep/qwen3.8-27b", "")
	if f.Usage() == nil || f.Usage().Provider != "neuraldeep" {
		t.Fatalf("a row without an alias key lost its snapshot: %+v", f.Usage())
	}
}

func TestFooterDropUsageIsPerSubjectOrPerRow(t *testing.T) {
	f := newFooter(newTheme("dark"), ".")
	f.SetUsage(coddySnapshot("terra", "2026-09-06T17:40:00Z", 41))
	f.SetUsage(coddySnapshot("luna", "2026-09-06T17:41:00Z", 70))
	f.SetUsage(usageFixtureUpdate())

	f.DropUsage("lab", "terra")
	f.SetModel("lab/terra", "")
	if f.Usage() != nil {
		t.Fatal("the alias's own drop left its snapshot")
	}
	f.SetModel("lab/luna", "")
	if f.Usage() == nil {
		t.Fatal("dropping one alias dropped another's snapshot")
	}
	// Without an alias the answer is about the whole row.
	f.DropUsage("lab", "")
	if f.Usage() != nil {
		t.Fatal("a row-wide drop left an alias's snapshot")
	}
	f.SetModel("neuraldeep/qwen3.8-27b", "")
	if f.Usage() == nil {
		t.Fatal("dropping a coddy row dropped another provider's snapshot")
	}
	// A provider whose name is the prefix of another's is not another's row.
	f.SetUsage(coddySnapshot("terra", "2026-09-06T17:40:00Z", 41))
	other := coddySnapshot("terra", "2026-09-06T17:40:00Z", 55)
	other.Provider = "lab-2"
	f.SetUsage(other)
	f.DropUsage("lab", "")
	f.SetModel("lab-2/terra", "")
	if f.Usage() == nil {
		t.Fatal("dropping the row \"lab\" dropped the row \"lab-2\"")
	}
}

// The footer applied snapshots in arrival order, so an answer asked for before a
// pushed frame and landing after it overwrote the newer numbers.
func TestFooterOrdersSnapshotsOfASubjectByTheirReadTime(t *testing.T) {
	f := newFooter(newTheme("dark"), ".")
	f.SetModel("lab/terra", "")
	newer := coddySnapshot("terra", "2026-09-06T17:50:00Z", 43)
	older := coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)

	if !f.SetUsage(newer) {
		t.Fatal("the first snapshot was refused")
	}
	if f.SetUsage(older) {
		t.Fatal("an older snapshot replaced a newer one")
	}
	if got := f.Usage(); got != newer {
		t.Fatalf("the footer shows %+v after the older answer landed", got)
	}
	// The same stamp is the same read: the later arrival wins the tie.
	tied := coddySnapshot("terra", "2026-09-06T17:50:00Z", 44)
	if !f.SetUsage(tied) || f.Usage() != tied {
		t.Fatal("a snapshot with the shown stamp was refused")
	}
	// A newer one replaces.
	latest := coddySnapshot("terra", "2026-09-06T17:51:00Z", 45)
	if !f.SetUsage(latest) || f.Usage() != latest {
		t.Fatal("a newer snapshot was refused")
	}
	// The order is per subject: another alias's newer read says nothing about
	// this one's older one.
	other := coddySnapshot("luna", "2026-09-06T18:00:00Z", 80)
	f.SetUsage(other)
	if !f.SetUsage(coddySnapshot("sol", "2026-09-06T17:00:00Z", 10)) {
		t.Fatal("an alias's snapshot was held against another alias's newer read")
	}
	// Rows of other types order the same way.
	nd := usageFixtureUpdate()
	f.SetUsage(nd)
	stale := usageFixtureUpdate()
	stale.FetchedAt = "2026-09-06T17:00:00Z"
	if f.SetUsage(stale) {
		t.Fatal("an older snapshot of a NeuralDeep row replaced a newer one")
	}
}

// A snapshot without a read time (a rejected key: the manager builds that one
// from nothing) is no read at all, and it still has to reach the line.
func TestFooterAppliesASnapshotWithoutAReadTimeByArrival(t *testing.T) {
	f := newFooter(newTheme("dark"), ".")
	f.SetModel("neuraldeep/qwen3.8-27b", "")
	f.SetUsage(usageFixtureUpdate())
	rejected := &acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: "neuraldeep", ProviderType: "neuraldeep", Error: "unauthorized",
	}
	if !f.SetUsage(rejected) || f.Usage() != rejected {
		t.Fatal("a rejected-key update with no read time was dropped")
	}
	// And a read replaces it again.
	if !f.SetUsage(usageFixtureUpdate()) {
		t.Fatal("a read after the unstamped update was refused")
	}
}

// An older snapshot is not adopted, so it must not trigger what adopting does:
// the notices and the reset timer belong to the snapshot that is shown.
func TestAnOlderSnapshotArmsNothingAndNoticesNothing(t *testing.T) {
	a, timers := remoteBusyApp(t)
	hot := busySnapshot("2026-09-06T17:50:00Z", 43)
	a.applyProviderUsage(hot)
	arms := len(timers.arms)

	old := busySnapshot("2026-09-06T17:40:00Z", 91)
	a.applyProviderUsage(old)
	if len(timers.arms) != arms {
		t.Fatalf("an older snapshot re-armed the reset timer (%d arms)", len(timers.arms))
	}
	if got := transcriptText(a); strings.Contains(got, "91%") {
		t.Fatalf("an older snapshot posted a notice: %q", got)
	}
	if u := a.foot.Usage(); u == nil || u.Windows[0].UsedPercent != 43 {
		t.Fatalf("an older snapshot replaced the shown one: %+v", u)
	}
}

func TestUsageActiveIsPerAliasForACoddyRow(t *testing.T) {
	a := newTestApp(t)
	a.modelID = "lab/terra"
	if !a.usageActive(coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)) {
		t.Fatal("the active alias's snapshot is not active")
	}
	if a.usageActive(coddySnapshot("luna", "2026-09-06T17:40:00Z", 41)) {
		t.Fatal("another alias's snapshot is active")
	}
	// No alias in the update (every other type, a remote that predates it):
	// the row decides.
	noAlias := coddySnapshot("", "2026-09-06T17:40:00Z", 41)
	if !a.usageActive(noAlias) {
		t.Fatal("an update with no alias is not active for its row")
	}
	other := coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)
	other.Provider = "lab-2"
	if a.usageActive(other) {
		t.Fatal("another row's snapshot is active")
	}
}

// The reset timer of a snapshot belongs to its alias: it reads again only while
// that alias is the active one.
func TestUsageResetTimerReadsTheAliasItWasArmedFor(t *testing.T) {
	a, timers := remoteBusyApp(t)
	backend := &usageBackend{}
	a.mgr = backend
	a.applyProviderUsage(busySnapshot("2026-09-06T17:40:00Z", 41))
	live := timers.live()
	if len(live) != 1 {
		t.Fatalf("armed timers = %d, want the one reset timer", len(live))
	}
	live[0].fn()
	msg := <-a.updatesCh
	due, ok := msg.update.(usageResetDue)
	if !ok || due.provider != "lab" || due.model != "terra" {
		t.Fatalf("the timer posted %+v", msg.update)
	}

	a.applyLoopMessage(msg)
	a.workers.Wait()
	if got := backend.calls(); len(got) != 1 || got[0] != "lab/terra" {
		t.Fatalf("the reset read asked for %v, want [lab/terra]", got)
	}

	// The model changed to another alias of the row meanwhile: nothing is read.
	a.modelID = "lab/luna"
	a.applyLoopMessage(msg)
	a.workers.Wait()
	if got := backend.calls(); len(got) != 1 {
		t.Fatalf("a switched-away alias was read: %v", got)
	}
}

// Every read the console makes names the subject by the active model's selector:
// the backend takes a row's name or a selector, ignores the alias for every row
// that is not a coddy one, and reads the alias for a coddy row.
func TestTheConsoleAsksTheBackendForTheActiveSubject(t *testing.T) {
	a := newTestApp(t)
	backend := &usageBackend{answer: coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)}
	a.mgr = backend
	a.modelID = "lab/terra"
	a.sessionID = "s1"

	a.refreshUsage(a.modelID, false)
	a.workers.Wait()
	a.showUsage()
	a.workers.Wait()
	a.applyLoopMessage(updateMsg{update: usageResumeDue{}})
	a.workers.Wait()
	want := []string{"lab/terra", "lab/terra", "lab/terra"}
	got := backend.calls()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("backend reads = %v, want %v", got, want)
	}
	if refreshes := backend.refreshes(); strings.Join(boolsToStrings(refreshes), ",") != "false,true,true" {
		t.Fatalf("refresh flags = %v", refreshes)
	}

	// A model without a provider names no subject.
	a.modelID = "agent"
	before := len(backend.calls())
	a.refreshUsage(usageSubjectOf(a.modelID), false)
	a.workers.Wait()
	if len(backend.calls()) != before {
		t.Fatal("a model with no provider was read")
	}
}

func TestSwitchingModelReadsTheNewSubject(t *testing.T) {
	a := newTestApp(t)
	backend := &usageBackend{answer: coddySnapshot("luna", "2026-09-06T17:40:00Z", 70)}
	a.mgr = backend
	a.sessionID = "s1"
	a.setModel("lab/luna")
	a.workers.Wait()
	if got := backend.calls(); len(got) != 1 || got[0] != "lab/luna" {
		t.Fatalf("a model switch read %v, want [lab/luna]", got)
	}
}

func TestUsageTitleNamesTheAliasOfACoddyRow(t *testing.T) {
	u := coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)
	if got := usageTitle(u); got != "lab · terra" {
		t.Fatalf("title = %q, want %q", got, "lab · terra")
	}
	if head := usageReportLines(u, "lab/terra", usageNow)[0]; !strings.HasPrefix(head, "lab · terra") {
		t.Fatalf("/usage head = %q", head)
	}
	// Every other type keeps its brand.
	if got := usageTitle(usageFixtureUpdate()); got != "NeuralDeep" {
		t.Fatalf("title = %q", got)
	}
}

// The remote folds a block of the alias's own gate into the alias's update under
// this blocker; it lifts on a clock like a window.
func TestAModelBlockedBlockerReadsAsAWindowLimit(t *testing.T) {
	u := coddySnapshot("terra", "2026-09-06T17:40:00Z", 41)
	u.Blocked, u.Blockers, u.RetryInSec = true, []string{"model_blocked"}, 3600
	u.RetryAt = "2026-09-06T18:47:12Z"
	seg := blockedSegment(u, usageNow)
	if !strings.HasPrefix(seg.text, "limit reached") || seg.role != roleError {
		t.Fatalf("segment = %+v", seg)
	}
}

// usageBackend answers the usage reads the console makes and records what it
// was asked for.
type usageBackend struct {
	backend
	mu      sync.Mutex
	answer  *acp.ProviderUsageUpdate
	names   []string
	refresh []bool
}

func (b *usageBackend) ProviderUsageForSession(_ context.Context, _, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.names = append(b.names, name)
	b.refresh = append(b.refresh, refresh)
	return b.answer, nil
}

// SessionByID: the console reads the session's state for its footer, and this
// backend has none.
func (b *usageBackend) SessionByID(string) *session.State { return nil }

// The reads a turn's end makes of the session.
func (b *usageBackend) BackgroundTasks(context.Context, string) ([]bgtask.Snapshot, error) {
	return nil, nil
}

func (b *usageBackend) QueuedTurnMessages(string) ([]session.QueuedMessage, error) { return nil, nil }

func (b *usageBackend) HandleSessionSetConfigOption(context.Context, acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return &acp.SessionSetConfigOptionResult{}, nil
}

func (b *usageBackend) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.names...)
}

func (b *usageBackend) refreshes() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.refresh...)
}

func boolsToStrings(in []bool) []string {
	out := make([]string, len(in))
	for i, v := range in {
		if v {
			out[i] = "true"
		} else {
			out[i] = "false"
		}
	}
	return out
}
