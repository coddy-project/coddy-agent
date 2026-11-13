//go:build !windows

package localipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func endpointAddress(home string) (string, string, error) {
	// Prefer a socket in the home itself: both processes see the same path
	// even when their temporary directories or mount namespaces differ. Keep
	// a few bytes clear of the shortest supported sockaddr_un path limit.
	address := filepath.Join(home, ".local-ipc", "runtime.sock")
	if len(address) <= 100 {
		return "unix", address, nil
	}
	digest := sha256.Sum256([]byte(home))
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		// Linux abstract sockets have no pathname length or TMPDIR dependency.
		// The peer-credential check and per-start token still protect them.
		return "unix", fmt.Sprintf("@coddy-local-ipc-%d-%s", os.Getuid(), hex.EncodeToString(digest[:16])), nil
	}
	// macOS has no abstract namespace. /tmp is a fixed, shared system path,
	// unlike TMPDIR, which may differ between the service and the window.
	dir := filepath.Join("/tmp", fmt.Sprintf("coddy-local-ipc-%d", os.Getuid()))
	return "unix", filepath.Join(dir, hex.EncodeToString(digest[:16])+".sock"), nil
}

func listen(home string) (net.Listener, record, error) {
	network, address, err := endpointAddress(home)
	if err != nil {
		return nil, record{}, err
	}
	if strings.HasPrefix(address, "@") {
		listener, err := net.Listen("unix", address)
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, record{}, ErrAlreadyOwned
		}
		if err != nil {
			return nil, record{}, err
		}
		return listener, record{Network: network, Address: address}, nil
	}
	dir := filepath.Dir(address)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, record{}, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, record{}, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || int64(info.Sys().(*syscall.Stat_t).Uid) != int64(os.Getuid()) {
		return nil, record{}, fmt.Errorf("%w: unsafe local socket directory %s", ErrUnsafe, dir)
	}
	// The owner lock is held, so only an endpoint of a crashed predecessor can
	// remain. A live socket without the lock is not removed: that would let a
	// second runtime steal an externally prepared endpoint.
	info, err = os.Lstat(address)
	if err == nil && info.Mode()&os.ModeSocket == 0 {
		return nil, record{}, fmt.Errorf("%w: the local socket path is not a socket", ErrUnsafe)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, record{}, err
	}
	conn, dialErr := net.DialTimeout("unix", address, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return nil, record{}, ErrAlreadyOwned
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return nil, record{}, fmt.Errorf("%w: cannot establish whether the local socket is live: %v", ErrUnsafe, dialErr)
	}
	if err := os.Remove(address); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, record{}, err
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, record{}, err
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		return nil, record{}, err
	}
	return listener, record{Network: network, Address: address}, nil
}

func dial(ctx context.Context, rec record) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, rec.Network, rec.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := sameUserPeer(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func privateRecordInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return errors.New("the endpoint record must be a regular 0600 file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Getuid()) {
		return errors.New("the endpoint record belongs to another user")
	}
	return nil
}
