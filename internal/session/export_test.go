package session

import "time"

// Test seams for the session_test package: pause points inside the manager
// and the deletion settle timeout. They exist only in test builds.

// SetSubagentPublishHookForTest runs fn once a child state has been published
// to the live map and before its bundle exists.
func (m *Manager) SetSubagentPublishHookForTest(fn func(*State)) {
	m.testHooks.afterSubagentPublish = fn
}

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
