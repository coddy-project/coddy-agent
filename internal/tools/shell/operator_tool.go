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
	// operatorCancelDrain bounds how long a cancelled turn waits for the
	// killed command to be reaped.
	operatorCancelDrain = 8 * time.Second
)

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
						"description": "The shell command for the operator to run, exactly as it will run. The operator sees it and cannot edit it.",
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
		" A decline is final for that command in this turn: do not ask again."
}

// operatorTerminalNote says whether the command gets a terminal: inside a
// multiplexer it does, and the operator can attach to type into it.
func operatorTerminalNote(settings *tooling.OperatorCommandSettings) string {
	if settings != nil && settings.Multiplexer != "" && runtime.GOOS != "windows" {
		return "in a " + settings.Multiplexer + " window: it has a terminal, and a prompt for input waits for the operator to attach and type."
	}
	return "with no terminal and no input: it cannot prompt for a password (use sudo -n, not sudo)."
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
	cwd, err := operatorCWD(env.CWD, args.CWD)
	if err != nil {
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
	if decision.Done != nil {
		defer decision.Done()
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
		return "", fmt.Errorf("the operator's command did not start: %w", err)
	}
	return superviseOperatorRun(ctx, env, req, argsJSON, run, decision)
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
			out := run.Output()
			code := run.ExitCode()
			phase := OperatorPhaseEnded
			if stopped {
				phase = OperatorPhaseStopped
				// The shell is gone, but what it started in the group may
				// not be: a stop ends the whole command.
				run.Kill(0)
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
			go run.Kill(0)
		case <-escalate:
			escalate = nil
			go run.Kill(operatorStopGrace)
		case <-ctx.Done():
			run.Interrupt()
			go func() {
				time.Sleep(operatorInterruptWait())
				run.Kill(operatorStopGrace)
			}()
			select {
			case <-run.Done():
			case <-time.After(operatorCancelDrain):
			}
			frame.send(OperatorPhaseStopped, run.Output(), run.ExitCode(), run.Dropped())
			return "", ctx.Err()
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

// operatorInterruptWait is how long a stop waits after the interrupt before it
// terminates the group. Windows has no interrupt to wait for: the command has
// no console to deliver Ctrl+C to, so the terminate follows at once.
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

// hiddenRune reports the format characters that change how text displays
// without showing: bidirectional controls and zero-width characters.
func hiddenRune(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
		return true
	case r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF, r == 0x00AD:
		return true
	}
	return false
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
