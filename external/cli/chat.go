//go:build cli

package cli

import (
	"encoding/json"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// userMessage renders one submitted prompt inside a full-width background box
// (pi UserMessageComponent: Box(1,1,userMessageBg) + text).
type userMessage struct {
	tui.Container
}

func newUserMessage(theme *tui.Theme, text string) *userMessage {
	u := &userMessage{}
	// One clear separator row keeps the box from sitting flush against the
	// block above it (pi spacing).
	u.AddChild(tui.NewSpacer(1))
	box := tui.NewBox(1, 1, theme.BgFn(roleUserMsgBg))
	box.AddChild(tui.NewText(theme.Fg(roleUserMsgText, tui.SanitizeText(text)), 0, 0, nil))
	u.AddChild(box)
	return u
}

// assistantMessage renders one assistant turn: optional thinking block plus
// streamed markdown (pi AssistantMessageComponent).
type assistantMessage struct {
	tui.Container

	theme    *tui.Theme
	mdTheme  tui.MarkdownTheme
	thinking *tui.Markdown
	answer   *tui.Markdown

	thinkingText  string
	answerText    string
	hideThinking  bool
	thinkingLabel *tui.Text
}

func newAssistantMessage(theme *tui.Theme, mdTheme tui.MarkdownTheme, hideThinking bool) *assistantMessage {
	return &assistantMessage{theme: theme, mdTheme: mdTheme, hideThinking: hideThinking}
}

// AppendThinking adds a reasoning delta.
func (a *assistantMessage) AppendThinking(delta string) {
	a.thinkingText += tui.SanitizeText(delta)
	a.rebuild()
}

// AppendText adds an answer delta.
func (a *assistantMessage) AppendText(delta string) {
	a.answerText += tui.SanitizeText(delta)
	a.rebuild()
}

// SetHideThinking collapses or expands the thinking block.
func (a *assistantMessage) SetHideThinking(hide bool) {
	a.hideThinking = hide
	a.rebuild()
}

// HasContent reports whether anything was streamed yet.
func (a *assistantMessage) HasContent() bool { return a.thinkingText != "" || a.answerText != "" }

func (a *assistantMessage) rebuild() {
	a.Clear()
	if a.thinkingText == "" && a.answerText == "" {
		return
	}
	// Separator row before the block: assistant text follows a user box or a
	// tool box and must not glue to it (pi spacing).
	a.AddChild(tui.NewSpacer(1))
	if a.thinkingText != "" {
		if a.hideThinking {
			if a.thinkingLabel == nil {
				a.thinkingLabel = tui.NewText(a.theme.Italic(a.theme.Fg(roleThinking, "Thinking...")), 1, 0, nil)
			}
			a.AddChild(a.thinkingLabel)
			a.AddChild(tui.NewSpacer(1))
		} else {
			think := tui.NewMarkdown("", 1, 0, a.mdTheme)
			think.SetText(a.theme.Italic(a.theme.Fg(roleThinking, a.thinkingText)))
			a.AddChild(think)
			a.AddChild(tui.NewSpacer(1))
		}
	}
	if a.answerText != "" {
		md := tui.NewMarkdown(a.answerText, 1, 0, a.mdTheme)
		a.AddChild(md)
	}
	a.thinking, a.answer = nil, nil
}

// collapsedPreviewLines is the client-side preview cap (pi shows 10 lines
// then the expand hint; the server already truncates previews at 19 lines).
const collapsedPreviewLines = 10

// toolBox renders one tool call: Spacer + Box whose background tracks the
// call status (pi ToolExecutionComponent).
type toolBox struct {
	tui.Container

	theme  *tui.Theme
	id     string
	name   string
	kind   string
	args   string
	status string // pending | in_progress | completed | failed | cancelled

	preview    string
	fullText   string
	expanded   bool
	loadFailed bool
	box        *tui.Box
	loadFull   func(id string) (string, bool)
	omitted    int
	totalOut   int
	hasResult  bool
}

func newToolBox(theme *tui.Theme, id, name, kind string, loadFull func(id string) (string, bool)) *toolBox {
	tb := &toolBox{theme: theme, id: id, name: tui.SanitizeText(name), kind: kind, status: "pending", loadFull: loadFull}
	tb.rebuild()
	return tb
}

// SetArgs stores the raw argument JSON streamed for the call.
func (t *toolBox) SetArgs(argsJSON string) {
	t.args = argsJSON
	t.rebuild()
}

// SetStatus updates the call status and optional preview text.
func (t *toolBox) SetStatus(status, preview string, omitted, total int) {
	t.status = status
	if preview != "" {
		t.preview = tui.SanitizeText(preview)
		t.hasResult = true
	}
	t.omitted = omitted
	t.totalOut = total
	t.rebuild()
}

// SetExpanded toggles full output (read from disk on first expand).
func (t *toolBox) SetExpanded(expanded bool) {
	t.expanded = expanded
	if expanded && t.fullText == "" && t.loadFull != nil {
		if full, ok := t.loadFull(t.id); ok {
			t.fullText = tui.SanitizeText(full)
		} else {
			t.loadFailed = true
		}
	}
	t.rebuild()
}

// Expanded reports the current expand state.
func (t *toolBox) Expanded() bool { return t.expanded }

func (t *toolBox) bgRole() string {
	switch t.status {
	case "completed":
		return roleToolOKBg
	case "failed", "cancelled":
		return roleToolErrBg
	default:
		return roleToolPendBg
	}
}

// title derives the display title from the tool name and streamed args
// (read/write show the path, run_command shows `$ command`).
func (t *toolBox) title() string {
	var parsed map[string]interface{}
	arg := func(keys ...string) string {
		if parsed == nil {
			if err := json.Unmarshal([]byte(t.args), &parsed); err != nil {
				parsed = map[string]interface{}{}
			}
		}
		for _, k := range keys {
			if v, ok := parsed[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	switch t.name {
	case "run_command":
		if cmd := arg("command"); cmd != "" {
			return t.theme.Bold("$ " + tui.SanitizeText(firstLine(cmd)))
		}
	case "read", "write", "edit", "apply_patch":
		if p := arg("path", "file_path", "filename"); p != "" {
			return t.theme.Bold(t.name) + " " + t.theme.Fg(roleAccent, tui.SanitizeText(p))
		}
	}
	return t.theme.Bold(t.name)
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx] + " ..."
	}
	return s
}

func (t *toolBox) rebuild() {
	t.Clear()
	t.AddChild(tui.NewSpacer(1))
	box := tui.NewBox(1, 1, t.theme.BgFn(t.bgRole()))
	box.AddChild(tui.NewText(t.theme.Fg(roleToolTitle, t.title()), 0, 0, nil))
	if t.status == "cancelled" {
		box.AddChild(tui.NewText(t.theme.Fg(roleError, "cancelled"), 0, 0, nil))
	}
	body := t.preview
	if t.expanded && t.fullText != "" {
		body = t.fullText
	}
	if t.expanded && t.loadFailed {
		box.AddChild(tui.NewText(t.theme.Fg(roleDim, "full output unavailable (tool_calls result missing)"), 0, 0, nil))
	}
	if body != "" {
		lines := strings.Split(body, "\n")
		shown := lines
		hiddenCount := t.omitted
		if !t.expanded && len(lines) > collapsedPreviewLines {
			shown = lines[:collapsedPreviewLines]
			hiddenCount += len(lines) - collapsedPreviewLines
		}
		box.AddChild(tui.NewSpacer(1))
		box.AddChild(tui.NewText(t.theme.Fg(roleToolOutput, strings.Join(shown, "\n")), 0, 0, nil))
		if !t.expanded && (hiddenCount > 0 || len(lines) > collapsedPreviewLines) {
			hint := "... (ctrl+o to expand)"
			if hiddenCount > 0 {
				hint = "... (" + itoa(hiddenCount) + " more lines, ctrl+o to expand)"
			}
			box.AddChild(tui.NewText(t.theme.Fg(roleDim, hint), 0, 0, nil))
		}
	}
	t.box = box
	t.AddChild(box)
}

// droppedAmount renders a byte count the way the block reports it: whole
// kibibytes once there are any, plain bytes below that, never a rounded "0".
func droppedAmount(n int64) string {
	if n < 1024 {
		return itoa(int(n)) + " bytes"
	}
	return itoa(int(n/1024)) + " KiB"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// shellBox renders one command the operator started with the `!!` prefix: the
// command line in the bash-mode color, its output, and the exit status. It is
// deliberately not a tool box - nothing here is persisted, so the full capture
// lives in memory instead of in `sessions/<id>/tool_calls/`.
type shellBox struct {
	tui.Container

	theme    *tui.Theme
	command  string
	output   string
	dropped  int64
	expanded bool
	done     bool
	// stopAsked records that the operator asked for this run to end. It is
	// pinned on the block, not read off console state when the command
	// finishes: by then the console may have moved on.
	stopAsked bool
	// released records that the console gave up waiting for a command that
	// would not die, so the block stops advertising a key that no longer acts.
	released bool
	exitCode int
	err      error
}

func newShellBox(theme *tui.Theme, command string) *shellBox {
	b := &shellBox{theme: theme, command: tui.SanitizeText(command), exitCode: -1}
	b.rebuild()
	return b
}

// SetOutput replaces the captured output; dropped counts the bytes that fell
// off the front of the capture because the command printed more than the
// runner keeps.
func (b *shellBox) SetOutput(text string, dropped int64) {
	b.output = tui.SanitizeText(text)
	b.dropped = dropped
	b.rebuild()
}

// Finish closes the block with an exit code; err is set when the command could
// not be started at all.
func (b *shellBox) Finish(exitCode int, err error) {
	b.done, b.exitCode, b.err = true, exitCode, err
	b.rebuild()
}

// RequestStop records that the operator asked this command to end, so the
// finished block can tell a kill it asked for from a signal it did not.
func (b *shellBox) RequestStop() {
	b.stopAsked = true
	b.rebuild()
}

// Release marks a run the console stopped waiting for. The command may still
// be alive; if it ever exits, the block completes as usual.
func (b *shellBox) Release() {
	b.released = true
	b.rebuild()
}

// SetExpanded switches between the output tail and the full capture.
func (b *shellBox) SetExpanded(expanded bool) {
	b.expanded = expanded
	b.rebuild()
}

func (b *shellBox) bgRole() string {
	switch {
	case !b.done:
		return roleToolPendBg
	case b.err == nil && !b.stoppedByOperator() && b.exitCode == 0:
		return roleToolOKBg
	default:
		return roleToolErrBg
	}
}

// body returns the visible output plus how many leading lines it hides. The
// collapsed view keeps the tail, because that is where a command's verdict
// sits (a tool box shows the head: the model reads the whole result anyway).
func (b *shellBox) body() (text string, hidden int) {
	trimmed := strings.TrimRight(b.output, "\n")
	if trimmed == "" {
		return "", 0
	}
	lines := strings.Split(trimmed, "\n")
	if b.expanded || len(lines) <= collapsedPreviewLines {
		return trimmed, 0
	}
	cut := len(lines) - collapsedPreviewLines
	return strings.Join(lines[cut:], "\n"), cut
}

// status is the closing row: progress while the command runs, the failure
// otherwise. A clean exit says nothing - the green background already does.
func (b *shellBox) status() (text, role string) {
	switch {
	case !b.done && b.released:
		return "left running (the console stopped waiting for it)", roleWarning
	case !b.done && b.stopAsked:
		return "stopping (escape again to leave it)", roleWarning
	case !b.done:
		return "running (escape to stop)", roleDim
	case b.err != nil:
		return "command failed: " + tui.SanitizeText(b.err.Error()), roleError
	case b.stoppedByOperator():
		return "stopped", roleError
	case b.exitCode < 0:
		// Killed by a signal nobody here asked for (a command that kills
		// itself, the OOM killer, an outside kill).
		return "terminated", roleError
	case b.exitCode > 0:
		return "exit " + itoa(b.exitCode), roleError
	default:
		return "", roleDim
	}
}

// stoppedByOperator reports a run the operator ended. A clean exit still reads
// as one: the command finished in the window between the key and the kill, and
// calling that a cancellation would be a lie (unix reports -1 for a signal,
// Windows usually 1 after taskkill, so the code alone cannot say).
func (b *shellBox) stoppedByOperator() bool {
	return b.stopAsked && b.exitCode != 0
}

func (b *shellBox) rebuild() {
	b.Clear()
	b.AddChild(tui.NewSpacer(1))
	box := tui.NewBox(1, 1, b.theme.BgFn(b.bgRole()))
	box.AddChild(tui.NewText(b.theme.Bold(b.theme.Fg(roleBashMode, "$ "+b.command)), 0, 0, nil))
	if b.dropped > 0 {
		box.AddChild(tui.NewText(b.theme.Fg(roleDim, "... ("+droppedAmount(b.dropped)+" of earlier output dropped)"), 0, 0, nil))
	}
	if body, hidden := b.body(); body != "" {
		box.AddChild(tui.NewSpacer(1))
		box.AddChild(tui.NewText(b.theme.Fg(roleToolOutput, body), 0, 0, nil))
		if hidden > 0 {
			box.AddChild(tui.NewText(b.theme.Fg(roleDim, "... ("+itoa(hidden)+" earlier lines, ctrl+o to expand)"), 0, 0, nil))
		}
	}
	if text, role := b.status(); text != "" {
		box.AddChild(tui.NewText(b.theme.Fg(role, text), 0, 0, nil))
	}
	b.AddChild(box)
}

// statusLine is one dim status row appended to the transcript (pi showStatus).
type statusLine struct{ *tui.Text }

func newStatusLine(theme *tui.Theme, role, msg string) *statusLine {
	return &statusLine{tui.NewText(theme.Fg(role, tui.SanitizeText(msg)), 1, 0, nil)}
}

// planWidget renders the current plan/todo entries above the editor
// (pi plan-mode widget style).
type planWidget struct {
	tui.Container
	theme   *tui.Theme
	entries []planEntry
}

type planEntry struct {
	content string
	status  string
}

func newPlanWidget(theme *tui.Theme) *planWidget { return &planWidget{theme: theme} }

// SetEntries replaces the plan entries.
func (p *planWidget) SetEntries(entries []planEntry) {
	p.entries = entries
	p.Clear()
	if len(entries) == 0 {
		return
	}
	var lines []string
	for _, e := range entries {
		content := tui.SanitizeText(e.content)
		switch e.status {
		case "completed":
			lines = append(lines, p.theme.Fg(roleSuccess, "✓ ")+p.theme.Fg(roleMuted, content))
		case "in_progress":
			lines = append(lines, p.theme.Fg(roleAccent, "◐ ")+content)
		case "failed":
			lines = append(lines, p.theme.Fg(roleError, "✗ ")+content)
		default:
			lines = append(lines, p.theme.Fg(roleDim, "○ ")+content)
		}
	}
	p.AddChild(tui.NewText(strings.Join(lines, "\n"), 1, 0, nil))
}
