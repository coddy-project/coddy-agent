package session

// SessionStart hooks: the manager fires them when a session is created or
// restored and keeps the context they hand over on the state, so every system
// prompt of the session carries it. See docs/features/hooks.md.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// Sources of the SessionStart event; the matcher is compared with them.
const (
	hookSourceStartup = "startup"
	hookSourceResume  = "resume"
)

// runSessionStartHooks fires SessionStart for st. The hooks run synchronously,
// bounded by their timeouts, so session creation waits for them; failures are
// logged and never block the session. The context the hooks return replaces
// the one stored on the state (a resume re-runs the hooks, so their answer is
// the current one), and systemMessage values become notice rows.
func (m *Manager) runSessionStartHooks(ctx context.Context, st *State, source string) {
	cfg := m.activeCfg()
	if cfg == nil || st == nil {
		return
	}
	// Whatever happens below, the stored context is what this run decided:
	// a resume after the hooks were removed, disabled or withdrawn must not
	// keep the text an earlier run stored.
	next := ""
	defer func() {
		if st.GetHookContext() != next {
			st.SetHookContext(next)
		}
	}()
	if !cfg.Hooks.ResolvedEnabled() {
		return
	}
	loader := hooks.NewLoader(cfg.Hooks.Files, cfg.Hooks.ResolvedProjectTrust()).
		WithStore(hooks.NewTrustStore(cfg.Paths.Home))
	loader.Log = m.log
	sources := loader.Load(st.GetCWD(), cfg.Paths.Home)
	if len(sources) == 0 {
		return
	}
	permMode := strings.TrimSpace(st.GetPermissionMode())
	if permMode == "" {
		permMode = cfg.Tools.PermissionMode
	}
	transcript := ""
	if sd := strings.TrimSpace(st.GetPersistedSessionDir()); sd != "" {
		transcript = filepath.Join(sd, MessagesFileName)
	}
	model := st.EffectiveModelID(cfg)
	r := &hooks.Runner{
		Sources: sources,
		Session: hooks.Session{
			ID:             st.GetID(),
			CWD:            st.GetCWD(),
			TranscriptPath: transcript,
			PermissionMode: permMode,
			Mode:           st.GetMode(),
			Model:          model,
			Turn:           CountUserTurns(st.GetMessages()),
		},
		Home:           cfg.Paths.Home,
		TimeoutSeconds: cfg.Hooks.EffectiveDefaultTimeoutSeconds(),
		MaxOutputChars: cfg.Hooks.EffectiveMaxOutputChars(),
		Log:            m.log,
	}
	if sub := st.Subagent(); sub != nil {
		r.Session.Subagent = &hooks.Subagent{Name: sub.Name, ParentSessionID: sub.ParentSessionID, Depth: sub.Depth}
	}
	if !r.HasHandlers(hooks.EventSessionStart) {
		return
	}
	out := r.Run(ctx, hooks.SessionStartEvent(source, model))
	turn := CountUserTurns(st.GetMessages()) + 1
	for _, msg := range out.SystemMessages {
		st.AppendUILogNotice(turn, msg)
	}
	for _, e := range out.Errors {
		m.log.Warn("session start hook error", "session", st.GetID(), "source", source, "error", e)
		if st.MarkHookNoticeShown("error:" + e) {
			st.AppendUILogNotice(turn, "Hook error: "+e)
		}
	}
	next = strings.Join(out.Context, "\n")
}
