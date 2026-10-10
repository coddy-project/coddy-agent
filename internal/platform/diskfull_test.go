package platform

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

// Every shape the operating system's "no room left" arrives in is recognised:
// the bare errno, the *os.PathError and *os.LinkError the os package wraps it
// in, an *os.SyscallError, an fmt.Errorf %w chain around any of them and an
// errors.Join that carries one among others.
func TestIsDiskFullRecognisesTheErrnoInEveryWrapper(t *testing.T) {
	for _, errno := range wantDiskFull {
		t.Run(errno.Error(), func(t *testing.T) {
			path := "/sessions/sess_x/messages.json"
			pathErr := &os.PathError{Op: "write", Path: path, Err: errno}
			cases := map[string]error{
				"bare":             errno,
				"path error":       pathErr,
				"link error":       &os.LinkError{Op: "rename", Old: path + ".tmp", New: path, Err: errno},
				"syscall error":    os.NewSyscallError("write", errno),
				"wrapped once":     fmt.Errorf("session/new: layout: %w", pathErr),
				"wrapped twice":    fmt.Errorf("session load failed: %w", fmt.Errorf("session/new: layout: %w", pathErr)),
				"joined with more": errors.Join(errors.New("first"), io.ErrUnexpectedEOF, fmt.Errorf("save: %w", pathErr)),
			}
			for name, err := range cases {
				if !IsDiskFull(err) {
					t.Errorf("%s: IsDiskFull(%v) = false, want true", name, err)
				}
			}
		})
	}
}

func TestIsDiskFullRefusesEverythingElse(t *testing.T) {
	cases := map[string]error{
		"nil":                nil,
		"plain error":        errors.New("boom"),
		"plain error w/text": errors.New("no space left on device"),
		"eof":                io.EOF,
		"not exist":          fs.ErrNotExist,
		"permission":         &os.PathError{Op: "open", Path: "/x", Err: syscall.EACCES},
		"no entry":           &os.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT},
		"wrapped permission": fmt.Errorf("layout: %w", &os.PathError{Op: "mkdir", Path: "/x", Err: syscall.EACCES}),
		"joined others":      errors.Join(errors.New("a"), &os.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}),
	}
	for name, err := range cases {
		if IsDiskFull(err) {
			t.Errorf("%s: IsDiskFull(%v) = true, want false", name, err)
		}
	}
}
