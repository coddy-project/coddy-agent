package dryrun

import (
	"errors"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// storageKey is the config key the free-space rows belong to: the threshold
// sessions.min_free_mb sets, and the one a fix names.
const storageKey = "sessions.min_free_mb"

// storage reports the room the disks Coddy writes to have left: the one that
// holds the sessions folder and, when it is another, the one that holds the
// home folder (issue #465). A session is saved on every turn, so a disk that is
// nearly full fails a long way into a conversation, not at start; this is where
// a deploy script hears of it first.
//
// An unreadable disk is skipped, never an error: the question was whether
// there is room, and a platform or a filesystem that does not say has no
// answer, which is not a problem of the file. A disk with no free byte is an
// error, since nothing can be saved on it; one under the threshold is a
// warning; with the warning off (sessions.min_free_mb: 0) the figures are
// printed and nothing is judged.
func (r *runner) storage() {
	cfg := r.req.Cfg
	minFree := cfg.Sessions.MinFreeBytes()
	for _, v := range session.ProbeStorage(r.req.DiskSpace, cfg.ResolvedSessionsRoot(), r.req.Paths.Home) {
		r.rep.add(r.storageCheck(v, minFree))
	}
}

func (r *runner) storageCheck(v session.StorageVolume, minFree uint64) Check {
	what := "the disk of the sessions folder"
	if v.Role == session.StorageVolumeHome {
		what = "the disk of the home folder"
	}
	where := fmt.Sprintf("%s (%s)", what, v.Path)
	if v.Err != nil {
		reason := shortErr(v.Err)
		if errors.Is(v.Err, platform.ErrDiskSpaceUnavailable) {
			reason = "this platform or filesystem does not report it"
		}
		return r.check(StatusSkipped, storageKey, storageKey, "free space on "+where+" cannot be read: "+reason, "")
	}
	room := fmt.Sprintf("%s free of %s on %s", formatBytes(v.Space.FreeBytes), formatBytes(v.Space.TotalBytes), where)
	switch v.State(minFree) {
	case session.StorageFull:
		return r.check(StatusError, storageKey, storageKey, "no free space left: "+room,
			"free space on that disk before starting: a new chat cannot be created and the turns of a running one are not saved")
	case session.StorageLow:
		return r.check(StatusWarning, storageKey, storageKey,
			fmt.Sprintf("%s; warning below %s", room, formatBytes(minFree)),
			"free space on that disk, or lower "+storageKey+" (0 turns the warning off); a long transcript needs room for a second copy of itself while it is saved")
	case session.StorageOK:
		return r.check(StatusOK, storageKey, storageKey, fmt.Sprintf("%s; warning below %s", room, formatBytes(minFree)), "")
	}
	return r.check(StatusSkipped, storageKey, storageKey, "low-space warning is off ("+storageKey+": 0); "+room, "")
}

// formatBytes writes a byte count in binary units with at most one decimal,
// the way df -h and a file manager do: 512 MiB, 1.5 GiB.
func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/float64(div)), ".0") + " " + string("KMGTPE"[exp]) + "iB"
}
