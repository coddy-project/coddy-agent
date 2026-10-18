//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/canvas"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// canvasFilePattern is the route a frame's page and its files load from. It
// is authenticated by the capability in its path alone (canvasFile checks it),
// so the auth gate lets it through and the opaque-origin refusal spares it.
const canvasFilePattern = "GET /coddy/canvas/{capability}/{path...}"

// canvasFilePrefix is the path every frame address starts with.
const canvasFilePrefix = "/coddy/canvas/"

// maxCanvasReportBytes bounds a browser's report.
const maxCanvasReportBytes = 64 << 10

func (s *Server) registerCanvasRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/canvas", s.canvasBoard)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames", s.canvasAddFrame)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}/canvas/frames/{frame}", s.canvasPatchFrame)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/canvas/frames/{frame}", s.canvasRemoveFrame)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames/{frame}/rotate", s.canvasRotateFrame)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames/{frame}/report", s.canvasReport)
	s.mux.HandleFunc(canvasFilePattern, s.canvasFile)
}

func (s *Server) canvasEnabled() bool {
	c := s.activeCfg()
	return c == nil || c.Tools.Canvas.ResolvedEnabled()
}

func (s *Server) canvasSources() []string {
	if c := s.activeCfg(); c != nil {
		return c.Tools.Canvas.Sources()
	}
	return nil
}

// canvasKey signs frame addresses: the media key's secret, derived again for
// the canvas so a token of one kind never passes for the other.
func (s *Server) canvasKey() []byte {
	return canvas.DeriveKey(s.workspaceSigningKey())
}

// canvasBundle resolves the board a session's routes act on: the bundle of the
// top session of its spawn tree.
func (s *Server) canvasBundle(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.canvasEnabled() {
		http.NotFound(w, r)
		return "", false
	}
	st := s.coddyEnsureLoaded(w, r, r.PathValue("id"))
	if st == nil {
		return "", false
	}
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		writeSubagentsError(w, http.StatusConflict, "this session is not saved, so it has no canvas")
		return "", false
	}
	return canvas.BoardBundle(dir), true
}

type canvasReportSummary struct {
	Stamp    string    `json:"stamp"`
	Loaded   bool      `json:"loaded"`
	Closed   bool      `json:"closed,omitempty"`
	Error    string    `json:"error,omitempty"`
	Title    string    `json:"title,omitempty"`
	Errors   int       `json:"errors"`
	Warnings int       `json:"warnings"`
	At       time.Time `json:"at"`
}

type canvasFrameJSON struct {
	ID        string               `json:"id"`
	Path      string               `json:"path"`
	Root      string               `json:"root"`
	Title     string               `json:"title,omitempty"`
	Device    string               `json:"device,omitempty"`
	Width     int                  `json:"width"`
	Height    int                  `json:"height"`
	X         int                  `json:"x"`
	Y         int                  `json:"y"`
	AddedBy   string               `json:"added_by,omitempty"`
	AddedAt   time.Time            `json:"added_at"`
	ShownAt   time.Time            `json:"shown_at"`
	Gen       int                  `json:"gen"`
	Stamp     string               `json:"stamp"`
	URL       string               `json:"url"`
	ExpiresAt time.Time            `json:"expires_at"`
	Report    *canvasReportSummary `json:"report,omitempty"`
}

type canvasBoardJSON struct {
	SessionID string            `json:"session_id"`
	Revision  int64             `json:"revision"`
	Frames    []canvasFrameJSON `json:"frames"`
}

// frameJSON is a frame as the web UI reads it: with its stamp, a fresh
// address relative to the API base and the newest report's counts.
func (s *Server) frameJSON(sessionID string, f canvas.Frame, now time.Time) canvasFrameJSON {
	token := canvas.SignCapability(s.canvasKey(), canvas.Claim{
		Session: sessionID, Frame: f.ID, Workspace: workspaceIdentity(f.Workspace), Root: f.Root, Gen: f.Gen,
	}, now)
	out := canvasFrameJSON{
		ID: f.ID, Path: f.Path, Root: f.Root, Title: f.Title, Device: f.Device, Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y, AddedBy: f.AddedBy, AddedAt: f.AddedAt, ShownAt: f.ShownAt, Gen: f.Gen,
		Stamp:     canvas.Stamp(f.Workspace, f.Root),
		URL:       canvasFilePrefix + token + "/" + escapeSlashPath(f.PageInRoot()),
		ExpiresAt: now.Add(canvas.CapabilityTTL).UTC(),
	}
	if rep, ok := canvas.DefaultReports().Get(sessionID, f.ID); ok {
		sum := &canvasReportSummary{Stamp: rep.Stamp, Loaded: rep.Loaded, Closed: rep.Closed, Error: rep.Error, Title: rep.Title, At: rep.At}
		for _, e := range rep.Console {
			if e.Level == "warning" {
				sum.Warnings++
			} else {
				sum.Errors++
			}
		}
		out.Report = sum
	}
	return out
}

func escapeSlashPath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (s *Server) boardJSON(bundle string, b canvas.Board) canvasBoardJSON {
	sid := canvas.SessionIDOfBundle(bundle)
	now := time.Now()
	out := canvasBoardJSON{SessionID: sid, Revision: b.Revision, Frames: make([]canvasFrameJSON, 0, len(b.Frames))}
	for _, f := range b.Frames {
		out.Frames = append(out.Frames, s.frameJSON(sid, f, now))
	}
	return out
}

func (s *Server) canvasBoard(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	b, err := canvas.Load(bundle)
	if err != nil {
		writeSubagentsError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.boardJSON(bundle, b))
}

func writeCanvasError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, canvas.ErrFrameNotFound):
		writeSubagentsError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, canvas.ErrTooManyFrames):
		writeSubagentsError(w, http.StatusConflict, err.Error())
	default:
		writeSubagentsError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) canvasAddFrame(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	var req struct {
		Path   string `json:"path"`
		Root   string `json:"root"`
		Title  string `json:"title"`
		Device string `json:"device"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req) != nil {
		writeSubagentsError(w, http.StatusBadRequest, "invalid frame request")
		return
	}
	// The page is named relative to the session's workspace; an absolute path
	// or a climb out is refused by canvas.Show like the tool's.
	if rel, err := session.NormalizeWorkspaceRelativePath(req.Path); err != nil || rel == "" {
		writeSubagentsError(w, http.StatusBadRequest, "invalid workspace path")
		return
	}
	st := s.mgr.SessionByID(r.PathValue("id"))
	if st == nil {
		writeSubagentsError(w, http.StatusNotFound, "session not found")
		return
	}
	res, err := canvas.Show(bundle, canvas.ShowRequest{
		Workspace: st.GetCWD(), Path: req.Path, Root: req.Root, Title: req.Title,
		Device: req.Device, Width: req.Width, Height: req.Height, AddedBy: "user",
	})
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reason": res.Reason, "frame": s.frameJSON(canvas.SessionIDOfBundle(bundle), res.Frame, time.Now()), "revision": res.Board.Revision})
}

func (s *Server) canvasPatchFrame(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	var req struct {
		Title  *string `json:"title"`
		Device *string `json:"device"`
		Width  *int    `json:"width"`
		Height *int    `json:"height"`
		X      *int    `json:"x"`
		Y      *int    `json:"y"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req) != nil {
		writeSubagentsError(w, http.StatusBadRequest, "invalid frame change")
		return
	}
	b, f, err := canvas.Update(bundle, r.PathValue("frame"), canvas.Patch{Title: req.Title, Device: req.Device, Width: req.Width, Height: req.Height, X: req.X, Y: req.Y})
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"frame": s.frameJSON(canvas.SessionIDOfBundle(bundle), f, time.Now()), "revision": b.Revision})
}

func (s *Server) canvasRemoveFrame(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	frame := r.PathValue("frame")
	b, err := canvas.Remove(bundle, frame)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	canvas.DefaultReports().Forget(canvas.SessionIDOfBundle(bundle), frame)
	writeJSON(w, http.StatusOK, map[string]any{"revision": b.Revision})
}

func (s *Server) canvasRotateFrame(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	b, f, err := canvas.Rotate(bundle, r.PathValue("frame"))
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"frame": s.frameJSON(canvas.SessionIDOfBundle(bundle), f, time.Now()), "revision": b.Revision})
}

func (s *Server) canvasReport(w http.ResponseWriter, r *http.Request) {
	bundle, ok := s.canvasBundle(w, r)
	if !ok {
		return
	}
	frame := r.PathValue("frame")
	b, err := canvas.Load(bundle)
	if err != nil {
		writeSubagentsError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, ok := b.Frame(frame); !ok {
		writeSubagentsError(w, http.StatusNotFound, "no such frame on the canvas")
		return
	}
	var req struct {
		Stamp   string                `json:"stamp"`
		Loaded  bool                  `json:"loaded"`
		Closed  bool                  `json:"closed"`
		Error   string                `json:"error"`
		Title   string                `json:"title"`
		Console []canvas.ConsoleEntry `json:"console"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, maxCanvasReportBytes)).Decode(&req) != nil {
		writeSubagentsError(w, http.StatusBadRequest, "invalid report")
		return
	}
	canvas.DefaultReports().Put(canvas.Report{
		SessionID: canvas.SessionIDOfBundle(bundle), FrameID: frame, Stamp: req.Stamp,
		Loaded: req.Loaded, Closed: req.Closed, Error: req.Error, Title: req.Title, Console: req.Console,
	})
	w.WriteHeader(http.StatusNoContent)
}

// canvasFile serves a frame's files. The capability names the session, the
// frame, its generation, its root and its workspace, and the request is
// served only while the board still holds that frame so: a frame removed,
// pointed elsewhere or rotated takes every address of it with it.
func (s *Server) canvasFile(w http.ResponseWriter, r *http.Request) {
	if !s.canvasEnabled() {
		http.NotFound(w, r)
		return
	}
	claim, err := canvas.VerifyCapability(s.canvasKey(), r.PathValue("capability"), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	fs := s.mgr.FileStore()
	if fs == nil || fs.Root == "" || session.ValidateFolderSessionID(claim.Session) != nil {
		http.NotFound(w, r)
		return
	}
	bundle := fs.SessionPath(claim.Session)
	b, err := canvas.Load(bundle)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, ok := b.Frame(claim.Frame)
	if !ok || f.Gen != claim.Gen || f.Root != claim.Root || workspaceIdentity(f.Workspace) != claim.Workspace {
		http.Error(w, "this canvas address no longer applies; reload the canvas", http.StatusUnauthorized)
		return
	}
	canvas.ServeFrame(w, r, f, r.PathValue("path"), canvas.ServeOptions{AllowedSources: s.canvasSources()})
}

// isCanvasFilePath reports whether a request path is a frame's address.
func isCanvasFilePath(p string) bool {
	return strings.HasPrefix(p, canvasFilePrefix)
}

// canvasChangedFrame is the thin event that tells the web UI to read a
// session's board again.
func canvasChangedFrame(c canvas.Change, at time.Time) []byte {
	body, err := json.Marshal(map[string]interface{}{
		"object":    "coddy.canvas_changed",
		"sessionId": c.SessionID,
		"revision":  c.Revision,
		"frameId":   c.FrameID,
		"reason":    c.Reason,
		"at":        at.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil
	}
	frame := make([]byte, 0, len(body)+40)
	frame = append(frame, "event: canvas_changed\ndata: "...)
	frame = append(frame, body...)
	frame = append(frame, "\n\n"...)
	return frame
}

// publishCanvasChanged tells every events subscriber that a board changed.
func (s *Server) publishCanvasChanged(c canvas.Change) {
	if s.events == nil || c.SessionID == "" {
		return
	}
	if frame := canvasChangedFrame(c, time.Now()); frame != nil {
		s.events.publish(frame)
	}
}
