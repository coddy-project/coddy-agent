//go:build windows

package localipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

func endpointAddress(home string) (string, string, error) {
	digest := sha256.Sum256([]byte(strings.ToLower(home)))
	return "pipe", `\\.\pipe\coddy-local-ipc-` + hex.EncodeToString(digest[:16]), nil
}

func listen(home string) (net.Listener, record, error) {
	network, address, err := endpointAddress(home)
	if err != nil {
		return nil, record{}, err
	}
	// D:P protects the DACL from inheritance; OW grants generic all only to
	// the owner. The per-start token is still required after this OS boundary.
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;OW)"})
	if err != nil {
		return nil, record{}, err
	}
	return listener, record{Network: network, Address: address}, nil
}

func dial(ctx context.Context, rec record) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, rec.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return conn, nil
}

func sameUserPeer(net.Conn) error { return nil }

func privateRecordInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("the endpoint record must be a regular file")
	}
	// Windows uses the containing profile's DACL. Claim creates the record
	// exclusively with 0600 semantics and the named pipe itself has an
	// owner-only DACL, so there is no POSIX mode assertion to make here.
	return nil
}
