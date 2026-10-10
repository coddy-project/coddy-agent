package session_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Godog harness for features/disk_space.feature: a manager over a real
// FileStore whose disk reads as the scenario says (a stand-in for the call
// that asks the operating system) and whose writes are made to fail the way a
// full disk fails them.
type diskSpaceWorld struct {
	t      *testing.T
	mgr    *session.Manager
	store  *session.FileStore
	vols   fakeVolumes
	sid    string
	events storageEvents
	remove func()
}

func (w *diskSpaceWorld) reset() {
	if w.remove != nil {
		w.remove()
	}
	w.mgr, w.store, w.vols = newMeasuredManager(w.t, 0, 1<<40)
	w.sid = ""
	w.events = storageEvents{}
	w.remove = w.mgr.AddStorageObserver(w.events.add)
}

func (w *diskSpaceWorld) diskHas(freeMB, thresholdMB int) error {
	for p, ds := range w.vols {
		if ds.Volume == "dev:sessions" {
			ds.FreeBytes = uint64(freeMB) << 20
			w.vols[p] = ds
		}
	}
	w.mgr.Cfg().Sessions.MinFreeMB = &thresholdMB
	return nil
}

func (w *diskSpaceWorld) stateIs(want string) error {
	st, ok := w.mgr.StorageStatus()
	if !ok {
		return fmt.Errorf("the manager has nothing to report about storage")
	}
	if string(st.State) != want {
		return fmt.Errorf("storage state = %q, want %q (%+v)", st.State, want, st)
	}
	return nil
}

func (w *diskSpaceWorld) reportSaysFree(mb int) error {
	st, _ := w.mgr.StorageStatus()
	if st.FreeBytes != uint64(mb)<<20 {
		return fmt.Errorf("the report says %d bytes free, want %d MB", st.FreeBytes, mb)
	}
	return nil
}

func (w *diskSpaceWorld) sessionOpen() error {
	res, err := w.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: w.t.TempDir()})
	if err != nil {
		return err
	}
	w.sid = res.SessionID
	return nil
}

func (w *diskSpaceWorld) diskRefusesWrites() error {
	w.store.SetFaultForTest(faultOn(diskFullError("write", "messages.json.tmp"), "save"))
	w.mgr.SetStorageWriteProbeForTest(func(string) error { return diskFullError("write", "probe") })
	return nil
}

func (w *diskSpaceWorld) diskAcceptsWrites() error {
	w.store.SetFaultForTest(nil)
	w.mgr.SetStorageWriteProbeForTest(nil)
	return nil
}

func (w *diskSpaceWorld) switchMode(mode string) error {
	st := w.mgr.SessionByID(w.sid)
	if st == nil {
		return fmt.Errorf("session %s is not live", w.sid)
	}
	st.SetMode(mode)
	return nil
}

func (w *diskSpaceWorld) toldOnceFailing() error {
	evs := w.events.all()
	if len(evs) != 1 || !evs[0].WriteFailing {
		return fmt.Errorf("clients heard %+v, want exactly one announcement that saves are failing", evs)
	}
	return nil
}

func (w *diskSpaceWorld) toldOver() error {
	evs := w.events.all()
	if len(evs) != 2 || evs[1].WriteFailing {
		return fmt.Errorf("clients heard %+v, want the failure and then its end", evs)
	}
	return nil
}

func TestDiskSpaceFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "disk-space",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &diskSpaceWorld{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if w.remove != nil {
					w.remove()
					w.remove = nil
				}
				return ctx, nil
			})
			sc.Step(`^the disk that holds the sessions has (\d+) MB free and the warning threshold is (\d+) MB$`, func(free, min string) error {
				f, _ := strconv.Atoi(free)
				m, _ := strconv.Atoi(min)
				return w.diskHas(f, m)
			})
			sc.Step(`^the storage state is "([^"]*)"$`, w.stateIs)
			sc.Step(`^the storage report says (\d+) MB are free$`, func(mb string) error {
				n, _ := strconv.Atoi(mb)
				return w.reportSaysFree(n)
			})
			sc.Step(`^a session is open$`, w.sessionOpen)
			sc.Step(`^the disk refuses writes$`, w.diskRefusesWrites)
			sc.Step(`^the disk accepts writes again$`, w.diskAcceptsWrites)
			sc.Step(`^the session is switched to (\w+) mode$`, w.switchMode)
			sc.Step(`^clients were told once that saves are failing$`, w.toldOnceFailing)
			sc.Step(`^clients were told that the failure is over$`, w.toldOver)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/disk_space.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("disk space feature failed")
	}
}
