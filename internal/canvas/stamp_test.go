package canvas

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStampMovesWithTheFilesOfTheRootOnly(t *testing.T) {
	ws, _ := workspace(t, "site/index.html", "site/css/main.css", "other/index.html")
	s0 := Stamp(ws, "site")
	if s0 == "" {
		t.Fatal("empty stamp")
	}
	if s := Stamp(ws, "site"); s != s0 {
		t.Fatalf("stamp moved without a change: %s then %s", s0, s)
	}
	// A file outside the root does not move it.
	if err := os.WriteFile(filepath.Join(ws, "other", "index.html"), []byte("changed elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Stamp(ws, "site"); s != s0 {
		t.Fatal("a file outside the root moved the stamp")
	}
	// Dot folders and dependency folders do not either.
	for _, dir := range []string{".cache", "node_modules", "vendor"} {
		p := filepath.Join(ws, "site", dir, "x.js")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if s := Stamp(ws, "site"); s != s0 {
		t.Fatal("a dot or dependency folder moved the stamp")
	}
	// A nested file of the root does.
	css := filepath.Join(ws, "site", "css", "main.css")
	if err := os.WriteFile(css, []byte("body{color:red}"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(css, later, later); err != nil {
		t.Fatal(err)
	}
	s1 := Stamp(ws, "site")
	if s1 == s0 {
		t.Fatal("an edit under the root left the stamp")
	}
	// A new file does too.
	if err := os.WriteFile(filepath.Join(ws, "site", "app.js"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Stamp(ws, "site"); s == s1 {
		t.Fatal("a new file under the root left the stamp")
	}
}

func TestStampOfAMissingRootIsStable(t *testing.T) {
	ws, _ := workspace(t)
	if a, b := Stamp(ws, "gone"), Stamp(ws, "gone"); a != b || a == "" {
		t.Fatalf("missing root stamps %q %q", a, b)
	}
}
