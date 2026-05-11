//go:build http && scheduler

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// Register LaunchManualJob (same as cmd/coddy linking scheduler).
	_ "github.com/EvilFreelancer/coddy-agent/external/scheduler"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestCoddySchedulerHTTPJobsEnvelopeCRUDPauseRunConflict(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "cwd")
	schedRoot := filepath.Join(root, "scheduler")
	sessRoot := filepath.Join(root, "sessions")
	for _, d := range []string{home, cwd, schedRoot, sessRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedRoot},
		Sessions:  config.Sessions{Dir: sessRoot},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
	}
	p := cfg.Paths
	cfg.Scheduler.Normalize(p)
	cfg.Scheduler.ApplyDefaults(p)

	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	store := &session.FileStore{Root: sessRoot}
	log := slog.Default()
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, store)
	ts := httptest.NewServer(New(cfg, mgr, log, cwd).Handler())
	defer ts.Close()

	r0, err := http.Get(ts.URL + "/coddy/scheduler/jobs")
	if err != nil {
		t.Fatal(err)
	}
	b0, _ := ioReadAllClose(r0.Body)
	if r0.StatusCode != http.StatusOK {
		t.Fatalf("jobs list status %d %s", r0.StatusCode, b0)
	}
	var env map[string]interface{}
	if err := json.Unmarshal(b0, &env); err != nil {
		t.Fatal(err)
	}
	if _, ok := env["scheduler"].(map[string]interface{}); !ok {
		t.Fatalf("missing scheduler envelope: %s", b0)
	}
	if _, ok := env["jobs"].([]interface{}); !ok {
		t.Fatalf("missing jobs arr: %s", b0)
	}

	payload := strings.NewReader(`{"job_id":"demo","description":"Demo","schedule":"0 9 * * 1","body":"Say hello."}`)
	rPost, err := http.Post(ts.URL+"/coddy/scheduler/jobs", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	bPost, _ := ioReadAllClose(rPost.Body)
	if rPost.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rPost.StatusCode, bPost)
	}

	rG, err := http.Get(ts.URL + "/coddy/scheduler/jobs/demo")
	if err != nil {
		t.Fatal(err)
	}
	bG, _ := ioReadAllClose(rG.Body)
	if rG.StatusCode != http.StatusOK {
		t.Fatalf("get demo %d %s", rG.StatusCode, bG)
	}

	rRuns, err := http.Get(ts.URL + "/coddy/scheduler/jobs/demo/runs")
	if err != nil {
		t.Fatal(err)
	}
	bRuns, _ := ioReadAllClose(rRuns.Body)
	if rRuns.StatusCode != http.StatusOK {
		t.Fatalf("runs empty list %d %s", rRuns.StatusCode, bRuns)
	}

	badURL := ts.URL + "/coddy/scheduler/jobs/" + url.PathEscape("a/b")
	rBad, err := http.Get(badURL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(rBad.Body)
	if rBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad job_id status=%d", rBad.StatusCode)
	}

	putBody := strings.NewReader(`{"description":"Demo2","schedule":"0 10 * * 1","paused":false,"body":"Next."}`)
	reqPut, err := http.NewRequest(http.MethodPut, ts.URL+"/coddy/scheduler/jobs/demo", putBody)
	if err != nil {
		t.Fatal(err)
	}
	reqPut.Header.Set("Content-Type", "application/json")
	putRes, err := http.DefaultClient.Do(reqPut)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(putRes.Body)
	if putRes.StatusCode != http.StatusOK {
		t.Fatalf("put demo %d", putRes.StatusCode)
	}

	patchPaused := strings.NewReader(`{"paused":true}`)
	reqP, err := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/scheduler/jobs/demo", patchPaused)
	if err != nil {
		t.Fatal(err)
	}
	reqP.Header.Set("Content-Type", "application/json")
	rP, err := http.DefaultClient.Do(reqP)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(rP.Body)
	if rP.StatusCode != http.StatusOK {
		t.Fatalf("pause %d", rP.StatusCode)
	}

	runReq, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/scheduler/jobs/demo/run", nil)
	if err != nil {
		t.Fatal(err)
	}
	runRes, err := http.DefaultClient.Do(runReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(runRes.Body)
	if runRes.StatusCode != http.StatusConflict {
		t.Fatalf("run while paused status=%d want 409", runRes.StatusCode)
	}

	lockPath := filepath.Join(schedRoot, "demo.lock")
	if err := os.WriteFile(lockPath, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	delReq, err := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/scheduler/jobs/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	delRes, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(delRes.Body)
	if delRes.StatusCode != http.StatusConflict {
		t.Fatalf("delete with lock=%d want 409", delRes.StatusCode)
	}
	_ = os.Remove(lockPath)

	rmReq, err := http.NewRequest(http.MethodDelete, ts.URL+"/coddy/scheduler/jobs/demo", nil)
	if err != nil {
		t.Fatal(err)
	}
	rmRes, err := http.DefaultClient.Do(rmReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ioReadAllClose(rmRes.Body)
	if rmRes.StatusCode != http.StatusNoContent {
		t.Fatalf("delete demo %d", rmRes.StatusCode)
	}

	rRuns404, err := http.Get(ts.URL + "/coddy/scheduler/jobs/demo/runs")
	if err != nil {
		t.Fatal(err)
	}
	b404, _ := ioReadAllClose(rRuns404.Body)
	if rRuns404.StatusCode != http.StatusNotFound {
		t.Fatalf("runs on deleted job=%d body=%s", rRuns404.StatusCode, b404)
	}

	dirS, err := store.EnsureLayout("sess_ui")
	if err != nil {
		t.Fatal(err)
	}
	ss := session.State{
		ID:         "sess_ui",
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: dirS,
	}
	ss.SetSchedulerRunMeta("jobx", "2026-05-01T00:00:00Z")
	ss.FinishSchedulerRun("2026-05-02T00:00:00Z", "completed")
	if err := store.Save(&ss); err != nil {
		t.Fatal(err)
	}
	ls, err := http.Get(ts.URL + "/coddy/sessions")
	if err != nil {
		t.Fatal(err)
	}
	bls, _ := ioReadAllClose(ls.Body)
	if ls.StatusCode != http.StatusOK {
		t.Fatalf("sessions list %d %s", ls.StatusCode, bls)
	}
	var defaultList struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(bls, &defaultList); err != nil {
		t.Fatal(err)
	}
	for _, row := range defaultList.Sessions {
		if row.ID == "sess_ui" {
			t.Fatalf("scheduler session leaked into default composer list")
		}
	}

	lsw, err := http.Get(ts.URL + "/coddy/sessions?include_scheduler=true")
	if err != nil {
		t.Fatal(err)
	}
	blsw, _ := ioReadAllClose(lsw.Body)
	if lsw.StatusCode != http.StatusOK {
		t.Fatalf("sessions+sched %d %s", lsw.StatusCode, blsw)
	}
	var withSched struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(blsw, &withSched); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range withSched.Sessions {
		if row.ID == "sess_ui" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("include_scheduler listing missing sess_ui: %s", blsw)
	}
}

func TestCoddySchedulerCancelClearsStaleLock(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "cwd")
	schedRoot := filepath.Join(root, "scheduler")
	sessRoot := filepath.Join(root, "sessions")
	for _, d := range []string{home, cwd, schedRoot, sessRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedRoot},
		Sessions:  config.Sessions{Dir: sessRoot},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
	}
	p := cfg.Paths
	cfg.Scheduler.Normalize(p)
	cfg.Scheduler.ApplyDefaults(p)

	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	store := &session.FileStore{Root: sessRoot}
	log := slog.Default()
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, store)
	ts := httptest.NewServer(New(cfg, mgr, log, cwd).Handler())
	defer ts.Close()

	payload := strings.NewReader(`{"job_id":"demo","description":"Demo","schedule":"0 9 * * 1","body":"Say hello."}`)
	rPost, err := http.Post(ts.URL+"/coddy/scheduler/jobs", "application/json", payload)
	if err != nil {
		t.Fatal(err)
	}
	bPost, _ := ioReadAllClose(rPost.Body)
	if rPost.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rPost.StatusCode, bPost)
	}

	lockPath := filepath.Join(schedRoot, "demo.lock")
	if err := os.WriteFile(lockPath, []byte("2020-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/coddy/scheduler/jobs/demo/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", res.StatusCode, b)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["cancelled"] != true {
		t.Fatalf("want cancelled true, got %+v", out)
	}
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("stale lock should be removed")
	}
}
