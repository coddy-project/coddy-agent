package canvas

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestShowAddsAFrameAtTheDeviceSize(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html", AddedBy: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonAdded {
		t.Fatalf("reason %q, want %q", res.Reason, ReasonAdded)
	}
	f := res.Frame
	if f.Path != "site/index.html" || f.Root != "site" || f.Workspace != ws {
		t.Fatalf("frame paths %+v", f)
	}
	if f.Device != DeviceDesktop || f.Width != 1280 || f.Height != 800 {
		t.Fatalf("frame size %+v", f)
	}
	if !strings.HasPrefix(f.ID, "fr_") || f.AddedBy != "agent" || f.AddedAt.IsZero() {
		t.Fatalf("frame identity %+v", f)
	}
	if res.Board.Revision != 1 || len(res.Board.Frames) != 1 {
		t.Fatalf("board %+v", res.Board)
	}
	if _, err := os.Stat(filepath.Join(bundle, FileName)); err != nil {
		t.Fatalf("board file not written: %v", err)
	}
	loaded, err := Load(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := loaded.Frame(f.ID); !ok || got.Path != f.Path {
		t.Fatalf("reloaded board %+v", loaded)
	}
}

func TestShowTheSameFileAtTheSameSizeKeepsOneFrame(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	first, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Show(bundle, ShowRequest{Workspace: ws, Path: filepath.Join(ws, "site", "index.html"), Title: "Landing"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Reason != ReasonShown || again.Frame.ID != first.Frame.ID {
		t.Fatalf("second show %q %s, want %q %s", again.Reason, again.Frame.ID, ReasonShown, first.Frame.ID)
	}
	if len(again.Board.Frames) != 1 || again.Board.Revision != 2 {
		t.Fatalf("board %+v", again.Board)
	}
	if again.Frame.Title != "Landing" {
		t.Fatalf("title %q", again.Frame.Title)
	}
	if !again.Frame.ShownAt.After(first.Frame.ShownAt) && !again.Frame.ShownAt.Equal(first.Frame.ShownAt) {
		t.Fatalf("shown_at went back: %v then %v", first.Frame.ShownAt, again.Frame.ShownAt)
	}
}

func TestShowTheSameFileAtAnotherSizeAddsAFrameBesideIt(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	desk, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html", Device: DevicePhone})
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
	custom, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html", Width: 1024, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	if custom.Frame.Device != "" || custom.Frame.Width != 1024 || custom.Frame.Height != 600 {
		t.Fatalf("custom frame %+v", custom.Frame)
	}
}

func TestShowUpdatesANamedFrameInPlace(t *testing.T) {
	ws, bundle := workspace(t, "a/index.html", "b/index.html")
	first, err := Show(bundle, ShowRequest{Workspace: ws, Path: "a/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	upd, err := Show(bundle, ShowRequest{Workspace: ws, Path: "b/index.html", FrameID: first.Frame.ID, Device: DeviceTablet})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Reason != ReasonUpdated || upd.Frame.ID != first.Frame.ID || upd.Frame.Path != "b/index.html" || upd.Frame.Root != "b" {
		t.Fatalf("updated frame %+v", upd)
	}
	if upd.Frame.Width != 820 || upd.Frame.Height != 1180 || len(upd.Board.Frames) != 1 {
		t.Fatalf("updated board %+v", upd.Board)
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "a/index.html", FrameID: "fr_missing"}); !errors.Is(err, ErrFrameNotFound) {
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
			req := tc.req
			req.Workspace = ws
			if _, err := Show(bundle, req); err == nil {
				t.Fatalf("Show(%+v) succeeded", req)
			}
		})
	}
	b, err := Load(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != 0 {
		t.Fatalf("refused shows left frames: %+v", b.Frames)
	}
}

func TestShowRefusesALinkOutOfTheWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	ws, bundle := workspace(t, "site/index.html")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "page.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "page.html"), filepath.Join(ws, "site", "linked.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/linked.html"}); err == nil {
		t.Fatal("a link out of the workspace was put on the canvas")
	}
}

func TestShowRefusesPastTheFrameLimit(t *testing.T) {
	files := make([]string, MaxFrames+1)
	for i := range files {
		files[i] = fmt.Sprintf("p%02d.html", i)
	}
	ws, bundle := workspace(t, files...)
	for i := 0; i < MaxFrames; i++ {
		if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: files[i]}); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: files[MaxFrames]}); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame past the limit: %v", err)
	}
	// Showing one already there is still fine at the limit.
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: files[0]}); err != nil {
		t.Fatalf("showing an existing frame at the limit: %v", err)
	}
}

func TestUpdateAndRemoveAFrame(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	title, x, y := "Hero", 40, 120
	phone := DevicePhone
	b, f, err := Update(bundle, res.Frame.ID, Patch{Title: &title, X: &x, Y: &y, Device: &phone})
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Hero" || f.X != 40 || f.Y != 120 || f.Width != 390 || f.Device != DevicePhone || b.Revision != 2 {
		t.Fatalf("patched frame %+v", f)
	}
	w, h := 5000, 800
	if _, _, err := Update(bundle, res.Frame.ID, Patch{Width: &w, Height: &h}); err == nil {
		t.Fatal("an out-of-range size was accepted")
	}
	if _, _, err := Update(bundle, "fr_nope", Patch{Title: &title}); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("patch of an unknown frame: %v", err)
	}
	b, err = Remove(bundle, res.Frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != 0 {
		t.Fatalf("frame still there: %+v", b)
	}
	if _, err := Remove(bundle, res.Frame.ID); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "site", "index.html")); err != nil {
		t.Fatalf("removing a frame touched the file: %v", err)
	}
}

func TestLoadAMissingBoardIsEmptyAndACorruptOneIsAnError(t *testing.T) {
	_, bundle := workspace(t)
	b, err := Load(bundle)
	if err != nil || len(b.Frames) != 0 || b.Revision != 0 {
		t.Fatalf("missing board: %+v %v", b, err)
	}
	if err := os.WriteFile(filepath.Join(bundle, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bundle); err == nil {
		t.Fatal("a corrupt board loaded")
	}
	ws, _ := workspace(t, "index.html")
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "index.html"}); err == nil {
		t.Fatal("Show overwrote a corrupt board")
	}
	raw, _ := os.ReadFile(filepath.Join(bundle, FileName))
	if string(raw) != "{not json" {
		t.Fatalf("corrupt board rewritten: %q", raw)
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

func TestConcurrentShowsKeepEveryFrame(t *testing.T) {
	files := make([]string, 10)
	for i := range files {
		files[i] = fmt.Sprintf("v%d/index.html", i)
	}
	ws, bundle := workspace(t, files...)
	var wg sync.WaitGroup
	errs := make(chan error, len(files))
	for _, f := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: f}); err != nil {
				errs <- err
			}
		}(f)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	b, err := Load(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != len(files) || b.Revision != int64(len(files)) {
		t.Fatalf("board after concurrent shows: %d frames, revision %d", len(b.Frames), b.Revision)
	}
	seen := map[string]bool{}
	for _, f := range b.Frames {
		if seen[f.ID] {
			t.Fatalf("duplicate frame id %s", f.ID)
		}
		seen[f.ID] = true
	}
}

func TestEveryChangeIsAnnounced(t *testing.T) {
	ws, bundle := workspace(t, "index.html")
	var mu sync.Mutex
	var got []Change
	stop := OnChange(func(c Change) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, c)
	})
	defer stop()
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Show(bundle, ShowRequest{Workspace: ws, Path: "index.html"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(bundle, res.Frame.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("changes %+v", got)
	}
	want := []string{ReasonAdded, ReasonShown, ReasonRemoved}
	for i, c := range got {
		if c.Reason != want[i] || c.SessionID != "sess_root" || c.Bundle != bundle || c.FrameID != res.Frame.ID || c.Revision != int64(i+1) {
			t.Fatalf("change %d: %+v", i, c)
		}
	}
}

func TestRotateMovesTheFrameToANewGeneration(t *testing.T) {
	ws, bundle := workspace(t, "index.html")
	res, err := Show(bundle, ShowRequest{Workspace: ws, Path: "index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Frame.Gen != 1 {
		t.Fatalf("new frame generation %d", res.Frame.Gen)
	}
	b, f, err := Rotate(bundle, res.Frame.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Gen != 2 || b.Revision != 2 {
		t.Fatalf("rotated frame %+v revision %d", f, b.Revision)
	}
	if _, _, err := Rotate(bundle, "fr_nope"); !errors.Is(err, ErrFrameNotFound) {
		t.Fatalf("rotate of an unknown frame: %v", err)
	}
}
