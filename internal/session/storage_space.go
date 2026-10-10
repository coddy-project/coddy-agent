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
// volume that could not be read has no state (""), and neither has any when
// minFree is 0: the warning is off and the numbers are only reported. A disk
// with no byte free is full, whatever the threshold says.
func (v StorageVolume) State(minFree uint64) StorageState {
	if v.Err != nil || minFree == 0 {
		return ""
	}
	switch {
	case v.Space.FreeBytes == 0:
		return StorageFull
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
	// no volume could be read and State came from a failed write alone.
	Measured bool
	// Volume names the volume the figures belong to: the one in the worst
	// state, the one with the least free space among equals.
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
// It returns false when there is nothing to report: no volume could be read
// and no write has failed.
func AssessStorage(volumes []StorageVolume, minFree uint64, writeFailed bool) (StorageStatus, bool) {
	var (
		best  *StorageVolume
		state StorageState
	)
	for i := range volumes {
		v := &volumes[i]
		if v.Err != nil {
			continue
		}
		s := v.State(minFree)
		if s == "" {
			s = StorageOK
		}
		if best == nil || s.rank() > state.rank() || (s.rank() == state.rank() && v.Space.FreeBytes < best.Space.FreeBytes) {
			best, state = v, s
		}
	}
	if best == nil && !writeFailed {
		return StorageStatus{}, false
	}
	out := StorageStatus{State: state, MinFreeBytes: minFree}
	if best != nil {
		out.Measured = true
		out.Volume = best.Role
		out.FreeBytes = best.Space.FreeBytes
		out.TotalBytes = best.Space.TotalBytes
	}
	if writeFailed {
		out.State = StorageFull
	}
	return out, true
}
