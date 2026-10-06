//go:build http && scheduler

package httpserver

// Godog harness for features/scheduler_project_jobs_http.feature, over the
// state of the scheduler runs HTTP harness: a session sits in a workspace of
// its own, and the requests carry its id the way the web UI sends it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// jobRef is the job the scenario works on, project or user.
func (s *schedulerHTTPState) jobRef() storage.JobRef {
	roots := schedservice.RootsOf(s.cfg)
	if s.project {
		return roots.ProjectRef(schedservice.CanonicalWorkspace(s.workspace), s.jobID)
	}
	return roots.UserRef(s.jobID)
}

func (s *schedulerHTTPState) sessionInWorkspace() error {
	s.workspace = filepath.Join(s.root, "workspace")
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return err
	}
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *schedulerHTTPState) serverWithWorkspaceSession() error {
	if err := s.serverWithJob("nightly"); err != nil {
		return err
	}
	return s.sessionInWorkspace()
}

func (s *schedulerHTTPState) sessionHeader() map[string]string {
	return map[string]string{"X-Coddy-Session-ID": s.sessionID}
}

func (s *schedulerHTTPState) postProjectJob(jobID string) error {
	body, _ := json.Marshal(schedservice.SchedulerJobCreate{
		Scope: "project", JobID: jobID, Description: "bdd " + jobID, Schedule: "0 3 * * *", Mode: "agent", Body: "Lint the project.",
	})
	s.jobID, s.project = jobID, true
	return s.do(http.MethodPost, "/coddy/scheduler/jobs", body, s.sessionHeader())
}

func (s *schedulerHTTPState) answers(code int) error {
	if s.status != code {
		return fmt.Errorf("status %d, want %d: %v", s.status, code, s.body)
	}
	return nil
}

func (s *schedulerHTTPState) listedJobs(headers map[string]string) ([]map[string]interface{}, error) {
	if err := s.do(http.MethodGet, "/coddy/scheduler/jobs", nil, headers); err != nil {
		return nil, err
	}
	if s.status != http.StatusOK {
		return nil, fmt.Errorf("list status %d: %v", s.status, s.body)
	}
	raw, _ := s.body["jobs"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		if row, ok := r.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *schedulerHTTPState) sessionListShowsProject(jobID, trust string) error {
	rows, err := s.listedJobs(s.sessionHeader())
	if err != nil {
		return err
	}
	want := schedservice.CanonicalWorkspace(s.workspace)
	for _, row := range rows {
		if str(row, "job_id") == jobID && str(row, "scope") == "project" {
			if str(row, "trust") != trust || str(row, "workspace") != want {
				return fmt.Errorf("row %v, want trust %q in %q", row, trust, want)
			}
			return nil
		}
	}
	return fmt.Errorf("project job %q not listed for the session: %v", jobID, rows)
}

func (s *schedulerHTTPState) plainListHides(jobID string) error {
	rows, err := s.listedJobs(nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if str(row, "job_id") == jobID {
			return fmt.Errorf("a listing without a session shows %q: %v", jobID, row)
		}
	}
	return nil
}

func (s *schedulerHTTPState) committedProjectJob(jobID string) error {
	dir := storage.ProjectDir(s.workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.jobID, s.project = jobID, true
	return os.WriteFile(filepath.Join(dir, jobID+".md"), []byte("---\nschedule: \"0 3 * * *\"\nmode: agent\n---\nFrom the repository.\n"), 0o644)
}

func projectQuery() string {
	return "?" + url.Values{"scope": {"project"}}.Encode()
}

func (s *schedulerHTTPState) getProjectJob(jobID string) error {
	return s.do(http.MethodGet, "/coddy/scheduler/jobs/"+jobID+projectQuery(), nil, s.sessionHeader())
}

func (s *schedulerHTTPState) answersRawAndDigest(trust string) error {
	if err := s.answers(http.StatusOK); err != nil {
		return err
	}
	if str(s.body, "raw") == "" || str(s.body, "digest") == "" || str(s.body, "trust") != trust {
		return fmt.Errorf("job = %v, want raw, digest and trust %q", s.body, trust)
	}
	s.digest = str(s.body, "digest")
	return nil
}

func (s *schedulerHTTPState) postProjectRun(jobID string) error {
	if err := s.do(http.MethodPost, "/coddy/scheduler/jobs/"+jobID+"/run"+projectQuery(), nil, s.sessionHeader()); err != nil {
		return err
	}
	if s.status == http.StatusAccepted {
		s.refs = append(s.refs, schedservice.RunRef{JobID: jobID, JobSessionID: str(s.body, "session_id"), TaskID: str(s.body, "task_id"), RunSessionID: str(s.body, "run_session_id")})
	}
	return nil
}

func (s *schedulerHTTPState) approveShownDigest(jobID string) error {
	body, _ := json.Marshal(map[string]string{"digest": s.digest})
	return s.do(http.MethodPost, "/coddy/scheduler/jobs/"+jobID+"/trust"+projectQuery(), body, s.sessionHeader())
}

func initializeSchedulerProjectHTTPScenario(sc *godog.ScenarioContext) {
	s := &schedulerHTTPState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy serve server with the scheduler and a session in a workspace$`, s.serverWithWorkspaceSession)
	sc.Step(`^a running coddy serve server with the scheduler and a job "([^"]*)"$`, s.serverWithJob)
	sc.Step(`^a session in a workspace$`, s.sessionInWorkspace)
	sc.Step(`^I POST the project job "([^"]*)" with the session header$`, s.postProjectJob)
	sc.Step(`^the API answers (\d+)$`, s.answers)
	sc.Step(`^the jobs listed for the session show the project job "([^"]*)" as "([^"]*)" in the session's workspace$`, s.sessionListShowsProject)
	sc.Step(`^the jobs listed without a session do not show "([^"]*)"$`, s.plainListHides)
	sc.Step(`^a project job "([^"]*)" committed to the session's workspace$`, s.committedProjectJob)
	sc.Step(`^I GET the project job "([^"]*)" with the session header$`, s.getProjectJob)
	sc.Step(`^it answers its raw file, its digest and the trust "([^"]*)"$`, s.answersRawAndDigest)
	sc.Step(`^I POST a run of the project job "([^"]*)" with the session header$`, s.postProjectRun)
	sc.Step(`^I approve the project job "([^"]*)" with the digest it showed$`, s.approveShownDigest)
}

func TestSchedulerProjectJobsHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "scheduler_project_jobs_http",
		ScenarioInitializer: initializeSchedulerProjectHTTPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/scheduler_project_jobs_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("scheduler project jobs HTTP feature suite failed")
	}
}
