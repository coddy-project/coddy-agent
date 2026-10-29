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

// StageFile opens a ticket good for ttl: as long as the action it belongs to
// may take, since the extension posts a download's bytes only once the person
// answered its questions and the download finished. For an upload to the
// page, data is what the extension downloads; for a download from the page,
// the extension posts the bytes it fetched and FileResult reads them. The
// returned func removes the ticket, which the call does as soon as it ends.
func StageFile(data []byte, toPage bool, ttl time.Duration) (string, func(), error) {
	if len(data) > MaxFileBytes {
		return "", nil, errors.New("the file exceeds 32 MiB")
	}
	id := randomID()
	transfers.Store(id, &transfer{data: data, upload: !toPage, expires: time.Now().Add(ttl)})
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
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = w.Write(t.data) //nolint:gosec // G705: a workspace file handed to the extension as bytes, typed octet-stream with nosniff, never rendered as a page
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
