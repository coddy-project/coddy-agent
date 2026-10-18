// Package canvas is the board of live frames beside a chat of the web UI: which
// pages of the workspace are on it, at what size and where, kept as one small
// file in the session's bundle. A frame shows a file of the workspace and
// nothing else; the page itself is written with the ordinary file tools, so
// git, the edits window and every surface see the same files.
//
// The package owns the board and its store, the stamp that tells a browser a
// frame's files changed, the capability that addresses a frame's files, the
// serving of those files (sandbox policy, the bridge script) and the reports a
// browser sends back about a frame it runs. The tools (internal/tools/canvas),
// the HTTP routes (external/httpserver) and the swarm relay build on it.
package canvas

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// FileName is the board's file inside a session bundle.
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

// Reasons a board changed, as Show, Update and Remove report them and as the
// change hook and the HTTP event carry them.
const (
	ReasonAdded   = "added"
	ReasonShown   = "shown"
	ReasonUpdated = "updated"
	ReasonRemoved = "removed"
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
	// ErrFrameNotFound is a frame id the board does not hold.
	ErrFrameNotFound = errors.New("no such frame on the canvas")
	// ErrTooManyFrames refuses a frame past MaxFrames.
	ErrTooManyFrames = fmt.Errorf("the canvas already holds %d frames; remove one from the canvas first", MaxFrames)
)

// Frame is one page on the board.
type Frame struct {
	ID string `json:"id"`
	// Path is the page, slash-separated and relative to Workspace.
	Path string `json:"path"`
	// Root is the folder the page may load files from, relative to Workspace;
	// "" is the workspace itself. It always contains Path.
	Root string `json:"root"`
	// Workspace is the folder Path and Root are relative to: the working
	// directory of the session that put the frame on the board.
	Workspace string    `json:"workspace"`
	Title     string    `json:"title,omitempty"`
	Device    string    `json:"device,omitempty"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	X         int       `json:"x"`
	Y         int       `json:"y"`
	AddedBy   string    `json:"added_by,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	ShownAt   time.Time `json:"shown_at"`
	// Gen is the frame's generation: its address is signed for one, so moving
	// the frame to the next (Rotate) revokes every address handed out before.
	Gen int `json:"gen"`
}

// RootAbs is the absolute folder a frame's files are served from.
func (f Frame) RootAbs() string {
	return filepath.Join(f.Workspace, filepath.FromSlash(f.Root))
}

// PageInRoot is the page's path relative to the frame's root.
func (f Frame) PageInRoot() string {
	if f.Root == "" {
		return f.Path
	}
	return strings.TrimPrefix(f.Path, f.Root+"/")
}

// Board is what a chat's canvas holds.
type Board struct {
	Version  int     `json:"version"`
	Revision int64   `json:"revision"`
	Frames   []Frame `json:"frames"`
	// Link names the .canvas file of the workspace the chat's canvas is, when
	// it is linked to one (JSON Canvas, Obsidian's format): its frames and its
	// other nodes are that file's, and every change is written back to it.
	Link *Link `json:"link,omitempty"`
	// Gens are the generations of a linked file's frames, kept here so the
	// workspace file carries none of the canvas's own bookkeeping.
	Gens map[string]int `json:"gens,omitempty"`

	// Items and Edges are the other nodes of a linked file and its lines.
	Items []Item `json:"-"`
	Edges []Edge `json:"-"`
	// LinkError says why a linked file cannot be read now; its frames are
	// then not shown and nothing is written to it.
	LinkError string `json:"-"`

	own    []Frame   // the chat's own frames, kept while the board is linked
	doc    *Document // the linked file as read
	inDoc  map[string]bool
	fileOK bool
	// dirty says a change touched the linked file's nodes; only then is the
	// file written, so linking a file never rewrites it.
	dirty bool
}

// Link is a .canvas file of the workspace a board is.
type Link struct {
	// Path is the file, slash-separated and relative to Workspace.
	Path string `json:"path"`
	// Workspace is the folder the file's own paths are relative to: the vault
	// root of a JSON Canvas file, the session's working directory here.
	Workspace string `json:"workspace"`
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

// BoardBundle is the bundle whose board a session writes to: the session at
// the root of its spawn tree, since a subagent's bundle sits under its parent's
// (<parent>/subagents/<child>/) and a person looks at the parent's chat.
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

// SessionIDOfBundle is the id of the session whose board a bundle holds: the
// folder name of the root of the spawn tree.
func SessionIDOfBundle(sessionDir string) string {
	return filepath.Base(BoardBundle(sessionDir))
}

// Load reads a bundle's board. A bundle without one has an empty board; a
// board that does not parse is an error, never silently replaced.
func Load(bundle string) (Board, error) {
	raw, err := os.ReadFile(filepath.Join(bundle, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Board{Version: 1}, nil
	}
	if err != nil {
		return Board{}, fmt.Errorf("read the canvas: %w", err)
	}
	var b Board
	if err := json.Unmarshal(raw, &b); err != nil {
		return Board{}, fmt.Errorf("the canvas file %s does not parse: %w", filepath.Join(bundle, FileName), err)
	}
	if b.Version == 0 {
		b.Version = 1
	}
	for i := range b.Frames {
		if b.Frames[i].Gen < 1 {
			b.Frames[i].Gen = 1
		}
	}
	if b.Link != nil {
		b.own = b.Frames
		b.Frames = nil
		readLinked(&b)
	}
	return b, nil
}

// ShowRequest asks for a page on the board.
type ShowRequest struct {
	// Workspace is the session's working directory.
	Workspace string
	// Path is the page, relative to Workspace or absolute inside it.
	Path string
	// Root is the folder the page may load files from; default its own folder.
	Root   string
	Title  string
	Device string
	// Width and Height are a custom size instead of Device.
	Width, Height int
	// FrameID updates that frame in place instead of adding or reusing one.
	FrameID string
	AddedBy string
}

// ShowResult is the board after a Show and the frame it touched.
type ShowResult struct {
	Board  Board
	Frame  Frame
	Reason string
}

// Show puts a page on the board: a new frame, the frame already showing that
// file at that size, or the named frame updated in place.
func Show(bundle string, req ShowRequest) (ShowResult, error) {
	page, err := resolvePage(req.Workspace, req.Path, req.Root)
	if err != nil {
		return ShowResult{}, err
	}
	device, width, height, err := resolveSize(req.Device, req.Width, req.Height)
	if err != nil {
		return ShowResult{}, err
	}
	title := strings.TrimSpace(req.Title)
	var res ShowResult
	err = mutate(bundle, func(b *Board) (string, error) {
		now := time.Now().UTC()
		if b.Link != nil && !session.SameWorkspacePath(b.Link.Workspace, req.Workspace) {
			// A node of the file names its page relative to the file's vault.
			return "", fmt.Errorf("the canvas is %s of %s, and a page of another working directory cannot be a node of it", b.Link.Path, b.Link.Workspace)
		}
		if id := strings.TrimSpace(req.FrameID); id != "" {
			i := indexOf(b.Frames, id)
			if i < 0 {
				return "", fmt.Errorf("%w: %s", ErrFrameNotFound, id)
			}
			f := &b.Frames[i]
			f.Path, f.Root, f.Workspace = page.path, page.root, req.Workspace
			f.Device, f.Width, f.Height = device, width, height
			if title != "" {
				f.Title = title
			}
			f.ShownAt = now
			b.dirty = true
			res.Frame, res.Reason = *f, ReasonUpdated
			return id, nil
		}
		for i := range b.Frames {
			f := &b.Frames[i]
			if f.Path == page.path && f.Width == width && f.Height == height && session.SameWorkspacePath(f.Workspace, req.Workspace) {
				if req.Root != "" {
					f.Root = page.root
				}
				if title != "" {
					f.Title = title
				}
				f.ShownAt = now
				b.dirty = b.dirty || req.Root != "" || title != ""
				res.Frame, res.Reason = *f, ReasonShown
				return f.ID, nil
			}
		}
		if len(b.Frames) >= MaxFrames {
			return "", ErrTooManyFrames
		}
		if b.Link != nil && !b.fileOK {
			return "", fmt.Errorf("the linked canvas %s cannot be read: %s", b.Link.Path, b.LinkError)
		}
		x := nextX(b)
		id := newFrameID()
		if b.Link != nil {
			id = newNodeID()
		}
		f := Frame{
			ID: id, Path: page.path, Root: page.root, Workspace: req.Workspace,
			Title: title, Device: device, Width: width, Height: height, X: x, Y: 0,
			AddedBy: strings.TrimSpace(req.AddedBy), AddedAt: now, ShownAt: now, Gen: 1,
		}
		b.Frames = append(b.Frames, f)
		b.dirty = true
		res.Frame, res.Reason = f, ReasonAdded
		return f.ID, nil
	}, func() string { return res.Reason }, &res.Board)
	if err != nil {
		return ShowResult{}, err
	}
	return res, nil
}

// Patch changes how a frame stands on the board; the page it shows does not
// change here (Show with a FrameID does that).
type Patch struct {
	Title  *string
	Device *string
	Width  *int
	Height *int
	X      *int
	Y      *int
}

// Update applies a patch to one frame.
func Update(bundle, frameID string, p Patch) (Board, Frame, error) {
	var out Frame
	var board Board
	err := mutate(bundle, func(b *Board) (string, error) {
		i := indexOf(b.Frames, frameID)
		if i < 0 {
			return "", fmt.Errorf("%w: %s", ErrFrameNotFound, frameID)
		}
		f := b.Frames[i]
		if p.Title != nil {
			f.Title = strings.TrimSpace(*p.Title)
		}
		if p.Device != nil || p.Width != nil || p.Height != nil {
			device, w, h := "", 0, 0
			switch {
			case p.Width != nil || p.Height != nil:
				// A custom size, starting from the frame's own.
				if p.Device != nil && *p.Device != "" {
					return "", errors.New("give either a device or a width and a height, not both")
				}
				w, h = f.Width, f.Height
				if p.Width != nil {
					w = *p.Width
				}
				if p.Height != nil {
					h = *p.Height
				}
			case *p.Device == "":
				// Custom at the size the frame has.
				w, h = f.Width, f.Height
			default:
				device = *p.Device
			}
			d, rw, rh, err := resolveSize(device, w, h)
			if err != nil {
				return "", err
			}
			f.Device, f.Width, f.Height = d, rw, rh
		}
		if p.X != nil {
			f.X = clamp(*p.X, -100000, 100000)
		}
		if p.Y != nil {
			f.Y = clamp(*p.Y, -100000, 100000)
		}
		b.Frames[i] = f
		b.dirty = true
		out = f
		return f.ID, nil
	}, func() string { return ReasonUpdated }, &board)
	return board, out, err
}

// Rotate moves a frame to its next generation, which revokes every address of
// it handed out so far: the web UI asks for it when the frame's page navigated
// away from the canvas, since that page could have taken the address along.
func Rotate(bundle, frameID string) (Board, Frame, error) {
	var out Frame
	var board Board
	err := mutate(bundle, func(b *Board) (string, error) {
		i := indexOf(b.Frames, frameID)
		if i < 0 {
			return "", fmt.Errorf("%w: %s", ErrFrameNotFound, frameID)
		}
		b.Frames[i].Gen++
		out = b.Frames[i]
		return frameID, nil
	}, func() string { return ReasonUpdated }, &board)
	return board, out, err
}

// Remove takes a frame off the board. Its files stay where they are.
func Remove(bundle, frameID string) (Board, error) {
	var board Board
	err := mutate(bundle, func(b *Board) (string, error) {
		i := indexOf(b.Frames, frameID)
		if i < 0 {
			return "", fmt.Errorf("%w: %s", ErrFrameNotFound, frameID)
		}
		b.Frames = append(b.Frames[:i], b.Frames[i+1:]...)
		b.dirty = true
		return frameID, nil
	}, func() string { return ReasonRemoved }, &board)
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

func newFrameID() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	out := make([]byte, len(raw))
	for i, c := range raw {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return "fr_" + string(out)
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
	rootRel := ""
	if dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(pageRel))); dir != "." {
		rootRel = dir
	}
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

// locks serialise the read-modify-write of one bundle's board in this process.
var locks sync.Map // canonical bundle -> *sync.Mutex

func lockFor(bundle string) *sync.Mutex {
	key := session.CanonicalWorkspacePath(bundle)
	m, _ := locks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// mutate reads the board, applies fn, bumps the revision, writes the board
// through a temporary file and a rename, and announces the change.
func mutate(bundle string, fn func(*Board) (frameID string, err error), reason func() string, out *Board) error {
	if strings.TrimSpace(bundle) == "" {
		return errors.New("the session is not persisted, so it has no canvas")
	}
	mu := lockFor(bundle)
	mu.Lock()
	b, err := Load(bundle)
	if err != nil {
		mu.Unlock()
		return err
	}
	frameID, err := fn(&b)
	if err != nil {
		mu.Unlock()
		return err
	}
	b.Version = 1
	b.Revision++
	if b.Frames == nil {
		b.Frames = []Frame{}
	}
	stored := b
	if b.Link != nil {
		if b.Gens == nil {
			b.Gens = map[string]int{}
		}
		for _, fr := range b.Frames {
			b.Gens[fr.ID] = fr.Gen
		}
		if b.fileOK && b.dirty {
			if err := writeLinked(&b); err != nil {
				mu.Unlock()
				return err
			}
		}
		stored.Frames = b.own
		stored.Gens = b.Gens
		if stored.Frames == nil {
			stored.Frames = []Frame{}
		}
	}
	if err := write(bundle, stored); err != nil {
		mu.Unlock()
		return err
	}
	mu.Unlock()
	*out = b
	announce(Change{Bundle: bundle, SessionID: filepath.Base(bundle), Revision: b.Revision, FrameID: frameID, Reason: reason()})
	return nil
}

func write(bundle string, b Board) error {
	raw, err := json.MarshalIndent(b, "", "  ")
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

// Change is one write to a board.
type Change struct {
	Bundle    string
	SessionID string
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
