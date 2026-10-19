package canvas

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
)

// workspace builds a throwaway workspace with the given files (slash paths)
// and returns its absolute path together with a fresh session bundle.
func workspace(t *testing.T, files ...string) (ws, bundle string) {
	t.Helper()
	ws = t.TempDir()
	for _, f := range files {
		p := filepath.Join(ws, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("<!doctype html><title>"+f+"</title>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bundle = filepath.Join(t.TempDir(), "sessions", "sess_root")
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	return ws, bundle
}

// boardOn is a workspace with an empty board file, design/board.canvas.
func boardOn(t *testing.T, files ...string) Ref {
	t.Helper()
	ws, bundle := workspace(t, files...)
	if err := createCanvasFile(ws, "design/board.canvas"); err != nil {
		t.Fatal(err)
	}
	return Ref{Bundle: bundle, Workspace: ws, File: "design/board.canvas"}
}

var obsidianID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestShowWithoutAFileMakesOneNamedAfterThePagesFolder(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "other/index.html")
	ref := Ref{Bundle: bundle, Workspace: ws}
	res, err := Show(ref, ShowRequest{Path: "site/index.html", AddedBy: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Board.File != "canvas/site.canvas" || res.Reason != ReasonAdded {
		t.Fatalf("show %+v", res)
	}
	f := res.Frame
	if !obsidianID.MatchString(f.ID) || f.Path != "site/index.html" || f.Root != "site" || f.Workspace != ws {
		t.Fatalf("frame %+v", f)
	}
	if f.Device != DeviceDesktop || f.Width != 1280 || f.Height != 800 || f.AddedBy != "agent" || f.Gen != 1 {
		t.Fatalf("frame size %+v", f)
	}
	d := readDoc(t, filepath.Join(ws, "canvas", "site.canvas"))
	if d.findNode(f.ID) < 0 {
		t.Fatal("the frame is not a node of the new file")
	}
	if cur, _ := Current(bundle, ws); cur != "canvas/site.canvas" {
		t.Fatalf("current file %q", cur)
	}
	// The next page without a file goes on the same board.
	next, err := Show(ref, ShowRequest{Path: "other/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Created || next.Board.File != "canvas/site.canvas" || len(next.Board.Frames) != 2 {
		t.Fatalf("second show %+v", next)
	}
}

func TestANewBoardTakesTheNextFreeName(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	if err := createCanvasFile(ws, "canvas/site.canvas"); err != nil {
		t.Fatal(err)
	}
	res, err := Show(Ref{Bundle: bundle, Workspace: ws}, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Board.File != "canvas/site-2.canvas" || !res.Created {
		t.Fatalf("board file %q created %v", res.Board.File, res.Created)
	}
	// A page at the top of the workspace names the file after itself.
	ws2, bundle2 := workspace(t, "landing.html")
	res, err = Show(Ref{Bundle: bundle2, Workspace: ws2}, ShowRequest{Path: "landing.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Board.File != "canvas/landing.canvas" {
		t.Fatalf("board file %q", res.Board.File)
	}
}

func TestShowTheSameFileAtTheSameSizeKeepsOneFrame(t *testing.T) {
	ref := boardOn(t, "site/index.html")
	first, err := Show(ref, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Show(ref, ShowRequest{Path: filepath.Join(ref.Workspace, "site", "index.html"), Title: "Landing"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Reason != ReasonShown || again.Frame.ID != first.Frame.ID {
		t.Fatalf("second show %q %s, want %q %s", again.Reason, again.Frame.ID, ReasonShown, first.Frame.ID)
	}
	if len(again.Board.Frames) != 1 || again.Board.Revision != 2 || again.Frame.Title != "Landing" {
		t.Fatalf("board %+v", again.Board)
	}
}

func TestShowTheSameFileAtAnotherSizeAddsAFrameBesideIt(t *testing.T) {
	ref := boardOn(t, "site/index.html")
	desk, err := Show(ref, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := Show(ref, ShowRequest{Path: "site/index.html", Device: DevicePhone})
	if err != nil {
		t.Fatal(err)
	}
	if phone.Reason != ReasonAdded || phone.Frame.ID == desk.Frame.ID {
		t.Fatalf("phone frame %+v", phone)
	}
	if phone.Frame.Width != 390 || phone.Frame.Height != 844 {
		t.Fatalf("phone size %dx%d", phone.Frame.Width, phone.Frame.Height)
	}
	if phone.Frame.X < desk.Frame.X+desk.Frame.Width {
		t.Fatalf("phone frame at x=%d overlaps the desktop one ending at %d", phone.Frame.X, desk.Frame.X+desk.Frame.Width)
	}
	custom, err := Show(ref, ShowRequest{Path: "site/index.html", Width: 1024, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	if custom.Frame.Device != "" || custom.Frame.Width != 1024 || custom.Frame.Height != 600 {
		t.Fatalf("custom frame %+v", custom.Frame)
	}
}

func TestShowUpdatesANamedFrameInPlace(t *testing.T) {
	ref := boardOn(t, "a/index.html", "a/other.html", "b/index.html")
	first, err := Show(ref, ShowRequest{Path: "a/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	upd, err := Show(ref, ShowRequest{Path: "b/index.html", FrameID: first.Frame.ID, Device: DeviceTablet})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Reason != ReasonUpdated || upd.Frame.ID != first.Frame.ID || upd.Frame.Path != "b/index.html" || upd.Frame.Root != "b" {
		t.Fatalf("updated frame %+v", upd)
	}
	if upd.Frame.Width != 820 || upd.Frame.Height != 1180 || len(upd.Board.Frames) != 1 {
		t.Fatalf("updated board %+v", upd.Board)
	}
	// Another page is loaded at a new address, even in the same folder.
	if upd.Frame.Gen != first.Frame.Gen+1 {
		t.Fatalf("pointing a frame at another page kept generation %d", upd.Frame.Gen)
	}
	same, err := Show(ref, ShowRequest{Path: "b/index.html", FrameID: first.Frame.ID, Device: DevicePhone})
	if err != nil {
		t.Fatal(err)
	}
	if same.Frame.Gen != upd.Frame.Gen {
		t.Fatalf("resizing a frame moved its generation to %d", same.Frame.Gen)
	}
	if _, err := Show(ref, ShowRequest{Path: "a/index.html", FrameID: "missing"}); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("unknown frame: %v", err)
	}
}

func TestShowRefusesWhatIsNotAPageOfTheWorkspace(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "site/app.js", ".hidden/index.html", "site/.drafts/index.html", "other/index.html")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  ShowRequest
	}{
		{"missing file", ShowRequest{Path: "site/missing.html"}},
		{"a folder", ShowRequest{Path: "site"}},
		{"not html", ShowRequest{Path: "site/app.js"}},
		{"climbs out", ShowRequest{Path: "../evil.html"}},
		{"absolute outside", ShowRequest{Path: filepath.Join(outside, "evil.html")}},
		{"dot folder", ShowRequest{Path: ".hidden/index.html"}},
		{"nested dot folder", ShowRequest{Path: "site/.drafts/index.html"}},
		{"root without the page", ShowRequest{Path: "site/index.html", Root: "other"}},
		{"root outside", ShowRequest{Path: "site/index.html", Root: outside}},
		{"root a dot folder", ShowRequest{Path: "site/.drafts/index.html", Root: "site/.drafts"}},
		{"unknown device", ShowRequest{Path: "site/index.html", Device: "watch"}},
		{"too narrow", ShowRequest{Path: "site/index.html", Width: 100, Height: 800}},
		{"too tall", ShowRequest{Path: "site/index.html", Width: 800, Height: 99999}},
		{"width without height", ShowRequest{Path: "site/index.html", Width: 800}},
		{"empty path", ShowRequest{Path: "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Show(Ref{Bundle: bundle, Workspace: ws}, tc.req); err == nil {
				t.Fatalf("Show(%+v) succeeded", tc.req)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(ws, "canvas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused shows made a board: %v", err)
	}
	if _, err := Show(Ref{Bundle: bundle, Workspace: ws, File: "site/index.html"}, ShowRequest{Path: "site/index.html"}); err == nil {
		t.Fatal("a page went onto a file that is not a .canvas one")
	}
}

func TestShowRefusesALinkOutOfTheWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	ref := boardOn(t, "site/index.html")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "page.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "page.html"), filepath.Join(ref.Workspace, "site", "linked.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(ref, ShowRequest{Path: "site/linked.html"}); err == nil {
		t.Fatal("a link out of the workspace was put on the canvas")
	}
}

func TestShowRefusesPastTheFrameLimit(t *testing.T) {
	files := make([]string, MaxFrames+1)
	for i := range files {
		files[i] = fmt.Sprintf("p%02d.html", i)
	}
	ref := boardOn(t, files...)
	for i := 0; i < MaxFrames; i++ {
		if _, err := Show(ref, ShowRequest{Path: files[i]}); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if _, err := Show(ref, ShowRequest{Path: files[MaxFrames]}); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame past the limit: %v", err)
	}
	// Showing one already there is still fine at the limit.
	if _, err := Show(ref, ShowRequest{Path: files[0]}); err != nil {
		t.Fatalf("showing an existing frame at the limit: %v", err)
	}
}

func TestUpdateNodesAndRemoveAFrame(t *testing.T) {
	ref := boardOn(t, "site/index.html")
	res, err := Show(ref, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	x, y := 40, 120
	phone := DevicePhone
	b, err := UpdateNodes(ref, []NodeChange{{ID: res.Frame.ID, X: &x, Y: &y, Device: &phone}})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := b.Frame(res.Frame.ID)
	if f.X != 40 || f.Y != 120 || f.Width != 390 || f.Device != DevicePhone || b.Revision != 2 {
		t.Fatalf("changed frame %+v", f)
	}
	w, h := 5000, 800
	if _, err := UpdateNodes(ref, []NodeChange{{ID: res.Frame.ID, Width: &w, Height: &h}}); err == nil {
		t.Fatal("an out-of-range size was accepted")
	}
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "nope", X: &x}}); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("a change of an unknown node: %v", err)
	}
	b, err = Remove(ref, res.Frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != 0 {
		t.Fatalf("frame still there: %+v", b)
	}
	if _, err := Remove(ref, res.Frame.ID); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ref.Workspace, "site", "index.html")); err != nil {
		t.Fatalf("removing a frame touched the page: %v", err)
	}
}

func TestACorruptBookkeepingIsAnErrorAndIsNotOverwritten(t *testing.T) {
	ref := boardOn(t, "index.html")
	if err := os.WriteFile(filepath.Join(ref.Bundle, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ref); err == nil {
		t.Fatal("a corrupt bookkeeping loaded")
	}
	if _, err := Show(ref, ShowRequest{Path: "index.html"}); err == nil {
		t.Fatal("Show overwrote a corrupt bookkeeping")
	}
	raw, _ := os.ReadFile(filepath.Join(ref.Bundle, FileName))
	if string(raw) != "{not json" {
		t.Fatalf("corrupt bookkeeping rewritten: %q", raw)
	}
}

func TestBoardBundleOfASubagentIsTheRootOfItsSpawnTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions", "sess_root")
	child := filepath.Join(root, "subagents", "sess_child")
	grandchild := filepath.Join(child, "subagents", "sess_grand")
	if err := os.MkdirAll(grandchild, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, child, grandchild} {
		if got := BoardBundle(dir); got != root {
			t.Fatalf("BoardBundle(%s) = %s, want %s", dir, got, root)
		}
	}
	if got := SessionIDOfBundle(grandchild); got != "sess_root" {
		t.Fatalf("SessionIDOfBundle = %q", got)
	}
}

func TestConcurrentShowsKeepEveryFrameOnOneBoard(t *testing.T) {
	files := make([]string, 10)
	for i := range files {
		files[i] = fmt.Sprintf("v%d/index.html", i)
	}
	ws, bundle := workspace(t, files...)
	ref := Ref{Bundle: bundle, Workspace: ws}
	var wg sync.WaitGroup
	errs := make(chan error, len(files))
	for _, f := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			if _, err := Show(ref, ShowRequest{Path: f}); err != nil {
				errs <- err
			}
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	made, _ := filepath.Glob(filepath.Join(ws, "canvas", "*.canvas"))
	if len(made) != 1 {
		t.Fatalf("concurrent shows made %d boards: %v", len(made), made)
	}
	cur, _ := Current(bundle, ws)
	b, err := Load(Ref{Bundle: bundle, Workspace: ws, File: cur})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != len(files) || b.Revision != int64(len(files)) {
		t.Fatalf("board after concurrent shows: %d frames, revision %d", len(b.Frames), b.Revision)
	}
}

func TestEveryChangeIsAnnounced(t *testing.T) {
	ref := boardOn(t, "index.html")
	var mu sync.Mutex
	var got []Change
	stop := OnChange(func(c Change) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
	})
	defer stop()
	res, err := Show(ref, ShowRequest{Path: "index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Show(ref, ShowRequest{Path: "index.html"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(ref, res.Frame.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("changes %+v", got)
	}
	want := []string{ReasonAdded, ReasonShown, ReasonRemoved}
	for i, c := range got {
		if c.Reason != want[i] || c.SessionID != "sess_root" || c.File != ref.File || c.FrameID != res.Frame.ID || c.Revision != int64(i+1) {
			t.Fatalf("change %d: %+v", i, c)
		}
	}
}

func TestRotateMovesTheFrameToANewGeneration(t *testing.T) {
	ref := boardOn(t, "index.html")
	res, err := Show(ref, ShowRequest{Path: "index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Frame.Gen != 1 {
		t.Fatalf("new frame generation %d", res.Frame.Gen)
	}
	b, f, err := Rotate(ref, res.Frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Gen != 2 || b.Revision != 2 {
		t.Fatalf("rotated frame %+v revision %d", f, b.Revision)
	}
	if _, _, err := Rotate(ref, "nope"); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("rotate of an unknown frame: %v", err)
	}
}

func TestOpenMakesAFileTheOneTheAgentShowsOn(t *testing.T) {
	ref := boardOn(t, "site/index.html")
	if err := createCanvasFile(ref.Workspace, "plans/flow.canvas"); err != nil {
		t.Fatal(err)
	}
	other := ref
	other.File = "plans/flow.canvas"
	b, err := Open(other)
	if err != nil {
		t.Fatal(err)
	}
	if b.File != "plans/flow.canvas" {
		t.Fatalf("opened %q", b.File)
	}
	res, err := Show(Ref{Bundle: ref.Bundle, Workspace: ref.Workspace}, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Board.File != "plans/flow.canvas" || res.Created {
		t.Fatalf("a show after the open went to %q (created %v)", res.Board.File, res.Created)
	}
	if err := os.WriteFile(filepath.Join(ref.Workspace, "broken.canvas"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := ref
	broken.File = "broken.canvas"
	if _, err := Open(broken); err == nil {
		t.Fatal("a file that does not parse was opened")
	}
	if cur, _ := Current(ref.Bundle, ref.Workspace); cur != "plans/flow.canvas" {
		t.Fatalf("a refused open moved the current file to %q", cur)
	}
}

func TestTheBookkeepingOfAnotherWorkspaceDoesNotApply(t *testing.T) {
	ref := boardOn(t, "site/index.html")
	if _, err := Show(ref, ShowRequest{Path: "site/index.html", Root: "."}); err != nil {
		t.Fatal(err)
	}
	// The chat moved to a worktree with the same files.
	other, _ := workspace(t, "site/index.html")
	if err := createCanvasFile(other, ref.File); err != nil {
		t.Fatal(err)
	}
	if cur, _ := Current(ref.Bundle, other); cur != "" {
		t.Fatalf("the current file of another workspace applies: %q", cur)
	}
	st, err := LoadState(ref.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.in(other); got.Roots != nil || got.Current != "" {
		t.Fatalf("the roots of another workspace apply: %+v", got)
	}
}
