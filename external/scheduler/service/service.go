//go:build scheduler

package schedservice

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Service centralizes scheduler job CRUD, trust, run tracking, and HTTP or
// tool adapters.
type Service struct {
	Cfg *config.Config
	Log *slog.Logger
	// ProcessCWD is the cwd of the process: a user job's relative cwd: is
	// resolved against it, and its workspace is one the daemon scans.
	ProcessCWD string
	// SessionWorkspace is the canonical workspace of the session behind the
	// request (an HTTP X-Coddy-Session-ID, the session of a tool call), empty
	// when none is. A project job of it may be created and approved, and it
	// is registered as a workspace the daemon scans.
	SessionWorkspace string
}

func NewService(cfg *config.Config, log *slog.Logger, processCWD string) *Service {
	return &Service{Cfg: cfg, Log: log, ProcessCWD: processCWD}
}

// WithSession returns the service acting for a session in cwd.
func (o *Service) WithSession(cwd string) *Service {
	cp := *o
	cp.SessionWorkspace = CanonicalWorkspace(cwd)
	return &cp
}

func (o *Service) requireEnabled() error {
	if o == nil || o.Cfg == nil || !o.Cfg.SchedulerEffectiveEnabled() {
		return ErrSchedulerDisabled
	}
	return nil
}

func (o *Service) roots() storage.Roots {
	return RootsOf(o.Cfg)
}

// NormalizeScope maps a request's scope onto user or project.
func NormalizeScope(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", storage.ScopeUser:
		return storage.ScopeUser, nil
	case storage.ScopeProject:
		return storage.ScopeProject, nil
	default:
		return "", fmt.Errorf("%w: scope must be user or project", ErrInvalidJob)
	}
}

// ProjectJob addresses a project job: of the workspace named explicitly when
// one is (it must be a workspace the daemon scans, or the session's own), or
// of the session's workspace.
func (o *Service) ProjectJob(id, workspace string) (JobAddr, error) {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		if o.SessionWorkspace == "" {
			return JobAddr{}, ErrWorkspaceUnknown
		}
		o.register()
		return JobAddr{Scope: storage.ScopeProject, Workspace: o.SessionWorkspace, ID: id}, nil
	}
	ws = CanonicalWorkspace(ws)
	if ws == o.SessionWorkspace {
		o.register()
	} else if !inScanSet(o.Cfg, o.ProcessCWD, ws) {
		return JobAddr{}, ErrWorkspaceUnknown
	}
	return JobAddr{Scope: storage.ScopeProject, Workspace: ws, ID: id}, nil
}

// ref resolves an address to its job file and state, checking the id and,
// for a project job, the folder.
func (o *Service) ref(addr JobAddr) (storage.JobRef, error) {
	if err := ValidateJobID(addr.ID); err != nil {
		return storage.JobRef{}, err
	}
	id := strings.TrimSpace(addr.ID)
	if addr.Scope != storage.ScopeProject {
		if o.Cfg.SchedulerUserDir() == "" {
			return storage.JobRef{}, fmt.Errorf("scheduler: no coddy home to keep the jobs in")
		}
		return o.roots().UserRef(id), nil
	}
	if addr.Workspace == "" {
		return storage.JobRef{}, ErrWorkspaceUnknown
	}
	if _, err := o.roots().CheckProjectDir(addr.Workspace); err != nil {
		return storage.JobRef{}, err
	}
	return o.roots().ProjectRef(addr.Workspace, id), nil
}

// register records the session's workspace as one the daemon scans.
func (o *Service) register() {
	if o.SessionWorkspace == "" || o.Cfg == nil {
		return
	}
	if _, err := o.roots().CheckProjectDir(o.SessionWorkspace); err != nil {
		return
	}
	if err := storage.NewRegistry(o.Cfg.Paths.Home).Add(o.SessionWorkspace); err != nil && o.Log != nil {
		o.Log.Warn("scheduler workspace registry", "workspace", o.SessionWorkspace, "error", err)
	}
}

// trustStore is the receipts store of this configuration.
func (o *Service) trustStore() *storage.TrustStore {
	return storage.NewTrustStore(o.Cfg.Paths.Home)
}
