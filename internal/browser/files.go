package browser

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Transfers carry bytes, never server paths. Tickets exist only for the life
// of a tool call and cannot be used to read arbitrary workspace files.
type Transfer struct {
	mu      sync.Mutex
	Name    string
	Data    []byte
	Upload  bool
	Used    bool
	Expires time.Time
}

var transfers sync.Map

func StageFile(name string, data []byte, upload bool) (string, func(), error) {
	if len(data) > MaxFileBytes {
		return "", nil, errors.New("file exceeds 32 MiB")
	}
	id := randomID()
	transfers.Store(id, &Transfer{Name: name, Data: data, Upload: upload, Expires: time.Now().Add(2 * time.Minute)})
	return id, func() { transfers.Delete(id) }, nil
}

func FileResult(id string) ([]byte, error) {
	v, ok := transfers.Load(id)
	if !ok {
		return nil, errors.New("transfer expired")
	}
	t := v.(*Transfer)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.Upload || !t.Used {
		return nil, errors.New("browser did not transfer the file")
	}
	return t.Data, nil
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/browser/v1/files/")
	v, ok := transfers.Load(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	t := v.(*Transfer)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Used || time.Now().After(t.Expires) {
		http.Error(w, "transfer expired or used", http.StatusGone)
		return
	}
	if r.Method == "GET" && !t.Upload {
		t.Used = true
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(t.Data)
		return
	}
	if r.Method == "POST" && t.Upload {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxFileBytes))
		if err != nil {
			http.Error(w, "file exceeds 32 MiB or transfer failed", http.StatusBadRequest)
			return
		}
		t.Data, t.Used = data, true
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
