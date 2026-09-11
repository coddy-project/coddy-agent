//go:build cli

package cli

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Provider usage on the status bar: the third footer line shows how much of
// the account's quota behind the active model is spent and when it resets
// (plus the wallet for wallet keys), a transcript notice warns at 80 % and
// on a hit limit, and /usage prints the whole breakdown. The numbers come
// from acp.ProviderUsageUpdate, which the manager publishes at session ready
// and at turn end; the console asks for one after /model and when a window's
// reset passes. Design record: docs/plans/neuraldeep-usage.md.

const (
	// usageWarnPercent is where a window's segment turns to the warning role
	// and the transcript gets its notice (Claude Desktop's threshold).
	usageWarnPercent = 80
	// usageShortBlockSec separates a rate-limit style block, rendered with a
	// countdown, from a window block, rendered with its reset time.
	usageShortBlockSec = 60
	// usageResetGrace is added to a reset deadline before the refresh, so the
	// hub has rolled the window when the console asks.
	usageResetGrace = 2 * time.Second
	// usageFollowUpDelay is the single retry when a refresh after a reset
	// still shows the old window.
	usageFollowUpDelay = 30 * time.Second
	// usageBarCells is the width of the bar in the /usage report.
	usageBarCells = 10
)

// usageResetDue is the internal loop message of the reset timer. forced says
// the deadline was a window reset or a retry time, so the read must reach
// the hub; a deadline from a deferred refresh is a cache read, the backend
// has fetched by then, and asking it to fetch again would only defer again.
// usageResumeDue is the console's own note that the reset a waiting turn
// counted down to has passed: the status row goes back to the model.
type usageResumeDue struct{}

type usageResetDue struct {
	provider string
	forced   bool
}

// usageReport is the internal loop message carrying a /usage answer.
type usageReport struct {
	provider string
	update   *acp.ProviderUsageUpdate
	err      error
}

// usageProviderOf is the provider row name of a model selector.
func usageProviderOf(modelID string) string {
	provider, _, _ := config.SplitModelRef(strings.TrimSpace(modelID))
	return provider
}

// usageModelOf is the upstream model id of a model selector, the part the
// provider's own lists (unlimitedModels) name.
func usageModelOf(modelID string) string {
	_, model, _ := config.SplitModelRef(strings.TrimSpace(modelID))
	return model
}

// usageBrand is the display name of the provider behind an update.
func usageBrand(u *acp.ProviderUsageUpdate) string {
	if u != nil && strings.EqualFold(u.ProviderType, "neuraldeep") {
		return "NeuralDeep"
	}
	if u != nil && u.Provider != "" {
		return tui.SanitizeText(u.Provider)
	}
	return "provider"
}

// modelUnlimited reports whether the active model bypasses the volume
// windows: the key has none, or the model is on the unlimited list.
func modelUnlimited(u *acp.ProviderUsageUpdate, modelID string) bool {
	if u == nil {
		return false
	}
	if u.Unlimited {
		return true
	}
	want := usageModelOf(modelID)
	if want == "" {
		return false
	}
	for _, m := range u.UnlimitedModels {
		if strings.EqualFold(strings.TrimSpace(m), want) {
			return true
		}
	}
	return false
}

// modelBlocked returns the entry refusing the active model, or nil. A model
// gate covers part of the catalogue, so the account snapshot stays green -
// Blocked speaks for the chat class as a whole and says nothing here. The
// selector suffix is compared the way unlimitedModels is.
//
// 10.09.26: without this the footer read a healthy account while every
// request to the selected model came back 429 for a month ahead.
func modelBlocked(u *acp.ProviderUsageUpdate, modelID string) *acp.UsageBlockedModel {
	if u == nil {
		return nil
	}
	want := usageModelOf(modelID)
	if want == "" {
		return nil
	}
	for i := range u.BlockedModels {
		if strings.EqualFold(strings.TrimSpace(u.BlockedModels[i].Model), want) {
			return &u.BlockedModels[i]
		}
	}
	return nil
}

// blockedModelSegment names the refused model and when it comes back. The
// model is named because the account is fine: "limit reached" alone would
// read as the whole key being out, and the operator would stop working
// instead of switching models.
func blockedModelSegment(b *acp.UsageBlockedModel, now time.Time) usageSegment {
	return usageSegment{
		text: tui.SanitizeText(strings.TrimSpace(b.Model)) + " blocked" + resetPhrase(b.RetryAt, now),
		role: roleError,
	}
}

// usagePercent rounds a percentage for display.
func usagePercent(pct float64) int {
	if pct < 0 || math.IsNaN(pct) {
		return 0
	}
	if pct > 100 {
		pct = 100
	}
	return int(math.Round(pct))
}

// usagePlanLabel renders the hub's tier id the way the footer and the
// report show it: sanitised, with its first letter in upper case ("pro"
// reads "Pro", "coder" reads "Coder"), no mapping, so a tier the hub adds
// tomorrow reads as well.
func usagePlanLabel(plan string) string {
	plan = tui.SanitizeText(strings.TrimSpace(plan))
	if plan == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(plan)
	return string(unicode.ToUpper(r)) + plan[size:]
}

// formatResetTime renders a hub timestamp in the reader's clock: the time
// of day within 24 h, the weekday and time within 7 days, the date beyond.
// Both instants are compared as given; at is shown in now's location.
func formatResetTime(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	local := at.In(now.Location())
	switch d := at.Sub(now); {
	case d < 24*time.Hour:
		return local.Format("15:04")
	case d < 7*24*time.Hour:
		return local.Format("Mon 15:04")
	default:
		return local.Format("Jan 2")
	}
}

// parseUsageTime reads a hub RFC3339 timestamp; zero when absent or broken.
func parseUsageTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return t
}

// resetPhrase renders "(resets 20:59)" for a window, or "" without a time.
func resetPhrase(resetsAt string, now time.Time) string {
	at := parseUsageTime(resetsAt)
	if at.IsZero() {
		return ""
	}
	return " (resets " + formatResetTime(at, now) + ")"
}

// formatRub renders rubles with an ASCII minus, a plain space between
// thousands and the ruble sign, the runes the width measurement counts on:
// "-1 229 ₽".
func formatRub(v float64) string {
	rounded := int64(math.Round(v))
	sign := ""
	if rounded < 0 {
		sign = "-"
		rounded = -rounded
	}
	return sign + groupThousands(rounded) + " ₽"
}

// groupThousands renders 1234567 as "1 234 567".
func groupThousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// formatDuration renders seconds as 42s, 12m 05s, 2h 10m.
func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "0s"
	}
	return formatElapsed(time.Duration(seconds) * time.Second)
}

// usageSegment is one piece of the footer line. drop is the order in which
// pieces leave a narrow terminal: the highest number goes first.
type usageSegment struct {
	text string
	role string
	drop int
}

const (
	dropNever = iota
	dropPlan
	dropWeek
	dropDay
	dropWallet
)

// blockerKind classifies a blocker id: timed by a window, timed for under a
// minute, or one that needs a person.
func blockerKind(id string) string {
	switch id {
	case "session_exhausted", "week_exhausted", "daily_capacity_exhausted", "session_cooldown", "abuse_cooldown":
		return "window"
	case "rpm_exhausted":
		return "rate"
	case "key_blocked", "key_cap_blocked":
		return "key"
	case "wallet_empty":
		return "wallet"
	case "user_blocked":
		return "account"
	default:
		return ""
	}
}

// blockedSegment renders a Blocked update: the reset time for a window
// block, a countdown for a short one, a plain reason otherwise.
func blockedSegment(u *acp.ProviderUsageUpdate, now time.Time) usageSegment {
	kind := ""
	for _, id := range u.Blockers {
		if k := blockerKind(id); k != "" {
			kind = k
			break
		}
	}
	switch kind {
	case "key":
		return usageSegment{text: "key blocked", role: roleError}
	case "wallet":
		return usageSegment{text: "wallet empty", role: roleError}
	case "account":
		return usageSegment{text: "account blocked", role: roleError}
	case "rate":
		return usageSegment{text: "rate limited (retry in " + formatDuration(u.RetryInSec) + ")", role: roleWarning}
	case "window":
		if u.RetryInSec > 0 && u.RetryInSec < usageShortBlockSec {
			return usageSegment{text: "rate limited (retry in " + formatDuration(u.RetryInSec) + ")", role: roleWarning}
		}
		return usageSegment{text: "limit reached" + resetPhrase(u.RetryAt, now), role: roleError}
	}
	if len(u.Blockers) > 0 {
		return usageSegment{text: "blocked: " + tui.SanitizeText(u.Blockers[0]), role: roleError}
	}
	return usageSegment{text: "blocked", role: roleError}
}

// usageFooterSegments builds the footer line for an update and the active
// model. Every string that came from the hub is sanitised here.
func usageFooterSegments(u *acp.ProviderUsageUpdate, modelID string, now time.Time) []usageSegment {
	if u == nil || u.Unsupported {
		return nil
	}
	if u.Error == "unauthorized" {
		// A rejected key outranks whatever numbers were read with it.
		provider := tui.SanitizeText(u.Provider)
		return []usageSegment{{text: provider + ": key rejected, run coddy providers login " + provider, role: roleWarning}}
	}
	if u.Error != "" && len(u.Windows) == 0 && u.Wallet == nil && !u.Blocked {
		return nil
	}
	var segs []usageSegment
	if plan := usagePlanLabel(u.Plan); plan != "" {
		segs = append(segs, usageSegment{text: plan, role: roleDim, drop: dropPlan})
	}
	blockedModel := modelBlocked(u, modelID)
	switch {
	case u.Blocked:
		segs = append(segs, blockedSegment(u, now))
	case blockedModel != nil:
		segs = append(segs, blockedModelSegment(blockedModel, now))
	case modelUnlimited(u, modelID):
		segs = append(segs, usageSegment{text: "∞ volume", role: roleDim})
	default:
		for _, w := range u.Windows {
			if w.ID == "day" {
				continue
			}
			segs = append(segs, windowSegment(w, now, dropFor(w.ID)))
		}
	}
	for _, w := range u.Windows {
		if w.ID == "day" && (w.UsedPercent > 0 || w.Exhausted) && !u.Blocked {
			segs = append(segs, windowSegment(w, now, dropDay))
		}
	}
	if u.Wallet != nil {
		role := roleDim
		if u.Wallet.BalanceRub < 0 {
			role = roleWarning
		}
		segs = append(segs, usageSegment{text: "wallet " + formatRub(u.Wallet.BalanceRub), role: role, drop: dropWallet})
	}
	if u.Error != "" && u.Stale {
		segs = append(segs, usageSegment{text: "(stale)", role: roleDim})
	}
	return segs
}

func dropFor(windowID string) int {
	if windowID == "week" {
		return dropWeek
	}
	return dropNever
}

// windowSegment renders "3h 3% (resets 20:59)"; the warning role from the
// threshold up.
func windowSegment(w acp.UsageWindow, now time.Time, drop int) usageSegment {
	label := tui.SanitizeText(strings.TrimSpace(w.Label))
	if label == "" {
		label = tui.SanitizeText(w.ID)
	}
	pct := usagePercent(w.UsedPercent)
	role := roleDim
	if pct >= usageWarnPercent || w.Exhausted {
		role = roleWarning
	}
	return usageSegment{text: fmt.Sprintf("%s %d%%%s", label, pct, resetPhrase(w.ResetsAt, now)), role: role, drop: drop}
}

// renderUsageLine joins the segments with dim separators and drops the most
// dispensable ones until the line fits the width; whatever is left is
// truncated like the other footer lines.
func renderUsageLine(th *tui.Theme, segs []usageSegment, width int) string {
	if len(segs) == 0 || width <= 0 {
		return ""
	}
	current := append([]usageSegment(nil), segs...)
	for {
		line := joinUsageSegments(th, current)
		if tui.VisibleWidth(line) <= width || len(current) <= 1 {
			return tui.TruncateToWidth(line, width, "")
		}
		// Drop the segment with the highest drop rank; ties keep the first.
		victim, rank := -1, dropNever
		for i, s := range current {
			if s.drop > rank {
				victim, rank = i, s.drop
			}
		}
		if victim < 0 {
			return tui.TruncateToWidth(line, width, "")
		}
		current = append(current[:victim], current[victim+1:]...)
	}
}

func joinUsageSegments(th *tui.Theme, segs []usageSegment) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString(th.Fg(roleDim, " • "))
		}
		b.WriteString(th.Fg(s.role, s.text))
	}
	return b.String()
}

// usageBar renders a ten-cell bar of ▮ and ▯.
func usageBar(pct float64) string {
	filled := int(math.Round(float64(usageBarCells) * math.Min(math.Max(pct, 0), 100) / 100))
	if pct > 0 && filled == 0 {
		filled = 1
	}
	return strings.Repeat("▮", filled) + strings.Repeat("▯", usageBarCells-filled)
}

// usageReportLines renders the /usage block: every window with its bar and
// counters, the live minute, the cooldown, the wallet, and the snapshot's
// age.
func usageReportLines(u *acp.ProviderUsageUpdate, modelID string, now time.Time) []string {
	if u == nil {
		return nil
	}
	head := usageBrand(u)
	if plan := usagePlanLabel(u.Plan); plan != "" {
		head += " · " + plan
	}
	if key := tui.SanitizeText(strings.TrimSpace(u.KeyName)); key != "" {
		head += " · key " + key
	}
	if u.Blocked {
		head += " · " + blockedSegment(u, now).text
	} else if b := modelBlocked(u, modelID); b != nil {
		head += " · " + blockedModelSegment(b, now).text
	}
	lines := []string{head}
	if u.Error == "unauthorized" {
		provider := tui.SanitizeText(u.Provider)
		return append(lines, "  key rejected, run coddy providers login "+provider)
	}
	if modelUnlimited(u, modelID) {
		lines = append(lines, "  volume        ∞ (this model bypasses the session and week windows)")
	}
	for _, w := range u.Windows {
		name := tui.SanitizeText(w.ID)
		if label := tui.SanitizeText(strings.TrimSpace(w.Label)); label != "" && label != name {
			name += " (" + label + ")"
		}
		row := fmt.Sprintf("  %-14s %s  %3d%%", name, usageBar(w.UsedPercent), usagePercent(w.UsedPercent))
		if w.Used != nil && w.Limit != nil {
			row += fmt.Sprintf("  %s / %s", groupThousands(int64(*w.Used)), groupThousands(int64(*w.Limit)))
		}
		if at := parseUsageTime(w.ResetsAt); !at.IsZero() {
			row += "  resets " + formatResetTime(at, now)
		}
		if w.Exhausted {
			row += "  exhausted"
		}
		lines = append(lines, row)
	}
	if u.Rate != nil {
		lines = append(lines, fmt.Sprintf("  %-14s %d / %d this minute", "rpm", u.Rate.Used, u.Rate.Limit))
	}
	cooldown := "none"
	if u.CooldownSec > 0 {
		cooldown = formatDuration(u.CooldownSec)
	}
	lines = append(lines, fmt.Sprintf("  %-14s %s", "cooldown", cooldown))
	if u.Wallet != nil {
		lines = append(lines, fmt.Sprintf("  %-14s %s (%s spent in 30 days)", "wallet", formatRub(u.Wallet.BalanceRub), formatRub(u.Wallet.SpentRub30d)))
	}
	if u.RefreshPending {
		lines = append(lines, fmt.Sprintf("  refresh in %s (pacing)", formatDuration(u.RefreshInSec)))
	}
	if u.Error != "" && u.Stale {
		lines = append(lines, "  stale: the latest read failed ("+tui.SanitizeText(u.Error)+")")
	}
	if at := parseUsageTime(u.FetchedAt); !at.IsZero() {
		if age := now.Sub(at); age >= 0 {
			lines = append(lines, "  observed "+formatDuration(int(age.Seconds()))+" ago")
		}
	}
	return lines
}

// earliestUsageDeadline is how long until the first reset, retry or
// deferred refresh in the update, plus the grace; zero when nothing is
// pending. forced reports whether that deadline is a reset or a retry (a
// hub read) rather than a deferred refresh (a cache read). The per-minute
// rate is ignored: it would fire every minute.
func earliestUsageDeadline(u *acp.ProviderUsageUpdate) (delay time.Duration, forced bool) {
	if u == nil {
		return 0, false
	}
	best := 0
	consider := func(sec int, hub bool) {
		if sec > 0 && (best == 0 || sec < best) {
			best, forced = sec, hub
		}
	}
	for _, w := range u.Windows {
		consider(w.ResetInSec, true)
	}
	consider(u.RetryInSec, true)
	if u.RefreshPending {
		consider(u.RefreshInSec, false)
	}
	if best == 0 {
		return 0, false
	}
	return time.Duration(best)*time.Second + usageResetGrace, forced
}

// passedResetKey names a window whose reset the snapshot says has passed
// (ResetInSec at zero with a reset time), so a follow-up can be armed once
// per such window; "" when none.
func passedResetKey(u *acp.ProviderUsageUpdate) string {
	if u == nil {
		return ""
	}
	for _, w := range u.Windows {
		if w.ResetInSec == 0 && w.ResetsAt != "" && w.ID != "day" {
			return w.ID + "@" + w.ResetsAt
		}
	}
	return ""
}

// --- App wiring ---

// usageActive reports whether an update belongs to the active model's
// provider; updates for other providers are kept but not shown.
func (a *App) usageActive(u *acp.ProviderUsageUpdate) bool {
	return u != nil && u.Provider != "" && u.Provider == usageProviderOf(a.modelID)
}

// applyProviderUsage adopts an update on the UI goroutine: the footer, the
// notices, the reset timer.
func (a *App) applyProviderUsage(u acp.ProviderUsageUpdate) {
	if u.Unsupported {
		// The row has no usage now: no source, or its panel switched off
		// (providers[].usage_limits_panel: false). A stale line must not
		// outlive that answer, nor may its reset timer ask again.
		if u.Provider != "" && a.foot != nil {
			a.foot.DropUsage(u.Provider)
		}
		if a.usageActive(&u) {
			a.stopUsageTimer()
		}
		return
	}
	if u.Resuming {
		// The turn is waiting for the limit to lift: the live status row
		// shows the countdown, without a running counter; the footer keeps
		// the hub's own snapshot and the notices stay quiet. At the reset
		// the row goes back to waiting for the model, since the re-issued
		// call announces itself only with its first chunk.
		text := "Usage limit reached"
		at := parseUsageTime(u.RetryAt)
		if !at.IsZero() {
			text += " · resuming at " + formatResetTime(at, a.usageNow())
		}
		a.setStatus(liveStatus{verb: text, startedAt: time.Now()})
		a.stopUsageResume()
		if !at.IsZero() {
			// The grace lets the hub roll the window before the read.
			sessionID := a.sessionID
			a.usageResume = a.usageAfter(at.Sub(a.usageNow())+usageResetGrace, func() {
				_ = a.Sender().SendSessionUpdate(sessionID, usageResumeDue{})
			})
		}
		return
	}
	snapshot := u
	// Every provider keeps its latest snapshot on the footer; only the
	// active model's renders, so a foreign row's update never blanks the
	// line during a model switch.
	a.foot.SetUsage(&snapshot)
	if !a.usageActive(&snapshot) {
		return
	}
	a.noticeUsage(&snapshot)
	a.armUsageTimer(&snapshot)
}

// usageNow is the clock of the reset-time wording, shared with the footer
// so tests can pin it.
func (a *App) usageNow() time.Time {
	if a.foot != nil && a.foot.now != nil {
		return a.foot.now()
	}
	return time.Now()
}

// noticeUsage adds the transcript notices: once per window per reset
// period at the threshold, once per retry time on a hit limit.
func (a *App) noticeUsage(u *acp.ProviderUsageUpdate) {
	if a.usageNotified == nil {
		a.usageNotified = make(map[string]bool)
	}
	now := a.usageNow()
	if u.Blocked {
		seg := blockedSegment(u, now)
		key := "blocked@" + u.RetryAt + "@" + strings.Join(u.Blockers, ",")
		if !a.usageNotified[key] {
			a.usageNotified[key] = true
			a.appendStatus(roleError, blockedNotice(seg.text))
		}
		return
	}
	if b := modelBlocked(u, a.modelID); b != nil {
		key := "model@" + b.Model + "@" + b.RetryAt
		if !a.usageNotified[key] {
			a.usageNotified[key] = true
			a.appendStatus(roleError, blockedNotice(blockedModelSegment(b, now).text))
		}
		return
	}
	if modelUnlimited(u, a.modelID) {
		return
	}
	for _, w := range u.Windows {
		pct := usagePercent(w.UsedPercent)
		if pct < usageWarnPercent {
			continue
		}
		key := w.ID + "@" + w.ResetsAt
		if a.usageNotified[key] {
			continue
		}
		a.usageNotified[key] = true
		label := tui.SanitizeText(strings.TrimSpace(w.Label))
		if label == "" {
			label = tui.SanitizeText(w.ID)
		}
		msg := fmt.Sprintf("You've used %d%% of your %s %s limit", pct, usageBrand(u), label)
		if at := parseUsageTime(w.ResetsAt); !at.IsZero() {
			msg += " · resets " + formatResetTime(at, now)
		}
		a.appendStatus(roleWarning, msg)
	}
}

// blockedNotice words the transcript notice of a block: "Usage limit
// reached (resets 20:59)" for a window, the reason with a capital for the
// rest ("Rate limited (retry in 42s)", "Key blocked").
func blockedNotice(segment string) string {
	if strings.HasPrefix(segment, "limit reached") {
		return "Usage " + segment
	}
	if segment == "" {
		return "Usage blocked"
	}
	first, size := utf8.DecodeRuneInString(segment)
	return string(unicode.ToUpper(first)) + segment[size:]
}

// armUsageTimer schedules the refresh for the first reset in the update,
// replacing the previous timer; when a reset has already passed and the
// snapshot still shows it, one follow-up is armed for that window.
func (a *App) armUsageTimer(u *acp.ProviderUsageUpdate) {
	a.stopUsageTimer()
	delay, forced := earliestUsageDeadline(u)
	// A window whose reset passed while the snapshot still shows it gets one
	// follow-up read, whatever the other windows' deadlines are: the week
	// window alone would otherwise park the timer for days.
	if key := passedResetKey(u); key != "" && a.usageFollowUp != key {
		a.usageFollowUp = key
		if delay == 0 || usageFollowUpDelay < delay {
			delay, forced = usageFollowUpDelay, true
		}
	}
	if delay == 0 {
		return
	}
	provider, sessionID := u.Provider, a.sessionID
	a.usageTimer = a.usageAfter(delay, func() {
		_ = a.Sender().SendSessionUpdate(sessionID, usageResetDue{provider: provider, forced: forced})
	})
}

// stopUsageResume drops the pending resume note (a turn ended, or a newer
// countdown replaced it).
func (a *App) stopUsageResume() {
	if a.usageResume != nil {
		a.usageResume()
		a.usageResume = nil
	}
}

func (a *App) stopUsageTimer() {
	if a.usageTimer != nil {
		a.usageTimer()
		a.usageTimer = nil
	}
}

// usageAfter schedules fn after d and returns its stop; tests inject a fake.
func (a *App) usageAfter(d time.Duration, fn func()) func() bool {
	if a.usageAfterFn != nil {
		return a.usageAfterFn(d, fn)
	}
	t := time.AfterFunc(d, fn)
	return t.Stop
}

// refreshUsage asks the backend for the usage behind provider on a worker
// and feeds the answer back as an update; refresh asks for a fresh read.
func (a *App) refreshUsage(provider string, refresh bool) {
	provider = strings.TrimSpace(provider)
	if provider == "" || a.mgr == nil || a.workCtx == nil {
		return
	}
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, 30*time.Second)
		defer cancel()
		u, err := a.mgr.ProviderUsageForSession(ctx, sessionID, provider, refresh)
		if err != nil || u == nil {
			return
		}
		// An unsupported answer travels too: it takes a stale line down when
		// the row's panel was switched off since the last snapshot.
		_ = a.Sender().SendSessionUpdate(sessionID, *u)
	}()
}

// showUsage is the /usage command: a fresh read printed as a block.
func (a *App) showUsage() {
	provider := usageProviderOf(a.modelID)
	if provider == "" {
		a.appendStatus(roleWarning, "usage: no model selected")
		return
	}
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, 30*time.Second)
		defer cancel()
		u, err := a.mgr.ProviderUsageForSession(ctx, sessionID, provider, true)
		_ = a.Sender().SendSessionUpdate(sessionID, usageReport{provider: provider, update: u, err: err})
	}()
}

// applyUsageReport renders a /usage answer on the UI goroutine.
func (a *App) applyUsageReport(r usageReport) {
	switch {
	case r.err != nil:
		a.appendStatus(roleError, "usage: "+tui.SanitizeText(r.err.Error()))
	case r.update == nil:
		a.appendStatus(roleError, "usage: no answer")
	case r.update.Disabled:
		// The type has a source, the operator switched this row's panel off.
		a.applyProviderUsage(*r.update)
		a.appendStatus(roleDim, tui.SanitizeText(r.provider)+": usage limits panel is switched off in config (providers[].usage_limits_panel: false)")
	case r.update.Unsupported:
		a.applyProviderUsage(*r.update)
		a.appendStatus(roleDim, tui.SanitizeText(r.provider)+" reports no account usage (provider type "+tui.SanitizeText(r.update.ProviderType)+")")
	default:
		a.applyProviderUsage(*r.update)
		a.appendStatus(roleDim, strings.Join(usageReportLines(r.update, a.modelID, a.usageNow()), "\n"))
	}
}
