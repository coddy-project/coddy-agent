//go:build http

package httpserver

// Godog harness for features/disk_space_http.feature: GET /coddy/info and the
// events stream over a real manager and a real store in a temporary folder.
// The disk the store lives on is the machine's own, so the scenarios fix the
// outcome with the threshold (1 MB is below any disk a test runs on, a petabyte
// above it) instead of with the room the machine happens to have.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type diskSpaceHTTPState struct {
	t        *testing.T
	ts       *httptest.Server
	srv      *Server
	info     map[string]interface{}
	events   *bufio.Reader
	stop     func()
	status   int
	body     []byte
	teardown []func()
}

func (s *diskSpaceHTTPState) reset() {
	s.close()
	s.ts, s.srv, s.info, s.events, s.stop, s.status, s.body = nil, nil, nil, nil, nil, 0, nil
}

func (s *diskSpaceHTTPState) close() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	for i := len(s.teardown) - 1; i >= 0; i-- {
		s.teardown[i]()
	}
	s.teardown = nil
}

func (s *diskSpaceHTTPState) start(minFreeMB int) error {
	// newStorageServer registers its own cleanup on the test, which runs at the
	// end of the whole feature; the scenario's server is closed by that, one
	// server per scenario.
	ts, srv, _ := newStorageServer(s.t, &minFreeMB)
	s.ts, s.srv = ts, srv
	return nil
}

func (s *diskSpaceHTTPState) readInfo() error {
	res, err := http.Get(s.ts.URL + "/coddy/info")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /coddy/info: status %d", res.StatusCode)
	}
	s.info = map[string]interface{}{}
	return json.NewDecoder(res.Body).Decode(&s.info)
}

func (s *diskSpaceHTTPState) storage() (map[string]interface{}, error) {
	st, _ := s.info["storage"].(map[string]interface{})
	if st == nil {
		return nil, fmt.Errorf("the answer has no storage object: %v", s.info)
	}
	return st, nil
}

func (s *diskSpaceHTTPState) stateIs(want string) error {
	st, err := s.storage()
	if err != nil {
		return err
	}
	if st["state"] != want {
		return fmt.Errorf("storage.state = %v, want %q (%v)", st["state"], want, st)
	}
	return nil
}

func (s *diskSpaceHTTPState) carriesFigures() error {
	st, err := s.storage()
	if err != nil {
		return err
	}
	free, _ := st["freeBytes"].(float64)
	total, _ := st["totalBytes"].(float64)
	if free <= 0 || total < free {
		return fmt.Errorf("freeBytes=%v totalBytes=%v do not describe a disk", st["freeBytes"], st["totalBytes"])
	}
	return nil
}

func (s *diskSpaceHTTPState) carriesThreshold(mb int) error {
	st, err := s.storage()
	if err != nil {
		return err
	}
	if got, _ := st["minFreeBytes"].(float64); got != float64(mb)*(1<<20) {
		return fmt.Errorf("minFreeBytes = %v, want %d MB", st["minFreeBytes"], mb)
	}
	return nil
}

func (s *diskSpaceHTTPState) listen() error {
	body, stop := subscribeEvents(s.t, s.ts, "")
	s.events, s.stop = body, stop
	readEventFrames(s.t, body, "event: ready")
	return nil
}

func (s *diskSpaceHTTPState) volumeFull() error {
	s.srv.ensureSession = func(context.Context, string, string, string) (*session.State, error) {
		return nil, fmt.Errorf("session/new: layout: %w", &os.PathError{Op: "mkdir", Path: "sessions/sess_new", Err: diskFullErrno})
	}
	return nil
}

func (s *diskSpaceHTTPState) postFirstMessage(endpoint string) error {
	res, err := http.Post(s.ts.URL+endpoint, "application/json", strings.NewReader(`{"model":"agent","input":"hello"}`))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, err = io.ReadAll(res.Body)
	return err
}

func (s *diskSpaceHTTPState) answerIs507() error {
	if s.status != http.StatusInsufficientStorage {
		return fmt.Errorf("status = %d, want 507; body %s", s.status, s.body)
	}
	return nil
}

func (s *diskSpaceHTTPState) streamAnnouncesFailure() error {
	frame := readEventFrames(s.t, s.events, "event: storage_status")
	if !strings.Contains(frame, `"writeFailing":true`) {
		return fmt.Errorf("the frame does not say saves are failing: %q", frame)
	}
	return nil
}

func initializeDiskSpaceHTTPScenario(t *testing.T) func(sc *godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		s := &diskSpaceHTTPState{t: t}
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			s.reset()
			return ctx, nil
		})
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			s.close()
			return ctx, nil
		})
		sc.Step(`^a running coddy HTTP server whose warning threshold is (\d+) MB$`, func(mb string) error {
			n, _ := strconv.Atoi(mb)
			return s.start(n)
		})
		sc.Step(`^a running coddy HTTP server whose warning threshold is more than the disk holds$`, func() error {
			return s.start(1 << 30)
		})
		sc.Step(`^a client reads /coddy/info$`, s.readInfo)
		sc.Step(`^the storage state is "([^"]*)"$`, s.stateIs)
		sc.Step(`^the answer carries the free and the total bytes of the disk$`, s.carriesFigures)
		sc.Step(`^the answer carries the threshold of (\d+) MB$`, func(mb string) error {
			n, _ := strconv.Atoi(mb)
			return s.carriesThreshold(n)
		})
		sc.Step(`^a client is listening to the events stream$`, s.listen)
		sc.Step(`^the volume that holds the sessions folder has no room left$`, s.volumeFull)
		sc.Step(`^a client posts a first message to (\S+) without a session$`, s.postFirstMessage)
		sc.Step(`^the answer is 507 Insufficient Storage$`, s.answerIs507)
		sc.Step(`^the events stream announces that saves are failing$`, s.streamAnnouncesFailure)
	}
}

func TestDiskSpaceHTTPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "disk-space-http",
		ScenarioInitializer: initializeDiskSpaceHTTPScenario(t),
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/disk_space_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("disk space HTTP feature failed")
	}
}
