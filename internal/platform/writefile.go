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
	"strings"
	"sync/atomic"
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
	var notes []string
	if r.Links > 1 {
		notes = append(notes, fmt.Sprintf("the file had %d names; the other names keep the old content", r.Links))
	}
	if r.DirSyncErr != nil {
		notes = append(notes, fmt.Sprintf("written, not yet known to be on disk: %v", r.DirSyncErr))
	}
	return strings.Join(notes, "; ")
}

// WriteFileReplacing writes data to path so that a failure leaves the old
// content in place; see ReplaceFileFrom.
func WriteFileReplacing(path string, data []byte, perm fs.FileMode) (WriteReport, error) {
	return ReplaceFileFrom(path, bytes.NewReader(data), perm)
}

// ReplaceFileFrom copies src to path so that a failure leaves the old content in
// place. It writes a temporary file named "<name>.<random>~" beside the
// target, flushes it, and renames it over the target; the rename is
// the commit point. An error before it removes the temporary file, leaves the
// target as it was and says so (a process killed before it leaves the target as
// it was too, and the temporary file behind). perm is the mode of a new file
// (under the umask); an existing file keeps its permissions (its mode, and its
// POSIX ACL on Linux or its DACL and attributes on Windows), which its
// replacement takes before it has any content.
//
// A target the process cannot write is refused with the error os.WriteFile
// gives, because a rename needs write access to the folder, not to the file. A
// symlink is resolved first, so the link survives and its target changes. A
// link whose target does not exist yet, a pipe and a device are written in
// place, as os.WriteFile does: there is no old content to keep, and replacing a
// pipe or a device would destroy it. The parent folder must exist.
func ReplaceFileFrom(path string, src io.Reader, perm fs.FileMode) (WriteReport, error) {
	var report WriteReport
	leftAsItWas := func(err error) (WriteReport, error) {
		return report, fmt.Errorf("%w (the file was left as it was)", err)
	}

	target, dangling, err := resolveWriteTarget(path)
	if err != nil {
		return leftAsItWas(err)
	}
	if dangling {
		// The kernel follows the link and creates its target, ".." and all.
		return report, writeInPlace(path, src, perm)
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
			return leftAsItWas(openErr)
		}
		_ = probe.Close()
		existing = true
		existingMode = info.Mode().Perm()
		createMode = 0o600
		report.Links = linkCount(target)
	case !errors.Is(statErr, fs.ErrNotExist):
		return leftAsItWas(statErr)
	}

	dir, base := filepath.Split(target)
	if dir == "" {
		dir = "."
	}
	tmp, err := createSibling(dir, base, createMode)
	if err != nil {
		return leftAsItWas(fmt.Errorf("no temporary file can be made beside %s: %w", target, err))
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// The replacement of an existing file takes its permissions before it has
	// any content, so the content is never open to more than the file was.
	if existing {
		if err := tmp.Chmod(existingMode); err != nil {
			return leftAsItWas(err)
		}
		if err := matchAccess(tmp, target); err != nil {
			return leftAsItWas(err)
		}
	}
	if _, err := io.Copy(faultableWriter(tmp), src); err != nil {
		return leftAsItWas(err)
	}
	if err := tmp.Sync(); err != nil {
		return leftAsItWas(err)
	}
	if err := tmp.Close(); err != nil {
		return leftAsItWas(err)
	}
	if err := commitReplace(tmpPath, target); err != nil {
		return leftAsItWas(err)
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

// resolveWriteTarget follows the links at path so the write lands on the file
// they name; EvalSymlinks walks them component by component as the kernel
// does, so a ".." in a link's text climbs from wherever the components before
// it lead. dangling reports a link whose target does not exist yet.
func resolveWriteTarget(path string) (target string, dangling bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return path, false, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, true, nil
	}
	return real, false, err
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

// createSibling creates an exclusive "<name>.<random>~" file in dir. The
// trailing tilde is the mark editors give their backup files, so a leftover
// reads as one and the usual "*~" ignore rule keeps it out of git.
func createSibling(dir, base string, perm fs.FileMode) (*os.File, error) {
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, tempStem(base)+"."+hex.EncodeToString(suffix[:])+"~")
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
