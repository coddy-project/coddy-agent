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
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/open", s.canvasOpen)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames", s.canvasAddFrame)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/canvas/frames/{frame}", s.canvasRemoveFrame)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames/{frame}/rotate", s.canvasRotateFrame)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/canvas/frames/{frame}/report", s.canvasReport)
	s.mux.HandleFunc("PATCH /coddy/sessions/{id}/canvas/nodes", s.canvasPatchNodes)
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

// canvasRef resolves the board a session's routes act on: the bookkeeping of
// the top session of its spawn tree, the session's working directory and the
// .canvas file the file query names ("" when it names none and need is false).
func (s *Server) canvasRef(w http.ResponseWriter, r *http.Request, need bool) (canvas.Ref, bool) {
	if !s.canvasEnabled() {
		http.NotFound(w, r)
		return canvas.Ref{}, false
	}
	st := s.coddyEnsureLoaded(w, r, r.PathValue("id"))
	if st == nil {
		return canvas.Ref{}, false
	}
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		writeSubagentsError(w, http.StatusConflict, "this session is not saved, so it has no canvas")
		return canvas.Ref{}, false
	}
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	if file != "" {
		// The file is named relative to the session's workspace; an absolute
		// path or a climb out is refused here and again by the canvas.
		rel, err := session.NormalizeWorkspaceRelativePath(file)
		if err != nil || rel == "" || !canvas.IsCanvasFile(rel) {
			writeSubagentsError(w, http.StatusBadRequest, "file must name a .canvas file of the working directory")
			return canvas.Ref{}, false
		}
		file = rel
	} else if need {
		writeSubagentsError(w, http.StatusBadRequest, "name the .canvas file with the file query")
		return canvas.Ref{}, false
	}
	return canvas.Ref{Bundle: canvas.BoardBundle(dir), Workspace: st.GetCWD(), File: file}, true
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
	Gen       int                  `json:"gen"`
	Stamp     string               `json:"stamp"`
	URL       string               `json:"url"`
	ExpiresAt time.Time            `json:"expires_at"`
	Report    *canvasReportSummary `json:"report,omitempty"`
}

type canvasBoardJSON struct {
	SessionID string `json:"session_id"`
	// File is the board's .canvas file, "" when the chat has none yet.
	File string `json:"file"`
	// Current says the agent shows pages on this file when it names none.
	Current  bool              `json:"current"`
	Revision int64             `json:"revision"`
	Stamp    string            `json:"stamp,omitempty"`
	Error    string            `json:"error,omitempty"`
	Frames   []canvasFrameJSON `json:"frames"`
	Items    []canvas.Item     `json:"items"`
	Edges    []canvas.Edge     `json:"edges"`
}

// frameJSON is a frame as the web UI reads it: with its stamp, a fresh
// address relative to the API base and the newest report's counts.
func (s *Server) frameJSON(sessionID, file string, f canvas.Frame, now time.Time) canvasFrameJSON {
	token := canvas.SignCapability(s.canvasKey(), canvas.Claim{
		Session: sessionID, File: file, Frame: f.ID, Workspace: workspaceIdentity(f.Workspace), Root: f.Root, Gen: f.Gen,
	}, now)
	out := canvasFrameJSON{
		ID: f.ID, Path: f.Path, Root: f.Root, Title: f.Title, Device: f.Device, Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y, AddedBy: f.AddedBy, Gen: f.Gen,
		Stamp:     canvas.Stamp(f.Workspace, f.Root),
		URL:       canvasFilePrefix + token + "/" + escapeSlashPath(f.PageInRoot()),
		ExpiresAt: now.Add(canvas.CapabilityTTL).UTC(),
	}
	if rep, ok := canvas.DefaultReports().Get(sessionID, canvas.NodeKey(file, f.ID)); ok {
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

func (s *Server) boardJSON(ref canvas.Ref, b canvas.Board) canvasBoardJSON {
	sid := canvas.SessionIDOfBundle(ref.Bundle)
	now := time.Now()
	out := canvasBoardJSON{
		SessionID: sid, File: b.File, Revision: b.Revision, Stamp: canvas.FileStamp(b), Error: b.Error,
		Frames: make([]canvasFrameJSON, 0, len(b.Frames)), Items: b.Items, Edges: b.Edges,
	}
	if out.Items == nil {
		out.Items = []canvas.Item{}
	}
	if out.Edges == nil {
		out.Edges = []canvas.Edge{}
	}
	if cur, err := canvas.Current(ref.Bundle, ref.Workspace); err == nil {
		out.Current = cur == b.File
	}
	for _, f := range b.Frames {
		out.Frames = append(out.Frames, s.frameJSON(sid, b.File, f, now))
	}
	return out
}

// canvasBoard answers a board: the file the query names, or the chat's
// current one, or an empty answer naming no file while the chat has none.
func (s *Server) canvasBoard(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, false)
	if !ok {
		return
	}
	if ref.File == "" {
		cur, err := canvas.Current(ref.Bundle, ref.Workspace)
		if err != nil {
			writeSubagentsError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if cur == "" {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, canvasBoardJSON{
				SessionID: canvas.SessionIDOfBundle(ref.Bundle), Frames: []canvasFrameJSON{}, Items: []canvas.Item{}, Edges: []canvas.Edge{},
			})
			return
		}
		ref.File = cur
	}
	b, err := canvas.Load(ref)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.boardJSON(ref, b))
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

// canvasOpen makes a .canvas file the one the agent shows pages on: the web
// UI asks for it when the person opens the file.
func (s *Server) canvasOpen(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, true)
	if !ok {
		return
	}
	b, err := canvas.Open(ref)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.boardJSON(ref, b))
}

func (s *Server) canvasAddFrame(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, false)
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
	res, err := canvas.Show(ref, canvas.ShowRequest{
		Path: req.Path, Root: req.Root, Title: req.Title,
		Device: req.Device, Width: req.Width, Height: req.Height, AddedBy: "user",
	})
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	sid := canvas.SessionIDOfBundle(ref.Bundle)
	writeJSON(w, http.StatusOK, map[string]any{
		"reason": res.Reason, "file": res.Board.File, "created": res.Created,
		"frame": s.frameJSON(sid, res.Board.File, res.Frame, time.Now()), "revision": res.Board.Revision,
	})
}

func (s *Server) canvasRemoveFrame(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, true)
	if !ok {
		return
	}
	frame := r.PathValue("frame")
	b, err := canvas.Remove(ref, frame)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	canvas.DefaultReports().Forget(canvas.SessionIDOfBundle(ref.Bundle), canvas.NodeKey(b.File, frame))
	writeJSON(w, http.StatusOK, map[string]any{"revision": b.Revision})
}

func (s *Server) canvasRotateFrame(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, true)
	if !ok {
		return
	}
	b, f, err := canvas.Rotate(ref, r.PathValue("frame"))
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"frame": s.frameJSON(canvas.SessionIDOfBundle(ref.Bundle), b.File, f, time.Now()), "revision": b.Revision})
}

func (s *Server) canvasPatchNodes(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Nodes []struct {
			ID     string  `json:"id"`
			X      *int    `json:"x"`
			Y      *int    `json:"y"`
			Width  *int    `json:"width"`
			Height *int    `json:"height"`
			Device *string `json:"device"`
		} `json:"nodes"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&req) != nil {
		writeSubagentsError(w, http.StatusBadRequest, "invalid node change")
		return
	}
	changes := make([]canvas.NodeChange, 0, len(req.Nodes))
	for _, n := range req.Nodes {
		changes = append(changes, canvas.NodeChange{ID: n.ID, X: n.X, Y: n.Y, Width: n.Width, Height: n.Height, Device: n.Device})
	}
	b, err := canvas.UpdateNodes(ref, changes)
	if err != nil {
		writeCanvasError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": b.Revision})
}

func (s *Server) canvasReport(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.canvasRef(w, r, true)
	if !ok {
		return
	}
	frame := r.PathValue("frame")
	b, err := canvas.Load(ref)
	if err != nil {
		writeCanvasError(w, err)
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
		SessionID: canvas.SessionIDOfBundle(ref.Bundle), FrameID: canvas.NodeKey(b.File, frame), Stamp: req.Stamp,
		Loaded: req.Loaded, Closed: req.Closed, Error: req.Error, Title: req.Title, Console: req.Console,
	})
	w.WriteHeader(http.StatusNoContent)
}

// canvasFile serves a frame's files. The capability names the session, the
// board's file, the frame, its generation, its root and its workspace, and the
// request is served only while the file still holds that frame so, in the
// session's working directory: a frame removed, pointed elsewhere or rotated
// takes every address of it with it.
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
	cwd := ""
	if st := s.mgr.SessionByID(claim.Session); st != nil {
		cwd = st.GetCWD()
	} else if meta, err := fs.ReadMeta(claim.Session); err == nil {
		cwd = meta.CWD
	}
	stale := func() {
		http.Error(w, "this canvas address no longer applies; reload the canvas", http.StatusUnauthorized)
	}
	if cwd == "" || workspaceIdentity(cwd) != claim.Workspace {
		stale()
		return
	}
	f, err := canvas.FrameOf(canvas.Ref{Bundle: fs.SessionPath(claim.Session), Workspace: cwd, File: claim.File}, claim.Frame)
	if err != nil || f.Gen != claim.Gen || f.Root != claim.Root {
		stale()
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
		"file":      c.File,
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
