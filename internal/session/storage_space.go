package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// StorageState is how much room the disks Coddy writes to have left.
type StorageState string

const (
	// StorageOK: every disk has at least sessions.min_free_mb free, or the
	// warning is off and no save has failed.
	StorageOK StorageState = "ok"
	// StorageLow: a disk has less than sessions.min_free_mb free. Saves still
	// work; the next long transcript or a pile of attachments may not.
	StorageLow StorageState = "low"
	// StorageFull: saves are failing. Either a write has failed on a full disk
	// and none has succeeded since, or the disk reports no free bytes at all.
	StorageFull StorageState = "full"
)

// The two disks the state of storage is read from.
const (
	// StorageVolumeSessions is the volume holding the sessions folder.
	StorageVolumeSessions = "sessions"
	// StorageVolumeHome is the volume holding CODDY_HOME (the config, the log,
	// the state files), read only when it is not the sessions folder's.
	StorageVolumeHome = "home"
)

// rank orders the states from the best to the worst.
func (s StorageState) rank() int {
	switch s {
	case StorageFull:
		return 2
	case StorageLow:
		return 1
	}
	return 0
}

// StorageProbe reads the room on the volume under a path
// (platform.ReadDiskSpace, which tests replace).
type StorageProbe func(path string) (platform.DiskSpace, error)

// StorageVolume is one disk Coddy writes to and what was read from it.
type StorageVolume struct {
	// Role is StorageVolumeSessions or StorageVolumeHome.
	Role string
	// Path is the folder the volume was read for.
	Path string
	// Space is what the probe returned; meaningless when Err is set.
	Space platform.DiskSpace
	// Err is why the probe could not read the volume; nil when it did.
	Err error
}

// State classifies the volume against the threshold minFree, in bytes. A
// volume that could not be read has no state (""). A disk with no byte free is
// full, whatever the threshold says, the warning being off included: that is no
// matter of taste, nothing can be saved on it. With the warning off (0) a disk
// that has any room left has no state either, and the numbers are only reported.
func (v StorageVolume) State(minFree uint64) StorageState {
	switch {
	case v.Err != nil:
		return ""
	case v.Space.FreeBytes == 0:
		return StorageFull
	case minFree == 0:
		return ""
	case v.Space.FreeBytes < minFree:
		return StorageLow
	}
	return StorageOK
}

// ProbeStorage reads the volume holding the sessions folder and, when it is
// another volume, the one holding home. The sessions volume comes first. A
// folder that does not exist yet is read through its nearest existing
// ancestor, which is the volume it will be made on.
func ProbeStorage(probe StorageProbe, sessionsRoot, home string) []StorageVolume {
	if probe == nil {
		probe = platform.ReadDiskSpace
	}
	var out []StorageVolume
	read := func(role, path string) {
		if path == "" {
			return
		}
		space, err := probe(path)
		out = append(out, StorageVolume{Role: role, Path: path, Space: space, Err: err})
	}
	read(StorageVolumeSessions, sessionsRoot)
	read(StorageVolumeHome, home)
	if len(out) == 2 && out[0].Err == nil && out[1].Err == nil && out[0].Space.Volume == out[1].Space.Volume {
		// One disk, read once: the home folder is not another place to run
		// out of room.
		out = out[:1]
	}
	return out
}

// StorageStatus is what GET /coddy/info reports about the room left.
type StorageStatus struct {
	// State is the worst state of the volumes read, or StorageFull when a
	// write has failed on a full disk.
	State StorageState
	// Measured says the figures below were read from a disk. It is false when
	// the volume they belong to could not be read and State came from a failed
	// write alone.
	Measured bool
	// Volume names the volume the figures belong to: while a failed write is on
	// record, the sessions volume, which every such write went to; otherwise the
	// one in the worst state, the one with the least free space among equals.
	Volume string
	// FreeBytes and TotalBytes are that volume's.
	FreeBytes  uint64
	TotalBytes uint64
	// MinFreeBytes is the threshold sessions.min_free_mb sets; 0 when the
	// warning is off.
	MinFreeBytes uint64
}

// AssessStorage folds the volumes read into one status. writeFailed says a
// write has failed on a full disk and none has succeeded since, which makes
// the state full whatever the numbers say (a quota is no free-space figure).
// Every failure on record is a write into the sessions folder, so it is
// attributed to the sessions volume - its figures, or none when it could not be
// read - and not to whichever volume the numbers rank worst. It returns false
// when there is nothing to report: no volume could be read and no write has
// failed.
func AssessStorage(volumes []StorageVolume, minFree uint64, writeFailed bool) (StorageStatus, bool) {
	var (
		best     *StorageVolume
		state    StorageState
		sessions *StorageVolume
	)
	for i := range volumes {
		v := &volumes[i]
		if v.Err != nil {
			continue
		}
		if v.Role == StorageVolumeSessions && sessions == nil {
			sessions = v
		}
		s := v.State(minFree)
		if s == "" {
			s = StorageOK
		}
		if best == nil || s.rank() > state.rank() || (s.rank() == state.rank() && v.Space.FreeBytes < best.Space.FreeBytes) {
			best, state = v, s
		}
	}
	if writeFailed {
		out := StorageStatus{State: StorageFull, MinFreeBytes: minFree}
		out.fill(sessions)
		return out, true
	}
	if best == nil {
		return StorageStatus{}, false
	}
	out := StorageStatus{State: state, MinFreeBytes: minFree}
	out.fill(best)
	return out, true
}

// fill takes the volume's figures; nil leaves the status without any.
func (s *StorageStatus) fill(v *StorageVolume) {
	if v == nil {
		return
	}
	s.Measured = true
	s.Volume = v.Role
	s.FreeBytes = v.Space.FreeBytes
	s.TotalBytes = v.Space.TotalBytes
}
