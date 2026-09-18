package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Runtime is the state every subsystem of one `coddy serve` process shares:
// the resolved paths, the logger, and - for the surfaces that run agent turns -
// the single session manager that makes a conversation started in a chat the
// same conversation a browser opens.
type Runtime struct {
	// Paths holds the resolved home, workspace and config file locations.
	Paths config.Paths
	// Log is the process logger.
	Log *slog.Logger
	// Store is the session bundle store, nil when no enabled subsystem runs turns.
	Store *session.FileStore
	// Mgr owns sessions, nil when no enabled subsystem runs turns.
	Mgr *session.Manager

	mirrorMu sync.RWMutex
	mirror   session.TurnMirror

	// approvers are the surfaces that offered to show a detached subagent's
	// permission prompt, keyed by the offer so withdrawing one never takes
	// another with it.
	approversMu sync.RWMutex
	approvers   map[uint64]agent.DetachedPermissionBroker
	approverSeq uint64

	// wakes are the surfaces that offered to run the turn a finished
	// background task starts (wake.go).
	wakes wakeSurfaces

	// cfg is what the process loaded. Once a manager exists it owns the live
	// pointer, because every reload path replaces it there.
	cfg *config.Config
}

// Cfg returns the live configuration.
func (r *Runtime) Cfg() *config.Config {
	if r.Mgr != nil {
		return r.Mgr.Cfg()
	}
	return r.cfg
}

// SetTurnMirror installs (or with nil clears) the surface that mirrors turns
// started elsewhere.
//
// It is a slot rather than a constructor argument because the surfaces come up
// in whatever order the configuration lists them, and because one of them can
// be restarted under a settings change while another keeps running. A gateway
// that started first still ends up mirroring into an HTTP server that started
// second.
func (r *Runtime) SetTurnMirror(m session.TurnMirror) {
	r.mirrorMu.Lock()
	r.mirror = m
	r.mirrorMu.Unlock()
}

// MirrorTurn implements session.TurnMirror by delegating to the installed
// mirror, or handing the sender straight back when nothing is watching.
func (r *Runtime) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func()) {
	r.mirrorMu.RLock()
	m := r.mirror
	r.mirrorMu.RUnlock()
	if m == nil {
		return primary, func() {}
	}
	return m.MirrorTurn(sessionID, primary)
}

var _ session.TurnMirror = (*Runtime)(nil)

// AddDetachedPermissionApprover offers a surface that can show the permission
// prompt of a subagent whose parent turn has ended, and returns the function
// that withdraws exactly this offer.
//
// An offer rather than a constructor argument for the same reason as
// SetTurnMirror: every turn on the shared manager is handed the runtime as its
// broker when it starts, while the surfaces that can actually show the prompt -
// the HTTP server, a messenger bot - come up, or are restarted, on their own
// schedule. A detached child asks at whatever moment it needs to, so the
// question is settled then, not when its parent's turn began. Withdrawing is
// keyed by the offer because a restarted surface can come up before the old one
// has finished stopping.
func (r *Runtime) AddDetachedPermissionApprover(b agent.DetachedPermissionBroker) (withdraw func()) {
	if b == nil {
		return func() {}
	}
	r.approversMu.Lock()
	r.approverSeq++
	id := r.approverSeq
	if r.approvers == nil {
		r.approvers = make(map[uint64]agent.DetachedPermissionBroker)
	}
	r.approvers[id] = b
	r.approversMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.approversMu.Lock()
			delete(r.approvers, id)
			r.approversMu.Unlock()
		})
	}
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker by
// offering the prompt to every surface at once: the person reading that
// conversation may be in a browser, in a console attached to this server or in
// a messenger chat, and the runtime cannot tell which. The first answer settles
// it; the context of every other surface is cancelled, so each takes its copy
// of the prompt down, and the call returns only once they all have.
//
// A surface that cannot show this prompt answers agent.ErrNoDetachedApprover (a
// failure to deliver it counts the same). When every surface did - or none was
// offered - nobody could be asked, and the relay turns that into a refusal that
// says so.
func (r *Runtime) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	r.approversMu.RLock()
	surfaces := make([]agent.DetachedPermissionBroker, 0, len(r.approvers))
	for _, b := range r.approvers {
		surfaces = append(surfaces, b)
	}
	r.approversMu.RUnlock()
	if len(surfaces) == 0 {
		return nil, agent.ErrNoDetachedApprover
	}

	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		res *acp.PermissionResult
		err error
	}
	answers := make(chan answer, len(surfaces))
	for _, b := range surfaces {
		go func() {
			res, err := b.RequestDetachedPermission(askCtx, req)
			answers <- answer{res: res, err: err}
		}()
	}

	var settled *acp.PermissionResult
	cannotShow := 0
	for range surfaces {
		a := <-answers
		switch {
		case settled != nil:
			// Answered elsewhere first; this surface was only taking its copy down.
		case a.err == nil && a.res != nil:
			settled = a.res
			cancel()
		case a.err != nil:
			cannotShow++
		}
	}
	if settled != nil {
		return settled, nil
	}
	if cannotShow == len(surfaces) {
		return nil, agent.ErrNoDetachedApprover
	}
	return nil, nil
}

var _ agent.DetachedPermissionBroker = (*Runtime)(nil)

// Options are the process-level inputs a `coddy serve` invocation resolved from
// its flags and environment.
type Options struct {
	// CLI carries the --config / --home / --cwd overrides.
	CLI config.CLIPaths
	// SessionsRoot overrides sessions.dir when non-empty.
	SessionsRoot string
	// PreferredSessionID names the folder a newly created session claims.
	PreferredSessionID string
	// NeedsSessions is false for a process whose enabled subsystems run no
	// agent turns - a bare swarm relay - so it opens no store and builds no
	// manager.
	NeedsSessions bool
	// Log is the already-built process logger.
	Log *slog.Logger
	// Cfg is the already-loaded configuration.
	Cfg *config.Config
}

// Init fills the runtime in place.
//
// It is two-phase on purpose: the subsystem descriptors close over the runtime,
// and which subsystems are enabled decides whether a session store is opened at
// all. The caller builds the empty runtime, describes the surfaces against it,
// resolves them, and only then comes back here.
func (r *Runtime) Init(opts Options) error {
	if opts.Cfg == nil {
		return fmt.Errorf("serve: configuration is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	paths, err := config.Resolve(opts.CLI)
	if err != nil {
		return err
	}
	r.Paths = paths
	r.Log = log
	r.cfg = opts.Cfg
	if !opts.NeedsSessions {
		return nil
	}

	store, err := OpenSessionStore(opts.SessionsRoot, opts.Cfg)
	if err != nil {
		return err
	}
	r.Store = store
	log.Info("session persistence enabled", "root", store.Root)

	var mgr *session.Manager
	live := func() *config.Config {
		if mgr != nil {
			return mgr.Cfg()
		}
		return opts.Cfg
	}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(live(), st, snd, log)
		loop.SetConfigReloader(func(ctx context.Context) ([]string, error) {
			return mgr.ReloadConfigForSession(ctx, st)
		})
		loop.SetSubagentRuntime(mgr)
		// A detached child outlives this turn, so its permission prompts need
		// somewhere to go once the turn's stream is gone: the runtime offers
		// them to every surface that is up when the child asks.
		loop.SetDetachedPermissionBroker(r)
		return loop.Run(ctx, prompt)
	}
	mgr = session.NewManager(opts.Cfg, &defaultSender{live: live}, runner, log, paths.CWD, store)
	if pid := strings.TrimSpace(opts.PreferredSessionID); pid != "" {
		if err := session.ValidateFolderSessionID(pid); err != nil {
			return fmt.Errorf("--session-id: %w", err)
		}
		mgr.SetPreferredSessionID(pid)
	}
	r.Mgr = mgr
	// A task that asked to be notified wakes the agent whichever surfaces are
	// enabled; they offer to run the turn as they come up (AddWakeSurface).
	r.attachWaker()
	return nil
}

// defaultSender is the manager's fallback surface: what answers when a turn
// runs with nobody attached to it.
//
// It refuses rather than approves. A daemon that starts turns of its own -
// a scheduled job, a background wake - has no human to ask, and the safe
// reading of "nobody is here" is no, not yes. Only an operator who set the
// permission mode to bypass has said otherwise, and a subagent's own narrowed
// mode overrides even that.
type defaultSender struct {
	live func() *config.Config
}

func (d *defaultSender) SendSessionUpdate(string, interface{}) error { return nil }

func (d *defaultSender) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	stamped := strings.TrimSpace(params.EffectivePermissionMode)
	if stamped == config.PermModeBypass {
		return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
	}
	if stamped == "" {
		if cfg := d.live(); cfg != nil && cfg.Tools.ResolvedPermMode() == config.PermModeBypass {
			return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
		}
	}
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func (d *defaultSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// EnsureHomeLayout creates the directories every surface expects under the
// agent home.
func EnsureHomeLayout(home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	for _, name := range []string{"sessions", "skills", "scheduler"} {
		p := filepath.Join(home, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", p, err)
		}
	}
	return nil
}

// OpenSessionStore resolves the session bundle root, preferring an explicit
// override over sessions.dir.
func OpenSessionStore(flagValue string, cfg *config.Config) (*session.FileStore, error) {
	if raw := strings.TrimSpace(flagValue); raw != "" {
		root, err := filepath.Abs(raw)
		if err != nil {
			return nil, fmt.Errorf("sessions-dir: %w", err)
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, fmt.Errorf("sessions-dir mkdir: %w", err)
		}
		return &session.FileStore{Root: root}, nil
	}
	root := cfg.ResolvedSessionsRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("sessions root mkdir: %w", err)
	}
	return &session.FileStore{Root: root}, nil
}
