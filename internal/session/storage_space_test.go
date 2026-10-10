package session_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const mib = 1 << 20

// fakeVolumes answers a storage probe from a table keyed by the folder asked
// about, so a test says how much room each place has without a full disk.
type fakeVolumes map[string]platform.DiskSpace

func (f fakeVolumes) probe(path string) (platform.DiskSpace, error) {
	if ds, ok := f[path]; ok {
		return ds, nil
	}
	return platform.DiskSpace{}, errors.New("not readable")
}

func space(volume string, free, total uint64) platform.DiskSpace {
	return platform.DiskSpace{Volume: volume, FreeBytes: free, TotalBytes: total}
}

// The sessions folder is read first. The home folder is read too, and kept
// only when it is on another volume: one disk is one place to run out of room.
func TestProbeStorageKeepsOneEntryPerVolume(t *testing.T) {
	shared := fakeVolumes{"/s": space("dev:1", 10*mib, 100*mib), "/h": space("dev:1", 10*mib, 100*mib)}
	if got := session.ProbeStorage(shared.probe, "/s", "/h"); len(got) != 1 || got[0].Role != session.StorageVolumeSessions {
		t.Fatalf("a shared volume is read as %+v, want the sessions entry alone", got)
	}

	apart := fakeVolumes{"/s": space("dev:1", 10*mib, 100*mib), "/h": space("dev:2", 5*mib, 50*mib)}
	got := session.ProbeStorage(apart.probe, "/s", "/h")
	if len(got) != 2 || got[0].Role != session.StorageVolumeSessions || got[1].Role != session.StorageVolumeHome {
		t.Fatalf("two volumes are read as %+v, want sessions then home", got)
	}
	if got[1].Path != "/h" || got[1].Space.FreeBytes != 5*mib {
		t.Fatalf("the home entry carries %+v", got[1])
	}

	if got := session.ProbeStorage(apart.probe, "/s", ""); len(got) != 1 {
		t.Fatalf("no home folder is read as %+v, want the sessions entry alone", got)
	}
}

// A volume that cannot be read stays in the answer with the reason, so a
// report can say so; it is the caller's call whether that is a problem.
func TestProbeStorageReportsAVolumeItCannotRead(t *testing.T) {
	only := fakeVolumes{"/s": space("dev:1", 10*mib, 100*mib)}
	got := session.ProbeStorage(only.probe, "/s", "/h")
	if len(got) != 2 || got[0].Err != nil || got[1].Err == nil || got[1].Role != session.StorageVolumeHome {
		t.Fatalf("got %+v, want the sessions volume read and the home volume with an error", got)
	}
}

// What a volume's numbers say against the threshold. A disk with no byte free
// is full, whatever the threshold; with the warning off (0) the numbers say
// nothing at all.
func TestStorageVolumeState(t *testing.T) {
	const min = 512 * mib
	cases := []struct {
		name string
		v    session.StorageVolume
		min  uint64
		want session.StorageState
	}{
		{"plenty", session.StorageVolume{Space: space("a", 2048*mib, 4096*mib)}, min, session.StorageOK},
		{"exactly the threshold", session.StorageVolume{Space: space("a", min, 4096*mib)}, min, session.StorageOK},
		{"one byte under", session.StorageVolume{Space: space("a", min-1, 4096*mib)}, min, session.StorageLow},
		{"almost nothing", session.StorageVolume{Space: space("a", 4096, 4096*mib)}, min, session.StorageLow},
		{"nothing", session.StorageVolume{Space: space("a", 0, 4096*mib)}, min, session.StorageFull},
		{"warning off, little left", session.StorageVolume{Space: space("a", 4096, 4096*mib)}, 0, ""},
		{"warning off, nothing left", session.StorageVolume{Space: space("a", 0, 4096*mib)}, 0, ""},
		{"unreadable", session.StorageVolume{Err: errors.New("no")}, min, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.State(tc.min); got != tc.want {
				t.Fatalf("State(%d) = %q, want %q", tc.min, got, tc.want)
			}
		})
	}
}

// The status takes the worst volume (the one with least room among equals),
// reports that volume's numbers, and calls the state full when a write has
// failed whatever the numbers say.
func TestAssessStorage(t *testing.T) {
	const min = 512 * mib
	sessions := session.StorageVolume{Role: session.StorageVolumeSessions, Space: space("a", 2048*mib, 4096*mib)}
	home := session.StorageVolume{Role: session.StorageVolumeHome, Space: space("b", 100*mib, 200*mib)}
	unreadable := session.StorageVolume{Role: session.StorageVolumeHome, Err: errors.New("no")}

	t.Run("worst volume decides", func(t *testing.T) {
		st, ok := session.AssessStorage([]session.StorageVolume{sessions, home}, min, false)
		if !ok || st.State != session.StorageLow || st.Volume != session.StorageVolumeHome {
			t.Fatalf("got %+v ok=%v, want low on the home volume", st, ok)
		}
		if !st.Measured || st.FreeBytes != 100*mib || st.TotalBytes != 200*mib || st.MinFreeBytes != min {
			t.Fatalf("figures are not the deciding volume's: %+v", st)
		}
	})
	t.Run("healthy", func(t *testing.T) {
		st, ok := session.AssessStorage([]session.StorageVolume{sessions}, min, false)
		if !ok || st.State != session.StorageOK || st.Volume != session.StorageVolumeSessions {
			t.Fatalf("got %+v ok=%v, want ok on the sessions volume", st, ok)
		}
	})
	t.Run("least room among equals", func(t *testing.T) {
		a := session.StorageVolume{Role: session.StorageVolumeSessions, Space: space("a", 900*mib, 4096*mib)}
		b := session.StorageVolume{Role: session.StorageVolumeHome, Space: space("b", 700*mib, 4096*mib)}
		st, _ := session.AssessStorage([]session.StorageVolume{a, b}, min, false)
		if st.Volume != session.StorageVolumeHome {
			t.Fatalf("both are ok; the one with less room (home) should be named, got %q", st.Volume)
		}
	})
	t.Run("warning off reports the numbers as ok", func(t *testing.T) {
		st, ok := session.AssessStorage([]session.StorageVolume{home}, 0, false)
		if !ok || st.State != session.StorageOK || st.MinFreeBytes != 0 || st.FreeBytes != 100*mib {
			t.Fatalf("got %+v ok=%v, want ok with the figures and no threshold", st, ok)
		}
	})
	t.Run("a failed write is full whatever the numbers say", func(t *testing.T) {
		st, ok := session.AssessStorage([]session.StorageVolume{sessions}, min, true)
		if !ok || st.State != session.StorageFull || st.FreeBytes != 2048*mib {
			t.Fatalf("got %+v ok=%v, want full with the figures kept", st, ok)
		}
		off, _ := session.AssessStorage([]session.StorageVolume{sessions}, 0, true)
		if off.State != session.StorageFull {
			t.Fatalf("the warning being off must not hide a failed write: %+v", off)
		}
	})
	t.Run("a failed write with nothing readable", func(t *testing.T) {
		st, ok := session.AssessStorage([]session.StorageVolume{unreadable}, min, true)
		if !ok || st.State != session.StorageFull || st.Measured {
			t.Fatalf("got %+v ok=%v, want full without figures", st, ok)
		}
	})
	t.Run("nothing to say", func(t *testing.T) {
		if st, ok := session.AssessStorage([]session.StorageVolume{unreadable}, min, false); ok {
			t.Fatalf("got %+v, want nothing to report", st)
		}
		if _, ok := session.AssessStorage(nil, min, false); ok {
			t.Fatal("no volumes at all must report nothing")
		}
	})
}

// newMeasuredManager is a manager over a store in a temporary folder whose
// disks read as the table says: the sessions folder on one volume, the home
// folder on another. The threshold is the configuration's, 512 MB unless the
// test sets it.
func newMeasuredManager(t *testing.T, sessionsFree, homeFree uint64) (*session.Manager, *session.FileStore, fakeVolumes) {
	t.Helper()
	m, store, _ := newStoreManager(t)
	home := filepath.Join(filepath.Dir(store.Root), "home")
	cfg := m.Cfg()
	cfg.Paths.Home = home
	vols := fakeVolumes{
		store.Root: space("dev:sessions", sessionsFree, 100*1024*mib),
		home:       space("dev:home", homeFree, 100*1024*mib),
	}
	m.SetStorageProbeForTest(vols.probe)
	return m, store, vols
}

func TestManagerStorageStatusFollowsTheRoomLeft(t *testing.T) {
	m, _, vols := newMeasuredManager(t, 4096*mib, 4096*mib)

	st, ok := m.StorageStatus()
	if !ok || st.State != session.StorageOK || st.MinFreeBytes != 512*mib {
		t.Fatalf("healthy disks read as %+v ok=%v", st, ok)
	}

	for p, ds := range vols {
		if ds.Volume == "dev:sessions" {
			ds.FreeBytes = 300 * mib
			vols[p] = ds
		}
	}
	st, _ = m.StorageStatus()
	if st.State != session.StorageLow || st.Volume != session.StorageVolumeSessions || st.FreeBytes != 300*mib {
		t.Fatalf("a sessions disk under the threshold reads as %+v", st)
	}

	// The threshold is read from the live configuration each time.
	zero := 0
	m.Cfg().Sessions.MinFreeMB = &zero
	if st, _ = m.StorageStatus(); st.State != session.StorageOK || st.MinFreeBytes != 0 {
		t.Fatalf("with the warning off the same disk reads as %+v", st)
	}
	mb := 200
	m.Cfg().Sessions.MinFreeMB = &mb
	if st, _ = m.StorageStatus(); st.State != session.StorageOK || st.MinFreeBytes != 200*mib {
		t.Fatalf("300 MB free over a 200 MB threshold reads as %+v, want ok", st)
	}
	mb = 400
	if st, _ = m.StorageStatus(); st.State != session.StorageLow || st.MinFreeBytes != 400*mib {
		t.Fatalf("300 MB free under a 400 MB threshold reads as %+v, want low", st)
	}
}

// With no disk the probe can read and no failed write, there is nothing to
// say, and a manager with no store has no disk to ask about.
func TestManagerStorageStatusHasNothingToSayWithoutAReadableDisk(t *testing.T) {
	m, _, _ := newMeasuredManager(t, 4096*mib, 4096*mib)
	m.SetStorageProbeForTest(func(string) (platform.DiskSpace, error) { return platform.DiskSpace{}, errors.New("no") })
	if st, ok := m.StorageStatus(); ok {
		t.Fatalf("unreadable disks reported %+v", st)
	}

	bare := session.NewManager(testConfig(), noopSender{}, noopRunner, slog.Default(), t.TempDir(), nil)
	if st, ok := bare.StorageStatus(); ok {
		t.Fatalf("a manager without a store reported %+v", st)
	}
}

// storageEvents collects what the manager announces.
type storageEvents struct {
	mu  sync.Mutex
	evs []session.StorageEvent
}

func (s *storageEvents) add(ev session.StorageEvent) {
	s.mu.Lock()
	s.evs = append(s.evs, ev)
	s.mu.Unlock()
}

func (s *storageEvents) all() []session.StorageEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]session.StorageEvent(nil), s.evs...)
}

// A save that fails on a full disk is a full disk to every client at once: the
// status says so whatever the numbers (here the disk reports plenty and a
// quota refuses the write, so a probe write fails too), the observers hear it
// once, and the failure repeating adds no second announcement. The notice is
// not written into the session, which is the thing that cannot be saved.
func TestAFullDiskFailureIsRecordedAndAnnouncedOnce(t *testing.T) {
	m, store, _ := newMeasuredManager(t, 4096*mib, 4096*mib)
	m.SetStorageWriteProbeForTest(func(string) error { return diskFullError("write", "probe") })
	var events storageEvents
	defer m.AddStorageObserver(events.add)()

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	store.SetFaultForTest(faultOn(diskFullError("write", "messages.json.tmp"), "save"))
	st.SetMode("plan")
	st.SetMode("ask")
	st.SetMode("agent")

	status, ok := m.StorageStatus()
	if !ok || status.State != session.StorageFull {
		t.Fatalf("after failed saves the status is %+v ok=%v, want full", status, ok)
	}
	if status.FreeBytes != 4096*mib {
		t.Fatalf("the figures are still the disk's: %+v", status)
	}
	got := events.all()
	if len(got) != 1 || !got[0].WriteFailing || got[0].At.IsZero() {
		t.Fatalf("observers heard %+v, want one announcement that writes are failing", got)
	}
}

// A failure that is not a full disk says nothing about room.
func TestAnotherFailureDoesNotRecordAFullDisk(t *testing.T) {
	m, store, _ := newMeasuredManager(t, 4096*mib, 4096*mib)
	var events storageEvents
	defer m.AddStorageObserver(events.add)()
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store.SetFaultForTest(faultOn(&os.PathError{Op: "write", Path: "/x", Err: syscall.EACCES}, "save"))
	m.SessionByID(res.SessionID).SetMode("plan")

	if status, _ := m.StorageStatus(); status.State != session.StorageOK {
		t.Fatalf("a permission failure reads as %+v", status)
	}
	if m.StorageFailureOnRecordForTest() || len(events.all()) != 0 {
		t.Fatalf("a permission failure was recorded or announced: %+v", events.all())
	}
}

// The failure is cleared when a save goes through and the disks report room
// again, and the observers hear that too. A save that squeezed through the
// last blocks of a disk that still reports almost nothing does not clear it:
// the next, bigger one would fail again.
func TestTheFailureClearsOnlyWhenARealWriteWorksAndRoomIsBack(t *testing.T) {
	m, store, vols := newMeasuredManager(t, 20*mib, 4096*mib)
	var events storageEvents
	defer m.AddStorageObserver(events.add)()
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	store.SetFaultForTest(faultOn(diskFullError("write", "messages.json.tmp"), "save"))
	st.SetMode("plan")
	store.SetFaultForTest(nil)

	// A save works, but the disk still reports 20 MB.
	st.SetMode("ask")
	if status, _ := m.StorageStatus(); status.State != session.StorageFull {
		t.Fatalf("20 MB free is not room enough to call the failure over: %+v", status)
	}

	// Space is freed; the next save is what ends it.
	for p, ds := range vols {
		if ds.Volume == "dev:sessions" {
			ds.FreeBytes = 2048 * mib
			vols[p] = ds
		}
	}
	m.SetStorageWriteProbeForTest(func(string) error { return errors.New("probe must not be needed: a real save clears it") })
	st.SetMode("agent")

	if m.StorageFailureOnRecordForTest() {
		t.Fatal("a save that went through on a disk with room did not clear the failure")
	}
	if status, _ := m.StorageStatus(); status.State != session.StorageOK {
		t.Fatalf("after recovery the status is %+v, want ok", status)
	}
	got := events.all()
	if len(got) != 2 || !got[0].WriteFailing || got[1].WriteFailing {
		t.Fatalf("observers heard %+v, want the failure and then its end", got)
	}
}

// An operator who frees space and does nothing else is not left looking at a
// full disk: reading the status tries one real write once the disks report
// room, and the failure ends if it works. A quota that still refuses writes
// keeps it, and the write is not tried more often than the clock allows.
func TestStatusRetriesAWriteOnceRoomIsBack(t *testing.T) {
	m, _, vols := newMeasuredManager(t, 20*mib, 4096*mib)
	var events storageEvents
	defer m.AddStorageObserver(events.add)()
	m.NoteStorageFailure()

	var attempts atomic.Int32
	var writeErr atomic.Pointer[error]
	refuse := error(diskFullError("write", "probe"))
	writeErr.Store(&refuse)
	m.SetStorageWriteProbeForTest(func(dir string) error {
		attempts.Add(1)
		if dir == "" {
			t.Error("the probe write was not given the sessions folder")
		}
		return *writeErr.Load()
	})

	// Almost no room: no attempt at all.
	if status, _ := m.StorageStatus(); status.State != session.StorageFull || attempts.Load() != 0 {
		t.Fatalf("20 MB free: status %+v after %d attempts, want full and none", status, attempts.Load())
	}

	for p, ds := range vols {
		if ds.Volume == "dev:sessions" {
			ds.FreeBytes = 2048 * mib
			vols[p] = ds
		}
	}
	// Room is back but the write is refused (a quota): still full.
	if status, _ := m.StorageStatus(); status.State != session.StorageFull || attempts.Load() != 1 {
		t.Fatalf("a refused write: status %+v after %d attempts, want full and one", status, attempts.Load())
	}
	// Asked again at once, it does not try again.
	m.StorageStatus()
	if attempts.Load() != 1 {
		t.Fatalf("the write was retried inside its interval: %d attempts", attempts.Load())
	}

	// Space freed for real: the write works and the failure ends.
	var ok error
	writeErr.Store(&ok)
	m.RewindStorageWriteProbeClockForTest()
	if status, _ := m.StorageStatus(); status.State != session.StorageOK {
		t.Fatalf("after a working write the status is %+v, want ok", status)
	}
	if got := events.all(); len(got) != 2 || got[1].WriteFailing {
		t.Fatalf("observers heard %+v, want the failure and then its end", got)
	}
}

// The probe write is a real write of a small file that is gone again, in the
// folder the sessions live in.
func TestProbeWriteLeavesNothingBehind(t *testing.T) {
	m, store, _ := newMeasuredManager(t, 4096*mib, 4096*mib)
	m.NoteStorageFailure()
	if status, _ := m.StorageStatus(); status.State != session.StorageOK {
		t.Fatalf("a disk with room and working writes reads as %+v after a failure on record", status)
	}
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe left %d entries in the sessions folder", len(entries))
	}
}

// The bar for room being back is 64 MB, or a quarter of the disk when the disk
// is smaller than four times that: a 16 MB volume must be able to recover.
func TestASmallDiskRecoversWithAQuarterOfItsSizeFree(t *testing.T) {
	m, store, vols := newMeasuredManager(t, 4096*mib, 4096*mib)
	setSessionsDisk := func(free, total uint64) {
		vols[store.Root] = space("dev:sessions", free, total)
	}
	setSessionsDisk(3*mib, 16*mib)
	var attempts atomic.Int32
	m.SetStorageWriteProbeForTest(func(string) error { attempts.Add(1); return nil })
	m.NoteStorageFailure()

	if status, _ := m.StorageStatus(); status.State != session.StorageFull || attempts.Load() != 0 {
		t.Fatalf("3 MB free of 16 MB: status %+v after %d attempts, want full and none", status, attempts.Load())
	}
	setSessionsDisk(5*mib, 16*mib)
	if status, _ := m.StorageStatus(); status.State != session.StorageLow || attempts.Load() != 1 {
		t.Fatalf("5 MB free of 16 MB: status %+v after %d attempts, want the failure cleared (low by the numbers) after one write", status, attempts.Load())
	}
}

// Every other place the manager writes a session through answers a full disk
// the way the first one did: an error in the log that names it, with the hint,
// and the failure on record for the clients. The error the caller gets keeps
// its cause. Failures that are not a full disk stay as they were.
func TestEveryStorePathLogsAFullDisk(t *testing.T) {
	t.Run("deferred bundle", func(t *testing.T) {
		m, store, logs := newStoreManager(t)
		m.SetDeferNewSessionBundle(true)
		res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		store.SetFaultForTest(faultOn(diskFullError("mkdir", "sessions/sess_x/todos"), "layout"))
		_, err = m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: res.SessionID,
			Prompt:    []acp.ContentBlock{acp.ContentBlock{Type: "text", Text: "hello"}},
		})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("the first prompt returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "create deferred session", res.SessionID)
	})
	t.Run("deferred bundle save", func(t *testing.T) {
		m, store, logs := newStoreManager(t)
		m.SetDeferNewSessionBundle(true)
		res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		store.SetFaultForTest(faultOn(diskFullError("write", "session.json"), "save"))
		_, err = m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: res.SessionID,
			Prompt:    []acp.ContentBlock{acp.ContentBlock{Type: "text", Text: "hello"}},
		})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("the first prompt returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "save deferred session", res.SessionID)
	})
	t.Run("scheduler job session layout", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		logs := &logRecords{}
		m = managerLoggingTo(t, m, store, root, logs)
		store.SetFaultForTest(faultOn(diskFullError("mkdir", "sessions/sess_job"), "layout"))
		id := session.NewSessionID()
		_, err := m.EnsureSchedulerJobSession(context.Background(), session.SchedulerJobSessionSpec{ID: id, JobID: "nightly", CWD: root})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("EnsureSchedulerJobSession returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "create scheduler job session", id)
	})
	t.Run("scheduler job session save", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		logs := &logRecords{}
		m = managerLoggingTo(t, m, store, root, logs)
		store.SetFaultForTest(faultOn(diskFullError("write", "session.json"), "save"))
		id := session.NewSessionID()
		_, err := m.EnsureSchedulerJobSession(context.Background(), session.SchedulerJobSessionSpec{ID: id, JobID: "nightly", CWD: root})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("EnsureSchedulerJobSession returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "save scheduler job session", id)
	})
	t.Run("subagent session layout", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		logs := &logRecords{}
		m = managerLoggingTo(t, m, store, root, logs)
		parent := newParent(t, m, root)
		store.SetFaultForTest(faultOn(diskFullError("mkdir", "sessions/sess_parent/subagents/sess_child"), "layout"))
		childID := session.NewSessionID()
		_, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("CreateSubagentSession returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "create subagent session", childID)
	})
	t.Run("subagent session save", func(t *testing.T) {
		m, store, root := newSubagentTestManager(t)
		logs := &logRecords{}
		m = managerLoggingTo(t, m, store, root, logs)
		parent := newParent(t, m, root)
		store.SetFaultForTest(faultOn(diskFullError("write", "session.json"), "save"))
		childID := session.NewSessionID()
		_, err := m.CreateSubagentSession(context.Background(), session.SubagentSpec{
			ID: childID, ParentSessionID: parent.ID, Name: "reviewer", TaskID: "bg_1", CWD: root,
		})
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("CreateSubagentSession returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "save subagent session", childID)
	})
	t.Run("rewound history", func(t *testing.T) {
		m, store, logs := newStoreManager(t)
		res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		st := m.SessionByID(res.SessionID)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "first"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "one"})
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "second"})
		store.SetFaultForTest(faultOn(diskFullError("write", "messages.json"), "save"))
		_, err = m.RewindSession(res.SessionID, 1)
		if err == nil || !platform.IsDiskFull(err) {
			t.Fatalf("RewindSession returned %v, want the full-disk cause kept", err)
		}
		expectDiskFullRecord(t, m, logs, "persist rewound history", res.SessionID)
	})
}

// managerLoggingTo builds a manager over the same store and home as m whose log
// goes to logs, so a test that needs a helper's wiring (newSubagentTestManager)
// can still read what the manager logged.
func managerLoggingTo(t *testing.T, m *session.Manager, store *session.FileStore, root string, logs *logRecords) *session.Manager {
	t.Helper()
	cfg := m.Cfg()
	return session.NewManager(cfg, noopSender{}, noopRunner, slog.New(logs.handler()), root, store)
}

// expectDiskFullRecord asserts one error record named op, for id, carrying the
// cause and the hint, and the failure on the manager's record.
func expectDiskFullRecord(t *testing.T, m *session.Manager, logs *logRecords, op, id string) {
	t.Helper()
	recs := logs.at(slog.LevelError, op+": no space left on device")
	if len(recs) != 1 {
		t.Fatalf("error records for %q = %d, want 1; log: %+v", op, len(recs), logs.all())
	}
	r := recs[0]
	if r.attrs["id"] != id || r.attrs["error"] == "" || r.attrs["hint"] == "" {
		t.Fatalf("the record lacks the id (%q wanted), the error or the hint: %+v", id, r)
	}
	if !m.StorageFailureOnRecordForTest() {
		t.Fatal("the full-disk failure is not on the manager's record")
	}
}
