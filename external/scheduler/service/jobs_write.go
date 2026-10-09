//go:build scheduler

package schedservice

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// CreateJob writes a new job file. A user job goes to ${CODDY_HOME}/scheduler;
// a project job to the request session's workspace, where it is approved at
// once for the bytes written: the operator created it. Ids never clash across
// scopes: a project job may not take a user job's id, nor a user job the id of
// a project job of a workspace the scheduler knows.
func (o *Service) CreateJob(in SchedulerJobCreate) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	scope, err := NormalizeScope(in.Scope)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(in.JobID)
	if err := ValidateJobID(id); err != nil {
		return err
	}
	addr := UserJob(id)
	if scope == storage.ScopeProject {
		if addr, err = o.ProjectJob(id, ""); err != nil {
			return err
		}
	}
	ref, err := o.ref(addr)
	if err != nil {
		return err
	}
	if err := o.checkIDFree(ref); err != nil {
		return err
	}
	fm, err := frontmatterFromCreate(in, ref)
	if err != nil {
		return err
	}
	data, err := formatJob(fm, in.Body)
	if err != nil {
		return err
	}
	if err := o.roots().CreateJobFile(ref, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrJobExists
		}
		return err
	}
	if ref.IsProject() {
		o.register()
		if err := o.trustStore().Approve(ref.Workspace, ref.ID, storage.Digest(data)); err != nil {
			return fmt.Errorf("project job written but not approved: %w", err)
		}
	}
	return nil
}

// checkIDFree refuses an id taken in this scope or clashing across scopes.
func (o *Service) checkIDFree(ref storage.JobRef) error {
	if _, err := os.Lstat(ref.Path); err == nil {
		return ErrJobExists
	}
	if ref.IsProject() {
		if userJobExists(o.Cfg, ref.ID) {
			return fmt.Errorf("%w: a user job is named %q; a project job may not take its id", ErrJobExists, ref.ID)
		}
		return nil
	}
	known := ScanWorkspaces(o.Cfg, o.ProcessCWD)
	if o.SessionWorkspace != "" {
		known = append(known, o.SessionWorkspace)
	}
	if hits := projectJobsNamed(o.Cfg, ref.ID, known); len(hits) > 0 {
		return fmt.Errorf("%w: the project job %q of %s has this id; a user job may not take it", ErrJobExists, ref.ID, hits[0])
	}
	return nil
}

// ReplaceJob overwrites an existing job file.
func (o *Service) ReplaceJob(addr JobAddr, in SchedulerJobCreate) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	if strings.TrimSpace(in.JobID) != "" && strings.TrimSpace(in.JobID) != addr.ID {
		return ErrInvalidJobID
	}
	ref, err := o.existingRef(addr)
	if err != nil {
		return err
	}
	_, trusted, err := o.editSnapshot(ref)
	if err != nil {
		return err
	}
	fm, err := frontmatterFromCreate(in, ref)
	if err != nil {
		return err
	}
	data, err := formatJob(fm, in.Body)
	if err != nil {
		return err
	}
	return o.writeEdit(ref, data, trusted)
}

// editSnapshot reads a job once before an edit and reports whether it was
// trusted: only then does the edit re-issue the receipt, so an edit through
// Coddy never approves a project job nobody approved.
func (o *Service) editSnapshot(ref storage.JobRef) (storage.JobSnapshot, bool, error) {
	snap, err := storage.ReadSnapshot(ref)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, false, ErrJobNotFound
		}
		return snap, false, err
	}
	if !ref.IsProject() {
		return snap, true, nil
	}
	state, _ := Decide(o.Cfg, snap)
	return snap, state == TrustTrusted && o.Cfg.Scheduler.ResolvedProjectTrust() == config.ProjectTrustAsk, nil
}

// writeEdit writes the buffer of an edit and, for a project job that was
// trusted before it, receipts exactly that buffer.
func (o *Service) writeEdit(ref storage.JobRef, data []byte, trusted bool) error {
	if err := o.roots().ReplaceJobFile(ref, data); err != nil {
		return err
	}
	if ref.IsProject() && trusted {
		return o.trustStore().Approve(ref.Workspace, ref.ID, storage.Digest(data))
	}
	return nil
}

func formatJob(fm *storage.JobFrontmatter, body string) ([]byte, error) {
	data, err := storage.FormatJobMarkdown(fm, body)
	if err != nil {
		return nil, err
	}
	if _, err := storage.ParseJobFromBytes(data); err != nil {
		return nil, err
	}
	return data, nil
}

// frontmatterFromCreate builds and validates the frontmatter of a create or
// replace body.
func frontmatterFromCreate(in SchedulerJobCreate, ref storage.JobRef) (*storage.JobFrontmatter, error) {
	fm := &storage.JobFrontmatter{
		Description:    strings.TrimSpace(in.Description),
		Schedule:       strings.TrimSpace(in.Schedule),
		Paused:         in.Paused,
		CWD:            strings.TrimSpace(in.CWD),
		Model:          strings.TrimSpace(in.Model),
		Mode:           strings.TrimSpace(in.Mode),
		Agent:          strings.TrimSpace(in.Agent),
		PermissionMode: strings.TrimSpace(in.PermissionMode),
	}
	if err := validateFrontmatter(fm, ref); err != nil {
		return nil, err
	}
	return fm, nil
}

// validateFrontmatter checks what a job file must satisfy to run: a cron
// expression, a known permission mode, a definition name that could exist,
// and for a project job a cwd inside its workspace.
func validateFrontmatter(fm *storage.JobFrontmatter, ref storage.JobRef) error {
	if _, err := storage.ParseCronUTC(fm.Schedule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJobID, err)
	}
	if pm := strings.TrimSpace(fm.PermissionMode); pm != "" {
		if _, ok := subagents.NormalizePermissionMode(pm); !ok {
			return fmt.Errorf("%w: permission_mode must be ask, accept_edits or bypass", ErrInvalidJob)
		}
	}
	if name := strings.TrimSpace(fm.Agent); name != "" && !subagents.ValidName(name) {
		return fmt.Errorf("%w: agent %q is not a subagent definition name", ErrInvalidJob, name)
	}
	if ref.IsProject() {
		if _, err := storage.ProjectCWD(ref.Workspace, fm.CWD); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidJob, err)
		}
	}
	return nil
}

// renameJob moves a job file, its state sidecar and its receipt to a new id of
// the same scope when the job is not running. The run history follows: the
// sidecar carries the job session id.
func (o *Service) renameJob(from storage.JobRef, newID string) (storage.JobRef, error) {
	if err := ValidateJobID(newID); err != nil {
		return storage.JobRef{}, err
	}
	addr := JobAddr{Scope: from.Scope, Workspace: from.Workspace, ID: newID}
	to, err := o.ref(addr)
	if err != nil {
		return storage.JobRef{}, err
	}
	if err := o.checkIDFree(to); err != nil {
		return storage.JobRef{}, err
	}
	if jobRunning(from) {
		return storage.JobRef{}, ErrJobBusy
	}
	if err := o.roots().RenameJobFile(from, to); err != nil {
		if errors.Is(err, os.ErrExist) {
			return storage.JobRef{}, ErrJobExists
		}
		return storage.JobRef{}, err
	}
	if _, err := os.Stat(from.StatePath); err == nil {
		if err := os.MkdirAll(dirOf(to.StatePath), 0o755); err != nil {
			return to, err
		}
		if err := os.Rename(from.StatePath, to.StatePath); err != nil {
			return to, err
		}
	} else if !os.IsNotExist(err) {
		return to, err
	}
	// The job session keeps the run history; it names the job by id, and the
	// bundle walk that finds a job session without a sidecar pointer matches
	// that id, so it has to follow the rename.
	if sid := o.jobSessionIDOf(to); sid != "" {
		if err := o.sessionStore().RenameSchedulerJobSession(sid, newID); err != nil {
			return to, err
		}
	}
	if from.IsProject() {
		if err := o.trustStore().Rename(from.Workspace, from.ID, newID); err != nil {
			return to, err
		}
	}
	return to, nil
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}

// PatchJob merges fields into an existing job file. The file is read once:
// its trust is decided on that read, the patch is applied to it in memory,
// and the written buffer is what a trusted project job is receipted for.
func (o *Service) PatchJob(addr JobAddr, p SchedulerJobPatch) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	ref, err := o.existingRef(addr)
	if err != nil {
		return err
	}
	snap, trusted, err := o.editSnapshot(ref)
	if err != nil {
		return err
	}
	if snap.FM == nil {
		if snap.Err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidJob, snap.Err)
		}
		return ErrInvalidJob
	}
	fm := *snap.FM
	body := snap.Body
	if p.Description != nil {
		fm.Description = strings.TrimSpace(*p.Description)
	}
	if p.Schedule != nil {
		fm.Schedule = strings.TrimSpace(*p.Schedule)
	}
	if p.Paused != nil {
		fm.Paused = *p.Paused
	}
	if p.CWD != nil {
		fm.CWD = strings.TrimSpace(*p.CWD)
	}
	if p.Model != nil {
		fm.Model = strings.TrimSpace(*p.Model)
	}
	if p.Mode != nil {
		fm.Mode = strings.TrimSpace(*p.Mode)
	}
	if p.Agent != nil {
		fm.Agent = strings.TrimSpace(*p.Agent)
	}
	if p.PermissionMode != nil {
		fm.PermissionMode = strings.TrimSpace(*p.PermissionMode)
	}
	if p.Body != nil {
		body = strings.TrimRight(*p.Body, "\n")
	}
	if err := validateFrontmatter(&fm, ref); err != nil {
		return err
	}
	data, err := formatJob(&fm, body)
	if err != nil {
		return err
	}
	if p.JobID != nil {
		if newID := strings.TrimSpace(*p.JobID); newID != "" && newID != ref.ID {
			if ref, err = o.renameJob(ref, newID); err != nil {
				return err
			}
		}
	}
	return o.writeEdit(ref, data, trusted)
}

// DeleteJob removes the job file, its .state sidecar, its receipt and its run
// history (the job session with every run under it) when the job is not
// running.
func (o *Service) DeleteJob(addr JobAddr) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	ref, err := o.existingRef(addr)
	if err != nil {
		return err
	}
	if jobRunning(ref) {
		return ErrJobBusy
	}
	if rt := CurrentRuntime(); rt != nil {
		if err := rt.DeleteJobHistory(ref); err != nil {
			return err
		}
	}
	_ = os.Remove(ref.StatePath)
	if err := os.Remove(ref.Path); err != nil {
		return err
	}
	if ref.IsProject() {
		if _, err := o.trustStore().Revoke(ref.Workspace, ref.ID); err != nil {
			return err
		}
	}
	return nil
}

// PauseJob sets paused:true in frontmatter without starting a run.
func (o *Service) PauseJob(addr JobAddr) error {
	v := true
	return o.PatchJob(addr, SchedulerJobPatch{Paused: &v})
}

// ResumeJob sets paused:false in frontmatter.
func (o *Service) ResumeJob(addr JobAddr) error {
	v := false
	return o.PatchJob(addr, SchedulerJobPatch{Paused: &v})
}

// TrustJob approves a project job for its workspace, bound to the digest the
// client showed: ErrDigestMismatch when the file changed since.
func (o *Service) TrustJob(addr JobAddr, digest string) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	if addr.Scope != storage.ScopeProject {
		return fmt.Errorf("%w: a user job is always trusted", ErrTrustNotApplicable)
	}
	if o.Cfg.Scheduler.ResolvedProjectTrust() == config.ProjectTrustDeny {
		return fmt.Errorf("%w: scheduler.project_trust is deny", ErrTrustNotApplicable)
	}
	ref, err := o.existingRef(addr)
	if err != nil {
		return err
	}
	snap, err := storage.ReadSnapshot(ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(digest) == "" || strings.TrimSpace(digest) != snap.Digest {
		return ErrDigestMismatch
	}
	switch state, reason := Decide(o.Cfg, snap); state {
	case TrustConflict, TrustInvalid:
		return untrustedErr(ref, state, reason)
	}
	if err := o.trustStore().Approve(ref.Workspace, ref.ID, snap.Digest); err != nil {
		return err
	}
	o.register()
	return nil
}

// UntrustJob withdraws the approval of a project job and reports whether one
// was on file.
func (o *Service) UntrustJob(addr JobAddr) (bool, error) {
	if err := o.requireEnabled(); err != nil {
		return false, err
	}
	if addr.Scope != storage.ScopeProject {
		return false, fmt.Errorf("%w: a user job is always trusted", ErrTrustNotApplicable)
	}
	ref, err := o.ref(addr)
	if err != nil {
		return false, err
	}
	return o.trustStore().Revoke(ref.Workspace, ref.ID)
}
