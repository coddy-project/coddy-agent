package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DiskSpace is the room the volume under a path has, as the account running
// Coddy sees it.
type DiskSpace struct {
	// TotalBytes is the size of the volume.
	TotalBytes uint64
	// FreeBytes is what the account can still write: the free blocks without
	// the ones reserved for the superuser on Unix (f_bavail), the share left
	// under the account's quota on Windows. It is never more than TotalBytes.
	FreeBytes uint64
	// Volume names the volume. Two paths report the same Volume exactly when
	// they are on the same one, so a caller probing the sessions folder and the
	// home folder tells a shared volume from two. The text itself means
	// nothing to a caller (a device number, a drive or a mount point).
	Volume string
}

// ErrDiskSpaceUnavailable says the free space of a path cannot be read here:
// the platform has no call for it, or the filesystem under the path reports no
// size at all (some network and virtual filesystems). A caller treats it as
// "not known", never as "full".
var ErrDiskSpaceUnavailable = errors.New("the free space of this disk cannot be read")

// ReadDiskSpace reports the room on the volume that holds path. The path need
// not exist: a folder Coddy has not made yet (the sessions folder of a first
// start) lives on the volume of its nearest existing ancestor, and a path that
// names a file is read as the folder around it. The syscalls cost microseconds
// and read no file, so it is cheap enough for an endpoint a page polls.
func ReadDiskSpace(path string) (DiskSpace, error) {
	if strings.TrimSpace(path) == "" {
		return DiskSpace{}, errors.New("disk space: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return DiskSpace{}, fmt.Errorf("disk space: %w", err)
	}
	dir, err := nearestFolder(abs)
	if err != nil {
		return DiskSpace{}, err
	}
	ds, err := readDiskSpace(dir)
	if err != nil {
		return DiskSpace{}, err
	}
	if ds.TotalBytes == 0 {
		return DiskSpace{}, fmt.Errorf("%s: %w", dir, ErrDiskSpaceUnavailable)
	}
	if ds.FreeBytes > ds.TotalBytes {
		ds.FreeBytes = ds.TotalBytes
	}
	return ds, nil
}

// nearestFolder walks up from path to the first ancestor that exists and is a
// folder (path itself when it is one; the folder around a file). A step that
// fails for another reason than "not there" - permission denied on a parent -
// is the answer: a folder Coddy cannot even look at is not one it can store
// sessions in, and guessing at a volume from further up would hide that.
func nearestFolder(path string) (string, error) {
	for cur := path; ; {
		st, err := os.Stat(cur)
		switch {
		case err == nil && st.IsDir():
			return cur, nil
		case err == nil:
			// A file: the folder around it is on the same volume.
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			// Not made yet, or a file in the middle of the path.
		default:
			return "", fmt.Errorf("disk space: %w", err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("disk space: no existing folder above %s", path)
		}
		cur = parent
	}
}
