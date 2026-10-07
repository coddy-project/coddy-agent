//go:build scheduler

package schedtools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// A project job the agent creates from a session is the operator's own:
// approved at once. Editing a job that came with the checkout through a tool
// approves nothing, and no tool exists that would.
func TestToolsCreateTrustedProjectJobsAndNeverApproveForeignOnes(t *testing.T) {
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	home, ws := filepath.Join(root, "home"), filepath.Join(root, "ws")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: root}, Scheduler: config.SchedulerConfig{Enabled: true}}
	env := &tooling.Env{CWD: ws}
	ctx := context.Background()

	if _, err := jobCreateTool(cfg).Execute(ctx, `{"scope":"project","job_id":"lint","description":"d","schedule":"0 3 * * *","permission_mode":"bypass","body":"lint"}`, env); err != nil {
		t.Fatal(err)
	}
	decide := func(id string) schedservice.TrustState {
		snap, err := storage.ReadSnapshot(schedservice.RootsOf(cfg).ProjectRef(ws, id))
		if err != nil {
			t.Fatal(err)
		}
		st, _ := schedservice.Decide(cfg, snap)
		return st
	}
	if got := decide("lint"); got != schedservice.TrustTrusted {
		t.Fatalf("a tool create is %s", got)
	}

	if err := os.WriteFile(filepath.Join(storage.ProjectDir(ws), "foreign.md"), []byte("---\nschedule: \"0 3 * * *\"\n---\nfrom git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := jobPatchTool(cfg).Execute(ctx, `{"scope":"project","job_id":"foreign","body":"edited by the agent"}`, env); err != nil {
		t.Fatal(err)
	}
	if got := decide("foreign"); got != schedservice.TrustNeedsApproval {
		t.Fatalf("a tool edit approved a foreign job: %s", got)
	}

	var names []string
	RegisterTools(func(tool *tooling.Tool) { names = append(names, tool.Definition.Name) }, cfg)
	for _, n := range names {
		if n == "coddy_scheduler_job_trust" || n == "coddy_scheduler_job_approve" {
			t.Fatalf("a tool approves project jobs: %s", n)
		}
	}
}

// A tool call from a session elsewhere still knows the daemon's own
// workspace: a user job may not take the id of a project job there.
func TestToolsKnowTheDaemonsWorkspace(t *testing.T) {
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	home, proc, other := filepath.Join(root, "home"), filepath.Join(root, "proc"), filepath.Join(root, "other")
	for _, d := range []string{home, filepath.Join(proc, ".coddy", "scheduler"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(proc, ".coddy", "scheduler", "nightly.md"), []byte("---\nschedule: \"0 3 * * *\"\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: proc}, Scheduler: config.SchedulerConfig{Enabled: true}}
	_, err := jobCreateTool(cfg).Execute(context.Background(), `{"job_id":"nightly","description":"d","schedule":"0 3 * * *","body":"x"}`, &tooling.Env{CWD: other})
	if err == nil {
		t.Fatal("a user job took the id of a project job of the daemon's workspace")
	}
}
