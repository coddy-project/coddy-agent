//go:build cli

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The memory run accessors of the agent package; tests swap them to stand a
// run in for the real pool.
var (
	memoryRunsInFlight = agent.MemoryRunsInFlight
	waitMemoryRuns     = agent.WaitMemoryRuns
)

// PrintOptions configures a one-shot non-interactive prompt run (-p/--prompt).
type PrintOptions struct {
	// Prompt is the user text for the single turn.
	Prompt string
	// Stdin is what was piped in under a prompt that came from elsewhere;
	// it rides after the prompt as a kind="stdin" attachment. Empty sends
	// nothing.
	Stdin string
	// Out receives the streamed assistant text (stdout in the CLI).
	Out io.Writer
	// ErrOut receives diagnostics (permission rejections, stop reasons).
	ErrOut io.Writer
	// SessionID pins or creates a specific session (mirrors --session-id).
	SessionID string
	// ContinueLast reopens the newest session for the folder (mirrors -c).
	ContinueLast bool
	// Model, Mode, PermMode mirror the interactive startup flags.
	Model    string
	Mode     string
	PermMode string
	// Config supplies paths and the permission fallback (local and remote).
	Config *config.Config
	// Ephemeral removes the session the run created when the run ends,
	// however it ends (mirrors --ephemeral). The session exists while the run
	// does, so subagents, background tasks and tool records work as in any
	// run; the caller refuses it together with ContinueLast and SessionID.
	Ephemeral bool
}

// ephemeralRemoveTimeout bounds the removal of an --ephemeral run's session:
// it runs after the prompt's own context may be gone (ctrl+c), and a server
// that stopped answering must not keep the process alive.
const ephemeralRemoveTimeout = 30 * time.Second

// removeEphemeralSession deletes the session an --ephemeral run created, with
// everything it spawned: in-process the manager's tree delete (turns
// cancelled and awaited, subagent and background tasks stopped, bundles
// removed deepest first), over --remote the server's DELETE of the same. A
// failure is a warning: the run already answered, and a hidden print run left
// behind costs less than a script retrying a run that worked.
func removeEphemeralSession(mgr backend, id string, errOut io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), ephemeralRemoveTimeout)
	defer cancel()
	var err error
	switch b := mgr.(type) {
	case *session.Manager:
		err = b.DeleteSessionTree(id, bgtask.Default())
	case interface {
		DeleteSession(ctx context.Context, id string) error
	}:
		err = b.DeleteSession(ctx, id)
	default:
		err = fmt.Errorf("this backend cannot delete sessions")
	}
	if err != nil && errOut != nil {
		_, _ = fmt.Fprintf(errOut, "warning: --ephemeral could not remove session %s: %v\n", id, err)
	}
}

// printSender streams assistant text to a writer and resolves permissions
// non-interactively: bypass allows, anything else rejects with a note.
type printSender struct {
	mgr    backend
	cfg    *config.Config
	out    io.Writer
	errOut io.Writer
	wrote  bool
	// remote suppresses the local bypass fallback: a permission event from a
	// remote server means its policy wants an explicit answer.
	remote bool
}

func (p *printSender) SendSessionUpdate(_ string, update interface{}) error {
	switch u := update.(type) {
	case acp.GoalTurnUpdate:
		// A turn the session supervisor started: its answer starts on a
		// paragraph of its own on stdout, and why it runs goes to stderr,
		// which a script reading the answer does not parse.
		if p.wrote {
			_, _ = io.WriteString(p.out, "\n\n")
		}
		if p.errOut != nil {
			_, _ = fmt.Fprintln(p.errOut, "[goal] "+session.GoalTurnNote(u))
		}
		return nil
	case acp.SessionGoalUpdate:
		// How the goal ended: complete, blocked with the question, paused,
		// out of budget.
		if note := session.GoalEndNote(u); p.errOut != nil && note != "" {
			_, _ = fmt.Fprintln(p.errOut, "[goal] "+note)
		}
		return nil
	}
	if chunk, ok := update.(acp.MessageChunkUpdate); ok {
		if chunk.SessionUpdate == "agent_message_chunk" && chunk.Content.Type == "text" && chunk.Content.Text != "" {
			text := chunk.Content.Text
			if !p.wrote {
				// Models often open with blank lines; keep stdout clean.
				text = strings.TrimLeft(text, "\r\n")
				if text == "" {
					return nil
				}
			}
			if _, err := io.WriteString(p.out, text); err == nil {
				p.wrote = true
			}
		}
	}
	return nil
}

func (p *printSender) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	// A subagent's request carries the child's own mode; see sender.go.
	if params.EffectivePermissionMode == "" && params.SessionPermissionMode == "" {
		if st := p.mgr.SessionByID(params.SessionID); st != nil {
			params.SessionPermissionMode = st.EffectivePermissionMode()
		}
	}
	cfgMode := ""
	if p.cfg != nil && !p.remote {
		cfgMode = p.cfg.Tools.ResolvedPermMode()
	}
	if permission.AutoApproves(params, cfgMode) {
		return permission.AutoAllow(), nil
	}
	if p.errOut != nil {
		_, _ = fmt.Fprintf(p.errOut, "permission rejected (non-interactive print mode): %s\n", params.ToolCall.Title)
	}
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (p *printSender) RequestQuestion(_ context.Context, _ acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	if p.errOut != nil {
		_, _ = fmt.Fprintln(p.errOut, "question skipped (non-interactive print mode)")
	}
	return &acp.QuestionResult{}, nil
}

// waitForMemoryRun keeps a one-shot print alive while the memory subagent
// of its turn is still running: the process has nothing else to do, and a
// `remember X` must not lose X to an exit. The wait is bounded by the run's
// own timeout, and after the drain grace a line on stderr says why the
// process is still there.
func waitForMemoryRun(ctx context.Context, cfg *config.Config, errOut io.Writer) {
	if memoryRunsInFlight() == 0 {
		return
	}
	if waitMemoryRuns(ctx, agent.MemoryDrainGrace) {
		return
	}
	timeout := time.Duration(config.MemoryDefaultTimeoutSeconds) * time.Second
	if cfg != nil {
		timeout = time.Duration(cfg.Memory.EffectiveTimeoutSeconds()) * time.Second
	}
	if errOut != nil {
		_, _ = fmt.Fprintln(errOut, "waiting for the memory subagent to finish before exiting")
	}
	waitMemoryRuns(ctx, timeout)
}

// promptBlocks is the prompt a one-shot run sends: the text exactly as read,
// then the piped data as an attachment nothing scans.
func promptBlocks(opts PrintOptions) []acp.ContentBlock {
	blocks := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: opts.Prompt}}
	if opts.Stdin != "" {
		blocks = append(blocks, session.StdinAttachment(opts.Stdin))
	}
	return blocks
}

// PrintPrompt runs one prompt turn without a TUI and streams the assistant
// text to opts.Out. The session persists like any other surface, marked as a
// print run: the pickers a person uses leave it out, a later `coddy -c -p`
// continues it, and --ephemeral removes it when the run ends.
func PrintPrompt(ctx context.Context, mgr backend, opts PrintOptions) error {
	if strings.TrimSpace(opts.Prompt) == "" {
		return fmt.Errorf("empty prompt")
	}
	cfg := opts.Config
	cwd := ""
	if cfg != nil {
		cwd = cfg.Paths.CWD
	}

	switch {
	case opts.ContinueLast:
		// A print run continues the previous run of the folder, print runs
		// included: that is how a script chains its turns.
		id, err := latestBackendSessionID(ctx, mgr, cwd, true)
		if err != nil {
			return err
		}
		mgr.SetPreferredSessionID(id)
	case opts.SessionID != "":
		if err := session.ValidateFolderSessionID(opts.SessionID); err != nil {
			return fmt.Errorf("--session-id: %w", err)
		}
		mgr.SetPreferredSessionID(opts.SessionID)
	}

	// A session this run creates is a print run, marked before its first
	// write; one it reopens (-c, an existing --session-id) keeps its origin.
	mgr.SetNextSessionOrigin(session.PrintOrigin)
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	if opts.Ephemeral {
		// Deferred, so a failed setting, a failed turn and a cancelled one
		// remove the session as well; it runs after waitForMemoryRun below.
		defer removeEphemeralSession(mgr, res.SessionID, opts.ErrOut)
	}
	// HandleSessionReady is deliberately not called: a reopened bundle would
	// otherwise replay its whole transcript into stdout.

	if opts.Model != "" {
		if _, err := mgr.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
			SessionID: res.SessionID, ConfigID: "model", Value: opts.Model,
		}); err != nil {
			return fmt.Errorf("--model: %w", err)
		}
	}
	if opts.Mode != "" {
		if err := mgr.HandleSessionSetMode(ctx, acp.SessionSetModeParams{SessionID: res.SessionID, ModeID: opts.Mode}); err != nil {
			return fmt.Errorf("--mode: %w", err)
		}
	}
	if opts.PermMode != "" {
		if _, err := mgr.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{
			SessionID: res.SessionID, ConfigID: "permission_mode", Value: opts.PermMode,
		}); err != nil {
			return fmt.Errorf("--permission-mode: %w", err)
		}
	}

	snd := &printSender{mgr: mgr, cfg: cfg, out: opts.Out, errOut: opts.ErrOut}
	// A one-shot print has no footer: no usage refresh at the end.
	result, err := mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    promptBlocks(opts),
	}, snd, &session.PromptRunOpts{SkipUsagePublish: true, Lang: docs.TurnLangFromEnv(os.Getenv)})
	if snd.wrote {
		_, _ = io.WriteString(opts.Out, "\n")
	}
	waitForMemoryRun(ctx, cfg, opts.ErrOut)
	if err != nil {
		return err
	}
	if result != nil && result.StopReason == acp.StopReasonCancelled {
		return fmt.Errorf("turn cancelled")
	}
	if result != nil && result.StopNotice != "" {
		// The answer on stdout stays clean for a pipe; why the turn stopped
		// short goes where errors go.
		_, _ = fmt.Fprintln(opts.ErrOut, result.StopNotice)
	}
	return nil
}
