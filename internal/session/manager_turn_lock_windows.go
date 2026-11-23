package session

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

const turnMutexAccess = windows.SYNCHRONIZE | windows.MUTEX_MODIFY_STATE

func (m *Manager) acquirePromptTurnLock(id string, st *State) (func(), error) {
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return m.acquireStubTurnLock(id)
	}
	return acquireWindowsTurnLock(dir)
}

func (m *Manager) acquireDeleteTurnLock(_, dir string) (func(), error) {
	return acquireWindowsTurnLock(dir)
}

// TurnLockHeld probes an existing mutex without creating a mutex or writing to the bundle.
func TurnLockHeld(sessionDir string) bool {
	name, err := windowsTurnMutexName(strings.TrimSpace(sessionDir))
	if err != nil {
		return false
	}
	h, err := windows.OpenMutex(turnMutexAccess, false, name)
	if err != nil {
		// A different account's default DACL can prevent probing its mutex.
		// Treat it as busy rather than reporting that the bundle is idle.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	unlock, err := tryWindowsTurnMutex(h)
	if err != nil {
		return errors.Is(err, ErrSessionTurnBusy)
	}
	unlock()
	return false
}

func acquireWindowsTurnLock(dir string) (func(), error) {
	name, err := windowsTurnMutexName(dir)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutexEx(nil, name, 0, turnMutexAccess)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, fmt.Errorf("turn lock create mutex: %w", err)
	}
	unlock, err := tryWindowsTurnMutex(h)
	if err != nil {
		return nil, err
	}
	// Deletion might have finished after we resolved the path but before we
	// acquired the mutex. Revalidate under the lock, without creating anything.
	current, err := windowsTurnMutexName(dir)
	if err != nil {
		unlock()
		return nil, err
	}
	if windows.UTF16PtrToString(current) != windows.UTF16PtrToString(name) {
		unlock()
		return nil, fmt.Errorf("turn lock: bundle path changed: %s", dir)
	}
	return unlock, nil
}

// tryWindowsTurnMutex takes ownership of h. Windows mutexes are thread-affine
// and recursive, so use a dedicated OS thread from wait through release. The
// caller (including an unlock callback run on another goroutine) need not pin.
func tryWindowsTurnMutex(h windows.Handle) (func(), error) {
	ready := make(chan error)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer close(done)
		defer func() { _ = windows.CloseHandle(h) }()
		status, err := windows.WaitForSingleObject(h, 0)
		switch {
		case err != nil:
			ready <- fmt.Errorf("turn lock wait: %w", err)
		case status == uint32(windows.WAIT_TIMEOUT):
			ready <- ErrSessionTurnBusy
		case status == windows.WAIT_OBJECT_0 || status == windows.WAIT_ABANDONED:
			// An abandoned mutex is already owned by this thread.
			ready <- nil
			<-release
			if windows.ReleaseMutex(h) != nil {
				// Do not reuse a thread that may still own the mutex. Go
				// terminates it when this locked goroutine exits.
				return
			}
		default:
			ready <- fmt.Errorf("turn lock wait: unexpected status %#x", status)
		}
		runtime.UnlockOSThread()
	}()
	if err := <-ready; err != nil {
		<-done
		return nil, err
	}
	return sync.OnceFunc(func() {
		close(release)
		<-done
	}), nil
}

func windowsTurnMutexName(dir string) (*uint16, error) {
	if dir == "" {
		return nil, &os.PathError{Op: "turn lock", Path: dir, Err: os.ErrNotExist}
	}
	path, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if len(path) >= 248 && !strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(path, `\\.\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Resolve aliases (including junctions) using an existing directory only.
	// Do not retain a filesystem handle for the lifetime of the mutex.
	h, err := windows.CreateFile(pathPtr, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "turn lock open directory", Path: dir, Err: err}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, &os.PathError{Op: "turn lock stat directory", Path: dir, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return nil, &os.PathError{Op: "turn lock", Path: dir, Err: windows.ERROR_DIRECTORY}
	}
	for size := uint32(256); ; {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], size, 0) // normalized DOS/UNC path
		if err != nil {
			return nil, &os.PathError{Op: "turn lock resolve directory", Path: dir, Err: err}
		}
		if n < size {
			key := strings.ToUpper(windows.UTF16ToString(buf[:n]))
			return windows.UTF16PtrFromString(fmt.Sprintf(`Global\coddy-turn-%x`, sha256.Sum256([]byte(key))))
		}
		size = n + 1
	}
}
