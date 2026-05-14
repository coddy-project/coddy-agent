package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const cancelRequestFile = ".coddy-cancel-request"

type cancelRequestPayload struct {
	RequestedAtRFC3339 string `json:"requestedAt"`
}

// WriteCancelRequest creates or replaces the cross-process cancel signal for a session bundle.
func WriteCancelRequest(sessionDir string) error {
	if sessionDir == "" {
		return nil
	}
	p := cancelRequestPath(sessionDir)
	payload := cancelRequestPayload{RequestedAtRFC3339: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := writeBytesAtomic(p, b); err != nil {
		return err
	}
	return nil
}

// ClearCancelRequest removes the cancel signal file if present.
func ClearCancelRequest(sessionDir string) error {
	if sessionDir == "" {
		return nil
	}
	p := cancelRequestPath(sessionDir)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// CancelRequestExists reports whether a cancel signal file is present.
func CancelRequestExists(sessionDir string) (bool, error) {
	if sessionDir == "" {
		return false, nil
	}
	p := cancelRequestPath(sessionDir)
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !fi.IsDir(), nil
}

func cancelRequestPath(sessionDir string) string {
	return filepath.Join(sessionDir, cancelRequestFile)
}
