//go:build scheduler

package schedservice

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// projectFixture is a coddy home, a process cwd and a second workspace a
// session sits in.
type projectFixture struct {
	cfg             *config.Config
	home, proc, ws  string
	svc, sessionSvc *Service
}

func newProjectFixture(t *testing.T, policy string) *projectFixture {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	f := &projectFixture{home: filepath.Join(root, "home"), proc: filepath.Join(root, "proc"), ws: filepath.Join(root, "ws")}
	for _, d := range []string{f.home, f.proc, f.ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.cfg = &config.Config{
		Paths:     config.Paths{Home: f.home, CWD: f.proc},
		Sessions:  config.Sessions{Dir: filepath.Join(root, "sessions")},
		Scheduler: config.SchedulerConfig{Enabled: true, ProjectTrust: policy},
	}
	f.svc = NewService(f.cfg, nil, f.proc)
	f.sessionSvc = f.svc.WithSession(f.ws)
	return f
}

func (f *projectFixture) commit(t *testing.T, id, body string) {
	t.Helper()
	dir := storage.ProjectDir(f.ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *projectFixture) addr(id string) JobAddr {
	return JobAddr{Scope: "project", Workspace: f.ws, ID: id}
}

func (f *projectFixture) state(t *testing.T, id string) TrustState {
	t.Helper()
	snap, err := storage.ReadSnapshot(RootsOf(f.cfg).ProjectRef(f.ws, id))
	if err != nil {
		t.Fatal(err)
	}
	st, _ := Decide(f.cfg, snap)
	return st
}

const committedJob = "---\nschedule: \"0 3 * * *\"\n---\nfrom the repository\n"

func TestAnEditOfAnUntrustedProjectJobIssuesNoReceipt(t *testing.T) {
	f := newProjectFixture(t, "")
	f.commit(t, "lint", committedJob)
	if err := f.sessionSvc.PauseJob(f.addr("lint")); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, "lint"); got != TrustNeedsApproval {
		t.Fatalf("a pause through Coddy laundered a foreign job: %s", got)
	}
	desc := "rewritten"
	if err := f.sessionSvc.PatchJob(f.addr("lint"), SchedulerJobPatch{Description: &desc}); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, "lint"); got != TrustNeedsApproval {
		t.Fatalf("a patch through Coddy approved a foreign job: %s", got)
	}
}

func TestAnEditOfATrustedProjectJobKeepsItTrusted(t *testing.T) {
	f := newProjectFixture(t, "")
	if err := f.sessionSvc.CreateJob(SchedulerJobCreate{Scope: "project", JobID: "lint", Schedule: "0 3 * * *", Body: "mine"}); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, "lint"); got != TrustTrusted {
		t.Fatalf("the operator's own create is %s", got)
	}
	body, newID := "mine, edited", "lint2"
	if err := f.sessionSvc.PatchJob(f.addr("lint"), SchedulerJobPatch{Body: &body, JobID: &newID}); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, "lint2"); got != TrustTrusted {
		t.Fatalf("an edit and rename of a trusted job left it %s", got)
	}
	if _, err := os.Stat(filepath.Join(storage.ProjectDir(f.ws), "lint.md")); !os.IsNotExist(err) {
		t.Fatal("the old file is still there")
	}
}

func TestIDsNeverClashAcrossScopes(t *testing.T) {
	f := newProjectFixture(t, "")
	if err := f.svc.CreateJob(SchedulerJobCreate{JobID: "nightly", Schedule: "0 3 * * *", Body: "user"}); err != nil {
		t.Fatal(err)
	}
	err := f.sessionSvc.CreateJob(SchedulerJobCreate{Scope: "project", JobID: "nightly", Schedule: "0 3 * * *", Body: "p"})
	if !errors.Is(err, ErrJobExists) {
		t.Fatalf("project create with a user id = %v", err)
	}
	if err := f.sessionSvc.CreateJob(SchedulerJobCreate{Scope: "project", JobID: "lint", Schedule: "0 3 * * *", Body: "p"}); err != nil {
		t.Fatal(err)
	}
	err = f.svc.CreateJob(SchedulerJobCreate{JobID: "lint", Schedule: "0 3 * * *", Body: "user"})
	if !errors.Is(err, ErrJobExists) {
		t.Fatalf("user create with the id of a project job of a known workspace = %v", err)
	}
	// A file that arrives with a clashing id anyway is a conflict and cannot
	// be approved.
	f.commit(t, "nightly", committedJob)
	if got := f.state(t, "nightly"); got != TrustConflict {
		t.Fatalf("clashing project job is %s", got)
	}
	snap, _ := storage.ReadSnapshot(RootsOf(f.cfg).ProjectRef(f.ws, "nightly"))
	if err := f.sessionSvc.TrustJob(f.addr("nightly"), snap.Digest); !errors.Is(err, ErrJobUntrusted) {
		t.Fatalf("approval of a conflict = %v", err)
	}
}

func TestApprovalIsBoundToTheShownDigest(t *testing.T) {
	f := newProjectFixture(t, "")
	f.commit(t, "lint", committedJob)
	if err := f.sessionSvc.TrustJob(f.addr("lint"), storage.Digest([]byte("something else"))); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("approval with a stale digest = %v", err)
	}
	if err := f.sessionSvc.TrustJob(UserJob("lint"), "x"); !errors.Is(err, ErrTrustNotApplicable) {
		t.Fatalf("approval of a user job = %v", err)
	}
	if err := f.sessionSvc.TrustJob(f.addr("lint"), storage.Digest([]byte(committedJob))); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, "lint"); got != TrustTrusted {
		t.Fatalf("approved job is %s", got)
	}
	if removed, err := f.sessionSvc.UntrustJob(f.addr("lint")); err != nil || !removed {
		t.Fatalf("untrust = %v, %v", removed, err)
	}
	if got := f.state(t, "lint"); got != TrustNeedsApproval {
		t.Fatalf("withdrawn job is %s", got)
	}
}

func TestDenyListsButNeverRunsNorApproves(t *testing.T) {
	f := newProjectFixture(t, "deny")
	f.commit(t, "lint", committedJob)
	if got := f.state(t, "lint"); got != TrustDenied {
		t.Fatalf("project job under deny is %s", got)
	}
	if _, err := RunnableSnapshot(f.cfg, RootsOf(f.cfg).ProjectRef(f.ws, "lint")); !errors.Is(err, ErrJobUntrusted) {
		t.Fatalf("runnable under deny = %v", err)
	}
	if err := f.sessionSvc.TrustJob(f.addr("lint"), storage.Digest([]byte(committedJob))); !errors.Is(err, ErrTrustNotApplicable) {
		t.Fatalf("approval under deny = %v", err)
	}
}

func TestAllowRunsOnceTheWorkspaceIsKnown(t *testing.T) {
	f := newProjectFixture(t, "allow")
	f.commit(t, "lint", committedJob)
	if got := f.state(t, "lint"); got != TrustTrusted {
		t.Fatalf("project job under allow is %s", got)
	}
	for _, ws := range ScanWorkspaces(f.cfg, f.proc) {
		if ws == f.ws {
			t.Fatal("a workspace nobody asked about is scanned")
		}
	}
	// A raw cwd listing (no session) does not register the workspace...
	out, err := f.svc.ListJobs(ListOptions{Workspace: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Scheduled {
		t.Fatalf("jobs of an unknown workspace = %+v", out.Jobs)
	}
	// ...a session listing does, and the job is scheduled from then on.
	if _, err := f.sessionSvc.ListJobs(ListOptions{Workspace: f.ws}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ws := range ScanWorkspaces(f.cfg, f.proc) {
		found = found || ws == f.ws
	}
	if !found {
		t.Fatal("the session's workspace was not registered")
	}
}

func TestTheUnscopedListIsUserJobsOnly(t *testing.T) {
	f := newProjectFixture(t, "")
	if err := f.svc.CreateJob(SchedulerJobCreate{JobID: "nightly", Schedule: "0 3 * * *", Body: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := f.sessionSvc.CreateJob(SchedulerJobCreate{Scope: "project", JobID: "lint", Schedule: "0 3 * * *", Body: "p"}); err != nil {
		t.Fatal(err)
	}
	out, err := f.svc.ListJobs(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].JobID != "nightly" || out.Jobs[0].Scope != "user" {
		t.Fatalf("unscoped list = %+v", out.Jobs)
	}
	scoped, err := f.sessionSvc.ListJobs(ListOptions{Workspace: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Jobs) != 2 || scoped.Jobs[1].Scope != "project" || scoped.Jobs[1].Trust != TrustTrusted {
		t.Fatalf("scoped list = %+v", scoped.Jobs)
	}
}

func TestAProjectJobOfAnUnknownWorkspaceIsNotAddressable(t *testing.T) {
	f := newProjectFixture(t, "")
	if _, err := f.svc.ProjectJob("lint", f.ws); !errors.Is(err, ErrWorkspaceUnknown) {
		t.Fatalf("a workspace without a session or a receipt = %v", err)
	}
	if _, err := f.svc.ProjectJob("lint", ""); !errors.Is(err, ErrWorkspaceUnknown) {
		t.Fatalf("no workspace at all = %v", err)
	}
	if _, err := f.sessionSvc.ProjectJob("lint", f.ws); err != nil {
		t.Fatalf("the session's own workspace = %v", err)
	}
}

func TestJobSessionFallbackKeepsScopesApart(t *testing.T) {
	f := newProjectFixture(t, "")
	store := &session.FileStore{Root: f.cfg.Sessions.Dir}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	id := session.NewSessionID()
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: id, CWD: f.ws, Mode: session.ModeAgent, SessionDir: dir}
	st.SetSchedulerJobWithoutPersist("lint")
	st.SetSchedulerJobWorkspaceWithoutPersist(f.ws)
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	roots := RootsOf(f.cfg)
	if got := JobSessionIDFor(store, roots.UserRef("lint")); got != "" {
		t.Fatalf("a user job adopted a project job's session: %q", got)
	}
	if got := JobSessionIDFor(store, roots.ProjectRef(f.ws, "lint")); got != id {
		t.Fatalf("the project job's session = %q, want %q", got, id)
	}
}

// A per-job request from a session in a workspace registers it, as a list
// does, so a job there runs under allow without ever being listed first.
func TestAPerJobRequestFromASessionRegistersItsWorkspace(t *testing.T) {
	f := newProjectFixture(t, "allow")
	f.commit(t, "lint", committedJob)
	if _, err := f.sessionSvc.ProjectJob("lint", ""); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ws := range ScanWorkspaces(f.cfg, f.proc) {
		found = found || ws == f.ws
	}
	if !found {
		t.Fatal("the session's workspace was not registered by a per-job request")
	}
}

// A rename carries the job session's name with it: a new job created under
// the old id does not adopt the renamed job's run history through the walk.
func TestARenameMovesTheJobSessionName(t *testing.T) {
	f := newProjectFixture(t, "")
	if err := f.svc.CreateJob(SchedulerJobCreate{JobID: "old", Schedule: "0 3 * * *", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	store := &session.FileStore{Root: f.cfg.Sessions.Dir}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	id := session.NewSessionID()
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &session.State{ID: id, CWD: f.proc, Mode: session.ModeAgent, SessionDir: dir}
	st.SetSchedulerJobWithoutPersist("old")
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	roots := RootsOf(f.cfg)
	if err := storage.WriteJobSessionID(roots.UserRef("old").StatePath, id); err != nil {
		t.Fatal(err)
	}
	newID := "new"
	if err := f.svc.PatchJob(UserJob("old"), SchedulerJobPatch{JobID: &newID}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CreateJob(SchedulerJobCreate{JobID: "old", Schedule: "0 4 * * *", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	if got := JobSessionIDFor(store, roots.UserRef("old")); got != "" {
		t.Fatalf("a new job under the old id adopted the renamed job's session %q", got)
	}
	if got := JobSessionIDFor(store, roots.UserRef("new")); got != id {
		t.Fatalf("the renamed job's session = %q, want %q", got, id)
	}
}
