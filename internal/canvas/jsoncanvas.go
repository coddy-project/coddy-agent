package canvas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// JSON Canvas 1.0 (https://jsoncanvas.org), the open format of Obsidian's
// canvases: a document of nodes (text, file, link, group) and edges between
// them. A board linked to a .canvas file of the workspace reads it and writes
// it back here. Everything the canvas does not know - fields of a later
// version, another tool's extensions, the order of the keys - is kept as it
// was, and the file is written the way Obsidian writes it: one node and one
// edge per line, so a change shows in git as the lines it changed.

// Node types of JSON Canvas 1.0.
const (
	NodeText  = "text"
	NodeFile  = "file"
	NodeLink  = "link"
	NodeGroup = "group"
)

// member is one key of a JSON object, its value kept as written.
type member struct {
	Key   string
	Value json.RawMessage
}

// object is a JSON object with its keys in their order.
type object []member

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Value, true
		}
	}
	return nil, false
}

func (o object) str(key string) string {
	raw, ok := o.get(key)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func (o object) num(key string) int {
	raw, ok := o.get(key)
	if !ok {
		return 0
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0
	}
	return int(f)
}

// set puts a value at its key's place, or appends the key.
func (o *object) set(key string, value any) {
	raw, err := encodeJSON(value)
	if err != nil {
		return
	}
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Value = raw
			return
		}
	}
	*o = append(*o, member{Key: key, Value: raw})
}

func (o *object) del(key string) {
	for i := range *o {
		if (*o)[i].Key == key {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := encodeJSON(m.Key)
		b.Write(k)
		b.WriteByte(':')
		var compact bytes.Buffer
		if err := json.Compact(&compact, m.Value); err != nil {
			return nil, err
		}
		b.Write(compact.Bytes())
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o *object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}
	var out object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		out = append(out, member{Key: key, Value: raw})
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	*o = out
	return nil
}

// Document is a JSON Canvas file: its top-level keys, its nodes and its edges,
// each as written.
type Document struct {
	top   object
	nodes []object
	edges []object
	// newlineAtEnd says the file ended with a newline. Obsidian ends one at
	// its closing brace; a file another tool ended with one keeps it.
	newlineAtEnd bool
}

// encodeJSON writes a value the way JSON.stringify, which Obsidian saves
// with, does: quotes, backslashes and control characters escaped, every other
// character as it is. Go's encoder would also escape <, > and & (turned off)
// and U+2028 and U+2029 (always), so a value Coddy writes would differ from
// the one Obsidian writes back and show in a diff.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if !bytes.Contains(out, []byte(`\u202`)) {
		return out, nil
	}
	// Outside strings there is no backslash, and inside one every backslash
	// starts a two-byte or a \uXXXX escape: copying escapes whole keeps an
	// escaped backslash before "u2028" from being taken for one.
	res := make([]byte, 0, len(out))
	for i := 0; i < len(out); i++ {
		if out[i] != '\\' || i+1 >= len(out) {
			res = append(res, out[i])
			continue
		}
		if i+5 < len(out) && string(out[i+1:i+5]) == "u202" && (out[i+5] == '8' || out[i+5] == '9') {
			if out[i+5] == '8' {
				res = append(res, "\u2028"...)
			} else {
				res = append(res, "\u2029"...)
			}
			i += 5
			continue
		}
		res = append(res, out[i], out[i+1])
		i++
	}
	return res, nil
}

// maxDocumentBytes bounds a .canvas file the board reads.
const maxDocumentBytes = 8 << 20

// ParseDocument reads a JSON Canvas document. Nodes without an id or a type
// and edges without their ends are kept as they are but never shown.
func ParseDocument(r io.Reader) (*Document, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDocumentBytes {
		return nil, fmt.Errorf("the canvas file is over %d MB", maxDocumentBytes>>20)
	}
	raw = bytes.TrimPrefix(raw, utf8BOM)
	d := &Document{newlineAtEnd: bytes.HasSuffix(raw, []byte("\n"))}
	if len(bytes.TrimSpace(raw)) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(raw, &d.top); err != nil {
		return nil, fmt.Errorf("not a JSON Canvas file: %w", err)
	}
	for _, key := range []string{"nodes", "edges"} {
		v, ok := d.top.get(key)
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue
		}
		var list []object
		if err := json.Unmarshal(v, &list); err != nil {
			return nil, fmt.Errorf("not a JSON Canvas file: %s: %w", key, err)
		}
		if key == "nodes" {
			d.nodes = list
		} else {
			d.edges = list
		}
	}
	return d, nil
}

// Marshal writes the document the way Obsidian does (obsidianStringify):
// the top-level keys in their order, nodes and edges first if the file had
// none, and the file ending where it ended (Obsidian's at the closing brace).
func (d *Document) Marshal() []byte {
	top := d.top
	if _, ok := top.get("nodes"); !ok {
		top = append(object{{Key: "nodes"}}, top...)
	}
	if _, ok := top.get("edges"); !ok {
		top = append(top, member{Key: "edges"})
	}
	doc := make(object, len(top))
	for i, m := range top {
		doc[i] = m
		switch m.Key {
		case "nodes":
			doc[i].Value = listJSON(d.nodes)
		case "edges":
			doc[i].Value = listJSON(d.edges)
		}
	}
	raw, err := doc.MarshalJSON()
	if err == nil {
		raw, err = obsidianStringify(raw)
	}
	if err != nil {
		raw = []byte("{}")
	}
	if d.newlineAtEnd {
		raw = append(raw, '\n')
	}
	return raw
}

func listJSON(list []object) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, o := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		raw, err := o.MarshalJSON()
		if err != nil {
			raw = []byte("{}")
		}
		b.Write(raw)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// Item is a node of a linked board that is not a live frame: a text card, a
// link, a group, a file the canvas does not run.
type Item struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Color  string `json:"color,omitempty"`
	Text   string `json:"text,omitempty"`
	File   string `json:"file,omitempty"`
	URL    string `json:"url,omitempty"`
	Label  string `json:"label,omitempty"`
	// Note says why a file node is not a live frame, when it looks like one.
	Note string `json:"note,omitempty"`
}

// Edge is a line between two nodes of a linked board.
type Edge struct {
	ID       string `json:"id"`
	FromNode string `json:"fromNode"`
	FromSide string `json:"fromSide,omitempty"`
	FromEnd  string `json:"fromEnd,omitempty"`
	ToNode   string `json:"toNode"`
	ToSide   string `json:"toSide,omitempty"`
	ToEnd    string `json:"toEnd,omitempty"`
	Color    string `json:"color,omitempty"`
	Label    string `json:"label,omitempty"`
}

// maxItemText bounds the text of a card the board hands the web UI.
const maxItemText = 20000

func (d *Document) findNode(id string) int {
	for i, n := range d.nodes {
		if n.str("id") == id {
			return i
		}
	}
	return -1
}

// removeNode drops a node and every edge that ends at it.
func (d *Document) removeNode(id string) bool {
	i := d.findNode(id)
	if i < 0 {
		return false
	}
	d.nodes = append(d.nodes[:i], d.nodes[i+1:]...)
	kept := d.edges[:0]
	for _, e := range d.edges {
		if e.str("fromNode") != id && e.str("toNode") != id {
			kept = append(kept, e)
		}
	}
	d.edges = kept
	return true
}

func (d *Document) edgeList() []Edge {
	var out []Edge
	for _, e := range d.edges {
		edge := Edge{
			ID: e.str("id"), FromNode: e.str("fromNode"), FromSide: e.str("fromSide"), FromEnd: e.str("fromEnd"),
			ToNode: e.str("toNode"), ToSide: e.str("toSide"), ToEnd: e.str("toEnd"),
			Color: e.str("color"), Label: cut(e.str("label"), maxReportText),
		}
		if edge.ID == "" || edge.FromNode == "" || edge.ToNode == "" {
			continue
		}
		out = append(out, edge)
	}
	return out
}

// coddyExt is the namespaced key the canvas keeps a frame's own settings in
// on its node: Obsidian keeps an unknown key of a node, and a node without one
// is read from its standard fields alone.
const coddyExt = "coddy"

// frameExt holds what JSON Canvas has no field for and is harmless to take
// from a file: never a root, which would widen what a page reads (the
// session keeps those, Board.Roots).
type frameExt struct {
	Device  string `json:"device,omitempty"`
	Title   string `json:"title,omitempty"`
	AddedBy string `json:"added_by,omitempty"`
}

func (o object) ext() frameExt {
	var e frameExt
	if raw, ok := o.get(coddyExt); ok {
		_ = json.Unmarshal(raw, &e)
	}
	return e
}

// newNodeID returns an id in Obsidian's own shape: 16 hex digits.
func newNodeID() string {
	id := newFrameID()
	var sum uint64 = 1469598103934665603
	for _, c := range id {
		sum ^= uint64(c)
		sum *= 1099511628211
	}
	return fmt.Sprintf("%016s", strconv.FormatUint(sum, 16))
}
