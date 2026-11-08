//go:build desktop

// Package shell is the page the desktop window opens on: a tab strip over one
// iframe per chat, each frame the web UI of this same server.
//
// It is deliberately a plain page - one HTML file, one stylesheet, one script,
// no React and no vite entry - because it is a frame around the SPA rather
// than part of it, and because a window backend must be able to open it
// without the SPA build having run.
//
// The server mounts it under the "/" catch-all rather than as a route of its
// own. That is not a detail: external/httpserver/auth.go exempts "" and "/"
// from the authentication gate and protects every registered pattern, so a
// shell served as `GET /desktop/` would answer 401 to a browser that has not
// signed in - which is every browser the first time a window opens, in a
// profile of its own with no cookie. Under the catch-all it gets the SPA's own
// treatment, and that is right: the page itself carries no data, and every
// title it draws still comes from the gated /coddy/sessions.
package shell

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// WindowControl is what the tab strip can do to the window around it: the
// buttons a frameless window has to carry itself, and the drag that replaces
// its title bar. A page cannot do any of it - no web API minimises, maximises
// or moves the window a document is drawn in - so the strip asks the process
// that started the browser, which talks to the window manager.
//
// The interface lives here rather than beside its implementation because this
// package is the one both sides already import.
type WindowControl interface {
	// Supported reports whether this machine can be asked at all. A strip
	// draws no window buttons when it answers false.
	Supported() bool
	Undecorate() error
	Decorate() error
	Minimise() error
	ToggleMaximise() error
	Close() error
	// BeginMove remembers where the window is, so the moves that follow are
	// measured from it, and lets a window the window manager has pinned -
	// maximised, or tiled against an edge - loose first, since such a window
	// refuses to be moved at all. x and y are where the pointer took hold, so
	// a window that gets its own size back can be put back under it.
	BeginMove(x, y int) error
	// MoveBy puts the window that far from where BeginMove found it.
	MoveBy(dx, dy int) error
	// WaitForWindow blocks until the window is on the display, or the bound
	// passes. A window that never appears is not an error.
	WaitForWindow(bound time.Duration) bool
}

// ChordSource is a window control that can also hand the strip the chords the
// browser answers itself.
//
// `Ctrl+T` and `Ctrl+W` never reach this page: a Chromium window opened with
// --app answers both of them on its own - a new browser window, and closing
// the application - and a page cannot cancel either, whatever it does in a key
// handler. The launcher takes them at the X server instead, with a passive
// grab on the window, and the strip asks for them here. Every other chord
// (`Alt`, and `Ctrl+1`..`Ctrl+9`, which the window does hand to the page) is
// read off the keyboard in the page itself.
type ChordSource interface {
	// NextChord answers the next chord taken from the window, or an empty
	// string when ctx ends before one is pressed.
	NextChord(ctx context.Context) string
}

// Settings are the window's own switches, the ones the menu in the bar draws.
// They belong to the machine rather than to the page: a tray icon, an entry in
// the session's autostart folder and the system frame outlive the document, and
// the browser scopes localStorage to the port, which moves.
type Settings struct {
	// Framed keeps the system title bar and borders. Without it the strip is
	// the title bar and carries the window's own buttons.
	Framed bool `json:"framed"`
	// Tray shows Coddy's mark in the system tray while the window runs.
	Tray bool `json:"tray"`
	// Autostart opens the window when this session starts.
	Autostart bool `json:"autostart"`
	// StartMinimised opens the window minimised, which is what an autostarted
	// window usually wants.
	StartMinimised bool `json:"start_minimised"`
}

// SettingsStore is where a launcher keeps them and what applies a change to
// this machine.
type SettingsStore interface {
	Settings() Settings
	SetSettings(Settings) error
}

// Prefix is where the shell answers. Everything it serves lives under it, so
// one prefix is all a mount or a probe needs.
const Prefix = "/desktop/"

// MarkerHeader is how a launcher tells a Coddy desktop from anything else that
// happens to be listening on the port it remembered. A plain 200 is not enough
// to decide that, and taking the port from a stranger - or handing a window to
// one - is worse than picking another port.
const MarkerHeader = "X-Coddy-Desktop"

//go:embed index.html shell.css shell.js
var assets embed.FS

// Handler serves the shell, and the window actions of the strip when a caller
// gave it a window to act on (nil draws no window buttons). It answers only
// the files it has; anything else under the prefix is a 404, so a typo does
// not fall through to the SPA and come back as a chat.
func Handler(ctl WindowControl) http.Handler {
	// One window, one drag at a time, so the gesture's state belongs to the
	// handler rather than to a request.
	drag := &dragGesture{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, Prefix) {
		case windowAction:
			serveWindowAction(w, r, ctl, drag)
			return
		case windowOpen:
			serveOpen(w, r, ctl)
			return
		case windowKeys:
			serveChords(w, r, ctl)
			return
		case windowSettings:
			serveSettings(w, r, ctl)
			return
		case windowMenu:
			serveMenu(w, r, ctl)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, Prefix)), "/")
		if name == "" || name == "." || name == "index.html" {
			serveAsset(w, "index.html", "text/html; charset=utf-8", true)
			return
		}
		switch name {
		case "shell.css":
			serveAsset(w, name, "text/css; charset=utf-8", false)
		case "shell.js":
			serveAsset(w, name, "text/javascript; charset=utf-8", false)
		default:
			http.NotFound(w, r)
		}
	})
}

// The routes of the shell that are not files: what the strip asks of the
// window around it, the chords the browser would otherwise answer itself, and
// the switches of the menu.
const (
	windowAction   = "window"
	windowKeys     = "keys"
	windowSettings = "settings"
	windowMenu     = "menu"
	windowOpen     = "open"
)

// chordWait bounds one long poll for a chord. It is long because a key is
// pressed rarely and a request that waits costs nothing, and bounded because a
// request that never answers is one a proxy, a browser or a reload would drop
// without either side noticing.
const chordWait = 25 * time.Second

// serveChords hands the strip the next chord the launcher took from the
// window, waiting until one is pressed or the poll runs out.
func serveChords(w http.ResponseWriter, r *http.Request, ctl WindowControl) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Same-origin like every other route here: a page from somewhere else
	// could not read the answer, but it could take a chord out of the queue
	// and the window would miss the key its operator pressed.
	if !sameOrigin(r) {
		http.Error(w, "the window is driven by its own page only", http.StatusForbidden)
		return
	}
	src, ok := ctl.(ChordSource)
	if !ok {
		http.Error(w, "this window takes no chords", http.StatusNotImplemented)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chordWait)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"chord": src.NextChord(ctx)})
}

// serveMenu hands the strip the rows of the menu behind its button.
//
// The page draws what it is given rather than a list of its own, because the
// tray icon draws the same rows from the same function (MenuRows) and the two
// menus have to offer the same thing.
func serveMenu(w http.ResponseWriter, r *http.Request, ctl WindowControl) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store, ok := ctl.(SettingsStore)
	if !ok {
		http.Error(w, "this window keeps no settings", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": MenuRows(store.Settings(), MenuInBar, ctl.Supported()),
	})
}

// serveSettings reads and writes the switches of the menu.
func serveSettings(w http.ResponseWriter, r *http.Request, ctl WindowControl) {
	store, ok := ctl.(SettingsStore)
	if !ok {
		http.Error(w, "this window keeps no settings", http.StatusNotImplemented)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(store.Settings())
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "the window is driven by its own page only", http.StatusForbidden)
			return
		}
		// The current settings are the defaults, so a page that sends one
		// switch changes one switch.
		next := store.Settings()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&next); err != nil {
			http.Error(w, "the settings are a small JSON object", http.StatusBadRequest)
			return
		}
		if err := store.SetSettings(next); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(store.Settings())
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveWindowAction answers the strip's buttons.
//
// It is reachable without a credential, because the shell is served under the
// catch-all the gate leaves open, so it refuses anything but a same-origin
// request of its own page: minimising or closing somebody's window from a page
// they are merely visiting is a nuisance worth closing off, and nothing here
// reads or writes a single byte of the session.
// serveOpen hands a link out of Coddy to this machine's own browser.
func serveOpen(w http.ResponseWriter, r *http.Request, ctl WindowControl) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "the window is driven by its own page only", http.StatusForbidden)
		return
	}
	opener, ok := ctl.(ExternalOpener)
	if !ok {
		http.Error(w, "this window opens nothing outside itself", http.StatusNotImplemented)
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxExternalURL+1<<10)).Decode(&body); err != nil {
		http.Error(w, "a link is a small JSON object", http.StatusBadRequest)
		return
	}
	link, ok := ExternalURL(body.URL)
	if !ok {
		http.Error(w, "only an http or https address is opened outside the window", http.StatusBadRequest)
		return
	}
	if err := opener.OpenExternal(link); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dragGesture is whether a drag of the bar is under way.
//
// The page sends the start of a drag and every move of it as separate
// requests, and a browser is free to deliver them in any order it likes: the
// first move used to overtake the start and be measured from wherever the
// previous drag had left the window, which threw the window sideways for a
// frame or two before the start landed and put it right. A move that arrives
// with no gesture behind it is refused, and the page waits for the start to be
// answered before it sends one.
type dragGesture struct {
	mu   sync.Mutex
	open bool
}

func (g *dragGesture) begin() {
	g.mu.Lock()
	g.open = true
	g.mu.Unlock()
}

func (g *dragGesture) end() {
	g.mu.Lock()
	g.open = false
	g.mu.Unlock()
}

func (g *dragGesture) under() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

func serveWindowAction(w http.ResponseWriter, r *http.Request, ctl WindowControl, drag *dragGesture) {
	if r.Method == http.MethodGet {
		// What the page asks before it draws: a strip that drew a close
		// button nothing answers would be worse than a system frame.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]bool{"supported": ctl != nil && ctl.Supported()})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "the window is driven by its own page only", http.StatusForbidden)
		return
	}
	if ctl == nil || !ctl.Supported() {
		http.Error(w, "this window cannot be asked", http.StatusNotImplemented)
		return
	}
	var body struct {
		Action string `json:"action"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		http.Error(w, "a window action is a small JSON object", http.StatusBadRequest)
		return
	}
	var err error
	switch body.Action {
	case "minimise":
		err = ctl.Minimise()
	case "maximise":
		err = ctl.ToggleMaximise()
	case "close":
		err = ctl.Close()
	case "move-begin":
		if err = ctl.BeginMove(body.X, body.Y); err == nil {
			drag.begin()
		}
	case "move-by":
		if !drag.under() {
			http.Error(w, "no drag of the bar has begun", http.StatusConflict)
			return
		}
		err = ctl.MoveBy(body.X, body.Y)
	case "move-end":
		drag.end()
	case "decorate":
		err = ctl.Decorate()
	case "undecorate":
		err = ctl.Undecorate()
	default:
		http.Error(w, "no such window action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sameOrigin keeps the window actions to the shell's own page. Every browser
// that can open this window sends Sec-Fetch-Site; a request without it is
// taken only when it names no other origin.
func sameOrigin(r *http.Request) bool {
	switch strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) {
	case "same-origin", "none":
		return true
	case "":
		return strings.TrimSpace(r.Header.Get("Origin")) == ""
	default:
		return false
	}
}

func serveAsset(w http.ResponseWriter, name, contentType string, marker bool) {
	b, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The files change with the binary and the window reloads rarely, so there
	// is nothing to gain from caching and something to lose after an update.
	w.Header().Set("Cache-Control", "no-store")
	if marker {
		w.Header().Set(MarkerHeader, "1")
	}
	// G705: b is an asset embedded in this binary, never anything a request carried.
	_, _ = w.Write(b) //nolint:gosec // G705: an embedded asset, not request data.
}
