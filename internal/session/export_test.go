package session

import "time"

// Test seams for the session_test package: pause points inside the manager
// and the deletion settle timeout. They exist only in test builds.

// SetSubagentPublishHookForTest runs fn once a child state has been published
// to the live map and before its bundle exists.
func (m *Manager) SetSubagentPublishHookForTest(fn func(*State)) {
	m.testHooks.afterSubagentPublish = fn
}

// SetSubagentPrePublishHookForTest runs fn once a child state is built (a
// resumed child's bundle read) and before it is published to the live map.
func (m *Manager) SetSubagentPrePublishHookForTest(fn func(*State)) {
	m.testHooks.beforeSubagentPublish = fn
}

// SetTurnEntryHookForTest runs fn at the start of turn admission, after the
// caller resolved its state and before anything is registered.
func (m *Manager) SetTurnEntryHookForTest(fn func(sessionID string)) {
	m.testHooks.beforeTurnAdmission = fn
}

// MarkDeletingForTest raises or lowers the deletion mark of ids directly.
func (m *Manager) MarkDeletingForTest(ids []string, on bool) { m.markDeleting(ids, on) }

// IsDeletingForTest reports whether the deletion mark of id is raised.
func (m *Manager) IsDeletingForTest(id string) bool { return m.isDeleting(id) }

// SetTurnAdmissionHookForTest runs fn after a turn installed its cancel
// function and before it rechecks the deleting mark.
func (m *Manager) SetTurnAdmissionHookForTest(fn func(sessionID string)) {
	m.testHooks.beforeTurnAdmissionRecheck = fn
}

// SetTreeScanHookForTest runs fn once DeleteSessionTree took its first
// snapshot of the tree and before it marks anything.
func (m *Manager) SetTreeScanHookForTest(fn func(rootID string)) {
	m.testHooks.afterTreeScan = fn
}

// SetDeleteSettleTimeoutForTest overrides how long DeleteSessionTree waits
// for a cancelled turn, returning a restore function.
func SetDeleteSettleTimeoutForTest(d time.Duration) (restore func()) {
	prev := deleteSettleTimeout
	deleteSettleTimeout = d
	return func() { deleteSettleTimeout = prev }
}

// SetMCPConnectTimeoutForTest shortens the per-server MCP connect budget.
func (m *Manager) SetMCPConnectTimeoutForTest(d time.Duration) {
	m.mcpConnectTimeout = d
	m.mcpPool.SetDialTimeout(d)
}

// SetMCPStopDelayForTest sets how long a shared MCP server nothing holds runs
// on before the pool stops it.
func (m *Manager) SetMCPStopDelayForTest(d time.Duration) { m.mcpPool.SetStopDelay(d) }

// SetFaultForTest makes the store fail the operations it names. fn is asked
// by EnsureLayout and EnsureChildLayout ("layout", once the bundle folder
// exists and before its files are written, as a failure partway through the
// layout looks) and by
// Save ("save", before it writes), and a non-nil answer is the error they
// return; nil restores the real filesystem. A test passes an error shaped
// like the disk-full one the operating system returns (a wrapped
// *os.PathError around ENOSPC).
func (f *FileStore) SetFaultForTest(fn func(op string) error) {
	if fn == nil {
		f.testFault.Store(nil)
		return
	}
	f.testFault.Store(&fn)
}

// SetStorageProbeForTest replaces the call that reads the room on a volume
// (platform.ReadDiskSpace); nil restores it.
func (m *Manager) SetStorageProbeForTest(probe StorageProbe) {
	m.storage.mu.Lock()
	m.storage.probe = probe
	m.storage.mu.Unlock()
}

// SetStorageWriteProbeForTest replaces the real write StorageStatus tries
// while a failure is on record; nil restores it. The clock that rate-limits
// the write is rewound, so the next StorageStatus tries it.
func (m *Manager) SetStorageWriteProbeForTest(write func(dir string) error) {
	m.storage.mu.Lock()
	m.storage.writeProbe = write
	m.storage.lastProbe = time.Time{}
	m.storage.mu.Unlock()
}

// RewindStorageWriteProbeClockForTest lets the next StorageStatus try its write
// again at once.
func (m *Manager) RewindStorageWriteProbeClockForTest() {
	m.storage.mu.Lock()
	m.storage.lastProbe = time.Time{}
	m.storage.mu.Unlock()
}

// StorageFailureOnRecordForTest reports whether a full-disk failure is on
// record.
func (m *Manager) StorageFailureOnRecordForTest() bool { return m.storage.failedAt.Load() != 0 }

// NoteStoreWriteForTest runs what every successful save of the store runs.
func (m *Manager) NoteStoreWriteForTest() { m.noteStoreWrite() }
