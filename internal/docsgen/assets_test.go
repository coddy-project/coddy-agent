package docsgen

import (
	"slices"
	"strings"
	"testing"
)

// A diagram is committed twice: the HTML source a change edits and the PNG a
// page embeds. The page references only the picture, so the source, and the
// stylesheet and script every source shares, count as used through it.
func TestDiagramSourcesCountWithTheirPicture(t *testing.T) {
	root := t.TempDir()
	html := `<link rel="stylesheet" href="diagram.css" /><script src="diagram.js"></script>`
	write(t, root, "docs/assets/INDEX.md", "# Assets\n")
	write(t, root, "docs/assets/diagrams/diagram.css", "body {}\n")
	write(t, root, "docs/assets/diagrams/diagram.js", "window.wires = () => {};\n")
	write(t, root, "docs/assets/diagrams/flow.html", html)
	write(t, root, "docs/assets/diagrams/flow.png", "png")
	write(t, root, "docs/page.md", "![Flow](../assets/diagrams/flow.png)\n")

	assets, err := AssetInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	if problems := CheckAssets(assets); len(problems) != 0 {
		t.Fatalf("a diagram and its source reported: %v", problems)
	}
	used := map[string][]string{}
	for _, a := range assets {
		used[a.Path] = a.UsedBy
	}
	if !slices.Contains(used["diagrams/flow.html"], renderDiagramsScript) {
		t.Errorf("the source is not used by the script that renders it: %v", used["diagrams/flow.html"])
	}
	for _, shared := range []string{"diagrams/diagram.css", "diagrams/diagram.js"} {
		if !slices.Contains(used[shared], "docs/assets/diagrams/flow.html") {
			t.Errorf("%s is not used by the source that links it: %v", shared, used[shared])
		}
	}
}

// A picture without its source cannot be changed and rendered again, and a
// source without its picture shows on no page: both are reported, once each.
func TestDiagramWithoutItsSourceOrPictureIsReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/assets/INDEX.md", "# Assets\n")
	write(t, root, "docs/assets/diagrams/drawn.png", "png")
	write(t, root, "docs/assets/diagrams/draft.html", "<p>draft</p>")
	write(t, root, "docs/page.md", "![Drawn](../assets/diagrams/drawn.png)\n")

	assets, err := AssetInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	problems := CheckAssets(assets)
	byFile := map[string][]string{}
	for _, p := range problems {
		byFile[p.File] = append(byFile[p.File], p.Message)
	}
	if got := byFile["docs/assets/diagrams/drawn.png"]; len(got) != 1 || !strings.Contains(got[0], "drawn.html") {
		t.Errorf("a picture without its source: %v", got)
	}
	if got := byFile["docs/assets/diagrams/draft.html"]; len(got) != 1 || !strings.Contains(got[0], "draft.png") {
		t.Errorf("a source without its picture: %v", got)
	}
	if len(problems) != 2 {
		t.Errorf("want two problems, got %v", problems)
	}
}
