//go:build scheduler

package schedservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// TrustState is what a job row reports and what decides whether a job runs.
type TrustState string

const (
	// TrustTrusted: a user job, a project job under allow, or a project job
	// under ask whose receipt matches the bytes on disk. Only these run.
	TrustTrusted TrustState = "trusted"
	// TrustNeedsApproval: a project job under ask with no matching receipt.
	TrustNeedsApproval TrustState = "needs_approval"
	// TrustDenied: a project job under deny.
	TrustDenied TrustState = "denied"
	// TrustConflict: a project job whose id is also a user job.
	TrustConflict TrustState = "conflict"
	// TrustInvalid: a file that does not read as a job, or a project job that
	// breaks a containment rule (a link, a cwd that leaves the workspace).
	TrustInvalid TrustState = "invalid"
)

// RootsOf names the job folders of a configuration.
func RootsOf(cfg *config.Config) storage.Roots {
	if cfg == nil {
		return storage.Roots{}
	}
	return storage.Roots{Home: cfg.Paths.Home, User: cfg.SchedulerUserDir()}
}

// CanonicalWorkspace is the key a project workspace is known by: absolute,
// links resolved, cleaned (the rule of every other project trust store).
func CanonicalWorkspace(cwd string) string {
	return mcp.CanonicalWorkspace(cwd)
}

// userJobExists reports whether a user job of that id is on disk.
func userJobExists(cfg *config.Config, id string) bool {
	_, err := os.Lstat(RootsOf(cfg).UserRef(id).Path)
	return err == nil
}

// Decide is the trust of one snapshot. The order is the order of the table in
// docs/plans/scheduler-project-jobs.md: an unreadable file, an id clash, the
// policy, then the receipt.
func Decide(cfg *config.Config, snap storage.JobSnapshot) (TrustState, string) {
	if snap.Err != nil {
		return TrustInvalid, snap.Err.Error()
	}
	if !snap.Ref.IsProject() {
		return TrustTrusted, ""
	}
	if userJobExists(cfg, snap.Ref.ID) {
		return TrustConflict, fmt.Sprintf("a user job is also named %q; rename this project job to run it", snap.Ref.ID)
	}
	switch cfg.Scheduler.ResolvedProjectTrust() {
	case config.ProjectTrustDeny:
		return TrustDenied, "scheduler.project_trust is deny"
	case config.ProjectTrustAllow:
		return TrustTrusted, ""
	}
	if storage.NewTrustStore(cfg.Paths.Home).Approved(snap.Ref.Workspace, snap.Ref.ID, snap.Digest) {
		return TrustTrusted, ""
	}
	return TrustNeedsApproval, "this project job is not approved for this workspace, or its file changed since it was"
}

// ScanWorkspaces lists the canonical workspaces whose project jobs the daemon
// runs: its own process cwd, every workspace holding a receipt and every
// workspace a session asked the scheduler about. A workspace whose project
// folder is refused (inside the coddy home, a link) is left out.
func ScanWorkspaces(cfg *config.Config, processCWD string) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(ws string) {
		ws = CanonicalWorkspace(ws)
		if ws == "" || seen[ws] {
			return
		}
		seen[ws] = true
		if _, err := RootsOf(cfg).CheckProjectDir(ws); err != nil {
			return
		}
		out = append(out, ws)
	}
	add(processCWD)
	for _, ws := range storage.NewTrustStore(cfg.Paths.Home).Workspaces() {
		add(ws)
	}
	for _, ws := range storage.NewRegistry(cfg.Paths.Home).List() {
		add(ws)
	}
	sort.Strings(out)
	return out
}

// inScanSet reports whether a canonical workspace is one the daemon scans.
func inScanSet(cfg *config.Config, processCWD, workspace string) bool {
	for _, ws := range ScanWorkspaces(cfg, processCWD) {
		if ws == workspace {
			return true
		}
	}
	return false
}

// projectJobsNamed lists the workspaces among ws whose project folder holds a
// job of that id.
func projectJobsNamed(cfg *config.Config, id string, workspaces []string) []string {
	var out []string
	for _, ws := range workspaces {
		if _, err := os.Lstat(filepath.Join(storage.ProjectDir(ws), id+".md")); err == nil {
			out = append(out, ws)
		}
	}
	return out
}

// RunnableSnapshot reads a job once and returns the snapshot when it may run,
// or the reason it may not: ErrJobUntrusted (with the state and the reason)
// for any state but trusted.
func RunnableSnapshot(cfg *config.Config, ref storage.JobRef) (storage.JobSnapshot, error) {
	snap, err := storage.ReadSnapshot(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, ErrJobNotFound
		}
		if errors.Is(err, storage.ErrUnsafePath) {
			return snap, fmt.Errorf("%w: %v", ErrJobUntrusted, err)
		}
		return snap, err
	}
	if state, reason := Decide(cfg, snap); state != TrustTrusted {
		return snap, untrustedErr(ref, state, reason)
	}
	return snap, nil
}

func untrustedErr(ref storage.JobRef, state TrustState, reason string) error {
	msg := fmt.Sprintf("job %q is %s", ref.ID, state)
	if reason != "" {
		msg += " (" + reason + ")"
	}
	if state == TrustNeedsApproval {
		msg += fmt.Sprintf("; approve it in the scheduler drawer, or POST /coddy/scheduler/jobs/%s/trust?scope=project with the digest it shows", ref.ID)
	}
	return fmt.Errorf("%w: %s", ErrJobUntrusted, strings.TrimSpace(msg))
}
