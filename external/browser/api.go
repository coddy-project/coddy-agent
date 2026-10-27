//go:build browser

package browser

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// remoteTurnIdle is how long a chat of another process may stay quiet before
// its tabs lose their "working" marks: the end of its turn happens where this
// server cannot see it. A variable for the tests.
var remoteTurnIdle = 45 * time.Second

// maxCallBody bounds a call: a file of MaxFileBytes in base64, and room for
// the rest of the request.
const maxCallBody = MaxFileBytes/3*4 + 1<<20

// Caller is the chat of another Coddy process that asks for an action.
type Caller struct {
	// Node is where the chat runs, as the caller names itself: its swarm name
	// or its host name. It is shown to the person, not trusted: the request's
	// credential is what lets it in.
	Node      string `json:"node"`
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
}

// FileBody is a file inside a call: what upload_file puts on the page, and
// what download_file brought back. Data is base64 on the wire.
type FileBody struct {
	Name string `json:"name"`
	Data []byte `json:"data,omitempty"`
}

// CallRequest is POST /coddy/browser/v1/call.
type CallRequest struct {
	BrowserID string          `json:"browser_id"`
	Action    string          `json:"action"`
	TabID     int             `json:"tab_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Caller    Caller          `json:"caller"`
	// File names the file of upload_file (with its bytes) and of
	// download_file (the name the page saves it under).
	File *FileBody `json:"file,omitempty"`
}

// CallResponse answers a call: what the extension answered, and the file of
// download_file.
type CallResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	File   *FileBody       `json:"file,omitempty"`
}

// BrowsersResponse is GET /coddy/browser/v1/browsers.
type BrowsersResponse struct {
	Browsers []Snapshot `json:"browsers"`
}

// RemoteOwner is the owner of the tabs of a chat of another process: its node
// and its session id. A session id of this process never holds a "/", so the
// two never meet.
func RemoteOwner(c Caller) (string, error) {
	if !validLabel(c.Node, 64) {
		return "", errors.New("caller.node is 1 to 64 letters, digits, dots, dashes or underscores")
	}
	if !validLabel(c.SessionID, 128) {
		return "", errors.New("caller.session_id is 1 to 128 letters, digits, dots, dashes or underscores")
	}
	return c.Node + "/" + c.SessionID, nil
}

func validLabel(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

// API serves the browsers of a hub to the chats of other Coddy processes. It
// sits behind the HTTP server's gate: whoever holds a credential of this
// server's API can already run turns here, so the routes give no new
// authority, and the person still decides in the extension.
type API struct {
	Hub *Hub
	// Allow is browser.serve_remote of the live configuration; nil serves.
	Allow func() bool
	Log   *slog.Logger

	mu   sync.Mutex
	idle map[string]*time.Timer
	// busy counts each remote chat's calls in flight: its quiet interval
	// starts only once the last of them ended.
	busy map[string]int
}

func (a *API) allowed() bool { return a.Allow == nil || a.Allow() }

// Register puts the API's routes on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc(APIBrowsersPattern, a.browsers)
	mux.HandleFunc(APICallPattern, a.call)
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeAPIJSON(w, status, map[string]string{"error": msg})
}

func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) browsers(w http.ResponseWriter, r *http.Request) {
	if !a.allowed() {
		http.NotFound(w, r)
		return
	}
	owner := ""
	q := r.URL.Query()
	if q.Get("node") != "" || q.Get("session_id") != "" {
		var err error
		if owner, err = RemoteOwner(Caller{Node: q.Get("node"), SessionID: q.Get("session_id")}); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeAPIJSON(w, http.StatusOK, BrowsersResponse{Browsers: a.Hub.List(owner)})
}

func (a *API) call(w http.ResponseWriter, r *http.Request) {
	if !a.allowed() {
		http.NotFound(w, r)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxCallBody+1))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "the request could not be read")
		return
	}
	if len(raw) > maxCallBody {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "a file moves 32 MiB at most")
		return
	}
	var req CallRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	owner, err := RemoteOwner(req.Caller)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(req.Caller.Title)
	if len(title) > 200 {
		title = title[:200]
	}
	transfer := req.Action == "upload_file" || req.Action == "download_file"
	if transfer && (req.File == nil || strings.TrimSpace(req.File.Name) == "" || len(req.File.Name) > 255) {
		writeAPIError(w, http.StatusBadRequest, req.Action+" needs file.name")
		return
	}
	call := Request{
		SessionID: owner, SessionTitle: title, Node: req.Caller.Node,
		BrowserID: req.BrowserID, Action: req.Action, TabID: req.TabID, Params: req.Params,
	}
	a.begin(owner)
	defer a.end(owner)
	var out CallResponse
	if transfer {
		var upload []byte
		if req.Action == "upload_file" {
			upload = req.File.Data
			if upload == nil {
				upload = []byte{}
			}
		}
		result, got, err := a.Hub.Transfer(r.Context(), call, req.File.Name, upload)
		if err != nil {
			writeAPIError(w, http.StatusConflict, err.Error())
			return
		}
		out.Result = result
		if got != nil {
			out.File = &FileBody{Name: req.File.Name, Data: got}
		}
	} else {
		if out.Result, err = a.Hub.Call(r.Context(), call); err != nil {
			writeAPIError(w, http.StatusConflict, err.Error())
			return
		}
	}
	writeAPIJSON(w, http.StatusOK, out)
}

// begin holds off the end of a remote chat's turn while one of its calls
// runs: a long download is not a quiet chat.
func (a *API) begin(owner string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy == nil {
		a.busy = make(map[string]int)
	}
	a.busy[owner]++
	if t := a.idle[owner]; t != nil {
		t.Stop()
		delete(a.idle, owner)
	}
}

// end starts the quiet interval after which a remote chat's turn counts as
// ended, once its last call in flight ended.
func (a *API) end(owner string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy[owner]--; a.busy[owner] > 0 {
		return
	}
	delete(a.busy, owner)
	if a.idle == nil {
		a.idle = make(map[string]*time.Timer)
	}
	var t *time.Timer
	t = time.AfterFunc(remoteTurnIdle, func() {
		a.mu.Lock()
		current := a.idle[owner] == t && a.busy[owner] == 0
		if current {
			delete(a.idle, owner)
		}
		a.mu.Unlock()
		if current {
			a.Hub.TurnEnded(owner)
		}
	})
	a.idle[owner] = t
}

// close stops the quiet intervals: the server is going away.
func (a *API) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for owner, t := range a.idle {
		t.Stop()
		delete(a.idle, owner)
	}
}
