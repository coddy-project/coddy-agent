package session

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// StorageEvent says the manager's record of failing saves changed in a way a
// connected client should hear of at once: a write failed on a full disk, or
// the failure was cleared. It names no figures; a client reads them from GET
// /coddy/info, the same answer a poll gets.
type StorageEvent struct {
	// WriteFailing is true when a write has just failed on a full disk and
	// false when the failure has just been cleared.
	WriteFailing bool
	At           time.Time
}

const (
	// writeProbeEvery bounds how often StorageStatus tries a real write while
	// a failure is on record, so a page polling GET /coddy/info cannot turn
	// the check into a stream of temporary files.
	writeProbeEvery = 10 * time.Second
	// writeProbeBytes is the size of the file a write probe stores: a few
	// blocks, enough for the filesystem to hand out a real extent.
	writeProbeBytes = 16 << 10
	// regainedFloor is the least free space a disk must report before a
	// failure on record is cleared: room for the biggest transcript a save is
	// likely to rewrite beside its own copy. It does not follow
	// sessions.min_free_mb, which only says when to warn: a high threshold
	// must not keep a failure on record for a disk that has room again. On a
	// disk smaller than four times that, a quarter of the disk is the bar
	// instead (regainedShare), or a small volume could never recover.
	regainedFloor = 64 << 20
	regainedShare = 4
)

// storageWatch is the manager's record of whether saves are failing for want
// of room, and who is told when that changes.
//
// The record is deliberately not a probe of free space: a quota or a
// filesystem that reports plenty and still refuses the write is a full disk to
// the user, and only a write tells. It is set where a save fails with a code
// platform.IsDiskFull recognises. It is cleared when a write goes through (a
// real save, or the probe write StorageStatus makes) and the disks report room
// again (roomRegained) - a save that squeezed into the last blocks proves
// nothing about the next, bigger one.
type storageWatch struct {
	// failedAt is the UnixNano of the first full-disk failure not yet cleared;
	// 0 when none is on record.
	failedAt atomic.Int64

	mu        sync.Mutex
	seq       int
	observers map[int]func(StorageEvent)
	lastProbe time.Time
	// probe reads the room on a volume; nil means platform.ReadDiskSpace.
	probe StorageProbe
	// writeProbe tries a real write into a folder; nil means probeWrite.
	writeProbe func(dir string) error
}

// AddStorageObserver registers fn for the moments the record changes and
// returns the function that removes it. The HTTP server puts the change on
// GET /coddy/events, so a browser hears that saves have started failing the
// moment they do, without persisting anything from the save that failed.
//
// fn runs on the goroutine that saw the failure or the recovery and MUST NOT
// block.
func (m *Manager) AddStorageObserver(fn func(StorageEvent)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	w := &m.storage
	w.mu.Lock()
	if w.observers == nil {
		w.observers = make(map[int]func(StorageEvent))
	}
	w.seq++
	id := w.seq
	w.observers[id] = fn
	w.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			delete(w.observers, id)
			w.mu.Unlock()
		})
	}
}

// publish tells the observers; it takes a copy of the list so a callback may
// register or remove one.
func (w *storageWatch) publish(ev StorageEvent) {
	w.mu.Lock()
	fns := make([]func(StorageEvent), 0, len(w.observers))
	for _, fn := range w.observers {
		fns = append(fns, fn)
	}
	w.mu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}

// NoteStorageFailure records that a write failed on a full disk. Only the
// first failure after a clean spell is announced: the ones that follow add
// nothing a client does not know. The manager calls it wherever it logs a full
// disk (logDiskFull); the HTTP layer calls it for a request it answers 507,
// so that answer and GET /coddy/info agree whichever path the failure took.
func (m *Manager) NoteStorageFailure() {
	if m == nil {
		return
	}
	now := time.Now()
	if m.storage.failedAt.CompareAndSwap(0, now.UnixNano()) {
		m.storage.publish(StorageEvent{WriteFailing: true, At: now})
	}
}

// clearStorageFailure ends the failure on record and says so.
func (m *Manager) clearStorageFailure() {
	w := &m.storage
	if at := w.failedAt.Load(); at != 0 && w.failedAt.CompareAndSwap(at, 0) {
		w.publish(StorageEvent{WriteFailing: false, At: time.Now()})
	}
}

// noteStoreWrite records that a write to the store went through. It is on the
// path of every save, so the common case - no failure on record - is one
// atomic load; with one on record it clears it once the disks report room.
func (m *Manager) noteStoreWrite() {
	if m.store == nil || m.storage.failedAt.Load() == 0 {
		return
	}
	volumes, _ := m.storageVolumes()
	if roomRegained(volumes) {
		m.clearStorageFailure()
	}
}

// storageVolumes reads the disks the store and the home folder are on, with
// the threshold of the live configuration.
func (m *Manager) storageVolumes() (volumes []StorageVolume, minFree uint64) {
	home := ""
	if cfg := m.activeCfg(); cfg != nil {
		home = cfg.Paths.Home
		minFree = cfg.Sessions.MinFreeBytes()
	}
	m.storage.mu.Lock()
	probe := m.storage.probe
	m.storage.mu.Unlock()
	return ProbeStorage(probe, m.store.Root, home), minFree
}

// roomRegained says the disks that could be read all have regainedFloor free,
// or a quarter of the disk when that is less. With none readable there are no
// numbers to contradict a write that went through, so it says so too.
func roomRegained(volumes []StorageVolume) bool {
	for _, v := range volumes {
		if v.Err == nil && v.Space.FreeBytes < min(regainedFloor, v.Space.TotalBytes/regainedShare) {
			return false
		}
	}
	return true
}

// StorageStatus reads how much room the disks Coddy writes to have left,
// against sessions.min_free_mb of the live configuration. It returns false
// when there is nothing to report: no store, no volume it can read, and no
// failed write on record.
//
// While a failed write is on record and the disks report room again, it tries
// one real write into the sessions folder, at most every writeProbeEvery: a
// disk the operator has cleaned reads as ok without waiting for the next save,
// and a quota that still refuses writes keeps reading as full.
func (m *Manager) StorageStatus() (StorageStatus, bool) {
	if m == nil || m.store == nil || m.store.Root == "" {
		return StorageStatus{}, false
	}
	volumes, minFree := m.storageVolumes()
	if m.storage.failedAt.Load() != 0 && roomRegained(volumes) {
		m.retryFailedWrite()
	}
	return AssessStorage(volumes, minFree, m.storage.failedAt.Load() != 0)
}

// retryFailedWrite clears the record when a real write into the sessions
// folder now succeeds. A failure that is not a full disk (permissions, a path
// that is gone) proves nothing either way and leaves the record alone.
func (m *Manager) retryFailedWrite() {
	w := &m.storage
	w.mu.Lock()
	if time.Since(w.lastProbe) < writeProbeEvery {
		w.mu.Unlock()
		return
	}
	w.lastProbe = time.Now()
	write := w.writeProbe
	w.mu.Unlock()
	if write == nil {
		write = probeWrite
	}
	if err := write(m.store.Root); err == nil {
		m.clearStorageFailure()
	}
}

// probeWrite stores a small file in dir, flushes it to the disk and removes it
// again: the one check that holds for a quota, a reservation and a filesystem
// that reports free space it will not hand out. The flush matters: a
// filesystem that allocates lazily accepts the write and fails at the sync.
func probeWrite(dir string) (err error) {
	f, err := os.CreateTemp(dir, ".coddy-space-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() {
		if rmErr := os.Remove(name); rmErr != nil && err == nil && !os.IsNotExist(rmErr) {
			err = rmErr
		}
	}()
	if _, werr := f.Write(make([]byte, writeProbeBytes)); werr != nil {
		_ = f.Close()
		return werr
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		return serr
	}
	return f.Close()
}

// logIfDiskFull logs err the way a failed save is logged when it is a full
// disk (an error that says so, with a hint, and a record for the clients) and
// does nothing for any other failure, which keeps whatever handling it had. It
// returns err unchanged so a call site can wrap it in place.
func (m *Manager) logIfDiskFull(op string, err error, args ...any) error {
	if platform.IsDiskFull(err) {
		m.logDiskFull(op, err, args...)
	}
	return err
}
