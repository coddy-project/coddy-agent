package session_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Godog harness for features/disk_full.feature: a manager over a real
// FileStore whose writes are made to fail the way a full volume fails them
// (the store's test fault, a *os.PathError around the platform's code), and
// the logger it writes to.
type diskFullWorld struct {
	t     *testing.T
	mgr   *session.Manager
	store *session.FileStore
	logs  *logRecords
	sid   string
	err   error
}

func (w *diskFullWorld) reset() {
	w.mgr, w.store, w.logs = newStoreManager(w.t)
	w.sid, w.err = "", nil
}

func (w *diskFullWorld) volumeFull() error {
	w.store.SetFaultForTest(faultOn(diskFullError("mkdir", "sessions/sess_new"), "layout", "save"))
	return nil
}

func (w *diskFullWorld) createSession() error {
	res, err := w.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: w.t.TempDir()})
	w.err = err
	if res != nil {
		w.sid = res.SessionID
	}
	return nil
}

func (w *diskFullWorld) noSessionCreated() error {
	if w.err == nil {
		return fmt.Errorf("the session was created (%s), want a failure", w.sid)
	}
	entries, err := os.ReadDir(w.store.Root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("the sessions folder holds %d entries after the failure, want none", len(entries))
	}
	return nil
}

func (w *diskFullWorld) failureIsDiskFull() error {
	if !platform.IsDiskFull(w.err) {
		return fmt.Errorf("the failure %v does not say the disk is full", w.err)
	}
	return nil
}

func (w *diskFullWorld) logsNoSpaceError() error {
	if recs := w.logs.at(slog.LevelError, "no space left on device"); len(recs) == 0 {
		return fmt.Errorf("no error record names the full disk; log: %+v", w.logs.all())
	}
	return nil
}

func (w *diskFullWorld) sessionCreatedWithRoom() error {
	if err := w.createSession(); err != nil {
		return err
	}
	if w.err != nil {
		return fmt.Errorf("creating the session: %w", w.err)
	}
	return nil
}

func (w *diskFullWorld) volumeRunsOut() error {
	w.store.SetFaultForTest(faultOn(diskFullError("write", "sessions/messages.json.tmp"), "save"))
	return nil
}

func (w *diskFullWorld) switchToPlan() error {
	st := w.mgr.SessionByID(w.sid)
	if st == nil {
		return fmt.Errorf("session %s is not live", w.sid)
	}
	st.SetMode("plan")
	return nil
}

func (w *diskFullWorld) inPlanMode() error {
	if got := w.mgr.SessionByID(w.sid).GetMode(); got != "plan" {
		return fmt.Errorf("mode = %q, want plan", got)
	}
	return nil
}

func (w *diskFullWorld) logsNotSavedError() error {
	if recs := w.logs.at(slog.LevelError, "persist session: no space left on device"); len(recs) == 0 {
		return fmt.Errorf("no error record says the session was not saved for lack of space; log: %+v", w.logs.all())
	}
	return nil
}

func (w *diskFullWorld) errorTellsToFreeSpace() error {
	for _, r := range w.logs.at(slog.LevelError, "persist session") {
		if strings.Contains(r.attrs["hint"], "free space") {
			return nil
		}
	}
	return fmt.Errorf("no error record carries a hint to free space; log: %+v", w.logs.all())
}

func TestDiskFullFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "disk-full",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &diskFullWorld{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return ctx, nil
			})
			sc.Step(`^the volume that holds the sessions folder has no room left$`, w.volumeFull)
			sc.Step(`^a client creates a session$`, w.createSession)
			sc.Step(`^no session is created$`, w.noSessionCreated)
			sc.Step(`^the failure says the disk is full$`, w.failureIsDiskFull)
			sc.Step(`^the log carries an error saying there is no space left on the device$`, w.logsNoSpaceError)
			sc.Step(`^a session was created while the volume had room$`, w.sessionCreatedWithRoom)
			sc.Step(`^the volume then runs out of room$`, w.volumeRunsOut)
			sc.Step(`^the session is switched to plan mode$`, w.switchToPlan)
			sc.Step(`^the session is in plan mode$`, w.inPlanMode)
			sc.Step(`^the log carries an error saying the session was not saved for lack of space$`, w.logsNotSavedError)
			sc.Step(`^the error tells the operator to free disk space$`, w.errorTellsToFreeSpace)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/disk_full.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("disk full feature failed")
	}
}
