package canvas

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// obsidianSample is sample.canvas of github.com/obsidianmd/jsoncanvas (MIT),
// commit 8ca9fdca, a canvas as Obsidian saves it: tabs, one node and one edge
// per line, no newline after the closing brace.
func obsidianSample(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "jsoncanvas-sample.canvas"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestObsidiansSampleRoundTripsByteForByte(t *testing.T) {
	raw := obsidianSample(t)
	d, err := ParseDocument(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if out := d.Marshal(); !bytes.Equal(out, raw) {
		t.Fatalf("the sample changed on a round trip:\n%q\nwant\n%q", out[len(out)-20:], raw[len(raw)-20:])
	}
}

func TestAMoveOfTheSampleChangesOnlyItsNodesLine(t *testing.T) {
	raw := obsidianSample(t)
	ws, bundle := workspace(t)
	if err := os.WriteFile(filepath.Join(ws, "sample.canvas"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := Ref{Bundle: bundle, Workspace: ws, File: "sample.canvas"}
	x := 41
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "59e896bc8da20699", X: &x}}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(ws, "sample.canvas"))
	was, now := strings.Split(string(raw), "\n"), strings.Split(string(after), "\n")
	if len(was) != len(now) {
		t.Fatalf("the file has %d lines, it had %d:\n%s", len(now), len(was), after)
	}
	for i := range was {
		if was[i] == now[i] {
			continue
		}
		if !strings.Contains(was[i], `"id":"59e896bc8da20699"`) || !strings.Contains(now[i], `"x":41,`) {
			t.Fatalf("line %d changed:\n%s\n%s", i+1, was[i], now[i])
		}
	}
}

func TestCoddyWritesCharactersAsObsidianDoes(t *testing.T) {
	ws, bundle := workspace(t, "site/a&b.html")
	doc := `{"nodes":[],"edges":[]}`
	if err := os.WriteFile(filepath.Join(ws, "board.canvas"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := Ref{Bundle: bundle, Workspace: ws, File: "board.canvas"}
	// JSON.stringify, which Obsidian saves with, escapes only quotes,
	// backslashes and control characters.
	title := "Cart <beta> & co\u2028\"q\" \\ \x01"
	if _, err := Show(ref, ShowRequest{Path: "site/a&b.html", Title: title}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "board.canvas"))
	for _, want := range []string{`"file":"site/a&b.html"`, "\"title\":\"Cart <beta> & co\u2028\\\"q\\\" \\\\ \\u0001\""} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("want %s in:\n%s", want, raw)
		}
	}
	if bytes.HasSuffix(raw, []byte("\n")) {
		t.Errorf("a file that ended at its brace gained a newline:\n%q", raw)
	}
	b, _ := Load(ref)
	if f := b.Frames[0]; f.Title != title {
		t.Fatalf("the title read back as %q", f.Title)
	}
}

func TestAFileThatEndedWithANewlineKeepsIt(t *testing.T) {
	ref, file := obsidianBoard(t)
	x := 5
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "0123456789abcdef", X: &x}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	if !bytes.HasSuffix(raw, []byte("}\n")) || bytes.HasSuffix(raw, []byte("\n\n")) {
		t.Fatalf("the file's end changed: %q", raw[len(raw)-10:])
	}
}

func TestANewBoardEndsAsObsidianDoes(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	if _, err := Show(Ref{Bundle: bundle, Workspace: ws}, ShowRequest{Path: "site/index.html"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "canvas", "site.canvas"))
	if !bytes.HasSuffix(raw, []byte("\n\t\"edges\":[]\n}")) {
		t.Fatalf("a new board ends %q", raw[len(raw)-12:])
	}
}

func TestWhatObsidianSavedRoundTripsByteForByte(t *testing.T) {
	// Saved by Obsidian 1.14.4 itself: the sample with a node of Coddy's
	// holding the coddy key, which Obsidian writes one key a line.
	raw, err := os.ReadFile(filepath.Join("testdata", "obsidian-1.14.4-saved.canvas"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDocument(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if out := d.Marshal(); !bytes.Equal(out, raw) {
		t.Fatalf("Obsidian's own file changed on a round trip:\n%s", out)
	}
}

func TestObsidianStringifyFollowsObsidian(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1,"b":"x","c":true}`, `{"a":1,"b":"x","c":true}`},
		// null is an object to typeof, so it spreads its object.
		{`{"a":null}`, "{\n\t\"a\":null\n}"},
		{`{"a":[1,2],"b":"x"}`, "{\n\t\"a\":[1,2],\n\t\"b\":\"x\"\n}"},
		{`[1,{"b":2}]`, "[\n\t1,\n\t{\"b\":2}\n]"},
		{`{"a":{},"b":[]}`, "{\n\t\"a\":{},\n\t\"b\":[]\n}"},
		{`[1,null]`, "[\n\t1,\n\tnull\n]"},
		{`{"s":"\u003cb\u003e \u00e9"}`, `{"s":"<b> é"}`},
	}
	for _, c := range cases {
		got, err := obsidianStringify(json.RawMessage(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.in, got, c.want)
		}
	}
}

func TestANodeCoddyAddsIsWrittenAsObsidianWritesIt(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	if err := os.WriteFile(filepath.Join(ws, "coddy.canvas"), obsidianSample(t), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := Ref{Bundle: bundle, Workspace: ws, File: "coddy.canvas"}
	res, err := Show(ref, ShowRequest{Path: "site/index.html", Title: "Cart <beta> & \"co\"", Device: DevicePhone, AddedBy: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "coddy.canvas"))
	saved, _ := os.ReadFile(filepath.Join("testdata", "obsidian-1.14.4-saved.canvas"))
	// The same page shown on the same sample: Obsidian wrote this node so.
	want := strings.ReplaceAll(nodeBlock(t, string(saved), "228bea3db36bd432"), "228bea3db36bd432", res.Frame.ID)
	if got := nodeBlock(t, string(raw), res.Frame.ID); got != want {
		t.Fatalf("Coddy wrote\n%s\nObsidian writes\n%s", got, want)
	}
}

func TestANodeCoddyMovesTakesObsidiansKeyOrder(t *testing.T) {
	ref, file := obsidianBoard(t)
	x := 7
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "0123456789abcdef", X: &x}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	block := nodeBlock(t, string(raw), "0123456789abcdef")
	// getData of a node Obsidian changed: the other keys, then the geometry,
	// then the colour.
	keys := regexp.MustCompile(`(?m)^\t\t\t"([a-zA-Z]+)":`).FindAllStringSubmatch(block, -1)
	var order []string
	for _, k := range keys {
		order = append(order, k[1])
	}
	if got := strings.Join(order, ","); got != "id,type,text,styleAttributes,x,y,width,height,color" {
		t.Fatalf("keys %s in\n%s", got, block)
	}
}

// nodeBlock is the text of one node in a file: its line, or its lines when
// Obsidian's stringify spreads it.
func nodeBlock(t *testing.T, file, id string) string {
	t.Helper()
	lines := strings.Split(file, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "\t\t{\"id\":\""+id+"\"") {
			return strings.TrimSuffix(l, ",")
		}
		if l == "\t\t{" && i+1 < len(lines) && lines[i+1] == "\t\t\t\"id\":\""+id+"\"," {
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "\t\t}") {
					return strings.TrimSuffix(strings.Join(lines[i:j+1], "\n"), ",")
				}
			}
		}
	}
	t.Fatalf("no node %s in\n%s", id, file)
	return ""
}

func TestAGroupCoddyMovesKeepsItsLabelAfterTheGeometry(t *testing.T) {
	raw := obsidianSample(t)
	ws, bundle := workspace(t)
	if err := os.WriteFile(filepath.Join(ws, "sample.canvas"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ref := Ref{Bundle: bundle, Workspace: ws, File: "sample.canvas"}
	y := -600
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "754a8ef995f366bc", Y: &y}}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(ws, "sample.canvas"))
	// What Obsidian wrote for this group after the same move.
	want := `{"id":"754a8ef995f366bc","type":"group","x":-300,"y":-600,"width":610,"height":200,"label":"JSON Canvas"}`
	if got := nodeBlock(t, string(after), "754a8ef995f366bc"); got != "\t\t"+want {
		t.Fatalf("the group reads\n%s\nObsidian writes\n\t\t%s", got, want)
	}
}
