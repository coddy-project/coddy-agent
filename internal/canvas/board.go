// Package canvas is the board of live frames the web UI shows in the Files
// window: a .canvas file of the workspace (JSON Canvas, the format of Obsidian's
// canvases) whose file nodes naming HTML pages run as live frames and whose
// other nodes and edges are drawn as they are. The pages are ordinary files
// written with the file tools, and the board is a file too, so git, the files
// window, Obsidian and every surface see the same thing.
//
// The package owns the board files (read, and written back byte for byte as
// Obsidian writes them), a chat's own bookkeeping in its bundle (the file the
// agent shows pages on, the generations of frame addresses, roots wider than a
// page's folder), the stamp that tells a browser a frame's files changed, the
// capability that addresses a frame's files, the serving of those files
// (sandbox policy, the bridge script) and the reports a browser sends back
// about a frame it runs. The tools (internal/tools/canvas), the HTTP routes
// (external/httpserver) and the swarm relay build on it.
package canvas

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// FileName is a chat's canvas bookkeeping inside its session bundle.
const FileName = "canvas.json"

// MaxFrames bounds a board: every frame is a live document in the browser.
const MaxFrames = 24

// Size bounds of a frame, in CSS pixels.
const (
	MinWidth  = 320
	MaxWidth  = 3840
	MinHeight = 320
	MaxHeight = 4320
)

// frameGap is the room between two frames placed side by side on the board.
const frameGap = 80

// Devices a frame can be sized by name.
const (
	DeviceDesktop = "desktop"
	DeviceTablet  = "tablet"
	DevicePhone   = "phone"
)

// Reasons a board changed, as the change hook and the HTTP event carry them.
const (
	ReasonAdded   = "added"
	ReasonShown   = "shown"
	ReasonUpdated = "updated"
	ReasonRemoved = "removed"
	// ReasonOpened is a .canvas file becoming the one the agent shows pages on.
	ReasonOpened = "opened"
)

var deviceSizes = map[string][2]int{
	DeviceDesktop: {1280, 800},
	DeviceTablet:  {820, 1180},
	DevicePhone:   {390, 844},
}

// DeviceSize returns the width and height of a named device.
func DeviceSize(device string) (width, height int, ok bool) {
	s, ok := deviceSizes[device]
	return s[0], s[1], ok
}

var (
	// ErrFrameNotFound is a node id the board does not hold.
	ErrFrameNotFound = errors.New("no such frame on the canvas")
	// ErrTooManyFrames refuses a frame past MaxFrames.
	ErrTooManyFrames = fmt.Errorf("the canvas already holds %d frames; remove one from the canvas first", MaxFrames)
)

// Frame is one page on the board: a file node of the board's file.
type Frame struct {
	ID string
	// Path is the page, slash-separated and relative to Workspace.
	Path string
	// Root is the folder the page may load files from, relative to Workspace;
	// "" is the workspace itself. It always contains Path.
	Root string
	// Workspace is the folder Path and Root are relative to: the chat's working
	// directory, the vault of the board's file.
	Workspace string
	Title     string
	Device    string
	Width     int
	Height    int
	X         int
	Y         int
	AddedBy   string
	// Gen is the frame's generation: its address is signed for one, so moving
	// the frame to the next (Rotate, or pointing it at another page) revokes
	// every address handed out before.
	Gen int
}

// PageInRoot is the page's path relative to the frame's root.
func (f Frame) PageInRoot() string {
	if f.Root == "" {
		return f.Path
	}
	return strings.TrimPrefix(f.Path, f.Root+"/")
}

// Ref names one board as one chat sees it.
type Ref struct {
	// Bundle is the bundle of the top session of the chat's spawn tree
	// (BoardBundle), which keeps the chat's bookkeeping.
	Bundle string
	// Workspace is the chat's working directory, the vault of the file.
	Workspace string
	// File is the .canvas file, relative to Workspace.
	File string
}

// Board is a .canvas file as the canvas shows it.
type Board struct {
	// File is the board's file, slash-separated and relative to Workspace.
	File      string
	Workspace string
	// Revision moves with every change made through the canvas, in any of the
	// chat's files.
	Revision int64
	Frames   []Frame
	// Items and Edges are the file's other nodes and its lines.
	Items []Item
	Edges []Edge
	// Error says why the file cannot be read now; nothing of it is shown and
	// nothing is written to it then.
	Error string

	state State
	inDoc map[string]bool
	// read is each frame of the file as it was read, so a write touches only
	// the frames that changed.
	read map[string]Frame
	// readItems are its other nodes as read, so only the ones dragged here
	// are written.
	readItems map[string]Item
	fileOK    bool
	// dirty says a change touched the file's nodes; only then is it written,
	// so reading a board never rewrites its file.
	dirty bool
}

// Frame returns the frame with this id.
func (b Board) Frame(id string) (Frame, bool) {
	for _, f := range b.Frames {
		if f.ID == id {
			return f, true
		}
	}
	return Frame{}, false
}

// State is a chat's canvas bookkeeping in the bundle of the top session of
// its spawn tree: what the files themselves must not decide.
type State struct {
	Version  int   `json:"version"`
	Revision int64 `json:"revision"`
	// Workspace is the working directory the bookkeeping is about; Current
	// and Roots apply only there.
	Workspace string `json:"workspace,omitempty"`
	// Current is the .canvas file canvas_show shows pages on when it names
	// none: the last one shown on or opened.
	Current string `json:"current,omitempty"`
	// Gens are the generations of the frames' addresses, by NodeKey. A frame
	// removed through the canvas keeps its generation one up, so none of its
	// addresses applies again should a node of that id come back.
	Gens map[string]int `json:"gens,omitempty"`
	// Roots are the frames' roots that are wider than the page's own folder
	// ("." for the whole workspace), by NodeKey: a .canvas file that arrives
	// with a checkout cannot widen what its pages read.
	Roots map[string]string `json:"roots,omitempty"`
}

// NodeKey names a node of one file in the chat's bookkeeping and its reports.
func NodeKey(file, id string) string {
	return file + "#" + id
}

// BoardBundle is the bundle whose bookkeeping a session uses: the session at
// the root of its spawn tree, since a subagent's bundle sits under its
// parent's (<parent>/subagents/<child>/) and a person looks at the parent's
// chat.
func BoardBundle(sessionDir string) string {
	dir := filepath.Clean(sessionDir)
	for {
		parent := filepath.Dir(dir)
		if filepath.Base(parent) != session.ChildSessionsDirName {
			return dir
		}
		dir = filepath.Dir(parent)
	}
}

// SessionIDOfBundle is the id of the session whose bookkeeping a bundle holds:
// the folder name of the root of the spawn tree.
func SessionIDOfBundle(sessionDir string) string {
	return filepath.Base(BoardBundle(sessionDir))
}

// LoadState reads a chat's bookkeeping; a bundle without any has none yet.
func LoadState(bundle string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(bundle, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read the canvas: %w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("the canvas bookkeeping %s does not parse: %w", filepath.Join(bundle, FileName), err)
	}
	if st.Version == 0 {
		st.Version = 1
	}
	return st, nil
}

// in is the bookkeeping as it applies in one workspace: the current file and
// the roots of another one do not.
func (st State) in(workspace string) State {
	if st.Workspace != "" && !session.SameWorkspacePath(st.Workspace, workspace) {
		st.Current, st.Roots = "", nil
	}
	return st
}

// Load reads a board: its file, with the chat's generations and roots.
func Load(ref Ref) (Board, error) {
	rel, err := resolveCanvasFile(ref.Workspace, ref.File, true)
	if err != nil {
		return Board{}, err
	}
	st, err := LoadState(ref.Bundle)
	if err != nil {
		return Board{}, err
	}
	st = st.in(ref.Workspace)
	b := Board{File: rel, Workspace: ref.Workspace, Revision: st.Revision, state: st}
	readBoardFile(&b)
	return b, nil
}

// Current is the .canvas file the chat shows pages on in a workspace, "" when
// it has none there.
func Current(bundle, workspace string) (string, error) {
	st, err := LoadState(bundle)
	if err != nil {
		return "", err
	}
	return st.in(workspace).Current, nil
}

// ShowRequest asks for a page on a board.
type ShowRequest struct {
	// Path is the page, relative to the workspace or absolute inside it.
	Path string
	// Root is the folder the page may load files from; default its own folder.
	Root   string
	Title  string
	Device string
	// Width and Height are a custom size instead of Device.
	Width, Height int
	// FrameID points that frame at the page instead of adding or reusing one.
	FrameID string
	AddedBy string
}

// ShowResult is the board after a Show and the frame it touched.
type ShowResult struct {
	Board  Board
	Frame  Frame
	Reason string
	// Created says the board's file was made for this page.
	Created bool
}

// Show puts a page on a board: a new frame, the frame already showing that
// page at that size, or the named frame pointed at it. A Ref without a File
// shows on the chat's current file, or on a new canvas/<folder>.canvas made
// for it when the chat has none.
func Show(ref Ref, req ShowRequest) (ShowResult, error) {
	page, err := resolvePage(ref.Workspace, req.Path, req.Root)
	if err != nil {
		return ShowResult{}, err
	}
	device, width, height, err := resolveSize(req.Device, req.Width, req.Height)
	if err != nil {
		return ShowResult{}, err
	}
	var res ShowResult
	var pick func(State) (string, bool, error)
	if ref.File == "" {
		pick = func(st State) (string, bool, error) { return currentOrNew(ref.Workspace, st, page.path) }
	}
	title := strings.TrimSpace(req.Title)
	err = mutate(ref, pick, func(b *Board) (string, error) {
		b.state.Current = b.File
		if id := strings.TrimSpace(req.FrameID); id != "" {
			i := indexOf(b.Frames, id)
			if i < 0 {
				return "", fmt.Errorf("%w: %s", ErrFrameNotFound, id)
			}
			f := &b.Frames[i]
			if f.Path != page.path {
				// Another page: the browser loads it at a new address.
				f.Gen++
			}
			f.Path, f.Root = page.path, page.root
			f.Device, f.Width, f.Height = device, width, height
			if title != "" {
				f.Title = title
			}
			b.dirty = true
			res.Frame, res.Reason = *f, ReasonUpdated
			return id, nil
		}
		for i := range b.Frames {
			f := &b.Frames[i]
			if f.Path == page.path && f.Width == width && f.Height == height {
				if req.Root != "" {
					f.Root = page.root
				}
				if title != "" && f.Title != title {
					f.Title = title
					b.dirty = true
				}
				res.Frame, res.Reason = *f, ReasonShown
				return f.ID, nil
			}
		}
		if len(b.Frames) >= MaxFrames {
			return "", ErrTooManyFrames
		}
		f := Frame{
			ID: newNodeID(), Path: page.path, Root: page.root, Workspace: b.Workspace,
			Title: title, Device: device, Width: width, Height: height, X: nextX(b), Y: 0,
			AddedBy: strings.TrimSpace(req.AddedBy), Gen: 1,
		}
		b.Frames = append(b.Frames, f)
		b.dirty = true
		res.Frame, res.Reason = f, ReasonAdded
		return f.ID, nil
	}, func() string { return res.Reason }, &res.Board, &res.Created)
	if err != nil {
		return ShowResult{}, err
	}
	return res, nil
}

// currentOrNew is the file a Show without one goes to: the chat's current
// file while it is still there, else a new canvas/<folder of the page>.canvas,
// numbered when the name is taken.
func currentOrNew(workspace string, st State, page string) (string, bool, error) {
	if cur := st.in(workspace).Current; cur != "" {
		if rel, err := resolveCanvasFile(workspace, cur, true); err == nil {
			return rel, false, nil
		}
	}
	base := path.Base(path.Dir(page))
	if base == "." || base == "/" {
		base = strings.TrimSuffix(path.Base(page), path.Ext(page))
	}
	for n := 1; n < 1000; n++ {
		name := "canvas/" + base + ".canvas"
		if n > 1 {
			name = fmt.Sprintf("canvas/%s-%d.canvas", base, n)
		}
		rel, err := resolveCanvasFile(workspace, name, false)
		if errors.Is(err, errCanvasExists) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if err := createCanvasFile(workspace, rel); err != nil {
			return "", false, err
		}
		return rel, true, nil
	}
	return "", false, errors.New("no free name for a new canvas file under canvas/")
}

// Open makes a .canvas file of the workspace the one the chat shows pages on.
func Open(ref Ref) (Board, error) {
	var board Board
	err := mutate(ref, nil, func(b *Board) (string, error) {
		if !b.fileOK {
			return "", fmt.Errorf("%s cannot be read: %s", b.File, b.Error)
		}
		b.state.Current = b.File
		return "", nil
	}, func() string { return ReasonOpened }, &board, nil)
	return board, err
}

// NodeChange moves or resizes one node of a board: a frame (by a device or a
// size), or a text card, a link or a group.
type NodeChange struct {
	ID                  string
	X, Y, Width, Height *int
	// Device sizes a frame by name instead of Width and Height.
	Device *string
}

// MaxNodeChanges bounds one change of nodes (a group dragged with all it holds).
const MaxNodeChanges = 500

// UpdateNodes applies the changes in one write: all of them, or none when one
// names a node the board does not hold.
func UpdateNodes(ref Ref, changes []NodeChange) (Board, error) {
	if len(changes) == 0 {
		return Board{}, errors.New("no node to change")
	}
	if len(changes) > MaxNodeChanges {
		return Board{}, fmt.Errorf("at most %d nodes change at once", MaxNodeChanges)
	}
	var board Board
	frameID := ""
	if len(changes) == 1 {
		frameID = changes[0].ID
	}
	err := mutate(ref, nil, func(b *Board) (string, error) {
		for _, c := range changes {
			if err := applyNodeChange(b, c); err != nil {
				return "", err
			}
		}
		b.dirty = true
		return frameID, nil
	}, func() string { return ReasonUpdated }, &board, nil)
	return board, err
}

func applyNodeChange(b *Board, c NodeChange) error {
	pos := func(dst *int, v *int) {
		if v != nil {
			*dst = clamp(*v, -100000, 100000)
		}
	}
	if i := indexOf(b.Frames, c.ID); i >= 0 {
		f := &b.Frames[i]
		pos(&f.X, c.X)
		pos(&f.Y, c.Y)
		switch {
		case c.Device != nil:
			d, w, h, err := resolveSize(*c.Device, 0, 0)
			if err != nil {
				return err
			}
			f.Device, f.Width, f.Height = d, w, h
		case c.Width != nil || c.Height != nil:
			w, h := f.Width, f.Height
			if c.Width != nil {
				w = *c.Width
			}
			if c.Height != nil {
				h = *c.Height
			}
			d, rw, rh, err := resolveSize("", w, h)
			if err != nil {
				return err
			}
			f.Device, f.Width, f.Height = d, rw, rh
		}
		return nil
	}
	for i := range b.Items {
		it := &b.Items[i]
		if it.ID != c.ID {
			continue
		}
		pos(&it.X, c.X)
		pos(&it.Y, c.Y)
		if c.Width != nil {
			it.Width = clamp(*c.Width, 20, 20000)
		}
		if c.Height != nil {
			it.Height = clamp(*c.Height, 20, 20000)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrFrameNotFound, c.ID)
}

// Rotate moves a frame to its next generation, which revokes every address of
// it handed out so far: the web UI asks for it when the frame's page navigated
// away from the canvas, since that page could have taken the address along.
// The file is not written.
func Rotate(ref Ref, frameID string) (Board, Frame, error) {
	var out Frame
	var board Board
	err := mutate(ref, nil, func(b *Board) (string, error) {
		i := indexOf(b.Frames, frameID)
		if i < 0 {
			return "", fmt.Errorf("%w: %s", ErrFrameNotFound, frameID)
		}
		b.Frames[i].Gen++
		out = b.Frames[i]
		return frameID, nil
	}, func() string { return ReasonUpdated }, &board, nil)
	return board, out, err
}

// Remove takes a frame off the board: its node and the edges at it leave the
// file, the page's files stay where they are.
func Remove(ref Ref, frameID string) (Board, error) {
	var board Board
	err := mutate(ref, nil, func(b *Board) (string, error) {
		i := indexOf(b.Frames, frameID)
		if i < 0 {
			return "", fmt.Errorf("%w: %s", ErrFrameNotFound, frameID)
		}
		b.Frames = append(b.Frames[:i], b.Frames[i+1:]...)
		b.dirty = true
		return frameID, nil
	}, func() string { return ReasonRemoved }, &board, nil)
	return board, err
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func indexOf(frames []Frame, id string) int {
	for i, f := range frames {
		if f.ID == id {
			return i
		}
	}
	return -1
}

// resolveSize turns a device name or a custom size into the frame's size.
func resolveSize(device string, width, height int) (string, int, int, error) {
	device = strings.ToLower(strings.TrimSpace(device))
	if width != 0 || height != 0 {
		if device != "" {
			return "", 0, 0, errors.New("give either a device or a width and a height, not both")
		}
		if width == 0 || height == 0 {
			return "", 0, 0, errors.New("a custom size needs both a width and a height")
		}
		if width < MinWidth || width > MaxWidth || height < MinHeight || height > MaxHeight {
			return "", 0, 0, fmt.Errorf("a frame is %d-%d pixels wide and %d-%d pixels tall", MinWidth, MaxWidth, MinHeight, MaxHeight)
		}
		return "", width, height, nil
	}
	if device == "" {
		device = DeviceDesktop
	}
	w, h, ok := DeviceSize(device)
	if !ok {
		return "", 0, 0, fmt.Errorf("unknown device %q: use desktop, tablet or phone, or a width and a height", device)
	}
	return device, w, h, nil
}

type resolvedPage struct {
	path string // slash, relative to the workspace
	root string // slash, relative to the workspace, "" for the workspace
}

// resolvePage checks that a page is an HTML file of the workspace outside every
// dot-named folder, and that its root is a folder of the workspace holding it,
// with links resolved: a frame serves files to a browser, so it serves nothing
// the session could not already read in its own working directory.
func resolvePage(workspace, pagePath, rootPath string) (resolvedPage, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return resolvedPage{}, errors.New("the session has no working directory for the canvas")
	}
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return resolvedPage{}, fmt.Errorf("working directory: %w", err)
	}
	pagePath = strings.TrimSpace(pagePath)
	if pagePath == "" {
		return resolvedPage{}, errors.New("name the HTML file to put on the canvas")
	}
	pageRel, err := insideWorkspace(workspace, realWS, pagePath)
	if err != nil {
		return resolvedPage{}, err
	}
	info, err := os.Stat(filepath.Join(realWS, filepath.FromSlash(pageRel)))
	if err != nil {
		return resolvedPage{}, fmt.Errorf("cannot put %s on the canvas: %w", pagePath, err)
	}
	if !info.Mode().IsRegular() {
		return resolvedPage{}, fmt.Errorf("%s is not a file; name an HTML page", pagePath)
	}
	if !webfs.IsHTML(pageRel) {
		return resolvedPage{}, fmt.Errorf("%s is not an HTML page (.html or .htm)", pagePath)
	}
	if info.Size() > MaxInjectBytes {
		return resolvedPage{}, fmt.Errorf("%s is %d MB; the canvas runs pages up to %d MB: move inlined data into files of its folder", pagePath, info.Size()>>20, MaxInjectBytes>>20)
	}
	rootRel := defaultRoot(pageRel)
	if strings.TrimSpace(rootPath) != "" {
		rootRel, err = insideWorkspace(workspace, realWS, rootPath)
		if err != nil {
			return resolvedPage{}, err
		}
		if rootRel == "." {
			rootRel = ""
		}
		info, err := os.Stat(filepath.Join(realWS, filepath.FromSlash(rootRel)))
		if err != nil || !info.IsDir() {
			return resolvedPage{}, fmt.Errorf("the root %s is not a folder of the working directory", rootPath)
		}
		if rootRel != "" && !strings.HasPrefix(pageRel, rootRel+"/") {
			return resolvedPage{}, fmt.Errorf("the root %s does not contain %s", rootPath, pagePath)
		}
	}
	return resolvedPage{path: pageRel, root: rootRel}, nil
}

// insideWorkspace resolves p (relative to the workspace or absolute) with links
// followed and returns it relative to the real workspace, slash-separated.
func insideWorkspace(workspace, realWS, p string) (string, error) {
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, filepath.FromSlash(target))
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("cannot put %s on the canvas: %w", p, err)
	}
	rel, err := filepath.Rel(realWS, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is outside the working directory %s; the canvas only shows the project", p, workspace)
	}
	rel = filepath.ToSlash(rel)
	if webfs.HiddenPathElement(rel) {
		return "", fmt.Errorf("%s is inside a dot-named path; the canvas never serves dot-files", p)
	}
	return rel, nil
}

// locks serialise the read-modify-write of one chat's bookkeeping in this
// process; fileLocks (file.go) serialise one file's across chats, taken inside.
var locks sync.Map // canonical bundle -> *sync.Mutex

func lockFor(bundle string) *sync.Mutex {
	key := session.CanonicalWorkspacePath(bundle)
	m, _ := locks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// mutate reads a board, applies fn, writes the file when fn changed its nodes,
// keeps the chat's generations and roots, bumps the revision and announces the
// change. pick, when set, chooses the file under the chat's lock (Show without
// a file), and created reports that it made one.
func mutate(ref Ref, pick func(State) (string, bool, error), fn func(*Board) (frameID string, err error), reason func() string, out *Board, created *bool) error {
	if strings.TrimSpace(ref.Bundle) == "" {
		return errors.New("the session is not persisted, so it has no canvas")
	}
	mu := lockFor(ref.Bundle)
	mu.Lock()
	defer mu.Unlock()
	made := false
	if pick != nil {
		st, err := LoadState(ref.Bundle)
		if err != nil {
			return err
		}
		ref.File, made, err = pick(st)
		if err != nil {
			return err
		}
	}
	err := mutateFile(ref, fn, reason, out)
	if err != nil && made {
		// Nothing went onto the file this Show made: it goes too, or the next
		// Show would number its name.
		_ = os.Remove(filepath.Join(ref.Workspace, filepath.FromSlash(ref.File)))
		made = false
	}
	if created != nil {
		*created = made
	}
	return err
}

func mutateFile(ref Ref, fn func(*Board) (string, error), reason func() string, out *Board) error {
	rel, err := resolveCanvasFile(ref.Workspace, ref.File, true)
	if err != nil {
		return err
	}
	ref.File = rel
	// The whole read-modify-write of one file is one turn across chats, so
	// two of them never replay a stale board over each other's change.
	fmu := fileLockFor(ref.Workspace, rel)
	fmu.Lock()
	defer fmu.Unlock()
	b, err := Load(ref)
	if err != nil {
		return err
	}
	frameID, err := fn(&b)
	if err != nil {
		return err
	}
	if b.dirty && !b.fileOK {
		return fmt.Errorf("%s cannot be read: %s", b.File, b.Error)
	}
	st := &b.state
	if st.Gens == nil {
		st.Gens = map[string]int{}
	}
	if b.fileOK {
		kept := map[string]bool{}
		for _, fr := range b.Frames {
			kept[fr.ID] = true
			st.Gens[NodeKey(b.File, fr.ID)] = fr.Gen
		}
		for id, was := range b.read {
			if !kept[id] {
				st.Gens[NodeKey(b.File, id)] = was.Gen + 1
			}
		}
		prefix := b.File + "#"
		for k := range st.Roots {
			if strings.HasPrefix(k, prefix) {
				delete(st.Roots, k)
			}
		}
		for _, fr := range b.Frames {
			if fr.Root != defaultRoot(fr.Path) {
				if st.Roots == nil {
					st.Roots = map[string]string{}
				}
				st.Roots[NodeKey(b.File, fr.ID)] = rootOrDot(fr.Root)
			}
		}
	}
	if b.dirty {
		if err := writeBoardFile(&b); err != nil {
			return err
		}
	}
	st.Version = 1
	st.Revision++
	st.Workspace = ref.Workspace
	if err := writeState(ref.Bundle, *st); err != nil {
		return err
	}
	b.Revision = st.Revision
	*out = b
	announce(Change{SessionID: filepath.Base(ref.Bundle), File: b.File, Revision: b.Revision, FrameID: frameID, Reason: reason()})
	return nil
}

func writeState(bundle string, st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		return fmt.Errorf("write the canvas: %w", err)
	}
	tmp, err := os.CreateTemp(bundle, ".canvas-*.json")
	if err != nil {
		return fmt.Errorf("write the canvas: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write the canvas: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write the canvas: %w", err)
	}
	if err := os.Rename(name, filepath.Join(bundle, FileName)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write the canvas: %w", err)
	}
	return nil
}

// Change is one write to a board through the canvas.
type Change struct {
	SessionID string
	File      string
	Revision  int64
	FrameID   string
	Reason    string
}

var (
	hooksMu sync.Mutex
	hooks   = map[int]func(Change){}
	hookSeq int
)

// OnChange registers fn for every change of every board in this process and
// returns the function that unregisters it. The HTTP server turns these into
// the canvas_changed event.
func OnChange(fn func(Change)) func() {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hookSeq++
	id := hookSeq
	hooks[id] = fn
	return func() {
		hooksMu.Lock()
		defer hooksMu.Unlock()
		delete(hooks, id)
	}
}

func announce(c Change) {
	hooksMu.Lock()
	fns := make([]func(Change), 0, len(hooks))
	for _, fn := range hooks {
		fns = append(fns, fn)
	}
	hooksMu.Unlock()
	for _, fn := range fns {
		fn(c)
	}
}

// ChatWorkspace is the working directory a bundle's session records, "" when
// it records none: the workspace a chat's boards are files of.
func ChatWorkspace(bundle string) string {
	raw, err := os.ReadFile(filepath.Join(bundle, sessionMetaFile))
	if err != nil {
		return ""
	}
	var meta struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return ""
	}
	return strings.TrimSpace(meta.CWD)
}

// sessionMetaFile is the session's own file in its bundle (internal/session).
const sessionMetaFile = "session.json"
