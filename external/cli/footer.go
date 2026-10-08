//go:build cli

package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// footer renders the status lines under the editor (pi FooterComponent):
// line 1: dim cwd (git branch) • session title [• plan]
// line 2: token stats + context percent left, (provider) model • reasoning right;
// line 3, only while the active model's provider reports account usage:
// plan • window percentages with reset times • wallet (usage.go).
type footer struct {
	theme *tui.Theme

	cwd       string
	gitBranch string
	title     string
	modeID    string

	tokensIn  int
	tokensOut int
	// runningTasks is how many background tasks of the session run right now.
	runningTasks int
	// mcpConnected of mcpTotal configured MCP servers have answered; the
	// segment shows only while mcpPending, i.e. while any is still connecting.
	mcpConnected int
	mcpTotal     int
	mcpPending   bool
	ctxPercent   float64
	ctxMax       int

	provider  string
	model     string
	reasoning string
	// permission is the session's permission mode; anything but ask is
	// named on the first line, bypass in the warning colour (#292).
	permission string
	// overrides are the settings changed for a number of turns.
	overrides []acp.TurnOverride

	// usages holds the latest usage update per subject (usageSubjectKey: the
	// provider row, and for a coddy row the alias too); the active model's
	// renders. now is the clock of the reset-time wording (tests pin it).
	usages map[string]*acp.ProviderUsageUpdate
	now    func() time.Time
}

func newFooter(theme *tui.Theme, cwd string) *footer {
	return &footer{theme: theme, cwd: cwd, gitBranch: detectGitBranch(cwd)}
}

// Invalidate is a no-op; the footer recomputes every render.
func (f *footer) Invalidate() {}

// SetSession updates title and mode.
func (f *footer) SetSession(title, modeID string) { f.title, f.modeID = title, modeID }

// AddTokens accumulates per-call token usage (the update carries per-call
// input/output, so directional counters are summed client-side).
func (f *footer) AddTokens(in, out int) { f.tokensIn += in; f.tokensOut += out }

// ResetTokens clears accumulated counters (new/switched session).
func (f *footer) ResetTokens() { f.tokensIn, f.tokensOut = 0, 0 }

// SetRunningTasks updates how many background tasks of the session run right now.
func (f *footer) SetRunningTasks(n int) { f.runningTasks = n }

// SetMCP updates the count of connected configured MCP servers. The segment
// is shown while pending and leaves the line once every server settled: the
// header names the servers, the footer only says the console is not yet
// holding all of their tools.
func (f *footer) SetMCP(connected, total int, pending bool) {
	f.mcpConnected, f.mcpTotal, f.mcpPending = connected, total, pending
}

// SetContext updates the context-window occupancy.
func (f *footer) SetContext(percent float64, maxTokens int) {
	f.ctxPercent, f.ctxMax = percent, maxTokens
}

// SetModel updates the provider/model/reasoning segment.
func (f *footer) SetModel(modelID, reasoning string) {
	f.provider, f.model = splitModelID(modelID)
	f.reasoning = reasoning
}

// SetSettings adopts a settings snapshot: the permission mode and the
// overrides for the running and the next turns.
func (f *footer) SetSettings(permission string, overrides []acp.TurnOverride) {
	f.permission = permission
	f.overrides = append([]acp.TurnOverride(nil), overrides...)
}

// SetPermission adopts the permission mode alone, keeping the override line.
func (f *footer) SetPermission(permission string) {
	f.permission = permission
}

// overridesText renders the turn overrides: "next 2 turns: model x".
func (f *footer) overridesText() string {
	if len(f.overrides) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f.overrides))
	for _, o := range f.overrides {
		label := o.Setting
		if label == "permission_mode" {
			label = "permissions"
		}
		scope := "this turn"
		switch {
		case o.Active && o.TurnsLeft > 0:
			scope = "this turn +" + itoa(o.TurnsLeft)
		case !o.Active && o.TurnsLeft == 1:
			scope = "next turn"
		case !o.Active:
			scope = "next " + itoa(o.TurnsLeft) + " turns"
		}
		parts = append(parts, scope+": "+label+" "+tui.SanitizeText(o.Value))
	}
	return strings.Join(parts, " • ")
}

// usageSubjectKey names what a usage update is about: the provider row, and
// for a row of type coddy (a remote Coddy's shared models, read per alias) the
// alias too, "provider/alias". A provider name has no slash, so a row's key
// never equals another row's alias key.
func usageSubjectKey(provider, model string) string {
	if model == "" {
		return provider
	}
	return provider + "/" + model
}

// SetUsage adopts a provider usage update for its subject and reports whether
// it did. Snapshots of one subject are ordered by the time they were read
// (usageIsNewer), not by arrival: an answer that was asked for before a pushed
// frame, and lands after it, leaves the newer numbers on the line.
func (f *footer) SetUsage(u *acp.ProviderUsageUpdate) bool {
	if u == nil || u.Provider == "" {
		return false
	}
	if f.usages == nil {
		f.usages = make(map[string]*acp.ProviderUsageUpdate)
	}
	key := usageSubjectKey(u.Provider, u.Model)
	if !usageIsNewer(u, f.usages[key]) {
		return false
	}
	f.usages[key] = u
	return true
}

// DropUsage forgets the snapshot of a subject: the backend answered that it
// has no usage now (its panel switched off in config, or the row retyped), so
// the line must not keep the old numbers. Without a model the answer is about
// the whole provider row, and every alias of a coddy row goes with it.
func (f *footer) DropUsage(provider, model string) {
	if f.usages == nil {
		return
	}
	if model != "" {
		delete(f.usages, usageSubjectKey(provider, model))
		return
	}
	delete(f.usages, provider)
	prefix := provider + "/"
	for key := range f.usages {
		if strings.HasPrefix(key, prefix) {
			delete(f.usages, key)
		}
	}
}

// Usage returns the update of the active model's subject, or nil: the alias's
// own for a coddy row, else the provider row's.
func (f *footer) Usage() *acp.ProviderUsageUpdate {
	if f.provider == "" || f.usages == nil {
		return nil
	}
	if f.model != "" {
		if u := f.usages[usageSubjectKey(f.provider, f.model)]; u != nil {
			return u
		}
	}
	return f.usages[f.provider]
}

// usageLine renders the third line, or "" when nothing applies.
func (f *footer) usageLine(width int) string {
	u := f.Usage()
	if u == nil {
		return ""
	}
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	modelID := f.provider + "/" + f.model
	return renderUsageLine(f.theme, usageFooterSegments(u, modelID, now), width)
}

func splitModelID(id string) (provider, model string) {
	if idx := strings.IndexByte(id, '/'); idx > 0 {
		return id[:idx], id[idx+1:]
	}
	return "", id
}

// Render draws both footer lines padded to width.
func (f *footer) Render(width int) []string {
	th := f.theme

	line1 := tui.SanitizeText(f.cwd)
	if f.gitBranch != "" {
		line1 += " (" + tui.SanitizeText(f.gitBranch) + ")"
	}
	if f.title != "" {
		line1 += " • " + tui.SanitizeText(f.title)
	}
	if f.modeID == "plan" || f.modeID == "ask" {
		line1 += " • " + f.modeID
	}
	// Background tasks outlive the turn that started them, and the status line that
	// counts them goes away with the turn. The footer keeps saying what still runs,
	// and names the command that lists it. The segment closes the line and is the part
	// of it that changes what the operator does next, so when the line does not fit it
	// is the path and the title that give way - a macOS temp folder or a deep monorepo
	// path would otherwise push the count off the screen.
	// The MCP count is transient - it is there while the servers come up
	// after the first frame - and, like the tasks segment, it is what the
	// operator reads. Both notes, and the permission mode after them, are
	// kept whole together: only the path and the title give way.
	notes := ""
	if f.mcpPending {
		notes += " • MCP " + itoa(f.mcpConnected) + "/" + itoa(f.mcpTotal)
	}
	if f.runningTasks > 0 {
		notes += " • " + itoa(f.runningTasks) + " " + plural(f.runningTasks, "task", "tasks") + " running (/tasks)"
	}
	perm := ""
	if f.permission != "" && f.permission != "ask" {
		perm = " • " + strings.ReplaceAll(f.permission, "_", " ")
	}
	if room := width - tui.VisibleWidth(notes) - tui.VisibleWidth(perm); tui.VisibleWidth(line1) > room {
		// On a line too narrow for even a shortened path, the path goes and
		// the notes stay.
		if room >= 8 {
			line1 = tui.TruncateToWidth(line1, room, "...")
		} else if tui.VisibleWidth(notes)+tui.VisibleWidth(perm) <= width {
			line1 = ""
		}
	}
	line1 += notes

	left := ""
	if f.tokensIn > 0 || f.tokensOut > 0 {
		left = "↑" + tui.FormatTokenCount(f.tokensIn) + " ↓" + tui.FormatTokenCount(f.tokensOut) + " "
	}
	if f.ctxMax > 0 {
		left += fmt.Sprintf("%.1f%%/%s (auto)", f.ctxPercent, tui.FormatTokenCount(f.ctxMax))
	}

	right := ""
	if f.model != "" {
		if f.provider != "" {
			right = "(" + tui.SanitizeText(f.provider) + ") " + tui.SanitizeText(f.model)
		} else {
			right = tui.SanitizeText(f.model)
		}
		if f.reasoning != "" {
			right += " • " + tui.SanitizeText(f.reasoning)
		}
	}

	gap := width - tui.VisibleWidth(left) - tui.VisibleWidth(right)
	if gap < 1 {
		gap = 1
	}
	line2 := left + strings.Repeat(" ", gap) + right

	// The permission mode closes the first line when it is not the asking
	// one: bypass in the warning colour, so a session that approves
	// everything never looks like one that asks.
	first := th.Fg(roleDim, tui.TruncateToWidth(line1, width, "..."))
	if perm != "" {
		if room := width - tui.VisibleWidth(perm); room >= 8 {
			role := roleDim
			if f.permission == "bypass" {
				role = roleWarning
			}
			first = th.Fg(roleDim, tui.TruncateToWidth(line1, room, "...")) + th.Fg(role, perm)
		}
	}
	lines := []string{
		first,
		th.Fg(roleDim, tui.TruncateToWidth(line2, width, "")),
	}
	if ov := f.overridesText(); ov != "" {
		lines = append(lines, th.Fg(roleAccent, tui.TruncateToWidth(ov, width, "...")))
	}
	if usage := f.usageLine(width); usage != "" {
		lines = append(lines, usage)
	}
	return lines
}

// gitBranchTimeout bounds the git call behind the footer's branch label. The
// footer is built before the first frame, and the label is decoration: a git
// that does not answer (a credential helper waiting on a prompt, the macOS
// developer-tools stub behind its install dialog) must not hold the console.
var gitBranchTimeout = 3 * time.Second

func detectGitBranch(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitBranchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Stderr = nil // never inherit the tty; raw mode must stay clean
	// A helper git left behind (a credential prompt) still holds the output
	// pipe after the timeout killed git itself; do not wait for it either.
	cmd.WaitDelay = time.Second
	platform.AdaptCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return ""
	}
	return branch
}
