//go:build cli

package cli

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

const (
	// localShellPrefix marks a submitted line as an operator command: coddy
	// runs it in the workspace and neither the command nor its output is ever
	// persisted or shown to the model (pi's `!!` bash mode).
	localShellPrefix = "!!"

	// localShellPollInterval is how often a running command's output is
	// pushed into the transcript.
	localShellPollInterval = 120 * time.Millisecond

	// localShellStopGrace is how long a terminated command may exit on its
	// own before the process group is killed.
	localShellStopGrace = 3 * time.Second

	// localShellQuitGrace is the same on the way out of the console. It is
	// shorter than the escape grace because JoinWorkers gives shutdown three
	// seconds in total: unix must reach SIGKILL before that budget runs out,
	// while Windows wants enough of it for taskkill /T to walk the tree.
	localShellQuitGrace = 2 * time.Second
)

// parseLocalCommand splits a submitted buffer into the local shell command.
// The prefix counts at the very start of the buffer only (the editor has
// already trimmed the text by then), so any other first character keeps the
// line on its way to the model.
func parseLocalCommand(text string) (command string, ok bool) {
	if !strings.HasPrefix(text, localShellPrefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, localShellPrefix)), true
}

// unescapeLocalShell strips the one backslash that lets a prompt start with
// the prefix: `\!!careful` reaches the model as `!!careful`. Nothing else is
// escaped, so a backslash anywhere else survives untouched.
func unescapeLocalShell(text string) string {
	if strings.HasPrefix(text, `\`+localShellPrefix) {
		return text[1:]
	}
	return text
}

// busyWithLocalShell refuses console work that would take the screen while a
// local command runs. Modals are the pointed case: an open modal swallows
// escape, which is the only key that can stop the command.
func (a *App) busyWithLocalShell() bool {
	if !a.shellActive {
		return false
	}
	a.appendStatus(roleWarning, "A local command is running (escape to stop it)")
	return true
}

// dispatchLocalShell handles a `!!` submission. It returns true once the text
// was recognised as a local command, whether or not it could be run, so the
// caller never forwards it to the model.
func (a *App) dispatchLocalShell(text string) bool {
	command, ok := parseLocalCommand(text)
	if !ok {
		return false
	}
	switch {
	case a.remoteURL != "":
		// The session workspace lives on the server; running the command here
		// would silently touch a different tree.
		a.appendStatus(roleWarning, "!! runs a command in the local workspace, so it is unavailable with --remote ("+tui.SanitizeText(a.remoteURL)+")")
	case command == "":
		a.appendStatus(roleDim, "Type a command after !!, for example !!git status")
	case a.shellActive:
		a.appendStatus(roleWarning, "A local command is already running (escape to stop it)")
	case a.turnActive:
		a.appendStatus(roleWarning, "A turn is already running (escape to interrupt)")
	default:
		a.startLocalShell(command)
	}
	return true
}

// startLocalShell launches the command and streams its output into a
// transcript block. Nothing here goes through the session: no prompt, no tool
// call, no persistence - that is the whole point of the prefix.
func (a *App) startLocalShell(command string) {
	box := newShellBox(a.theme, command)
	a.chat.AddChild(box)
	a.curShell, a.lastShell = box, box
	box.SetExpanded(a.expanded)

	cmd, err := shell.StartOperatorCommand(command, a.cfg.Paths.CWD)
	if err != nil {
		box.Finish(-1, err)
		a.curShell = nil
		return
	}
	a.shellCmd = cmd
	a.shellActive = true
	a.shellStopping = false

	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ticker := time.NewTicker(localShellPollInterval)
		defer ticker.Stop()
		last := ""
		for {
			select {
			case <-cmd.Done():
				a.postLocalShell(localShellDone{
					box: box, text: cmd.Output(), dropped: cmd.Dropped(), exitCode: cmd.ExitCode(),
				})
				return
			case <-a.closed:
				// The console is leaving: kill the command rather than let it
				// print into the terminal that was just handed back. No drain
				// here - JoinWorkers gives shutdown a few seconds in total, and
				// nobody will read the output anyway.
				cmd.Kill(localShellQuitGrace)
				return
			case <-ticker.C:
				// A silent command must not repaint the transcript.
				if out := cmd.Output(); out != last {
					last = out
					a.postLocalShell(localShellOutput{box: box, text: out, dropped: cmd.Dropped()})
				}
			}
		}
	}()
}

// stopLocalShell kills the running command and everything it spawned. The
// kill itself waits for the process to be reaped, so it runs on a worker and
// never blocks the UI loop.
func (a *App) stopLocalShell() {
	if !a.shellActive {
		return
	}
	if a.shellStopping {
		// The kill already went out and the command still has not been reaped
		// (an uninterruptible process, a taskkill that failed). A second
		// escape gives the console back rather than wedging it forever; the
		// block stays open until the process really ends, if it ever does.
		a.appendStatus(roleWarning, "The local command has not exited yet; releasing the console (the process may still be running)")
		a.releaseLocalShell()
		return
	}
	a.shellStopping = true
	if a.curShell != nil {
		a.curShell.RequestStop()
	}
	a.appendStatus(roleWarning, "Stopping the local command")
	cmd := a.shellCmd
	if cmd == nil {
		return
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		cmd.Terminate(localShellStopGrace)
	}()
}

// releaseLocalShell drops the console's claim on a command that refuses to
// die. The polling worker stays alive and still completes the block if the
// process is eventually reaped.
func (a *App) releaseLocalShell() {
	if a.curShell != nil {
		a.curShell.Release()
	}
	a.shellActive, a.shellStopping = false, false
	a.shellCmd = nil
	a.curShell = nil
}

// postLocalShell queues a local-shell update on the UI loop. The session id is
// empty on purpose: these updates belong to the console, not to a session, and
// must survive a session switch that would otherwise drop them.
func (a *App) postLocalShell(update interface{}) {
	select {
	case a.updatesCh <- updateMsg{update: update}:
	case <-a.closed:
	}
}

// localShellOutput carries the output captured so far, plus how many bytes
// fell off the front of the capture.
type localShellOutput struct {
	box     *shellBox
	text    string
	dropped int64
}

// localShellDone completes a block with the final output and exit status.
type localShellDone struct {
	box      *shellBox
	text     string
	dropped  int64
	exitCode int
	err      error
}
