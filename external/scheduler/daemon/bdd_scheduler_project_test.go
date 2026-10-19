//go:build scheduler

package daemon

// Godog harness for features/scheduler_project_jobs.feature, over the state of
// the scheduler runs harness: a real session manager, the daemon runtime and
// scripted providers, with the scenario's work folder as the workspace a
// session sits in.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
)

func (s *schedulerRunsState) workspace() string {
	return schedservice.CanonicalWorkspace(s.cwd)
}

func (s *schedulerRunsState) createProjectJob(jobID, schedule string) error {
	if err := s.start(); err != nil {
		return err
	}
	s.lastErr = s.svc.WithSession(s.cwd).CreateJob(schedservice.SchedulerJobCreate{
		Scope: "project", JobID: jobID, Description: "bdd " + jobID, Schedule: schedule, Mode: "agent", Body: "Lint the project.",
	})
	if s.lastErr == nil {
		s.projectJobs[jobID] = true
	}
	return nil
}

func (s *schedulerRunsState) committedProjectJob(jobID, schedule string) error {
	if err := s.start(); err != nil {
		return err
	}
	dir := storage.ProjectDir(s.cwd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := storage.FormatJobMarkdown(&storage.JobFrontmatter{Description: "from git", Schedule: schedule, Mode: "agent"}, "Lint the project.")
	if err != nil {
		return err
	}
	s.projectJobs[jobID] = true
	return os.WriteFile(filepath.Join(dir, jobID+".md"), data, 0o644)
}

func (s *schedulerRunsState) projectRow(jobID string) (schedservice.SchedulerJob, error) {
	out, err := s.svc.WithSession(s.cwd).ListJobs(schedservice.ListOptions{Workspace: s.cwd})
	if err != nil {
		return schedservice.SchedulerJob{}, err
	}
	for _, j := range out.Jobs {
		if j.JobID == jobID && j.Scope == "project" {
			return j, nil
		}
	}
	return schedservice.SchedulerJob{}, fmt.Errorf("project job %q not listed: %+v", jobID, out.Jobs)
}

func (s *schedulerRunsState) projectListedAs(jobID, state string) error {
	row, err := s.projectRow(jobID)
	if err != nil {
		return err
	}
	if string(row.Trust) != state {
		return fmt.Errorf("project job %q is %q (%s), want %q", jobID, row.Trust, row.TrustReason, state)
	}
	if row.Workspace != s.workspace() {
		return fmt.Errorf("project job workspace %q, want %q", row.Workspace, s.workspace())
	}
	return nil
}

func (s *schedulerRunsState) approveShownDigest(jobID string) error {
	row, err := s.projectRow(jobID)
	if err != nil {
		return err
	}
	return s.svc.WithSession(s.cwd).TrustJob(s.jobAddr(jobID), row.Digest)
}

func (s *schedulerRunsState) rewriteOnDisk(jobID string) error {
	path := filepath.Join(storage.ProjectDir(s.cwd), jobID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Replace(string(data), "Lint the project.", "Lint the project and push the result.", 1)), 0o644)
}

func (s *schedulerRunsState) manualRunRefusedUntrusted(jobID string) error {
	_, err := s.svc.TriggerJobRun(s.jobAddr(jobID))
	if !errors.Is(err, schedservice.ErrJobUntrusted) {
		return fmt.Errorf("manual run error = %v, want ErrJobUntrusted", err)
	}
	return nil
}

func (s *schedulerRunsState) runWorksInWorkspace(jobID string) error {
	run, err := s.lastRun(jobID)
	if err != nil {
		return err
	}
	meta, err := s.store.ReadMeta(run.SessionID)
	if err != nil {
		return err
	}
	if got := schedservice.CanonicalWorkspace(meta.CWD); got != s.workspace() {
		return fmt.Errorf("run of %q works in %q, want the workspace %q", jobID, got, s.workspace())
	}
	return nil
}

func (s *schedulerRunsState) noStateInWorkspace() error {
	var found []string
	_ = filepath.Walk(s.cwd, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".state") {
			found = append(found, p)
		}
		return nil
	})
	if len(found) > 0 {
		return fmt.Errorf("state files in the workspace: %v", found)
	}
	return nil
}

func (s *schedulerRunsState) createRefusedIDTaken() error {
	if !errors.Is(s.lastErr, schedservice.ErrJobExists) {
		return fmt.Errorf("create error = %v, want ErrJobExists", s.lastErr)
	}
	return nil
}

// worktree is the linked worktree the scenario's workspace holds once it is a
// repository (gitwstest.AddWorktree lays it out there).
func (s *schedulerRunsState) worktree() string {
	return filepath.Join(s.cwd, ".coddy", "worktrees", "feature-login")
}

func (s *schedulerRunsState) workspaceIsARepositoryWithAWorktree() error {
	if err := gitwstest.Init(s.cwd); err != nil {
		return err
	}
	_, err := gitwstest.AddWorktree(s.cwd)
	return err
}

func (s *schedulerRunsState) committedToTheWorkspaceAndItsWorktree(jobID, schedule string) error {
	if err := s.committedProjectJob(jobID, schedule); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(storage.ProjectDir(s.cwd), jobID+".md"))
	if err != nil {
		return err
	}
	dir := storage.ProjectDir(s.worktree())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, jobID+".md"), data, 0o644)
}

func (s *schedulerRunsState) chatInTheWorktreeOpensTheScheduler() error {
	_, err := s.svc.WithSession(s.worktree()).ListJobs(schedservice.ListOptions{Workspace: s.worktree()})
	return err
}

func (s *schedulerRunsState) worktreeCopyListedUnscheduled(jobID, state string) error {
	out, err := s.svc.WithSession(s.worktree()).ListJobs(schedservice.ListOptions{Workspace: s.worktree()})
	if err != nil {
		return err
	}
	ws := schedservice.CanonicalWorkspace(s.worktree())
	for _, j := range out.Jobs {
		if j.JobID != jobID || j.Scope != "project" || j.Workspace != ws {
			continue
		}
		if string(j.Trust) != state || j.Scheduled {
			return fmt.Errorf("the worktree's copy of %q is %q (%s), scheduled %t; want %q and not scheduled", jobID, j.Trust, j.TrustReason, j.Scheduled, state)
		}
		return nil
	}
	return fmt.Errorf("the worktree's copy of %q is not listed: %+v", jobID, out.Jobs)
}

func (s *schedulerRunsState) worktreeCopyHasRuns(jobID string, n int) error {
	addr := schedservice.JobAddr{Scope: storage.ScopeProject, Workspace: schedservice.CanonicalWorkspace(s.worktree()), ID: jobID}
	runs, err := s.svc.ListJobRuns(addr, 0)
	if err != nil {
		return err
	}
	if len(runs) != n {
		return fmt.Errorf("the worktree's copy of %q has %d runs, want %d: %+v", jobID, len(runs), n, runs)
	}
	return nil
}

func initializeSchedulerProjectScenario(sc *godog.ScenarioContext) {
	s := initializeSchedulerRunsSteps(sc)
	sc.Step(`^a scheduler$`, s.start)
	sc.Step(`^the operator creates a project job "([^"]*)" scheduled "([^"]*)" from a session in the workspace$`, s.createProjectJobStep)
	sc.Step(`^a project job "([^"]*)" scheduled "([^"]*)" committed to the workspace$`, s.committedProjectJob)
	sc.Step(`^the project job "([^"]*)" is listed as "([^"]*)"$`, s.projectListedAs)
	sc.Step(`^the operator approves the project job "([^"]*)" with the digest it was shown$`, s.approveShownDigest)
	sc.Step(`^the project job "([^"]*)" is rewritten on disk$`, s.rewriteOnDisk)
	sc.Step(`^a manual run of "([^"]*)" is refused because the job is not trusted$`, s.manualRunRefusedUntrusted)
	sc.Step(`^the run of "([^"]*)" works in the workspace$`, s.runWorksInWorkspace)
	sc.Step(`^no state file is left in the workspace$`, s.noStateInWorkspace)
	sc.Step(`^the create is refused because the id is taken$`, s.createRefusedIDTaken)
	sc.Step(`^the workspace is a git repository with a worktree$`, s.workspaceIsARepositoryWithAWorktree)
	sc.Step(`^a project job "([^"]*)" scheduled "([^"]*)" committed to the workspace and its worktree$`, s.committedToTheWorkspaceAndItsWorktree)
	sc.Step(`^a chat in the worktree opens the scheduler$`, s.chatInTheWorktreeOpensTheScheduler)
	sc.Step(`^the copy of "([^"]*)" in the worktree is listed as "([^"]*)" and not scheduled$`, s.worktreeCopyListedUnscheduled)
	sc.Step(`^the copy of "([^"]*)" in the worktree has (\d+) runs?$`, s.worktreeCopyHasRuns)
}

// createProjectJobStep keeps a refused create for the step that checks it,
// and fails at once on any other error.
func (s *schedulerRunsState) createProjectJobStep(jobID, schedule string) error {
	if err := s.createProjectJob(jobID, schedule); err != nil {
		return err
	}
	if s.lastErr != nil && !errors.Is(s.lastErr, schedservice.ErrJobExists) {
		return s.lastErr
	}
	return nil
}

func TestSchedulerProjectJobsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "scheduler_project_jobs",
		ScenarioInitializer: initializeSchedulerProjectScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/scheduler_project_jobs.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("scheduler project jobs feature suite failed")
	}
}
