package session

import "github.com/EvilFreelancer/coddy-agent/internal/platform"

// DiskFullMessage is what a client is told when a session cannot be created
// because the volume that holds the sessions folder has no room left. It
// names the cause in the words Unix uses ("no space left on device"), the
// same on every platform, because the platform's own text differs (Windows
// words it another way) and a client that looks for the cause should not
// have to know that.
const DiskFullMessage = "no space left on device: the disk that stores Coddy sessions is full"

// diskFullHint is what the log tells an operator beside a full-disk error:
// what to do, and what the failure costs.
const diskFullHint = "free space on the volume that holds the sessions folder (<home>/sessions, or sessions.dir); " +
	"what changed since the last successful save exists only in memory and is lost if the process stops"

// logDiskFull reports a write to the session store that failed because the
// volume is full. It is an error and not a warning: every turn that runs
// while it lasts is missing from the transcript after a restart, and the
// operator has no other sign of it.
func (m *Manager) logDiskFull(op string, err error, args ...any) {
	m.log.Error(op+": no space left on device", append(args, "error", err, "hint", diskFullHint)...)
}

// logStoreFailure reports a failed write to the session store: a full disk
// as an error that says so (logDiskFull), any other failure as the warning it
// has always been.
func (m *Manager) logStoreFailure(op string, err error, args ...any) {
	if platform.IsDiskFull(err) {
		m.logDiskFull(op, err, args...)
		return
	}
	m.log.Warn(op, append(args, "error", err)...)
}
