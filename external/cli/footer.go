//go:build cli

package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// footer renders the two status lines under the editor (pi FooterComponent):
// line 1: dim cwd (git branch) • session title [• plan]
// line 2: token stats + context percent left, (provider) model • reasoning right.
type footer struct {
	theme *tui.Theme

	cwd       string
	gitBranch string
	title     string
	modeID    string

	tokensIn   int
	tokensOut  int
	ctxPercent float64
	ctxMax     int

	provider  string
	model     string
	reasoning string
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

// SetContext updates the context-window occupancy.
func (f *footer) SetContext(percent float64, maxTokens int) {
	f.ctxPercent, f.ctxMax = percent, maxTokens
}

// SetModel updates the provider/model/reasoning segment.
func (f *footer) SetModel(modelID, reasoning string) {
	f.provider, f.model = splitModelID(modelID)
	f.reasoning = reasoning
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

	line1 := f.cwd
	if f.gitBranch != "" {
		line1 += " (" + f.gitBranch + ")"
	}
	if f.title != "" {
		line1 += " • " + tui.SanitizeText(f.title)
	}
	if f.modeID == "plan" {
		line1 += " • plan"
	}

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
			right = "(" + f.provider + ") " + f.model
		} else {
			right = f.model
		}
		if f.reasoning != "" {
			right += " • " + f.reasoning
		}
	}

	gap := width - tui.VisibleWidth(left) - tui.VisibleWidth(right)
	if gap < 1 {
		gap = 1
	}
	line2 := left + strings.Repeat(" ", gap) + right

	return []string{
		th.Fg(roleDim, tui.TruncateToWidth(line1, width, "...")),
		th.Fg(roleDim, tui.TruncateToWidth(line2, width, "")),
	}
}

func detectGitBranch(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Stderr = nil // never inherit the tty; raw mode must stay clean
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
