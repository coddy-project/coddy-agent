package platform

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// WriteReport tells a caller of WriteFileReplacing what happened beyond the
// bytes written.
type WriteReport struct {
	// Links is the number of names the replaced file had, 0 when unknown or
	// when the file was new. A value above 1 means the other names keep the old
	// content: the replacement is a new file with a new identity.
	Links uint64
	// DirSyncErr is set when the rename succeeded but flushing the folder did
	// not, so the new content is in place but not yet known to be on disk.
	DirSyncErr error
}

// Note is a short sentence for the result line a model reads, "" when the write
// needs no remark.
func (r WriteReport) Note() string {
	switch {
	case r.Links > 1:
		return fmt.Sprintf("the file had %d names; the other names keep the old content", r.Links)
	case r.DirSyncErr != nil:
		return fmt.Sprintf("written, not yet known to be on disk: %v", r.DirSyncErr)
	}
	return ""
}

// WriteFileReplacing writes data to path so that a failure leaves the old
// content in place; see ReplaceFileFrom.
func WriteFileReplacing(path string, data []byte, perm fs.FileMode) (WriteReport, error) {
	return ReplaceFileFrom(path, bytes.NewReader(data), perm)
}

// ReplaceFileFrom copies src to path so that a failure leaves the old content in
// place. It writes a dot-named temporary file beside the target, flushes it, and
// renames it over the target; the rename is the commit point. An error before it
// removes the temporary file and leaves the target as it was (a process killed
// before it leaves the target as it was too, and the temporary file behind).
// perm is the mode of a new file (under the umask); an existing file keeps its
// own mode, and its replacement is private until it has the content.
//
// A target the process cannot write is refused with the error os.WriteFile
// gives, because a rename needs write access to the folder, not to the file. A
// symlink is resolved first, so the link survives and its target changes, and
// a link whose target does not exist yet creates it. A target that is not a
// regular file (a pipe, a device) is written in place, as os.WriteFile does:
// replacing it would destroy it. The parent folder must exist.
func ReplaceFileFrom(path string, src io.Reader, perm fs.FileMode) (WriteReport, error) {
	var report WriteReport
	target, err := resolveWriteTarget(path)
	if err != nil {
		return report, err
	}

	createMode := perm
	var existingMode fs.FileMode
	existing := false
	switch info, statErr := os.Stat(target); {
	case statErr == nil:
		// A pipe or a device is written as it is: opening it just to probe would
		// hand a reader an end of file, and replacing it would destroy it.
		if !info.Mode().IsRegular() && !info.IsDir() {
			return report, writeInPlace(target, src, perm)
		}
		// Open without truncating: the same refusal os.WriteFile would give for
		// a directory, a read-only file or a file another handle denies.
		probe, openErr := os.OpenFile(target, os.O_WRONLY, 0)
		if openErr != nil {
			return report, openErr
		}
		_ = probe.Close()
		existing = true
		existingMode = info.Mode().Perm()
		createMode = 0o600
		report.Links = linkCount(target)
	case !errors.Is(statErr, fs.ErrNotExist):
		return report, statErr
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, err := createSibling(dir, base, createMode)
	if err != nil {
		return report, fmt.Errorf("no temporary file can be made beside %s: %w", target, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(faultableWriter(tmp), src); err != nil {
		return report, err
	}
	if existing {
		if err := tmp.Chmod(existingMode); err != nil {
			return report, err
		}
	}
	if err := tmp.Sync(); err != nil {
		return report, err
	}
	if err := tmp.Close(); err != nil {
		return report, err
	}
	if err := commitReplace(tmpPath, target, existing); err != nil {
		return report, err
	}
	committed = true
	report.DirSyncErr = syncDir(dir)
	return report, nil
}

// writeInPlace writes a target that must keep its identity.
func writeInPlace(target string, src io.Reader, perm fs.FileMode) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// maxSymlinkHops bounds the walk of a chain of links, as the kernel does.
const maxSymlinkHops = 40

// resolveWriteTarget follows links at path so the write lands on the target.
// A missing path, or a link whose target is missing, resolves to the path a
// plain write would create. A link is resolved component by component, as the
// kernel does, so a ".." in its text climbs from wherever the components before
// it lead, never lexically.
func resolveWriteTarget(path string) (string, error) {
	current := path
	for hop := 0; hop <= maxSymlinkHops; hop++ {
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return current, nil
			}
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return current, nil
		}
		// A link whose target exists: the walk of EvalSymlinks is the kernel's.
		if real, evalErr := filepath.EvalSymlinks(current); evalErr == nil {
			return real, nil
		}
		link, err := os.Readlink(current)
		if err != nil {
			return "", err
		}
		// A dangling link: its last component is missing, so resolve the folder
		// part of the raw, uncleaned text and keep the missing name.
		raw := link
		if !filepath.IsAbs(link) {
			raw = filepath.Dir(current) + string(os.PathSeparator) + link
		}
		dirPart, name := splitLast(raw)
		if name == "" || name == "." || name == ".." {
			return "", &fs.PathError{Op: "open", Path: path, Err: errors.New("link target names no file")}
		}
		dirReal, err := filepath.EvalSymlinks(dirPart)
		if err != nil {
			return "", err
		}
		current = filepath.Join(dirReal, name)
	}
	return "", &fs.PathError{Op: "open", Path: path, Err: errors.New("too many levels of symbolic links")}
}

// splitLast splits p at its last separator without cleaning it, so the ".."
// elements before the last one are left for EvalSymlinks to walk.
func splitLast(p string) (dir, name string) {
	i := len(p) - 1
	for i >= 0 && !os.IsPathSeparator(p[i]) {
		i--
	}
	if i < 0 {
		return ".", p
	}
	if i == 0 {
		return p[:1], p[1:]
	}
	return p[:i], p[i+1:]
}

// maxTempStem bounds how much of the target's name the temporary file keeps, so
// a long but valid name does not overflow the 255-byte limit of a component.
const maxTempStem = 64

// tempStem is the start of base cut at a rune boundary within maxTempStem bytes.
func tempStem(base string) string {
	if len(base) <= maxTempStem {
		return base
	}
	cut := 0
	for i := range base {
		if i > maxTempStem {
			break
		}
		cut = i
	}
	return base[:cut]
}

// createSibling creates an exclusive, dot-named file in dir.
func createSibling(dir, base string, perm fs.FileMode) (*os.File, error) {
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, "."+tempStem(base)+".tmp."+hex.EncodeToString(suffix[:]))
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err == nil {
			return f, nil
		}
		lastErr = err
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	return nil, lastErr
}

// retryReplace runs op, retrying briefly where the platform reports a transient
// sharing error (Windows, another handle open).
func retryReplace(op func() error) error {
	const maxAttempts = 20
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = op(); err == nil || !isRetryableRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}

// ErrInjectedWriteFault is what a write fault from InjectWriteFault returns.
var ErrInjectedWriteFault = errors.New("injected write fault")

// writeFaultAfter is the byte count after which the next write fails, -1 when
// no fault is armed.
var writeFaultAfter atomic.Int64

func init() { writeFaultAfter.Store(-1) }

// InjectWriteFault makes the data write of ReplaceFileFrom fail with
// ErrInjectedWriteFault after afterBytes bytes, until the returned function is
// called. It is a test seam for packages that cannot fill a disk: a half-written
// temporary file is what a full disk leaves behind. Tests that arm it must not
// run in parallel with other writers.
func InjectWriteFault(afterBytes int) (restore func()) {
	previous := writeFaultAfter.Swap(int64(afterBytes))
	return func() { writeFaultAfter.Store(previous) }
}

// faultWriter passes the first after bytes through, then fails.
type faultWriter struct {
	w         io.Writer
	remaining int
}

func (f *faultWriter) Write(p []byte) (int, error) {
	if len(p) <= f.remaining {
		f.remaining -= len(p)
		return f.w.Write(p)
	}
	n, err := f.w.Write(p[:f.remaining])
	f.remaining = 0
	if err != nil {
		return n, err
	}
	return n, ErrInjectedWriteFault
}

func faultableWriter(w io.Writer) io.Writer {
	if after := writeFaultAfter.Load(); after >= 0 {
		return &faultWriter{w: w, remaining: int(after)}
	}
	return w
}
