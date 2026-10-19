package canvas

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

// errCanvasExists is a name a new board file cannot take.
var errCanvasExists = errors.New("that .canvas file already exists")

// frameIDRe is the shape of a node id a frame can carry: Obsidian's 16 hex
// digits and anything as tame.
var frameIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// IsCanvasFile reports a JSON Canvas file by its extension.
func IsCanvasFile(name string) bool {
	return strings.EqualFold(path.Ext(name), ".canvas")
}

func (b *Board) filePath() string {
	return filepath.Join(b.Workspace, filepath.FromSlash(b.File))
}

// readBoardFile fills a board from its file: every file node naming an HTML
// page of the workspace is a frame, every other node an item.
func readBoardFile(b *Board) {
	b.inDoc = map[string]bool{}
	b.read = map[string]Frame{}
	b.readItems = map[string]Item{}
	f, err := os.Open(b.filePath())
	if err != nil {
		b.Error = err.Error()
		return
	}
	doc, err := ParseDocument(f)
	_ = f.Close()
	if err != nil {
		b.Error = err.Error()
		return
	}
	b.fileOK = true
	pages := 0
	for _, n := range doc.nodes {
		id, typ := n.str("id"), n.str("type")
		if id == "" || typ == "" {
			continue
		}
		item := nodeItem(n)
		if typ == NodeFile && webfs.IsHTML(item.File) {
			if why := pageSlot(id, &pages); why != "" {
				item.Note = why
				b.Items = append(b.Items, item)
				continue
			}
			frame, why := frameFromNode(b, n, item)
			if why == "" {
				b.Frames = append(b.Frames, frame)
				b.inDoc[id] = true
				b.read[id] = frame
				continue
			}
			item.Note = why
		}
		b.Items = append(b.Items, item)
		b.readItems[id] = item
	}
	b.Edges = doc.edgeList()
}

func nodeItem(n object) Item {
	return Item{
		ID: n.str("id"), Type: n.str("type"), X: n.num("x"), Y: n.num("y"), Width: n.num("width"), Height: n.num("height"),
		Color: n.str("color"), Text: cut(n.str("text"), maxItemText), File: n.str("file"),
		URL: n.str("url"), Label: cut(n.str("label"), maxReportText),
	}
}

// pageSlot says why a node naming a page cannot run, "" when it takes one of
// the board's MaxFrames slots: the first MaxFrames such nodes with an id of
// the right shape do, whether their pages resolve or not, so one frame is
// decided without reading the others (FrameOf).
func pageSlot(id string, pages *int) string {
	if !frameIDRe.MatchString(id) {
		// The id goes into the frame's address and the bridge's messages.
		return "a page runs only from a node whose id is letters, digits, - and _, at most 64 of them"
	}
	if *pages >= MaxFrames {
		return fmt.Sprintf("the canvas runs at most %d pages at once", MaxFrames)
	}
	*pages++
	return ""
}

// FrameOf reads one frame of a board as Load gives it, resolving no other
// page: what serving a frame's files needs on every request.
func FrameOf(ref Ref, id string) (Frame, error) {
	rel, err := resolveCanvasFile(ref.Workspace, ref.File, true)
	if err != nil {
		return Frame{}, err
	}
	st, err := LoadState(ref.Bundle)
	if err != nil {
		return Frame{}, err
	}
	b := Board{File: rel, Workspace: ref.Workspace, state: st.in(ref.Workspace)}
	f, err := os.Open(b.filePath())
	if err != nil {
		return Frame{}, err
	}
	doc, err := ParseDocument(f)
	_ = f.Close()
	if err != nil {
		return Frame{}, err
	}
	pages := 0
	for _, n := range doc.nodes {
		item := nodeItem(n)
		if item.ID == "" || item.Type != NodeFile || !webfs.IsHTML(item.File) {
			continue
		}
		why := pageSlot(item.ID, &pages)
		if item.ID != id {
			continue
		}
		if why != "" {
			return Frame{}, errors.New(why)
		}
		frame, why := frameFromNode(&b, n, item)
		if why != "" {
			return Frame{}, errors.New(why)
		}
		return frame, nil
	}
	return Frame{}, fmt.Errorf("%w: %s", ErrFrameNotFound, id)
}

// frameFromNode reads a file node as a live frame: its page checked as
// canvas_show checks it, its root the page's folder unless the chat widened it
// (State.Roots), its size the node's within the frame bounds.
func frameFromNode(b *Board, n object, item Item) (Frame, string) {
	ext := n.ext()
	key := NodeKey(b.File, item.ID)
	root := b.state.Roots[key]
	page, err := resolvePage(b.Workspace, item.File, root)
	if err != nil && root != "" {
		// The page left the root it was shown with: its own folder then.
		page, err = resolvePage(b.Workspace, item.File, "")
	}
	if err != nil {
		return Frame{}, err.Error()
	}
	w := clamp(item.Width, MinWidth, MaxWidth)
	h := clamp(item.Height, MinHeight, MaxHeight)
	device := ext.Device
	if dw, dh, ok := DeviceSize(device); !ok || dw != w || dh != h {
		device = inferDevice(w, h)
	}
	gen := b.state.Gens[key]
	if gen < 1 {
		gen = 1
	}
	return Frame{
		ID: item.ID, Path: page.path, Root: page.root, Workspace: b.Workspace,
		Title: ext.Title, Device: device, Width: w, Height: h, X: item.X, Y: item.Y,
		AddedBy: ext.AddedBy, Gen: gen,
	}, ""
}

func inferDevice(w, h int) string {
	for name, size := range deviceSizes {
		if size[0] == w && size[1] == h {
			return name
		}
	}
	return ""
}

// nextX is where a new frame goes: right of everything on the board.
func nextX(b *Board) int {
	x := 0
	for _, f := range b.Frames {
		if end := f.X + f.Width + frameGap; end > x {
			x = end
		}
	}
	for _, it := range b.Items {
		if end := it.X + it.Width + frameGap; end > x {
			x = end
		}
	}
	return x
}

// writeBoardFile writes a board's changes back into its file: the geometry and
// page of the frames that changed on the nodes they came from, a node for a
// new frame, the removal of a frame's node with its edges, the geometry of the
// other nodes dragged here. Every other node, edge and key stays as the file
// has it now, so an edit made since the board was read (Obsidian, the agent's
// file tools) survives. The caller holds the file's lock.
func writeBoardFile(b *Board) error {
	f, err := os.Open(b.filePath())
	if err != nil {
		return fmt.Errorf("the canvas %s: %w", b.File, err)
	}
	doc, err := ParseDocument(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("the canvas %s: %w", b.File, err)
	}
	keep := map[string]bool{}
	for _, fr := range b.Frames {
		keep[fr.ID] = true
		was, read := b.read[fr.ID]
		i := doc.findNode(fr.ID)
		if i < 0 {
			if read {
				// Deleted from the file since it was read: it stays deleted.
				continue
			}
			node := object{}
			node.set("id", fr.ID)
			node.set("type", NodeFile)
			node.set("file", fr.Path)
			doc.nodes = append(doc.nodes, node)
			i = len(doc.nodes) - 1
		}
		node := &doc.nodes[i]
		if !read || was.Path != fr.Path {
			node.set("file", fr.Path)
		}
		// A frame Coddy did not move keeps the node's own numbers: a node
		// smaller than a frame may be is drawn at the frame bounds but stays
		// as Obsidian has it.
		moved := !read || was.X != fr.X || was.Y != fr.Y || was.Width != fr.Width || was.Height != fr.Height
		if moved {
			node.set("x", fr.X)
			node.set("y", fr.Y)
			node.set("width", fr.Width)
			node.set("height", fr.Height)
		}
		changed := moved || was.Path != fr.Path
		if moved || was.Title != fr.Title || was.Device != fr.Device || was.AddedBy != fr.AddedBy {
			changed = true
			ext := frameExt{Title: fr.Title, AddedBy: fr.AddedBy}
			if fr.Device != "" && inferDevice(fr.Width, fr.Height) != fr.Device {
				ext.Device = fr.Device
			}
			if _, has := node.get(coddyExt); has || ext != (frameExt{}) {
				if ext == (frameExt{}) {
					node.del(coddyExt)
				} else if node.ext() != ext {
					node.set(coddyExt, ext)
				}
			}
		}
		if changed {
			node.obsidianOrder()
		}
	}
	for id := range b.inDoc {
		if !keep[id] {
			doc.removeNode(id)
		}
	}
	for _, it := range b.Items {
		was, ok := b.readItems[it.ID]
		if !ok || (was.X == it.X && was.Y == it.Y && was.Width == it.Width && was.Height == it.Height) {
			continue
		}
		if i := doc.findNode(it.ID); i >= 0 {
			node := &doc.nodes[i]
			node.set("x", it.X)
			node.set("y", it.Y)
			node.set("width", it.Width)
			node.set("height", it.Height)
			node.obsidianOrder()
		}
	}
	return writeFileAtomic(b.filePath(), doc.Marshal())
}

// fileLocks serialise the read-modify-write of one board file in this process
// (canonical path -> *sync.Mutex), taken inside a chat's own lock.
var fileLocks sync.Map

func fileLockFor(workspace, rel string) *sync.Mutex {
	key := session.CanonicalWorkspacePath(filepath.Join(workspace, filepath.FromSlash(rel)))
	m, _ := fileLocks.LoadOrStore(key, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// rootOrDot names the whole workspace "." where an empty root would read as
// "the page's own folder".
func rootOrDot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func defaultRoot(p string) string {
	if dir := path.Dir(p); dir != "." {
		return dir
	}
	return ""
}

// writeFileAtomic replaces a file of the workspace through a dot-named
// temporary file beside it, keeping its mode.
func writeFileAtomic(name string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(name); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	_ = os.Chmod(tmpName, mode)
	if err := os.Rename(tmpName, name); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// createCanvasFile makes a new, empty board file of the workspace, with the
// folders it needs; a file already there is left alone (errCanvasExists).
func createCanvasFile(workspace, rel string) error {
	name := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", errCanvasExists, rel)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write((&Document{}).Marshal()); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	return f.Close()
}

// resolveCanvasFile checks that p names a .canvas file of the workspace
// outside every dot-named folder; it may not exist yet when exists is false.
func resolveCanvasFile(workspace, p string, exists bool) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", errors.New("the session has no working directory for the canvas")
	}
	p = strings.TrimSpace(p)
	if !IsCanvasFile(p) {
		return "", fmt.Errorf("%s is not a .canvas file", p)
	}
	realWS, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if exists {
		rel, err := insideWorkspace(workspace, realWS, p)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(filepath.Join(realWS, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a file of the working directory", p)
		}
		return rel, nil
	}
	// A new file: the nearest folder of its path that exists must be of the
	// workspace, and no name below it a dot one; the missing folders are made
	// with the file.
	target := p
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, filepath.FromSlash(target))
	}
	target = filepath.Clean(target)
	var missing []string
	dir := filepath.Dir(target)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot put %s on the canvas: no folder of it exists", p)
		}
		missing = append([]string{filepath.Base(dir)}, missing...)
		dir = parent
	}
	dirRel, err := insideWorkspace(workspace, realWS, dir)
	if err != nil {
		return "", err
	}
	parts := append(missing, filepath.Base(target))
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("%s is inside a dot-named path; the canvas never serves dot-files", p)
		}
	}
	rel := strings.Join(parts, "/")
	if dirRel != "." && dirRel != "" {
		rel = dirRel + "/" + rel
	}
	if _, err := os.Lstat(filepath.Join(realWS, filepath.FromSlash(rel))); err == nil {
		return "", fmt.Errorf("%w: %s", errCanvasExists, p)
	}
	return rel, nil
}

// FileStamp digests the board file's size and time, so a reader of the board
// sees that someone else (Obsidian, the agent's file tools) changed it.
func FileStamp(b Board) string {
	info, err := os.Stat(b.filePath())
	if err != nil {
		return "missing"
	}
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[:8], uint64(info.Size()))
	binary.LittleEndian.PutUint64(buf[8:], uint64(info.ModTime().UnixNano()))
	sum := sha256.Sum256(buf[:])
	return hex.EncodeToString(sum[:8])
}

// Ensure makes a board's file, empty, when it is not there yet.
func Ensure(ref Ref) error {
	if _, err := resolveCanvasFile(ref.Workspace, ref.File, true); err == nil {
		return nil
	}
	rel, err := resolveCanvasFile(ref.Workspace, ref.File, false)
	if err != nil {
		return err
	}
	if err := createCanvasFile(ref.Workspace, rel); err != nil && !errors.Is(err, errCanvasExists) {
		return err
	}
	return nil
}
