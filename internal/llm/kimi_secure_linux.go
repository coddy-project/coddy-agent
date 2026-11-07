//go:build linux || android

package llm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"golang.org/x/sys/unix"
)

const kimiSecureStoreAvailable = true

// Other processes of this UID are trusted and must obey the lock. A different
// UID must be unable to replace any component or entry of the store.
func openKimiRoot(path string, create bool) (*os.Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("kimi credentials: empty directory")
	}
	parent := filepath.Dir(abs)
	existing := parent
	for {
		_, err = os.Stat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		next := filepath.Dir(existing)
		if next == existing {
			return nil, err
		}
		existing = next
	}
	if err = checkKimiLexicalPath(existing); err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return nil, err
	}
	if err = checkKimiAncestors(real); err != nil {
		return nil, err
	}
	created := existing != parent
	if create {
		if err = os.MkdirAll(parent, 0700); err != nil {
			return nil, err
		}
		if err = os.Mkdir(abs, 0700); err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	if err = checkKimiLexicalPath(parent); err != nil {
		return nil, err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	if err = checkKimiAncestors(realParent); err != nil {
		return nil, err
	}
	final := filepath.Join(realParent, filepath.Base(abs))
	info, err := os.Lstat(final)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("kimi credentials: unsafe store directory %s", final)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(st.Uid) != int64(os.Geteuid()) {
		return nil, errors.New("kimi credentials: store directory owner mismatch")
	}
	root, err := os.OpenRoot(final)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, errors.New("kimi credentials: store directory changed")
	}
	if created {
		if err = platform.SyncRoot(root); err != nil {
			_ = root.Close()
			return nil, err
		}
		for dir := realParent; ; dir = filepath.Dir(dir) {
			if err = platform.SyncDir(dir); err != nil {
				_ = root.Close()
				return nil, err
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return root, nil
}

// A symlink is trusted only if its containing directory protects its name.
// Sticky directories protect an entry only when it belongs to us or root.
func checkKimiLexicalPath(path string) error {
	for {
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		entry, err := os.Lstat(path)
		if err != nil {
			return err
		}
		dir, err := os.Stat(parent)
		if err != nil {
			return err
		}
		ds, ok := dir.Sys().(*syscall.Stat_t)
		if !ok || !dir.IsDir() || (ds.Uid != 0 && int64(ds.Uid) != int64(os.Geteuid())) {
			return fmt.Errorf("kimi credentials: untrusted ancestor %s", parent)
		}
		if dir.Mode().Perm()&0022 != 0 {
			es, ok := entry.Sys().(*syscall.Stat_t)
			if ds.Uid != 0 || dir.Mode()&os.ModeSticky == 0 || !ok || (es.Uid != 0 && int64(es.Uid) != int64(os.Geteuid())) {
				return fmt.Errorf("kimi credentials: writable ancestor %s", parent)
			}
		}
		path = parent
	}
}

func kimiCheckPrivateFile(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || int64(st.Uid) != int64(os.Geteuid()) {
		return errors.New("kimi credentials: unsafe file owner, type or permissions")
	}
	return nil
}

func checkKimiAncestors(path string) error {
	var child os.FileInfo
	for {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (st.Uid != 0 && int64(st.Uid) != int64(os.Geteuid())) {
			return fmt.Errorf("kimi credentials: untrusted ancestor %s", path)
		}
		if info.Mode().Perm()&0022 != 0 {
			if st.Uid != 0 || info.Mode()&os.ModeSticky == 0 || child == nil {
				return fmt.Errorf("kimi credentials: writable ancestor %s", path)
			}
			cs, ok := child.Sys().(*syscall.Stat_t)
			if !ok || (cs.Uid != 0 && int64(cs.Uid) != int64(os.Geteuid())) {
				return fmt.Errorf("kimi credentials: untrusted child of %s", path)
			}
		}
		next := filepath.Dir(path)
		if next == path {
			return nil
		}
		child = info
		path = next
	}
}

func kimiTryFlock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func kimiUnlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }
