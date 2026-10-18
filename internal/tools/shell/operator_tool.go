package shell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// OperatorCommandToolName is the tool through which the agent asks the
// operator to run a command it was refused.
const OperatorCommandToolName = "operator_command"

const (
	// operatorFrameInterval is how often a running command's output window is
	// sent while it changes.
	operatorFrameInterval = 500 * time.Millisecond
	// operatorFrameLines and operatorFrameBytes bound the output window of one
	// frame: a follower is current after one frame, and a chatty command
	// cannot drain the composer relay's buffer.
	operatorFrameLines = 40
	operatorFrameBytes = 4 << 10
	// operatorStopGrace is how long a stopped command has after the interrupt
	// before its group is terminated, and the terminate's own grace.
	operatorStopGrace = 3 * time.Second
)

// operatorCancelDrain bounds how long a cancelled turn waits for the killed
// command to be reaped: the interrupt's wait, the terminate's grace and the
// time a child holding the output pipe gets (waitPipeDrainDelay), so that in
// practice the request ends with the turn. A variable so a test can shorten
// it.
var operatorCancelDrain = 12 * time.Second

// The phases a frame of a run reports in _meta.coddy.operatorCommand.
const (
	OperatorPhaseRunning  = "running"
	OperatorPhaseStopping = "stopping"
	OperatorPhaseStopped  = "stopped"
	OperatorPhaseEnded    = "ended"
)

// operatorRun is a started operator command, run directly or inside a
// multiplexer.
type operatorRun interface {
	Output() string
	Dropped() int64
	Done() <-chan struct{}
	ExitCode() int
	Interrupt()
	Kill(grace time.Duration)
}

type operatorCommandArgs struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
	CWD     string `json:"cwd"`
}

// OperatorCommandTool is the operator_command tool. Its description names the
// operator shell the settings resolve to, so the model writes for that shell.
func OperatorCommandTool(settings *tooling.OperatorCommandSettings) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        OperatorCommandToolName,
			Description: operatorCommandDescription(settings),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command for the operator to run, exactly as it will run. The operator sees it and cannot edit it. It must be a command that asks nothing: no password, no confirmation, no editor or pager.",
					},
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "Why the operator has to run it: what refused it to you, and what the command does. Shown to the operator next to the command.",
					},
					"cwd": map[string]interface{}{
						"type":        "string",
						"description": "Working directory, relative to the session workspace or absolute. Defaults to the workspace.",
					},
				},
				"required": []interface{}{"command", "reason"},
			},
		},
		// The operator's own click on Run is this tool's gate: it must never
		// meet the permission gate, the allowlist or bypass mode, which would
		// turn a refused command into one that runs by itself.
		RequiresPermission: false,
		Execute:            executeOperatorCommand,
	}
}

func operatorCommandDescription(settings *tooling.OperatorCommandSettings) string {
	spec := ""
	if settings != nil {
		spec = settings.Shell
	}
	shellName := spec
	if sh, err := platform.ResolveShell(spec, runtime.GOOS, nil); err == nil {
		shellName = string(sh.Kind)
	}
	return "Ask the operator to run a shell command that you were refused: run_command was rejected by a policy " +
		"(an unsafe-command check, a toolset or a surface that does not offer it), or the command needs the operator's own account. " +
		"The operator sees the command exactly as you write it, cannot edit it, and presses Run or Decline; while it runs they can Stop it. " +
		"The result is the command's output and exit code. Never use this tool for a command run_command is allowed to run. " +
		"One command per call. The command runs in " + shellName + " on the machine of the session, " + operatorTerminalNote(settings) +
		" The command must not wait for input: nobody types into it. Write the non-interactive form: sudo -n, not sudo " +
		"(it never prompts, so it runs only what needs no password), apt-get -y, ssh -o BatchMode=yes, git with GIT_TERMINAL_PROMPT=0, " +
		"no editor or pager. When a command cannot run without a password or an answer, do not call this tool: " +
		"give the command to the operator in your reply and ask them to run it themselves." +
		" A decline is final for that command in this turn: do not ask again."
}

// operatorTerminalNote says what a prompt does to the command. Interactive
// input is not a feature of the tool: inside a multiplexer the command has a
// terminal, so a prompt does not fail, it hangs until the operator stops it.
func operatorTerminalNote(settings *tooling.OperatorCommandSettings) string {
	if settings != nil && settings.Multiplexer != "" && runtime.GOOS != "windows" {
		return "in a " + settings.Multiplexer + " window: a prompt there does not fail, it hangs until the operator stops the command."
	}
	if runtime.GOOS == "windows" {
		return "with no terminal and empty input; its console has no window, so a program that reads the console hangs until the operator stops the command."
	}
	return "with no terminal and empty input: a prompt usually fails at once, and one that retries or opens a window of its own hangs until the operator stops the command."
}

func executeOperatorCommand(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	var args operatorCommandArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if err := validateOperatorCommand(args.Command); err != nil {
		return "", err
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return "", fmt.Errorf("reason is required: tell the operator why they have to run the command")
	}
	if err := validateOperatorText("reason", reason); err != nil {
		return "", err
	}
	if env == nil {
		return "", errNoOperator
	}
	operator := acp.OperatorCommandsOf(env.Sender)
	if operator == nil {
		return "", errNoOperator
	}
	settings := tooling.OperatorCommandSettings{Enabled: true}
	if env.OperatorCommand != nil {
		settings = *env.OperatorCommand
	}
	if !settings.Enabled {
		return "", fmt.Errorf("operator_command is turned off (tools.operator_command.enable)")
	}
	if env.OperatorDeclined.Has(args.Command) {
		return "The operator already declined this command in this turn. Do not ask again; tell the operator what you could not do.", nil
	}
	commandShell, err := platform.ResolveShell(settings.Shell, runtime.GOOS, nil)
	if err != nil {
		return "", fmt.Errorf("tools.operator_command.shell: %w", err)
	}
	if err := validateOperatorCommandFor(args.Command, commandShell.Kind); err != nil {
		return "", err
	}
	cwd, err := operatorCWD(env.CWD, args.CWD)
	if err != nil {
		return "", err
	}
	// The folder is on the card too.
	if err := operatorFolderFor(cwd, commandShell.Kind); err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	req := acp.OperatorCommandRequest{
		SessionID:  env.SessionID,
		RequestID:  newOperatorRequestID(),
		ToolCallID: env.ToolCallID,
		Command:    args.Command,
		Reason:     reason,
		CWD:        cwd,
		Shell:      string(commandShell.Kind),
		ShellPath:  commandShell.Path,
		Host:       host,
	}
	mux, err := operatorMultiplexer(settings.Multiplexer, env, req)
	if err != nil {
		return "", err
	}
	if mux != nil {
		req.Multiplexer = mux.Name()
		req.AttachCommand = mux.AttachCommand()
	}

	decision, err := operator.RequestOperatorCommand(ctx, req)
	if err != nil {
		return "", err
	}
	if decision == nil {
		return "", fmt.Errorf("the operator's surface gave no answer")
	}
	if decision.Action != acp.OperatorActionRun {
		if decision.Done != nil {
			decision.Done()
		}
		env.OperatorDeclined.Add(args.Command)
		out := "The operator declined to run the command."
		if note := strings.TrimSpace(decision.Note); note != "" {
			out += " Their note: " + note
		}
		return out, nil
	}
	// A Run that lands while the turn is cancelled starts nothing: the turn
	// would interrupt the command at once and leave it half done.
	if err := ctx.Err(); err != nil {
		if decision.Done != nil {
			decision.Done()
		}
		return "", err
	}
	// The folder shown is the folder the command runs in: a link swapped
	// into its path while the card waited is refused.
	if again, err := operatorCWD(env.CWD, args.CWD); err != nil || again != cwd {
		frame := operatorFrame{env: env, req: req, args: argsJSON}
		frame.send(OperatorPhaseEnded, "", -1, 0)
		if decision.Done != nil {
			decision.Done()
		}
		return "", fmt.Errorf("the folder %s changed while the operator decided; the command was not run", cwd)
	}
	var run operatorRun
	if mux != nil {
		run, err = mux.Start(args.Command, cwd, commandShell)
	} else {
		run, err = StartOperatorToolCommand(args.Command, cwd, commandShell)
	}
	if err != nil {
		frame := operatorFrame{env: env, req: req, args: argsJSON}
		frame.send(OperatorPhaseEnded, "", -1, 0)
		if decision.Done != nil {
			decision.Done()
		}
		return "", fmt.Errorf("the operator's command did not start: %w", err)
	}
	return superviseOperatorRunAndRelease(ctx, env, req, argsJSON, run, decision)
}

// superviseOperatorRunAndRelease supervises the run and ends the request once
// the process has ended. A turn cancelled while the process outlives the drain
// returns at once, but the request stays running on every surface - so a Stop
// still reaches the process - until the process has really ended.
func superviseOperatorRunAndRelease(ctx context.Context, env *tooling.Env, req acp.OperatorCommandRequest, argsJSON string, run operatorRun, decision *acp.OperatorCommandDecision) (string, error) {
	out, err := superviseOperatorRun(ctx, env, req, argsJSON, run, decision)
	release := func() {
		if decision.Done != nil {
			decision.Done()
		}
	}
	select {
	case <-run.Done():
		release()
		return out, err
	default:
	}
	// The turn is over: nothing is sent from here on, its stream has ended.
	go func() {
		stop, kill := decision.Stop, decision.Kill
		for {
			select {
			case <-run.Done():
				// The turn was cancelled, and a cancel is a stop: what
				// the shell left in its group ends with it.
				endLeftovers(run)
				release()
				return
			case <-stop:
				stop = nil
				go killIfRunning(run, operatorStopGrace)
			case <-kill:
				kill = nil
				go killIfRunning(run, 0)
			}
		}
	}()
	return out, err
}

var errNoOperator = fmt.Errorf("operator_command needs an operator at the web UI or the console; nobody can answer on this surface")

// superviseOperatorRun streams the run's output window until it ends, applies
// the operator's Stop (interrupt, then terminate after the grace; a second
// Stop terminates at once) and a turn cancel, and renders the result.
func superviseOperatorRun(ctx context.Context, env *tooling.Env, req acp.OperatorCommandRequest, argsJSON string, run operatorRun, decision *acp.OperatorCommandDecision) (string, error) {
	frame := operatorFrame{env: env, req: req, args: argsJSON}
	frame.send(OperatorPhaseRunning, "", 0, 0)

	ticker := time.NewTicker(operatorFrameInterval)
	defer ticker.Stop()
	stop, kill := decision.Stop, decision.Kill
	var escalate <-chan time.Time
	stopped := false
	last := ""
	for {
		select {
		case <-run.Done():
			if !stopped {
				// A Stop and the shell's exit can be ready at once, and
				// select picks either: an accepted Stop is still a stop.
				select {
				case <-decision.Stop:
					stopped = true
				default:
				}
			}
			out := run.Output()
			code := run.ExitCode()
			phase := OperatorPhaseEnded
			if stopped {
				phase = OperatorPhaseStopped
				endLeftovers(run)
			}
			frame.send(phase, out, code, run.Dropped())
			return operatorResult(req, out, run.Dropped(), code, stopped), nil
		case <-stop:
			stop = nil
			stopped = true
			run.Interrupt()
			escalate = time.After(operatorInterruptWait())
			frame.send(OperatorPhaseStopping, run.Output(), 0, run.Dropped())
		case <-kill:
			kill = nil
			stopped = true
			escalate = nil
			go killIfRunning(run, 0)
		case <-escalate:
			escalate = nil
			go killIfRunning(run, operatorStopGrace)
		case <-ctx.Done():
			// A cancel is a stop: the interrupt goes out now, and the card
			// says so at once.
			run.Interrupt()
			frame.send(OperatorPhaseStopping, run.Output(), 0, run.Dropped())
			go func() {
				time.Sleep(operatorInterruptWait())
				killIfRunning(run, operatorStopGrace)
			}()
			drain := time.NewTimer(operatorCancelDrain)
			defer drain.Stop()
			for {
				select {
				case <-run.Done():
					endLeftovers(run)
					frame.send(OperatorPhaseStopped, run.Output(), run.ExitCode(), run.Dropped())
					return "", ctx.Err()
				case <-stop:
					// The interrupt already went out with the cancel, so
					// the operator's Stop is the kill.
					stop = nil
					go killIfRunning(run, 0)
					continue
				case <-kill:
					// A second Stop during the drain kills at once.
					kill = nil
					go killIfRunning(run, 0)
					continue
				case <-drain.C:
					// Still running: the request stays open, so a Stop
					// still reaches it, until the process has ended. This is
					// the last frame: the turn's stream ends with the turn.
					frame.send(OperatorPhaseStopping, run.Output(), 0, run.Dropped())
					return "", ctx.Err()
				}
			}
		case <-ticker.C:
			if out := run.Output(); out != last {
				last = out
				phase := OperatorPhaseRunning
				if stopped {
					phase = OperatorPhaseStopping
				}
				frame.send(phase, out, 0, run.Dropped())
			}
		}
	}
}

// killIfRunning terminates a run that has not ended yet. A run that has is
// left alone: on Windows its pid may already name another process.
// endLeftovers ends what a stopped command's shell left running in its group:
// a stop or a cancel ends the whole command, not only the shell. Not on
// Windows, where the reaped pid can already name another process.
func endLeftovers(run operatorRun) {
	if runtime.GOOS != "windows" {
		run.Kill(0)
	}
}

func killIfRunning(run operatorRun, grace time.Duration) {
	select {
	case <-run.Done():
		return
	default:
	}
	run.Kill(grace)
}

// operatorInterruptWait is how long a stop waits after the interrupt before it
// terminates the group. Windows has no interrupt to wait for: the command's
// console is not Coddy's to deliver Ctrl+C to, so the terminate follows at once.
func operatorInterruptWait() time.Duration {
	if runtime.GOOS == "windows" {
		return 0
	}
	return operatorStopGrace
}

func operatorResult(req acp.OperatorCommandRequest, out string, dropped int64, code int, stopped bool) string {
	var b strings.Builder
	// Where it ran heads the result: the model reads it, and a surface that
	// rebuilds the card from the history after a reload has nothing else.
	if stopped {
		fmt.Fprintf(&b, "The operator stopped the command in %s on %s, in %s (exit code %d). Output so far:\n", req.Shell, req.Host, req.CWD, code)
	} else {
		fmt.Fprintf(&b, "The operator ran the command in %s on %s, in %s (exit code %d):\n", req.Shell, req.Host, req.CWD, code)
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "[the first %d bytes of the output were dropped]\n", dropped)
	}
	if strings.TrimSpace(out) == "" {
		b.WriteString("(no output)")
	} else {
		b.WriteString(out)
	}
	return b.String()
}

// operatorFrame sends the progress of a run as tool_call_update frames. The
// content stays the call's arguments, which is what every client reads from an
// in_progress frame; the run travels in _meta.coddy.operatorCommand.
type operatorFrame struct {
	env  *tooling.Env
	req  acp.OperatorCommandRequest
	args string
}

func (f operatorFrame) send(phase, output string, exitCode int, dropped int64) {
	if f.env == nil || f.env.Sender == nil {
		return
	}
	meta := map[string]interface{}{
		"requestId": f.req.RequestID,
		"phase":     phase,
		"shell":     f.req.Shell,
		"host":      f.req.Host,
		"cwd":       f.req.CWD,
		"output":    outputWindow(output),
	}
	if dropped > 0 {
		meta["droppedBytes"] = dropped
	}
	if phase == OperatorPhaseEnded || phase == OperatorPhaseStopped {
		meta["exitCode"] = exitCode
	}
	if f.req.Multiplexer != "" {
		meta["multiplexer"] = f.req.Multiplexer
	}
	_ = f.env.Sender.SendSessionUpdate(f.req.SessionID, acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    f.req.ToolCallID,
		Status:        "in_progress",
		Content: []acp.ToolCallResultItem{
			{Type: "content", Content: acp.ContentBlock{Type: "text", Text: f.args}},
		},
		Meta: map[string]interface{}{"coddy": map[string]interface{}{"operatorCommand": meta}},
	})
}

// outputWindow is the tail of the output a frame carries: the last
// operatorFrameLines lines, at most operatorFrameBytes, cut at a rune.
func outputWindow(out string) string {
	if n := strings.Count(out, "\n"); n > operatorFrameLines {
		idx := len(out)
		for i := 0; i <= operatorFrameLines && idx > 0; i++ {
			idx = strings.LastIndexByte(out[:idx], '\n')
			if idx < 0 {
				idx = 0
				break
			}
		}
		out = strings.TrimPrefix(out[idx:], "\n")
	}
	if len(out) > operatorFrameBytes {
		cut := len(out) - operatorFrameBytes
		for cut < len(out) && !utf8.RuneStart(out[cut]) {
			cut++
		}
		out = out[cut:]
	}
	return out
}

// validateOperatorCommand refuses a command whose text would not read the way
// the shell gets it: control characters other than a newline and a tab, the
// bidirectional controls and the zero-width characters. The operator must be
// able to trust that what they read is byte for byte what runs.
func validateOperatorCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	return validateOperatorText("command", command)
}

func validateOperatorText(field, text string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	for i, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) || hiddenRune(r) {
			return fmt.Errorf("%s holds the invisible or control character U+%04X at byte %d: the operator would not read what runs; write the command without it", field, r, i)
		}
	}
	return nil
}

// hiddenRune reports the characters that change how text displays without
// showing, or show as nothing: every format character (Cf: bidirectional
// controls, zero-width characters, invisible operators, tag characters), the
// line and paragraph separators, the variation selectors, the combining
// grapheme joiner and the Hangul fillers.
func hiddenRune(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF, r == 0x180B, r == 0x180C, r == 0x180D, r == 0x180F:
		return true
	case r == 0x034F, r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0:
		return true
	case unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r):
		// What Unicode lets a renderer draw as nothing (U+17B4, U+2065,
		// U+FFF0-FFF8, U+E0000-E0FFF, ...).
		return true
	}
	return false
}

// operatorFolderFor refuses a folder that would not read the way the command
// runs in it, or that the shell cannot start in: a newline or a tab, spaces at
// its edges (the card trims them, and a result header a reloaded card reads
// back must not be forgeable), and a UNC folder for cmd, which starts in the
// Windows folder instead.
func operatorFolderFor(cwd string, kind platform.ShellKind) error {
	if err := validateOperatorText("cwd", cwd); err != nil {
		return err
	}
	if strings.ContainsAny(cwd, "\n\t") || strings.TrimSpace(cwd) != cwd {
		return fmt.Errorf("cwd holds a newline, a tab or spaces at its edges: the operator would not read where the command runs")
	}
	if kind == platform.ShellCmd && (strings.HasPrefix(cwd, `\\`) || strings.HasPrefix(cwd, "//")) {
		return fmt.Errorf("cmd cannot start in a UNC folder (%s) and would run in the Windows folder: use a mapped drive or a PowerShell operator shell", cwd)
	}
	return nil
}

// validateOperatorCommandFor adds what one shell cannot run as shown: cmd /c
// runs the first line of its command line only, so a later line the operator
// read would never run.
func validateOperatorCommandFor(command string, kind platform.ShellKind) error {
	if err := validateOperatorCommand(command); err != nil {
		return err
	}
	if kind == platform.ShellCmd && strings.ContainsAny(command, "\r\n") {
		return fmt.Errorf("command has more than one line, and cmd runs only the first: join the lines with & or use a PowerShell operator shell")
	}
	return nil
}

// operatorCWD resolves the folder the command runs in the way run_command
// does, then to an absolute path with links resolved: what the card shows is
// the folder the command really starts in.
func operatorCWD(base, requested string) (string, error) {
	cwd := base
	if requested != "" {
		cwd = requested
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(base, cwd)
		}
	}
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	cwd, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("command cwd is not a directory: %s", cwd)
	}
	return cwd, nil
}

func newOperatorRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("oc_%d", time.Now().UnixNano())
	}
	return "oc_" + hex.EncodeToString(b[:])
}
