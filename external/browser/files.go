//go:build browser

package browser

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// A transfer carries bytes, never a server path. A ticket exists for the
// length of one tool call, is good for one request and cannot be turned into
// a read of an arbitrary workspace file.
type transfer struct {
	mu      sync.Mutex
	data    []byte
	upload  bool
	used    bool
	expires time.Time
}

var transfers sync.Map

// transferTTL bounds how long the extension has to fetch or post the bytes.
const transferTTL = 2 * time.Minute

// StageFile opens a ticket. For an upload to the page, data is what the
// extension downloads; for a download from the page, the extension posts the
// bytes it fetched and FileResult reads them. The returned func removes the
// ticket.
func StageFile(data []byte, toPage bool) (string, func(), error) {
	if len(data) > MaxFileBytes {
		return "", nil, errors.New("the file exceeds 32 MiB")
	}
	id := randomID()
	transfers.Store(id, &transfer{data: data, upload: !toPage, expires: time.Now().Add(transferTTL)})
	return id, func() { transfers.Delete(id) }, nil
}

// FileResult is what the extension posted under a download ticket.
func FileResult(id string) ([]byte, error) {
	v, ok := transfers.Load(id)
	if !ok {
		return nil, errors.New("the transfer expired")
	}
	t := v.(*transfer)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.upload || !t.used {
		return nil, errors.New("the browser did not transfer the file")
	}
	return t.data, nil
}

func serveFile(w http.ResponseWriter, r *http.Request, id string) {
	v, ok := transfers.Load(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	t := v.(*transfer)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.used || time.Now().After(t.expires) {
		http.Error(w, "transfer expired or used", http.StatusGone)
		return
	}
	switch {
	case r.Method == http.MethodGet && !t.upload:
		t.used = true
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(t.data)
	case r.Method == http.MethodPost && t.upload:
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxFileBytes))
		if err != nil {
			http.Error(w, "the file exceeds 32 MiB or the transfer failed", http.StatusBadRequest)
			return
		}
		t.data, t.used = data, true
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
