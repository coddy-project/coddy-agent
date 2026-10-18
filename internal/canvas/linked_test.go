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

func linkedWorkspace(t *testing.T) (ws, bundle, file string) {
	t.Helper()
	ws, bundle = workspace(t, "site/index.html", "site/about.html", "notes/brief.md", "other/index.html")
	file = filepath.Join(ws, "design", "shop.canvas")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(obsidianCanvas), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, bundle, file
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

func TestLinkingAFileShowsItsPagesAsFramesAndLeavesTheFileAlone(t *testing.T) {
	ws, bundle, file := linkedWorkspace(t)
	before, _ := os.ReadFile(file)
	b, err := LinkFile(bundle, ws, "design/shop.canvas")
	if err != nil {
		t.Fatal(err)
	}
	if b.Link == nil || b.Link.Path != "design/shop.canvas" || b.LinkError != "" {
		t.Fatalf("link %+v %q", b.Link, b.LinkError)
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
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("linking rewrote the file")
	}
}

func TestChangesOfALinkedBoardAreWrittenBackToTheFile(t *testing.T) {
	ws, bundle, file := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "other/index.html", Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(res.Frame.ID) {
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
	if _, _, err := Update(bundle, "fedcba9876543210", Patch{X: &x, Y: &y}); err != nil {
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
	if _, err := UpdateNodes(bundle, []NodeChange{{ID: "0123456789abcdef", X: &tx}, {ID: "a1b2c3d4e5f60718", X: &gx}}); err != nil {
		t.Fatal(err)
	}
	d = readDoc(t, file)
	if d.nodes[d.findNode("0123456789abcdef")].num("x") != 30 || d.nodes[d.findNode("a1b2c3d4e5f60718")].num("x") != -100 {
		t.Fatal("a text card and a group did not move in the file")
	}
	before, _ := os.ReadFile(file)
	if _, err := UpdateNodes(bundle, []NodeChange{{ID: "0123456789abcdef", X: &gx}, {ID: "nope", X: &gx}}); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("a change naming an unknown node: %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Fatal("a refused change of nodes wrote part of itself")
	}

	if _, err := Remove(bundle, "fedcba9876543210"); err != nil {
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

func TestRotatingALinkedFrameKeepsItsGenerationOutOfTheFile(t *testing.T) {
	ws, bundle, file := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(file)
	_, f, err := Rotate(bundle, "fedcba9876543210")
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
	b, _ := Load(bundle)
	if got, _ := b.Frame("fedcba9876543210"); got.Gen != 2 {
		t.Fatalf("the generation did not survive a reload: %+v", got)
	}
}

func TestAnEditOfTheFileElsewhereIsWhatTheBoardShows(t *testing.T) {
	ws, bundle, file := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(obsidianCanvas, `"x":400,"y":0,"width":390`, `"x":900,"y":0,"width":390`, 1)
	edited = strings.Replace(edited, `{"id":"2222222222222222"`, `{"id":"4444444444444444","type":"file","file":"site/about.html","x":0,"y":1200,"width":1280,"height":800},
		{"id":"2222222222222222"`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(bundle)
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

func TestUnlinkGivesTheChatItsOwnBoardBack(t *testing.T) {
	ws, bundle, _ := linkedWorkspace(t)
	own, err := Show(bundle, ShowRequest{Workspace: ws, Path: "other/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	b, err := Unlink(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if b.Link != nil || len(b.Frames) != 1 || b.Frames[0].ID != own.Frame.ID {
		t.Fatalf("own board back %+v", b)
	}
	if _, err := Unlink(bundle); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("second unlink: %v", err)
	}
}

func TestExportWritesTheBoardAsAnObsidianCanvas(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "other/index.html")
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html", Title: "Shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "other/index.html", Device: DevicePhone}); err != nil {
		t.Fatal(err)
	}
	b, err := Export(bundle, ws, "design/board.canvas")
	if err != nil {
		t.Fatal(err)
	}
	if b.Link == nil || b.Link.Path != "design/board.canvas" || len(b.Frames) != 2 {
		t.Fatalf("exported board %+v", b)
	}
	d := readDoc(t, filepath.Join(ws, "design", "board.canvas"))
	if len(d.nodes) != 2 || d.nodes[0].str("type") != NodeFile || d.nodes[0].str("file") != "site/index.html" || d.nodes[1].num("width") != 390 {
		t.Fatalf("exported nodes %s", d.Marshal())
	}
	if _, err := Export(bundle, ws, "design/board2.canvas"); err == nil {
		t.Fatal("an export of a linked board went through")
	}
	if _, err := Unlink(bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(bundle, ws, "design/board.canvas"); !errors.Is(err, ErrCanvasExists) {
		t.Fatalf("an export over an existing file: %v", err)
	}
}

func TestLinkFileRefusals(t *testing.T) {
	ws, bundle, _ := linkedWorkspace(t)
	outside := filepath.Join(t.TempDir(), "x.canvas")
	_ = os.WriteFile(outside, []byte(obsidianCanvas), 0o644)
	_ = os.MkdirAll(filepath.Join(ws, ".obsidian"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, ".obsidian", "x.canvas"), []byte(obsidianCanvas), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "broken.canvas"), []byte("{nope"), 0o644)
	for _, p := range []string{"site/index.html", outside, ".obsidian/x.canvas", "missing.canvas", "broken.canvas", "../x.canvas"} {
		if _, err := LinkFile(bundle, ws, p); err == nil {
			t.Errorf("linked %s", p)
		}
	}
	for _, p := range []string{"site/.hidden.canvas", "../escape.canvas", "notes.txt"} {
		if _, err := Export(bundle, ws, p); err == nil {
			t.Errorf("exported to %s", p)
		}
	}
}

func mustJSON(o object) string {
	raw, _ := o.MarshalJSON()
	return string(raw)
}

func TestALinkedBoardTakesNoPageOfAnotherWorkspace(t *testing.T) {
	ws, bundle, _ := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	other, _ := workspace(t, "site/index.html")
	if _, err := Show(bundle, ShowRequest{Workspace: other, Path: "site/index.html"}); err == nil || !strings.Contains(err.Error(), "another working directory") {
		t.Fatalf("a page of a worktree became a node of the file: %v", err)
	}
}

func TestAWriteLeavesTheNodesCoddyDidNotMoveAsTheyWere(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "other/index.html")
	file := filepath.Join(ws, "small.canvas")
	// A node smaller than a frame may be: drawn at the bounds, kept as it is.
	doc := `{"nodes":[{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":10.5,"y":0,"width":200,"height":100}],"edges":[]}`
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := LinkFile(bundle, ws, "small.canvas")
	if err != nil {
		t.Fatal(err)
	}
	if f := b.Frames[0]; f.Width != MinWidth || f.Height != MinHeight {
		t.Fatalf("the frame is not at the bounds: %+v", f)
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "other/index.html"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !bytes.Contains(raw, []byte(`{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":10.5,"y":0,"width":200,"height":100}`)) {
		t.Fatalf("a node Coddy did not move was rewritten:\n%s", raw)
	}
}

func TestAFileCannotWidenWhatItsPagesRead(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	file := filepath.Join(ws, "wide.canvas")
	doc := `{"nodes":[{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":0,"y":0,"width":1280,"height":800,"coddy":{"root":"."}}]}`
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := LinkFile(bundle, ws, "wide.canvas")
	if err != nil {
		t.Fatal(err)
	}
	if b.Frames[0].Root != "site" {
		t.Fatalf("a root named by the file was taken: %q", b.Frames[0].Root)
	}
}

func TestARootTheSessionWidenedStaysWithTheSession(t *testing.T) {
	ws, bundle, file := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "other/index.html", Root: "."})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Load(bundle)
	if f, _ := b.Frame(res.Frame.ID); f.Root != "" {
		t.Fatalf("the widened root did not survive a reload: %q", f.Root)
	}
	if raw, _ := os.ReadFile(file); bytes.Contains(raw, []byte(`"root"`)) {
		t.Fatalf("a root reached the workspace file:\n%s", raw)
	}
	if _, err := Remove(bundle, res.Frame.ID); err != nil {
		t.Fatal(err)
	}
	var stored Board
	raw, _ := os.ReadFile(filepath.Join(bundle, FileName))
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if _, kept := stored.Roots[res.Frame.ID]; kept {
		t.Fatalf("the root of a removed frame stayed:\n%s", raw)
	}
	// Its generation stays one up, so none of its addresses applies again.
	if stored.Gens[res.Frame.ID] != res.Frame.Gen+1 {
		t.Fatalf("the removed frame's generation %d, want %d", stored.Gens[res.Frame.ID], res.Frame.Gen+1)
	}
}

func TestAFailedExportLeavesNoFileBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only bundle folder does not refuse a write on Windows")
	}
	ws, bundle := workspace(t, "site/index.html")
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html"}); err != nil {
		t.Fatal(err)
	}
	// The bundle cannot take the board any more: the export must not stay.
	if err := os.Chmod(bundle, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bundle, 0o700) }()
	if _, err := Export(bundle, ws, "board.canvas"); err == nil {
		t.Skip("the bundle folder took a write anyway (running as root)")
	}
	if _, err := os.Stat(filepath.Join(ws, "board.canvas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed export left its file: %v", err)
	}
}

func TestRelinkingAFileNeverBringsARevokedAddressBack(t *testing.T) {
	ws, bundle, _ := linkedWorkspace(t)
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Rotate(bundle, "fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	gen := func() int {
		b, _ := Load(bundle)
		f, _ := b.Frame("fedcba9876543210")
		return f.Gen
	}
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	if g := gen(); g != 2 {
		t.Fatalf("relinking the file reset the frame to generation %d", g)
	}
	if _, err := Unlink(bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
		t.Fatal(err)
	}
	if g := gen(); g != 2 {
		t.Fatalf("unlinking and linking again reset the frame to generation %d", g)
	}
}

func TestTwoBoardsOfOneFileKeepEachOthersChanges(t *testing.T) {
	ws, bundleA, file := linkedWorkspace(t)
	bundleB := filepath.Join(t.TempDir(), "sess_b")
	if err := os.MkdirAll(bundleB, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bundle := range []string{bundleA, bundleB} {
		if _, err := LinkFile(bundle, ws, "design/shop.canvas"); err != nil {
			t.Fatal(err)
		}
	}
	// Both chats read the file before either writes, as two requests at once
	// do: each then writes what it changed.
	a, _ := Load(bundleA)
	b, _ := Load(bundleB)
	a.Items[findItem(t, a, "0123456789abcdef")].X = 77
	a.dirty = true
	if err := writeLinked(&a); err != nil {
		t.Fatal(err)
	}
	b.Items[findItem(t, b, "2222222222222222")].X = 88
	b.dirty = true
	if err := writeLinked(&b); err != nil {
		t.Fatal(err)
	}
	d := readDoc(t, file)
	if d.nodes[d.findNode("0123456789abcdef")].num("x") != 77 || d.nodes[d.findNode("2222222222222222")].num("x") != 88 {
		t.Fatalf("one chat's move undid the other's:\n%s", d.Marshal())
	}
	// A frame deleted in Obsidian meanwhile stays deleted.
	c, _ := Load(bundleB)
	raw, _ := os.ReadFile(file)
	doc, _ := ParseDocument(bytes.NewReader(raw))
	doc.removeNode("fedcba9876543210")
	if err := os.WriteFile(file, doc.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Items[findItem(t, c, "1111111111111111")].Y = 999
	if err := writeLinked(&c); err != nil {
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
