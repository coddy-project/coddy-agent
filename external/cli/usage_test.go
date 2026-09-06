//go:build cli

package cli

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func ip(v int) *int { return &v }

// usageFixtureUpdate is a pro wallet key with the session at 2.7 %, the
// week at 6.7 % and a negative balance, as the hub answered on 2026-09-06.
func usageFixtureUpdate() *acp.ProviderUsageUpdate {
	return &acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "neuraldeep",
		ProviderType:  "neuraldeep",
		ObservedAt:    "2026-09-06T17:47:02Z",
		FetchedAt:     "2026-09-06T17:47:10Z",
		Plan:          "pro",
		KeyName:       "coddy",
		Windows: []acp.UsageWindow{
			{ID: "session", Label: "3h", Used: ip(407), Limit: ip(15000), Remaining: ip(14593), UsedPercent: 2.71, ResetsAt: "2026-09-06T17:59:59Z", ResetInSec: 777},
			{ID: "week", Label: "week", Used: ip(9981), Limit: ip(150000), Remaining: ip(140019), UsedPercent: 6.65, ResetsAt: "2026-09-07T00:00:00Z", ResetInSec: 22378},
			{ID: "day", Label: "day", UsedPercent: 0, ResetsAt: "2026-09-07T00:00:00Z", ResetInSec: 22378},
		},
		Rate:            &acp.UsageRate{Used: 2, Limit: 120, Remaining: 118, ResetInSec: 58},
		Wallet:          &acp.UsageWallet{BalanceRub: -1229.24, SpentRub30d: 2000.74},
		UnlimitedModels: []string{"qwen3.6-35b-a3b"},
	}
}

var usageNow = time.Date(2026, 9, 6, 17, 47, 12, 0, time.UTC)

func plain(s string) string { return tui.StripTerminalSequences(s) }

func TestUsageFooterLineReadsPlanWindowsAndWallet(t *testing.T) {
	th := newTheme("dark")
	segs := usageFooterSegments(usageFixtureUpdate(), "neuraldeep/qwen3.8-27b", usageNow)
	line := plain(renderUsageLine(th, segs, 120))
	want := "pro • 3h 3% (resets 17:59) • week 7% (resets 00:00) • wallet -1 229 ₽"
	if line != want {
		t.Fatalf("footer line = %q, want %q", line, want)
	}
}

func TestUsageFooterDropsSegmentsOnNarrowTerminals(t *testing.T) {
	th := newTheme("dark")
	segs := usageFooterSegments(usageFixtureUpdate(), "neuraldeep/qwen3.8-27b", usageNow)
	cases := []struct {
		width int
		want  string
	}{
		{60, "pro • 3h 3% (resets 17:59) • week 7% (resets 00:00)"},
		{40, "pro • 3h 3% (resets 17:59)"},
		{24, "3h 3% (resets 17:59)"},
		{12, "3h 3% (reset"},
	}
	for _, tc := range cases {
		got := plain(renderUsageLine(th, segs, tc.width))
		if got != tc.want {
			t.Errorf("width %d: %q, want %q", tc.width, got, tc.want)
		}
		if tui.VisibleWidth(got) > tc.width {
			t.Errorf("width %d: line is %d cells wide", tc.width, tui.VisibleWidth(got))
		}
	}
}

func TestUsageFooterWarnsAtTheThresholdAndOnABlock(t *testing.T) {
	th := newTheme("dark")
	u := usageFixtureUpdate()
	u.Windows[0].UsedPercent = 82
	segs := usageFooterSegments(u, "neuraldeep/qwen3.8-27b", usageNow)
	if segs[1].role != roleWarning || !strings.HasPrefix(segs[1].text, "3h 82%") {
		t.Fatalf("session segment = %+v, want the warning role", segs[1])
	}
	if segs[2].role != roleDim {
		t.Fatalf("week segment = %+v, want dim", segs[2])
	}
	if segs[3].role != roleWarning || segs[3].text != "wallet -1 229 ₽" {
		t.Fatalf("negative wallet = %+v, want the warning role", segs[3])
	}
	line := renderUsageLine(th, segs, 120)
	if !strings.Contains(line, th.Fg(roleWarning, "3h 82% (resets 17:59)")) {
		t.Fatalf("rendered line lacks the warning-coloured segment: %q", line)
	}

	blocked := usageFixtureUpdate()
	blocked.Blocked = true
	blocked.Blockers = []string{"session_exhausted"}
	blocked.RetryAt = "2026-09-06T17:59:59Z"
	blocked.RetryInSec = 767
	segs = usageFooterSegments(blocked, "neuraldeep/qwen3.8-27b", usageNow)
	if got := plain(renderUsageLine(th, segs, 120)); got != "pro • limit reached (resets 17:59) • wallet -1 229 ₽" {
		t.Fatalf("blocked line = %q", got)
	}
	if segs[1].role != roleError {
		t.Fatalf("blocked segment role = %q", segs[1].role)
	}
}

func TestBlockedSegmentCopyPerBlocker(t *testing.T) {
	cases := []struct {
		blockers []string
		retryIn  int
		want     string
	}{
		{[]string{"rpm_exhausted"}, 42, "rate limited (retry in 42s)"},
		{[]string{"session_cooldown"}, 30, "rate limited (retry in 30s)"},
		{[]string{"week_exhausted"}, 22378, "limit reached (resets 00:00)"},
		{[]string{"key_cap_blocked"}, 0, "key blocked"},
		{[]string{"wallet_empty"}, 0, "wallet empty"},
		{[]string{"user_blocked"}, 0, "account blocked"},
		{[]string{"mystery_gate"}, 0, "blocked: mystery_gate"},
		{nil, 0, "blocked"},
	}
	for _, tc := range cases {
		u := &acp.ProviderUsageUpdate{Blocked: true, Blockers: tc.blockers, RetryInSec: tc.retryIn, RetryAt: "2026-09-07T00:00:00Z"}
		if got := blockedSegment(u, usageNow).text; got != tc.want {
			t.Errorf("%v: %q, want %q", tc.blockers, got, tc.want)
		}
	}
}

func TestBlockedNoticeWording(t *testing.T) {
	cases := map[string]string{
		"limit reached (resets 17:59)": "Usage limit reached (resets 17:59)",
		"rate limited (retry in 42s)":  "Rate limited (retry in 42s)",
		"key blocked":                  "Key blocked",
		"":                             "Usage blocked",
	}
	for in, want := range cases {
		if got := blockedNotice(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestUsageFooterUnlimitedModelAndKey(t *testing.T) {
	th := newTheme("dark")
	u := usageFixtureUpdate()
	segs := usageFooterSegments(u, "neuraldeep/qwen3.6-35b-a3b", usageNow)
	if got := plain(renderUsageLine(th, segs, 120)); got != "pro • ∞ volume • wallet -1 229 ₽" {
		t.Fatalf("unlimited model line = %q", got)
	}
	u.Unlimited = true
	u.Windows = u.Windows[2:] // only the day window survives on such a key
	u.Windows[0].UsedPercent = 12.5
	segs = usageFooterSegments(u, "neuraldeep/qwen3.8-27b", usageNow)
	if got := plain(renderUsageLine(th, segs, 120)); got != "pro • ∞ volume • day 13% (resets 00:00) • wallet -1 229 ₽" {
		t.Fatalf("unlimited key line = %q", got)
	}
}

func TestUsageFooterErrorsAndStaleness(t *testing.T) {
	th := newTheme("dark")
	rejected := &acp.ProviderUsageUpdate{Provider: "nd-work", ProviderType: "neuraldeep", Error: "unauthorized"}
	if got := plain(renderUsageLine(th, usageFooterSegments(rejected, "nd-work/x", usageNow), 120)); got != "nd-work: key rejected, run coddy providers login nd-work" {
		t.Fatalf("rejected key line = %q", got)
	}
	if segs := usageFooterSegments(&acp.ProviderUsageUpdate{Provider: "neuraldeep", Error: "unavailable"}, "neuraldeep/x", usageNow); segs != nil {
		t.Fatalf("a first failure with nothing to show must hide the line: %+v", segs)
	}
	stale := usageFixtureUpdate()
	stale.Error, stale.Stale = "unavailable", true
	if got := plain(renderUsageLine(th, usageFooterSegments(stale, "neuraldeep/x", usageNow), 120)); !strings.HasSuffix(got, "• (stale)") {
		t.Fatalf("stale line = %q", got)
	}
	revoked := usageFixtureUpdate()
	revoked.Error = "unauthorized"
	if got := plain(renderUsageLine(th, usageFooterSegments(revoked, "neuraldeep/x", usageNow), 120)); got != "neuraldeep: key rejected, run coddy providers login neuraldeep" {
		t.Fatalf("a rejected key must outrank old numbers: %q", got)
	}
	if lines := usageReportLines(revoked, "neuraldeep/x", usageNow); len(lines) != 2 || !strings.Contains(lines[1], "key rejected") {
		t.Fatalf("report for a rejected key = %q", lines)
	}
	if segs := usageFooterSegments(&acp.ProviderUsageUpdate{Provider: "stub", Unsupported: true}, "stub/x", usageNow); segs != nil {
		t.Fatalf("unsupported must render nothing: %+v", segs)
	}
}

func TestUsageStringsFromTheHubAreSanitised(t *testing.T) {
	th := newTheme("dark")
	u := usageFixtureUpdate()
	u.Plan = "pro\x1b[31mred\x1b[0m"
	u.KeyName = "key\x07bell"
	u.Windows[0].Label = "3h\x1b]8;;http://evil\x1b\\"
	u.Blocked, u.Blockers = true, []string{"odd\x1bblocker"}
	line := renderUsageLine(th, usageFooterSegments(u, "neuraldeep/x", usageNow), 200)
	for _, bad := range []string{"\x1b[31m", "\x07", "\x1b]8"} {
		if strings.Contains(strings.TrimPrefix(line, th.Fg(roleDim, "")), bad) && strings.Contains(plain(line), bad) {
			t.Fatalf("hub string reached the terminal unsanitised: %q", line)
		}
	}
	if p := plain(line); strings.Contains(p, "\x07") || strings.Contains(p, "evil") {
		t.Fatalf("sanitised line still carries hub control text: %q", p)
	}
	report := strings.Join(usageReportLines(u, "neuraldeep/x", usageNow), "\n")
	if strings.Contains(report, "\x07") || strings.Contains(report, "\x1b") {
		t.Fatalf("report carries control characters: %q", report)
	}
}

func TestUsageReportLines(t *testing.T) {
	lines := usageReportLines(usageFixtureUpdate(), "neuraldeep/qwen3.8-27b", usageNow)
	want := []string{
		"NeuralDeep · pro · key coddy",
		"  session (3h)   ▮▯▯▯▯▯▯▯▯▯    3%  407 / 15 000  resets 17:59",
		"  week           ▮▯▯▯▯▯▯▯▯▯    7%  9 981 / 150 000  resets 00:00",
		"  day            ▯▯▯▯▯▯▯▯▯▯    0%  resets 00:00",
		"  rpm            2 / 120 this minute",
		"  cooldown       none",
		"  wallet         -1 229 ₽ (2 001 ₽ spent in 30 days)",
		"  observed 2s ago",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("report =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	u := usageFixtureUpdate()
	u.CooldownSec, u.RefreshPending, u.RefreshInSec = 725, true, 9
	lines = usageReportLines(u, "neuraldeep/qwen3.6-35b-a3b", usageNow)
	joined := strings.Join(lines, "\n")
	for _, s := range []string{"  cooldown       12m 05s", "  refresh in 9s (pacing)", "  volume        ∞"} {
		if !strings.Contains(joined, s) {
			t.Fatalf("report lacks %q:\n%s", s, joined)
		}
	}
}

func TestFormatResetTimeBuckets(t *testing.T) {
	now := time.Date(2026, 9, 6, 17, 47, 12, 0, time.UTC)
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(12 * time.Minute), "17:59"},
		{now.Add(23 * time.Hour), "16:47"},
		{now.Add(30 * time.Hour), "Mon 23:47"},
		{now.Add(6 * 24 * time.Hour), "Sat 17:47"},
		{now.Add(8 * 24 * time.Hour), "Sep 14"},
	}
	for _, tc := range cases {
		if got := formatResetTime(tc.at, now); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.at, got, tc.want)
		}
	}
	if formatResetTime(time.Time{}, now) != "" {
		t.Fatalf("zero time must render empty")
	}
}

func TestFormatRubAndThousands(t *testing.T) {
	cases := map[float64]string{-1229.24: "-1 229 ₽", 0: "0 ₽", 999: "999 ₽", 1000: "1 000 ₽", 2000.74: "2 001 ₽", 1234567.4: "1 234 567 ₽"}
	for v, want := range cases {
		if got := formatRub(v); got != want {
			t.Errorf("%v: %q, want %q", v, got, want)
		}
	}
	if w := tui.VisibleWidth("-1 229 ₽"); w != 8 {
		t.Fatalf("wallet width = %d cells, want 8", w)
	}
}

func TestEarliestUsageDeadlineIgnoresTheRate(t *testing.T) {
	u := usageFixtureUpdate()
	if d, forced := earliestUsageDeadline(u); d != 777*time.Second+usageResetGrace || !forced {
		t.Fatalf("deadline = %s forced=%v, want the session reset plus grace, forced", d, forced)
	}
	u.Windows[0].ResetInSec = 0
	u.Windows[1].ResetInSec = 0
	u.Windows[2].ResetInSec = 0
	if d, _ := earliestUsageDeadline(u); d != 0 {
		t.Fatalf("with no window reset the rate must not arm a timer: %s", d)
	}
	u.RefreshPending, u.RefreshInSec = true, 9
	if d, forced := earliestUsageDeadline(u); d != 9*time.Second+usageResetGrace || forced {
		t.Fatalf("deferred refresh deadline = %s forced=%v, want a cache read", d, forced)
	}
	// A reset sooner than the deferred refresh wins, and it is a hub read.
	u.Windows[1].ResetInSec = 4
	if d, forced := earliestUsageDeadline(u); d != 4*time.Second+usageResetGrace || !forced {
		t.Fatalf("earliest deadline = %s forced=%v", d, forced)
	}
	u.Windows[1].ResetInSec = 0
	if key := passedResetKey(u); key != "session@2026-09-06T17:59:59Z" {
		t.Fatalf("passed reset key = %q", key)
	}
}

func TestUsageTimerPostsACacheReadForADeferredRefresh(t *testing.T) {
	a := &App{updatesCh: make(chan updateMsg, 8)}
	a.foot = newFooter(newTheme("dark"), ".")
	a.chat = &tui.Container{}
	a.modelID = "neuraldeep/qwen3.8-27b"
	a.sessionID = "s1"
	var fire func()
	a.usageAfterFn = func(_ time.Duration, fn func()) func() bool {
		fire = fn
		return func() bool { return true }
	}
	deferred := usageFixtureUpdate()
	for i := range deferred.Windows {
		deferred.Windows[i].ResetInSec = 0
	}
	deferred.RefreshPending, deferred.RefreshInSec = true, 9
	a.applyProviderUsage(*deferred)
	fire()
	msg := <-a.updatesCh
	if due, ok := msg.update.(usageResetDue); !ok || due.forced {
		t.Fatalf("a deferred refresh must post a cache read, got %+v", msg.update)
	}
	a.applyProviderUsage(*usageFixtureUpdate())
	fire()
	msg = <-a.updatesCh
	if due, ok := msg.update.(usageResetDue); !ok || !due.forced {
		t.Fatalf("a window reset must post a hub read, got %+v", msg.update)
	}
}

func TestUsageTimerRearmsAndFollowsUpOnce(t *testing.T) {
	a := &App{updatesCh: make(chan updateMsg, 8)}
	a.foot = newFooter(newTheme("dark"), ".")
	a.modelID = "neuraldeep/qwen3.8-27b"
	a.sessionID = "s1"
	type armed struct {
		d  time.Duration
		fn func()
	}
	var arms []armed
	stopped := 0
	a.usageAfterFn = func(d time.Duration, fn func()) func() bool {
		arms = append(arms, armed{d, fn})
		return func() bool { stopped++; return true }
	}
	a.chat = &tui.Container{}

	a.applyProviderUsage(*usageFixtureUpdate())
	if len(arms) != 1 || arms[0].d != 777*time.Second+usageResetGrace {
		t.Fatalf("arms = %+v", arms)
	}
	// A new snapshot re-arms (the old timer stops) at its own deadline.
	next := usageFixtureUpdate()
	next.Windows[0].ResetInSec = 100
	a.applyProviderUsage(*next)
	if len(arms) != 2 || stopped != 1 || arms[1].d != 100*time.Second+usageResetGrace {
		t.Fatalf("re-arm: arms=%d stopped=%d last=%s", len(arms), stopped, arms[1].d)
	}
	// The timer posts the internal message for the active provider.
	arms[1].fn()
	select {
	case msg := <-a.updatesCh:
		due, ok := msg.update.(usageResetDue)
		if !ok || due.provider != "neuraldeep" || msg.sessionID != "s1" {
			t.Fatalf("timer posted %+v", msg)
		}
	default:
		t.Fatalf("timer posted nothing")
	}
	// A snapshot whose reset has passed but still shows the old window arms
	// one follow-up, and only one.
	passed := usageFixtureUpdate()
	for i := range passed.Windows {
		passed.Windows[i].ResetInSec = 0
	}
	a.applyProviderUsage(*passed)
	if len(arms) != 3 || arms[2].d != usageFollowUpDelay {
		t.Fatalf("follow-up: arms=%d", len(arms))
	}
	a.applyProviderUsage(*passed)
	if len(arms) != 3 {
		t.Fatalf("a second follow-up must not be armed for the same window: arms=%d", len(arms))
	}
	// A passed session reset with the week window still far away arms the
	// follow-up, not the week deadline, and only once for that window.
	weekIntact := usageFixtureUpdate()
	weekIntact.Windows[0].ResetInSec = 0
	weekIntact.Windows[0].ResetsAt = "2026-09-06T14:59:59Z"
	a.usageFollowUp = ""
	a.applyProviderUsage(*weekIntact)
	if len(arms) != 4 || arms[3].d != usageFollowUpDelay {
		t.Fatalf("follow-up with the week intact: arms=%d last=%s", len(arms), arms[len(arms)-1].d)
	}
	a.applyProviderUsage(*weekIntact)
	if len(arms) != 5 || arms[4].d != 22378*time.Second+usageResetGrace {
		t.Fatalf("after the single follow-up the week deadline is armed: arms=%d last=%s", len(arms), arms[len(arms)-1].d)
	}
	// Another provider's update never touches the timer.
	stoppedBefore := stopped
	other := usageFixtureUpdate()
	other.Provider = "nd-work"
	a.applyProviderUsage(*other)
	if len(arms) != 5 || stopped != stoppedBefore {
		t.Fatalf("foreign provider armed a timer: arms=%d stopped=%d", len(arms), stopped)
	}
}

func TestUsageNoticesOncePerWindowAndBlock(t *testing.T) {
	a := &App{updatesCh: make(chan updateMsg, 8)}
	a.theme = newTheme("dark")
	a.foot = newFooter(a.theme, ".")
	a.foot.now = func() time.Time { return usageNow }
	a.chat = &tui.Container{}
	a.modelID = "neuraldeep/qwen3.8-27b"
	a.usageAfterFn = func(time.Duration, func()) func() bool { return func() bool { return true } }
	rows := func() []string {
		var out []string
		for _, line := range a.chat.Render(200) {
			out = append(out, strings.TrimSpace(plain(line)))
		}
		return out
	}
	warm := usageFixtureUpdate()
	warm.Windows[0].UsedPercent = 85
	a.applyProviderUsage(*warm)
	a.applyProviderUsage(*warm)
	got := rows()
	if len(got) != 1 || got[0] != "You've used 85% of your NeuralDeep 3h limit · resets 17:59" {
		t.Fatalf("notices = %q", got)
	}
	warm.Windows[0].ResetsAt = "2026-09-06T20:59:59Z" // next period: notice again
	a.applyProviderUsage(*warm)
	if got = rows(); len(got) != 2 {
		t.Fatalf("a new period must notify again: %q", got)
	}
	blocked := usageFixtureUpdate()
	blocked.Blocked, blocked.Blockers, blocked.RetryAt, blocked.RetryInSec = true, []string{"session_exhausted"}, "2026-09-06T17:59:59Z", 767
	a.applyProviderUsage(*blocked)
	a.applyProviderUsage(*blocked)
	if got = rows(); len(got) != 3 || got[2] != "Usage limit reached (resets 17:59)" {
		t.Fatalf("blocked notice = %q", got)
	}
	// A foreign provider's update adds no notice.
	other := usageFixtureUpdate()
	other.Provider, other.Windows[0].UsedPercent = "nd-work", 99
	a.applyProviderUsage(*other)
	if got = rows(); len(got) != 3 {
		t.Fatalf("foreign provider notified: %q", got)
	}
}

func TestThemeSwitchKeepsTheUsageLine(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}},
		Models:    []config.ModelEntry{{Model: "neuraldeep/qwen3.8-27b", MaxTokens: 100, MaxContextTokens: 1000}},
		Agent:     config.Agent{Model: "neuraldeep/qwen3.8-27b"},
	}
	mgr := session.NewManager(cfg, nil, nil, slog.New(slog.DiscardHandler), home, nil)
	a := newApp(cfg, mgr, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 30}, "dark", true)
	a.modelID = "neuraldeep/qwen3.8-27b"
	a.refreshFooterModel()
	a.foot.now = func() time.Time { return usageNow }
	a.applyProviderUsage(*usageFixtureUpdate())
	if lines := a.foot.Render(120); len(lines) != 3 {
		t.Fatalf("footer before the switch = %q", lines)
	}
	a.switchTheme("light")
	lines := a.foot.Render(120)
	if len(lines) != 3 || !strings.Contains(plain(lines[2]), "3h 3% (resets 17:59)") {
		t.Fatalf("footer after the switch = %q", lines)
	}
}

func TestFooterShowsUsageOnlyForTheActiveProvider(t *testing.T) {
	f := newFooter(newTheme("dark"), ".")
	f.now = func() time.Time { return usageNow }
	f.SetModel("neuraldeep/qwen3.8-27b", "")
	f.SetUsage(usageFixtureUpdate())
	lines := f.Render(120)
	if len(lines) != 3 || !strings.Contains(plain(lines[2]), "3h 3%") {
		t.Fatalf("footer = %q", lines)
	}
	f.SetModel("stub/model", "")
	if lines = f.Render(120); len(lines) != 2 {
		t.Fatalf("another provider must hide the line: %q", lines)
	}
	// A foreign row's update never blanks the active provider's line.
	other := usageFixtureUpdate()
	other.Provider = "nd-work"
	f.SetUsage(other)
	f.SetModel("neuraldeep/qwen3.8-27b", "")
	if lines = f.Render(120); len(lines) != 3 || !strings.Contains(plain(lines[2]), "3h 3%") {
		t.Fatalf("foreign update hid the active line: %q", lines)
	}
	f.SetModel("neuraldeep/qwen3.6-35b-a3b", "")
	if lines = f.Render(120); len(lines) != 3 || !strings.Contains(plain(lines[2]), "∞ volume") {
		t.Fatalf("unlimited model must read ∞: %q", lines)
	}
}

// A resuming update (the agent waiting for a hit limit to lift) drives the
// live status row and leaves the footer's hub snapshot, the notices and the
// reset timer alone: it is the turn talking, not the usage source.
func TestUsageResumingUpdateDrivesTheStatusRowOnly(t *testing.T) {
	a := &App{updatesCh: make(chan updateMsg, 8)}
	a.foot = newFooter(newTheme("dark"), ".")
	a.chat = &tui.Container{}
	a.modelID = "neuraldeep/qwen3.8-27b"
	a.sessionID = "s1"
	armed := 0
	a.usageAfterFn = func(_ time.Duration, _ func()) func() bool {
		armed++
		return func() bool { return true }
	}
	resuming := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "neuraldeep",
		ProviderType:  "neuraldeep",
		Blocked:       true,
		Resuming:      true,
		RetryAt:       "2026-09-06T17:59:59Z",
		RetryInSec:    767,
	}
	a.applyProviderUsage(resuming)
	status := a.statusMessage()
	if !strings.HasPrefix(status, "Usage limit reached · resuming at ") {
		t.Fatalf("status row = %q, want the resuming line", status)
	}
	if a.foot.Usage() != nil {
		t.Fatal("a resuming update must not replace the footer's hub snapshot")
	}
	if armed != 0 {
		t.Fatalf("a resuming update armed %d reset timers, want none", armed)
	}
	if len(a.chat.Children()) != 0 {
		t.Fatalf("a resuming update posted %d transcript rows, want none", len(a.chat.Children()))
	}
	// The countdown is re-sent every 20 s with the same reset time: the
	// status keeps its start time rather than restarting.
	started := a.stepStatus.startedAt
	resuming.RetryInSec = 747
	a.applyProviderUsage(resuming)
	if a.stepStatus.startedAt != started {
		t.Fatal("a re-sent countdown must not restart the status clock")
	}
}
