package preview

// Godog harness for features/preview_server.feature: drives preview_server and
// the background task tools against a real pool, a real directory and a real
// HTTP client, so the scenarios assert what a browser would get.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
)

const bddSessionID = "bdd-preview"

var urlInResult = regexp.MustCompile(`http://[^\s]+`)

type previewServerState struct {
	pool *bgtask.Pool
	env  *tooling.Env
	dir  string

	result      string
	url         string
	previousURL string
	taskID      string
	listing     string
}

func (s *previewServerState) reset() error {
	dir, err := os.MkdirTemp("", "coddy-preview-bdd-")
	if err != nil {
		return err
	}
	s.dir = dir
	s.pool = bgtask.New(bgtask.Config{})
	s.env = &tooling.Env{
		CWD:               dir,
		SessionID:         bddSessionID,
		BackgroundEnabled: true,
		Background:        s.pool,
	}
	s.result, s.url, s.previousURL, s.taskID, s.listing = "", "", "", "", ""
	return nil
}

func (s *previewServerState) cleanup() {
	if s.pool != nil {
		s.pool.StopSession(bddSessionID)
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func (s *previewServerState) projectWithPage(name, text string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte("<!doctype html><p>"+text+"</p>"), 0o644)
}

func (s *previewServerState) startWith(args map[string]interface{}) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	out, err := PreviewServerTool().Execute(context.Background(), string(raw), s.env)
	if err != nil {
		return fmt.Errorf("preview_server returned %v", err)
	}
	s.result = out
	s.previousURL = s.url
	s.url = urlInResult.FindString(out)
	if s.url == "" {
		return fmt.Errorf("no URL in the tool result %q", out)
	}
	for _, snap := range s.pool.List(bddSessionID) {
		if snap.URL == s.url {
			s.taskID = snap.ID
		}
	}
	if s.taskID == "" {
		return fmt.Errorf("the pool holds no task for %s", s.url)
	}
	return nil
}

func (s *previewServerState) start() error { return s.startWith(map[string]interface{}{}) }

func (s *previewServerState) startWithTimeout(seconds int) error {
	return s.startWith(map[string]interface{}{"timeout_seconds": seconds})
}

func (s *previewServerState) reportsLocalhostURL() error {
	u, err := url.Parse(s.url)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/" {
		return fmt.Errorf("URL %q is not http://127.0.0.1:<port>/", s.url)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return fmt.Errorf("URL %q carries no port", s.url)
	}
	return nil
}

func get(address string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(address)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", address, resp.Status)
	}
	return string(body), nil
}

func (s *previewServerState) openingReturns(text string) error {
	body, err := get(s.url)
	if err != nil {
		return err
	}
	if !strings.Contains(body, text) {
		return fmt.Errorf("page %q does not contain %q", body, text)
	}
	return nil
}

func (s *previewServerState) invitesTheUser() error {
	lower := strings.ToLower(s.result)
	for _, want := range []string{"invite", "browser", s.url} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			return fmt.Errorf("tool result %q does not mention %q", s.result, want)
		}
	}
	return nil
}

func (s *previewServerState) listTasks() error {
	out, err := shell.BackgroundListTool().Execute(context.Background(), "{}", s.env)
	if err != nil {
		return err
	}
	s.listing = out
	return nil
}

func (s *previewServerState) listingShowsServer() error {
	for _, line := range strings.Split(s.listing, "\n") {
		if strings.Contains(line, s.taskID) && strings.Contains(line, "[running]") && strings.Contains(line, s.url) {
			snap, err := s.pool.Get(bddSessionID, s.taskID)
			if err != nil {
				return err
			}
			if snap.Kind != bgtask.KindServer {
				return fmt.Errorf("task kind = %q, want server", snap.Kind)
			}
			return nil
		}
	}
	return fmt.Errorf("listing %q has no running row for %s at %s", s.listing, s.taskID, s.url)
}

func (s *previewServerState) rewritePage(name, text string) error {
	return s.projectWithPage(name, text)
}

func (s *previewServerState) sameURL() error {
	if s.previousURL == "" || s.url != s.previousURL {
		return fmt.Errorf("second call reported %q, first reported %q", s.url, s.previousURL)
	}
	return nil
}

func (s *previewServerState) oneRunningServer() error {
	running := 0
	for _, snap := range s.pool.List(bddSessionID) {
		if snap.Kind == bgtask.KindServer && !snap.Status.Finished() {
			running++
		}
	}
	if running != 1 {
		return fmt.Errorf("%d running server tasks, want 1", running)
	}
	return nil
}

func (s *previewServerState) stopTask() error {
	args, _ := json.Marshal(map[string]interface{}{"task_id": s.taskID})
	_, err := shell.BackgroundStopTool().Execute(context.Background(), string(args), s.env)
	return err
}

func (s *previewServerState) waitForTask() error {
	snap, err := s.pool.Wait(context.Background(), bddSessionID, s.taskID, 10*time.Second)
	if err != nil {
		return err
	}
	if !snap.Status.Finished() {
		return fmt.Errorf("task %s is still %s", s.taskID, snap.Status)
	}
	return nil
}

func (s *previewServerState) taskHasStatus(want bgtask.Status) error {
	snap, err := s.pool.Get(bddSessionID, s.taskID)
	if err != nil {
		return err
	}
	if snap.Status != want {
		return fmt.Errorf("task status = %q, want %q", snap.Status, want)
	}
	return nil
}

func (s *previewServerState) taskIsStopped() error  { return s.taskHasStatus(bgtask.StatusStopped) }
func (s *previewServerState) taskIsTimedOut() error { return s.taskHasStatus(bgtask.StatusTimedOut) }

func (s *previewServerState) sessionIsDeleted() error {
	s.pool.StopSession(bddSessionID)
	return nil
}

func (s *previewServerState) urlNoLongerAnswers() error {
	u, err := url.Parse(s.url)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("%s still accepts connections", s.url)
	}
	return nil
}

func initializePreviewServerScenario(sc *godog.ScenarioContext) {
	s := &previewServerState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a project directory with an "([^"]*)" that says "([^"]*)"$`, s.projectWithPage)
	sc.Step(`^I start the preview server$`, s.start)
	sc.Step(`^I start the preview server again$`, s.start)
	sc.Step(`^I start the preview server with a timeout of (\d+) second$`, s.startWithTimeout)
	sc.Step(`^the tool reports a localhost URL on a free port$`, s.reportsLocalhostURL)
	sc.Step(`^opening that URL returns "([^"]*)"$`, s.openingReturns)
	sc.Step(`^the tool result invites the user to open that URL in a browser$`, s.invitesTheUser)
	sc.Step(`^I list the background tasks$`, s.listTasks)
	sc.Step(`^the listing shows a running server task with that URL$`, s.listingShowsServer)
	sc.Step(`^"([^"]*)" is rewritten to say "([^"]*)"$`, s.rewritePage)
	sc.Step(`^the tool reports the same URL as before$`, s.sameURL)
	sc.Step(`^the session has one running server task$`, s.oneRunningServer)
	sc.Step(`^I stop that background task$`, s.stopTask)
	sc.Step(`^I wait for that background task to finish$`, s.waitForTask)
	sc.Step(`^the task is stopped$`, s.taskIsStopped)
	sc.Step(`^the task is timed out$`, s.taskIsTimedOut)
	sc.Step(`^the session is deleted$`, s.sessionIsDeleted)
	sc.Step(`^that URL no longer answers$`, s.urlNoLongerAnswers)
}

func TestPreviewServerFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "preview-server",
		ScenarioInitializer: initializePreviewServerScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/preview_server.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("preview server feature suite failed")
	}
}
