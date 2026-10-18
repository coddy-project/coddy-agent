package canvas

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// obsidianCanvas is a board as Obsidian writes it, with a key of a later
// version on a node, one at the top level and an extension of another tool.
const obsidianCanvas = `{
	"nodes":[
		{"id":"a1b2c3d4e5f60718","type":"group","x":-60,"y":-80,"width":1600,"height":1000,"label":"Shop"},
		{"id":"0123456789abcdef","type":"text","text":"# Checkout\nThe flow","x":0,"y":0,"width":260,"height":120,"color":"4","styleAttributes":{"shape":"pill"}},
		{"id":"fedcba9876543210","type":"file","file":"site/index.html","x":400,"y":0,"width":390,"height":844},
		{"id":"1111111111111111","type":"file","file":"notes/brief.md","x":0,"y":200,"width":300,"height":200},
		{"id":"2222222222222222","type":"link","url":"https://example.com","x":0,"y":500,"width":300,"height":120},
		{"id":"3333333333333333","type":"file","file":"../outside.html","x":0,"y":700,"width":400,"height":400},
		{"id":"a b\"<c>","type":"file","file":"site/about.html","x":0,"y":1200,"width":400,"height":400}
	],
	"edges":[
		{"id":"e1","fromNode":"0123456789abcdef","fromSide":"right","toNode":"fedcba9876543210","toSide":"left","label":"opens"},
		{"id":"e2","fromNode":"1111111111111111","toNode":"0123456789abcdef"}
	],
	"metadata":{"version":"1.0-1.0"}
}
`

// obsidianBoard is a workspace whose board design/shop.canvas is
// obsidianCanvas.
func obsidianBoard(t *testing.T) (Ref, string) {
	t.Helper()
	ws, bundle := workspace(t, "site/index.html", "site/about.html", "notes/brief.md", "other/index.html")
	file := filepath.Join(ws, "design", "shop.canvas")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(obsidianCanvas), 0o644); err != nil {
		t.Fatal(err)
	}
	return Ref{Bundle: bundle, Workspace: ws, File: "design/shop.canvas"}, file
}

func readDoc(t *testing.T, file string) *Document {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	d, err := ParseDocument(f)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestADocumentRoundTripsWhatItDoesNotKnow(t *testing.T) {
	d, err := ParseDocument(strings.NewReader(obsidianCanvas))
	if err != nil {
		t.Fatal(err)
	}
	out := d.Marshal()
	again, err := ParseDocument(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Marshal(), out) {
		t.Fatal("a second round trip changed the file")
	}
	for _, want := range []string{`"styleAttributes":{"shape":"pill"}`, `"metadata":{"version":"1.0-1.0"}`, `"label":"opens"`} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("lost %s:\n%s", want, out)
		}
	}
	// As Obsidian writes them: a node of plain values on one line, keys in
	// their order; one holding an object (styleAttributes) one key a line.
	if !regexp.MustCompile(`\n\t\t\{"id":"fedcba9876543210","type":"file","file":"site/index.html",`).Match(out) {
		t.Fatalf("a flat node is not on one line in key order:\n%s", out)
	}
	if !regexp.MustCompile(`\n\t\t\{\n\t\t\t"id":"0123456789abcdef",\n\t\t\t"type":"text",\n(?s:.*)\t\t\t"styleAttributes":\{"shape":"pill"\}\n\t\t\},`).Match(out) {
		t.Fatalf("a node holding an object is not one key a line:\n%s", out)
	}
	var generic map[string]any
	if err := json.Unmarshal(out, &generic); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
}

func TestReadingAFileShowsItsPagesAsFramesAndLeavesTheFileAlone(t *testing.T) {
	ref, file := obsidianBoard(t)
	before, _ := os.ReadFile(file)
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if b.File != "design/shop.canvas" || b.Error != "" {
		t.Fatalf("board %q %q", b.File, b.Error)
	}
	if len(b.Frames) != 1 {
		t.Fatalf("frames %+v", b.Frames)
	}
	f := b.Frames[0]
	if f.ID != "fedcba9876543210" || f.Path != "site/index.html" || f.Root != "site" || f.Device != DevicePhone || f.X != 400 {
		t.Fatalf("frame %+v", f)
	}
	kinds := map[string]int{}
	var outside Item
	for _, it := range b.Items {
		kinds[it.Type]++
		if it.ID == "3333333333333333" {
			outside = it
		}
	}
	if kinds[NodeGroup] != 1 || kinds[NodeText] != 1 || kinds[NodeLink] != 1 || kinds[NodeFile] != 3 {
		t.Fatalf("items %+v", b.Items)
	}
	if outside.Note == "" {
		t.Fatal("a page outside the workspace became a frame or says nothing")
	}
	if len(b.Edges) != 2 || b.Edges[0].Label != "opens" {
		t.Fatalf("edges %+v", b.Edges)
	}
	if _, err := Open(ref); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("reading and opening the board rewrote its file")
	}
}

func TestChangesOfABoardAreWrittenBackToItsFile(t *testing.T) {
	ref, file := obsidianBoard(t)
	res, err := Show(ref, ShowRequest{Path: "other/index.html", Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if !obsidianID.MatchString(res.Frame.ID) {
		t.Fatalf("a new node id %q is not in Obsidian's shape", res.Frame.ID)
	}
	d := readDoc(t, file)
	i := d.findNode(res.Frame.ID)
	if i < 0 {
		t.Fatal("the new frame is not a node of the file")
	}
	n := d.nodes[i]
	if n.str("type") != NodeFile || n.str("file") != "other/index.html" || n.num("width") != 1280 || n.num("height") != 800 {
		t.Fatalf("new node %s", mustJSON(n))
	}
	if n.ext().Title != "Other" {
		t.Fatalf("the title is not kept on the node: %s", mustJSON(n))
	}
	if n.num("x") < 1540 {
		t.Fatalf("the new frame overlaps the group at x=%d", n.num("x"))
	}
	if !bytes.Contains(d.Marshal(), []byte(`"styleAttributes":{"shape":"pill"}`)) {
		t.Fatal("a key Coddy does not know was lost")
	}

	x, y := 420, 40
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "fedcba9876543210", X: &x, Y: &y}}); err != nil {
		t.Fatal(err)
	}
	d = readDoc(t, file)
	moved := d.nodes[d.findNode("fedcba9876543210")]
	if moved.num("x") != 420 || moved.num("y") != 40 || moved.num("width") != 390 {
		t.Fatalf("moved node %s", mustJSON(moved))
	}
	if _, has := moved.get(coddyExt); has {
		t.Fatalf("a node Coddy only moved got its key: %s", mustJSON(moved))
	}

	tx, gx := 30, -100
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "0123456789abcdef", X: &tx}, {ID: "a1b2c3d4e5f60718", X: &gx}}); err != nil {
		t.Fatal(err)
	}
	d = readDoc(t, file)
	if d.nodes[d.findNode("0123456789abcdef")].num("x") != 30 || d.nodes[d.findNode("a1b2c3d4e5f60718")].num("x") != -100 {
		t.Fatal("a text card and a group did not move in the file")
	}
	before, _ := os.ReadFile(file)
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "0123456789abcdef", X: &gx}, {ID: "nope", X: &gx}}); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("a change naming an unknown node: %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Fatal("a refused change of nodes wrote part of itself")
	}

	if _, err := Remove(ref, "fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	d = readDoc(t, file)
	if d.findNode("fedcba9876543210") >= 0 {
		t.Fatal("the removed frame's node is still in the file")
	}
	for _, e := range d.edgeList() {
		if e.ToNode == "fedcba9876543210" || e.FromNode == "fedcba9876543210" {
			t.Fatalf("an edge of the removed node stayed: %+v", e)
		}
	}
	if len(d.edgeList()) != 1 {
		t.Fatalf("edges after the removal %+v", d.edgeList())
	}
}

func TestRotatingAFrameKeepsItsGenerationOutOfTheFile(t *testing.T) {
	ref, file := obsidianBoard(t)
	before, _ := os.ReadFile(file)
	_, f, err := Rotate(ref, "fedcba9876543210")
	if err != nil {
		t.Fatal(err)
	}
	if f.Gen != 2 {
		t.Fatalf("gen %d", f.Gen)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("a rotation wrote the workspace file")
	}
	b, _ := Load(ref)
	if got, _ := b.Frame("fedcba9876543210"); got.Gen != 2 {
		t.Fatalf("the generation did not survive a reload: %+v", got)
	}
}

func TestAnEditOfTheFileElsewhereIsWhatTheBoardShows(t *testing.T) {
	ref, file := obsidianBoard(t)
	if _, err := Load(ref); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(obsidianCanvas, `"x":400,"y":0,"width":390`, `"x":900,"y":0,"width":390`, 1)
	edited = strings.Replace(edited, `{"id":"2222222222222222"`, `{"id":"4444444444444444","type":"file","file":"site/about.html","x":0,"y":1200,"width":1280,"height":800},
		{"id":"2222222222222222"`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != 2 {
		t.Fatalf("frames after an outside edit %+v", b.Frames)
	}
	if f, _ := b.Frame("fedcba9876543210"); f.X != 900 {
		t.Fatalf("the moved node is at x=%d", f.X)
	}
}

func TestABoardFileThatCannotBeReadIsAnErrorOrRefused(t *testing.T) {
	ref, _ := obsidianBoard(t)
	ws := ref.Workspace
	outside := filepath.Join(t.TempDir(), "x.canvas")
	_ = os.WriteFile(outside, []byte(obsidianCanvas), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, ".obsidian"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, ".obsidian", "x.canvas"), []byte(obsidianCanvas), 0o644)
	for _, p := range []string{"site/index.html", outside, ".obsidian/x.canvas", "missing.canvas", "../x.canvas"} {
		r := ref
		r.File = p
		if _, err := Load(r); err == nil {
			t.Errorf("loaded %s", p)
		}
		if _, err := Open(r); err == nil {
			t.Errorf("opened %s", p)
		}
	}
	// A file that does not parse is a board with an error, written nothing.
	if err := os.WriteFile(filepath.Join(ws, "broken.canvas"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := ref
	r.File = "broken.canvas"
	b, err := Load(r)
	if err != nil || b.Error == "" || len(b.Frames) != 0 {
		t.Fatalf("a broken board %+v %v", b, err)
	}
	if _, err := Show(r, ShowRequest{Path: "site/index.html"}); err == nil {
		t.Fatal("a page went onto a file that does not parse")
	}
	if raw, _ := os.ReadFile(filepath.Join(ws, "broken.canvas")); string(raw) != "{nope" {
		t.Fatalf("a broken board was rewritten: %q", raw)
	}
}

func mustJSON(o object) string {
	raw, _ := o.MarshalJSON()
	return string(raw)
}

func TestAWriteLeavesTheNodesCoddyDidNotMoveAsTheyWere(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "other/index.html")
	// A node smaller than a frame may be: drawn at the bounds, kept as it is.
	doc := `{"nodes":[{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":10.5,"y":0,"width":200,"height":100}],"edges":[]}`
	if err := os.WriteFile(filepath.Join(ws, "small.canvas"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := Ref{Bundle: bundle, Workspace: ws, File: "small.canvas"}
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if f := b.Frames[0]; f.Width != MinWidth || f.Height != MinHeight {
		t.Fatalf("the frame is not at the bounds: %+v", f)
	}
	if _, err := Show(ref, ShowRequest{Path: "other/index.html"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "small.canvas"))
	if !bytes.Contains(raw, []byte(`{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":10.5,"y":0,"width":200,"height":100}`)) {
		t.Fatalf("a node Coddy did not move was rewritten:\n%s", raw)
	}
}

func TestAFileCannotWidenWhatItsPagesRead(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	doc := `{"nodes":[{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":0,"y":0,"width":1280,"height":800,"coddy":{"root":"."}}]}`
	if err := os.WriteFile(filepath.Join(ws, "wide.canvas"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(Ref{Bundle: bundle, Workspace: ws, File: "wide.canvas"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Frames[0].Root != "site" {
		t.Fatalf("a root named by the file was taken: %q", b.Frames[0].Root)
	}
}

func TestARootTheChatWidenedStaysWithTheChat(t *testing.T) {
	ref, file := obsidianBoard(t)
	res, err := Show(ref, ShowRequest{Path: "other/index.html", Root: "."})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Load(ref)
	if f, _ := b.Frame(res.Frame.ID); f.Root != "" {
		t.Fatalf("the widened root did not survive a reload: %q", f.Root)
	}
	if raw, _ := os.ReadFile(file); bytes.Contains(raw, []byte(`"root"`)) {
		t.Fatalf("a root reached the workspace file:\n%s", raw)
	}
	// Another chat reading the same file sees the page's own folder.
	_, otherBundle := workspace(t)
	theirs, err := Load(Ref{Bundle: otherBundle, Workspace: ref.Workspace, File: ref.File})
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := theirs.Frame(res.Frame.ID); f.Root != "other" {
		t.Fatalf("another chat took the widened root: %q", f.Root)
	}
	if _, err := Remove(ref, res.Frame.ID); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(ref.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	key := NodeKey(ref.File, res.Frame.ID)
	if _, kept := st.Roots[key]; kept {
		t.Fatalf("the root of a removed frame stayed: %+v", st.Roots)
	}
	// Its generation stays one up, so none of its addresses applies again.
	if st.Gens[key] != res.Frame.Gen+1 {
		t.Fatalf("the removed frame's generation %d, want %d", st.Gens[key], res.Frame.Gen+1)
	}
}

func TestAFailedShowLeavesNoNewBoardBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only bundle folder does not refuse a write on Windows")
	}
	ws, bundle := workspace(t, "site/index.html")
	// The bundle cannot take the bookkeeping: the new board must not stay.
	if err := os.Chmod(bundle, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bundle, 0o700) }()
	if _, err := Show(Ref{Bundle: bundle, Workspace: ws}, ShowRequest{Path: "site/index.html"}); err == nil {
		t.Skip("the bundle folder took a write anyway (running as root)")
	}
	if _, err := os.Stat(filepath.Join(ws, "canvas", "site.canvas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed show left its new board: %v", err)
	}
}

func TestTwoChatsOfOneFileKeepEachOthersChanges(t *testing.T) {
	refA, file := obsidianBoard(t)
	refB := refA
	refB.Bundle = filepath.Join(t.TempDir(), "sess_b")
	if err := os.MkdirAll(refB.Bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		x, y := 1000+i, 2000+i
		go func() {
			defer wg.Done()
			if _, err := UpdateNodes(refA, []NodeChange{{ID: "0123456789abcdef", X: &x}}); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := UpdateNodes(refB, []NodeChange{{ID: "2222222222222222", Y: &y}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	d := readDoc(t, file)
	if d.nodes[d.findNode("0123456789abcdef")].num("x") < 1000 || d.nodes[d.findNode("2222222222222222")].num("y") < 2000 {
		t.Fatalf("one chat's move undid the other's:\n%s", d.Marshal())
	}
	// A board read before a node was deleted in Obsidian does not bring it
	// back when it writes.
	c, _ := Load(refB)
	raw, _ := os.ReadFile(file)
	doc, _ := ParseDocument(bytes.NewReader(raw))
	doc.removeNode("fedcba9876543210")
	if err := os.WriteFile(file, doc.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Items[findItem(t, c, "1111111111111111")].Y = 999
	if err := writeBoardFile(&c); err != nil {
		t.Fatal(err)
	}
	if readDoc(t, file).findNode("fedcba9876543210") >= 0 {
		t.Fatal("a stale board brought back a node deleted in the file")
	}
}

func findItem(t *testing.T, b Board, id string) int {
	t.Helper()
	for i, it := range b.Items {
		if it.ID == id {
			return i
		}
	}
	t.Fatalf("no item %s", id)
	return -1
}

func TestFrameOfIsTheFrameLoadGives(t *testing.T) {
	ref, _ := obsidianBoard(t)
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range b.Frames {
		got, err := FrameOf(ref, want.ID)
		if err != nil || got != want {
			t.Fatalf("FrameOf(%s) = %+v %v, Load gives %+v", want.ID, got, err, want)
		}
	}
	for _, id := range []string{"3333333333333333", "0123456789abcdef", "a b\"<c>", "missing"} {
		if _, err := FrameOf(ref, id); err == nil {
			t.Errorf("FrameOf(%s) gave a frame", id)
		}
	}
}
