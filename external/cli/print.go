//go:build cli

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// PrintOptions configures a one-shot non-interactive prompt run (-p/--prompt).
type PrintOptions struct {
	// Prompt is the user text for the single turn.
	Prompt string
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
	mode := strings.TrimSpace(params.EffectivePermissionMode)
	if mode == "" {
		if st := p.mgr.SessionByID(params.SessionID); st != nil {
			mode = st.GetPermissionMode()
		}
	}
	if mode == "" && p.cfg != nil && !p.remote {
		mode = p.cfg.Tools.ResolvedPermMode()
	}
	if mode == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
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

// PrintPrompt runs one prompt turn without a TUI and streams the assistant
// text to opts.Out. The session persists like any other surface, so a later
// `coddy -c` (interactive or print) continues it.
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
		id, err := latestBackendSessionID(ctx, mgr, cwd)
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

	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
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
	result, err := mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: opts.Prompt}},
	}, snd, nil)
	if snd.wrote {
		_, _ = io.WriteString(opts.Out, "\n")
	}
	if err != nil {
		return err
	}
	if result != nil && result.StopReason == acp.StopReasonCancelled {
		return fmt.Errorf("turn cancelled")
	}
	return nil
}
