package canvas

import (
	"bytes"
	"encoding/json"
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

// Reasons of the changes a link makes.
const (
	ReasonLinked   = "linked"
	ReasonUnlinked = "unlinked"
)

// ErrNotLinked refuses a change only a linked board has (moving a text card).
var ErrNotLinked = errors.New("the canvas is not linked to a .canvas file")

// ErrAlreadyLinked refuses to export a board that already is a file.
var ErrAlreadyLinked = errors.New("the canvas is already a .canvas file")

// ErrCanvasExists refuses to export over a file that is already there.
var ErrCanvasExists = errors.New("that .canvas file already exists; open it on the canvas instead or pick another name")

// frameIDRe is the shape of a node id a frame can carry: Obsidian's 16 hex
// digits, Coddy's fr_ ids and anything tamer.
var frameIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// IsCanvasFile reports a JSON Canvas file by its extension.
func IsCanvasFile(name string) bool {
	return strings.EqualFold(path.Ext(name), ".canvas")
}

func (b *Board) linkedFile() string {
	return filepath.Join(b.Link.Workspace, filepath.FromSlash(b.Link.Path))
}

// readLinked fills a linked board from its file: every file node naming an
// HTML page of the workspace is a frame, every other node an item.
func readLinked(b *Board) {
	b.inDoc = map[string]bool{}
	b.read = map[string]Frame{}
	b.readItems = map[string]Item{}
	f, err := os.Open(b.linkedFile())
	if err != nil {
		b.LinkError = err.Error()
		return
	}
	doc, err := ParseDocument(f)
	_ = f.Close()
	if err != nil {
		b.LinkError = err.Error()
		return
	}
	b.doc, b.fileOK = doc, true
	for _, n := range doc.nodes {
		id, typ := n.str("id"), n.str("type")
		if id == "" || typ == "" {
			continue
		}
		item := Item{
			ID: id, Type: typ, X: n.num("x"), Y: n.num("y"), Width: n.num("width"), Height: n.num("height"),
			Color: n.str("color"), Text: cut(n.str("text"), maxItemText), File: n.str("file"),
			URL: n.str("url"), Label: cut(n.str("label"), maxReportText),
		}
		if typ == NodeFile && webfs.IsHTML(item.File) {
			if !frameIDRe.MatchString(id) {
				// The id goes into the frame's address and the bridge's messages.
				item.Note = "a page runs only from a node whose id is letters, digits, - and _, at most 64 of them"
				b.Items = append(b.Items, item)
				continue
			}
			if len(b.Frames) >= MaxFrames {
				item.Note = fmt.Sprintf("the canvas runs at most %d pages at once", MaxFrames)
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

// frameFromNode reads a file node as a live frame: its page checked as
// canvas_show checks it, its root the page's folder unless the session widened
// it (Board.Roots), its size the node's within the frame bounds.
func frameFromNode(b *Board, n object, item Item) (Frame, string) {
	ext := n.ext()
	page, err := resolvePage(b.Link.Workspace, item.File, b.Roots[item.ID])
	if err != nil && b.Roots[item.ID] != "" {
		// The page left the root it was shown with: its own folder then.
		page, err = resolvePage(b.Link.Workspace, item.File, "")
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
	gen := b.Gens[item.ID]
	if gen < 1 {
		gen = 1
	}
	return Frame{
		ID: item.ID, Path: page.path, Root: page.root, Workspace: b.Link.Workspace,
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

// writeLinked writes a linked board's frames back into its file: their
// geometry and page on the nodes they came from, a node for a new frame, and
// the removal of a frame's node with its edges. Every other node, edge and key
// stays as it was; the generations go to the bundle.
func writeLinked(b *Board) error {
	// Two chats linked to one file take turns, and each applies only its own
	// changes to what the other wrote.
	mu, _ := fileLocks.LoadOrStore(session.CanonicalWorkspacePath(b.linkedFile()), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	// The file may have changed since it was read (Obsidian, another chat, the
	// agent): this board's changes are applied to what is there now, and only
	// they - a node it did not touch keeps what the file says.
	f, err := os.Open(b.linkedFile())
	if err != nil {
		return fmt.Errorf("the linked canvas %s: %w", b.Link.Path, err)
	}
	doc, err := ParseDocument(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("the linked canvas %s: %w", b.Link.Path, err)
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
			// An address of the removed frame never applies again, should a
			// node of that id come back.
			b.Gens[id]++
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
	return writeFileAtomic(b.linkedFile(), doc.Marshal())
}

// fileLocks serialise the read and the write of one linked file in this
// process (canonical path -> *sync.Mutex), taken inside a board's own lock.
var fileLocks sync.Map

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
	// workspace, and no name below it a dot one; the missing folders are
	// made by the export.
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
		return "", fmt.Errorf("%w: %s", ErrCanvasExists, p)
	}
	return rel, nil
}

// LinkFile makes a .canvas file of the workspace the chat's canvas: from now
// on its frames and nodes are the file's, and changes are written back to it.
// The chat's own frames wait in the bundle until the link is undone.
func LinkFile(bundle, workspace, p string) (Board, error) {
	rel, err := resolveCanvasFile(workspace, p, true)
	if err != nil {
		return Board{}, err
	}
	f, err := os.Open(filepath.Join(workspace, filepath.FromSlash(rel)))
	if err != nil {
		return Board{}, err
	}
	_, err = ParseDocument(f)
	_ = f.Close()
	if err != nil {
		return Board{}, fmt.Errorf("%s: %w", p, err)
	}
	var board Board
	err = mutate(bundle, func(b *Board) (string, error) {
		if b.Link == nil {
			b.own = b.Frames
		}
		link := &Link{Path: rel, Workspace: workspace}
		// The generations stay: relinking a file must not bring a revoked
		// address back.
		*b = Board{Version: b.Version, Revision: b.Revision, Link: link, own: b.own, Gens: b.Gens, Roots: b.Roots}
		readLinked(b)
		return "", nil
	}, func() string { return ReasonLinked }, &board)
	return board, err
}

// Unlink gives the chat its own board back; the file stays as it is.
func Unlink(bundle string) (Board, error) {
	var board Board
	err := mutate(bundle, func(b *Board) (string, error) {
		if b.Link == nil {
			return "", ErrNotLinked
		}
		*b = Board{Version: b.Version, Revision: b.Revision, Frames: b.own, Gens: b.Gens, Roots: b.Roots}
		return "", nil
	}, func() string { return ReasonUnlinked }, &board)
	return board, err
}

// Export writes the chat's own board as a new .canvas file of the workspace
// and links the canvas to it, so the board opens in Obsidian as it is.
func Export(bundle, workspace, p string) (Board, error) {
	rel, err := resolveCanvasFile(workspace, p, false)
	if err != nil {
		return Board{}, err
	}
	var board Board
	created := ""
	err = mutate(bundle, func(b *Board) (string, error) {
		if b.Link != nil {
			return "", fmt.Errorf("%w: %s", ErrAlreadyLinked, b.Link.Path)
		}
		doc := &Document{}
		frames := make([]Frame, 0, len(b.Frames))
		for _, fr := range b.Frames {
			// A frame of another workspace (a subagent's worktree) has no
			// path in this file's vault and stays behind.
			if !session.SameWorkspacePath(fr.Workspace, workspace) {
				continue
			}
			frames = append(frames, fr)
		}
		target := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := writeFileAtomic(target, doc.Marshal()); err != nil {
			return "", err
		}
		created = target
		own := b.Frames
		*b = Board{Version: b.Version, Revision: b.Revision, Link: &Link{Path: rel, Workspace: workspace}, own: own, Gens: b.Gens, Roots: b.Roots}
		readLinked(b)
		// The frames move into the file with ids in Obsidian's shape.
		for _, fr := range frames {
			fr.ID = newNodeID()
			fr.Workspace = workspace
			fr.Gen = 1
			b.Frames = append(b.Frames, fr)
		}
		b.dirty = true
		return "", nil
	}, func() string { return ReasonLinked }, &board)
	if err != nil && created != "" {
		// The board was not linked: the file this export made goes too, or a
		// retry would find it there.
		_ = os.Remove(created)
	}
	return board, err
}

// NodeChange moves or resizes one node of the board: a frame, or a text card,
// a link or a group of a linked file.
type NodeChange struct {
	ID                  string
	X, Y, Width, Height *int
}

// MaxNodeChanges bounds one change of nodes (a group dragged with all it holds).
const MaxNodeChanges = 500

// UpdateNodes applies the changes in one write: all of them, or none when one
// names a node the board does not hold.
func UpdateNodes(bundle string, changes []NodeChange) (Board, error) {
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
	err := mutate(bundle, func(b *Board) (string, error) {
		for _, c := range changes {
			if err := applyNodeChange(b, c); err != nil {
				return "", err
			}
		}
		b.dirty = true
		return frameID, nil
	}, func() string { return ReasonUpdated }, &board)
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
		if c.Width != nil || c.Height != nil {
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
	if b.Link != nil {
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
	}
	return fmt.Errorf("%w: %s", ErrFrameNotFound, c.ID)
}

// LinkStamp digests the linked file's size and time, so a reader of the
// board sees that someone else (Obsidian, the agent) changed it.
func LinkStamp(b Board) string {
	if b.Link == nil {
		return ""
	}
	info, err := os.Stat(b.linkedFile())
	if err != nil {
		return "missing"
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode([]int64{info.Size(), info.ModTime().UnixNano()})
	return strings.TrimSpace(buf.String())
}
