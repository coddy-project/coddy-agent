package canvas

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
)

// checkoutBoard is a board that came with the workspace, design/shop.canvas,
// naming two pages and a note, read under policy with receipts in a home of
// its own.
func checkoutBoard(t *testing.T, policy string) Ref {
	t.Helper()
	ws, bundle := workspace(t, "site/index.html", "site/about.html", "other/index.html")
	return checkoutBoardIn(t, ws, bundle, NewTrust(t.TempDir(), policy))
}

const checkoutCanvas = `{
	"nodes":[
		{"id":"0123456789abcdef","type":"text","text":"Checkout","x":0,"y":0,"width":260,"height":120},
		{"id":"aaaaaaaaaaaaaaaa","type":"file","file":"site/index.html","x":400,"y":0,"width":390,"height":844},
		{"id":"bbbbbbbbbbbbbbbb","type":"file","file":"site/about.html","x":900,"y":0,"width":390,"height":844}
	],
	"edges":[]
}
`

func checkoutBoardIn(t *testing.T, ws, bundle string, trust *Trust) Ref {
	t.Helper()
	file := filepath.Join(ws, "design", "shop.canvas")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(checkoutCanvas), 0o644); err != nil {
		t.Fatal(err)
	}
	return Ref{Bundle: bundle, Workspace: ws, File: "design/shop.canvas", Trust: trust}
}

// held lists the pages of a board that wait, sorted by frame order.
func held(t *testing.T, ref Ref) []string {
	t.Helper()
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range b.Frames {
		if f.Held {
			out = append(out, f.Path)
		}
	}
	return out
}

func trustAll(t *testing.T, ref Ref) {
	t.Helper()
	b, err := Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TrustBoard(ref, PagesDigest(b.Pages())); err != nil {
		t.Fatal(err)
	}
}

// editFile rewrites the board file as another tool (git, Obsidian) would.
func editFile(t *testing.T, ref Ref, fn func(string) string) {
	t.Helper()
	p := filepath.Join(ref.Workspace, filepath.FromSlash(ref.File))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(fn(string(raw))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestABoardThatCameWithTheCheckoutHoldsItsPagesUntilTheyAreTrusted(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	if got := held(t, ref); strings.Join(got, ",") != "site/index.html,site/about.html" {
		t.Fatalf("held %v, want both pages of the checkout's board", got)
	}
	for _, id := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		if _, err := FrameOf(ref, id); !errors.Is(err, ErrFrameHeld) {
			t.Fatalf("FrameOf(%s) of a held board = %v, want ErrFrameHeld", id, err)
		}
	}
	b, err := TrustBoard(ref, PagesDigest([]string{"site/about.html", "site/index.html"}))
	if err != nil {
		t.Fatal(err)
	}
	if b.Held() {
		t.Fatalf("the board TrustBoard answered still holds a page: %+v", b.Frames)
	}
	if got := held(t, ref); len(got) != 0 {
		t.Fatalf("held after the person trusted the board: %v", got)
	}
	if _, err := FrameOf(ref, "aaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("FrameOf after trust: %v", err)
	}
}

func TestARefWithoutTrustRunsNoPage(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAllow)
	ref.Trust = nil
	if got := held(t, ref); len(got) != 2 {
		t.Fatalf("a Ref with no trust ran pages: held %v", got)
	}
}

func TestABoardCanvasShowMakesRunsAtOnce(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html")
	ref := Ref{Bundle: bundle, Workspace: ws, Trust: NewTrust(t.TempDir(), config.ProjectTrustAsk)}
	res, err := Show(ref, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Frame.Held || res.Board.Held() {
		t.Fatalf("a board canvas_show made holds its page: %+v", res)
	}
	ref.File = res.Board.File
	if got := held(t, ref); len(got) != 0 {
		t.Fatalf("held on the next read: %v", got)
	}
}

// A page put on a held board runs; the pages that came with it still wait,
// so opening a checked-out board and adding a page approves nothing else.
func TestAPagePutOnAHeldBoardRunsAloneTheOthersStayHeld(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	res, err := Show(ref, ShowRequest{Path: "other/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Frame.Held {
		t.Fatalf("the page Coddy put waits: %+v", res.Frame)
	}
	if got := held(t, ref); strings.Join(got, ",") != "site/index.html,site/about.html" {
		t.Fatalf("held %v, want the checkout's own pages only", got)
	}
	// Showing a page the board already holds is the agent choosing it.
	if _, err := Show(ref, ShowRequest{Path: "site/index.html", Device: DevicePhone}); err != nil {
		t.Fatal(err)
	}
	if got := held(t, ref); strings.Join(got, ",") != "site/about.html" {
		t.Fatalf("held %v after canvas_show of site/index.html", got)
	}
}

// The receipt binds which pages run, not what they hold or what else the
// board draws.
func TestTheReceiptBindsThePageList(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	trustAll(t, ref)

	// Moving a card, adding a note or editing a page keeps it.
	x := 40
	if _, err := UpdateNodes(ref, []NodeChange{{ID: "0123456789abcdef", X: &x}, {ID: "aaaaaaaaaaaaaaaa", X: &x}}); err != nil {
		t.Fatal(err)
	}
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"nodes":[`, `"nodes":[{"id":"cccccccccccccccc","type":"text","text":"New note","x":0,"y":400,"width":200,"height":100},`, 1)
	})
	if err := os.WriteFile(filepath.Join(ref.Workspace, "site", "index.html"), []byte("<!doctype html><title>edited</title><script>1</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := held(t, ref); len(got) != 0 {
		t.Fatalf("held %v after a move, a note and an edit inside a page", got)
	}

	// A page the file adds waits, alone.
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"nodes":[`, `"nodes":[{"id":"dddddddddddddddd","type":"file","file":"other/index.html","x":0,"y":900,"width":390,"height":844},`, 1)
	})
	if got := held(t, ref); strings.Join(got, ",") != "other/index.html" {
		t.Fatalf("held %v, want the added page alone", got)
	}

	// A page renamed in the file is another page.
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"file":"site/about.html"`, `"file":"other/index.html"`, 1)
	})
	got := held(t, ref)
	if len(got) != 2 || got[0] != "other/index.html" || got[1] != "other/index.html" {
		t.Fatalf("held %v after a rename, want both nodes of other/index.html", got)
	}

	// A page removed from the file lets nothing new run: the rest keep running.
	ref2 := checkoutBoard(t, config.ProjectTrustAsk)
	trustAll(t, ref2)
	editFile(t, ref2, func(s string) string {
		return strings.Replace(s, `,
		{"id":"bbbbbbbbbbbbbbbb","type":"file","file":"site/about.html","x":900,"y":0,"width":390,"height":844}`, "", 1)
	})
	if got := held(t, ref2); len(got) != 0 {
		t.Fatalf("held %v after a page left the file", got)
	}
}

func TestTrustWithAStaleDigestIsRefused(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	shown := PagesDigest([]string{"site/index.html", "site/about.html"})
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"file":"site/about.html"`, `"file":"other/index.html"`, 1)
	})
	if _, err := TrustBoard(ref, shown); !errors.Is(err, ErrPagesMoved) {
		t.Fatalf("TrustBoard with the digest of a list the board no longer holds = %v, want ErrPagesMoved", err)
	}
	if got := held(t, ref); len(got) != 2 {
		t.Fatalf("a refused approval let pages run: held %v", got)
	}
}

func TestDenyRunsOnlyThePagesCoddyPut(t *testing.T) {
	home := t.TempDir()
	ws, bundle := workspace(t, "site/index.html", "site/about.html", "other/index.html")
	ask := checkoutBoardIn(t, ws, bundle, NewTrust(home, config.ProjectTrustAsk))
	trustAll(t, ask)

	deny := ask
	deny.Trust = NewTrust(home, config.ProjectTrustDeny)
	if got := held(t, deny); len(got) != 2 {
		t.Fatalf("deny ran pages the person approved under ask: held %v", got)
	}
	if _, err := TrustBoard(deny, PagesDigest([]string{"site/about.html", "site/index.html"})); !errors.Is(err, ErrTrustDenied) {
		t.Fatalf("TrustBoard under deny = %v, want ErrTrustDenied", err)
	}
	if _, err := Show(deny, ShowRequest{Path: "other/index.html"}); err != nil {
		t.Fatal(err)
	}
	if got := held(t, deny); strings.Join(got, ",") != "site/index.html,site/about.html" {
		t.Fatalf("held %v under deny, want every page Coddy did not put", got)
	}
}

func TestAllowRunsEveryPage(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAllow)
	if got := held(t, ref); len(got) != 0 {
		t.Fatalf("held %v under allow", got)
	}
}

func TestUntrustHoldsTheBoardAgain(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	trustAll(t, ref)
	if _, err := Show(ref, ShowRequest{Path: "other/index.html"}); err != nil {
		t.Fatal(err)
	}
	b, err := UntrustBoard(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Frames) != 3 {
		t.Fatalf("frames %+v", b.Frames)
	}
	for _, f := range b.Frames {
		if !f.Held {
			t.Fatalf("%s runs after untrust", f.Path)
		}
	}
	if got := held(t, ref); len(got) != 3 {
		t.Fatalf("held %v on the next read, want every page", got)
	}
}

// A linked worktree reads what its main checkout decided, and what a branch
// adds asks again; each checkout keeps a receipt of its own under the key of
// the project.
func TestAWorktreeSharesTheReceiptsOfItsProject(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	trust := NewTrust(t.TempDir(), config.ProjectTrustAsk)
	for _, ws := range []string{main, wt} {
		for _, f := range []string{"site/index.html", "site/about.html", "other/index.html"} {
			p := filepath.Join(ws, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("<!doctype html>"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	bundle := filepath.Join(t.TempDir(), "sess_root")
	refMain := checkoutBoardIn(t, main, bundle, trust)
	refWT := checkoutBoardIn(t, wt, filepath.Join(t.TempDir(), "sess_wt"), trust)
	trustAll(t, refMain)
	if got := held(t, refWT); len(got) != 0 {
		t.Fatalf("the worktree holds %v its main checkout trusted", got)
	}
	editFile(t, refWT, func(s string) string {
		return strings.Replace(s, `"file":"site/about.html"`, `"file":"other/index.html"`, 1)
	})
	if got := held(t, refWT); strings.Join(got, ",") != "other/index.html" {
		t.Fatalf("held %v in the worktree, want the page its branch added", got)
	}
	trustAll(t, refWT)
	recs := trust.Store.Records(wt)
	if len(recs) != 2 || recs[0].Checkout == recs[1].Checkout {
		t.Fatalf("records %+v, want one receipt for each checkout under the project's key", recs)
	}
	if got := held(t, refMain); len(got) != 0 {
		t.Fatalf("the worktree's approval took the main checkout's: held %v", got)
	}
	// An explicit untrust takes the project's.
	if _, err := UntrustBoard(refWT); err != nil {
		t.Fatal(err)
	}
	if got := held(t, refMain); len(got) != 2 {
		t.Fatalf("held %v in the main checkout after the worktree untrusted the board", got)
	}
}

// A page Coddy put on a board and then took off leaves the receipt with its
// frame: the receipt stays the size of the board, and the page asks again
// when the file brings it back.
func TestAPageTakenOffTheBoardLeavesTheReceipt(t *testing.T) {
	ws, bundle := workspace(t, "site/index.html", "other/index.html")
	trust := NewTrust(t.TempDir(), config.ProjectTrustAsk)
	ref := Ref{Bundle: bundle, Workspace: ws, Trust: trust}
	first, err := Show(ref, ShowRequest{Path: "site/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	ref.File = first.Board.File
	if _, err := Show(ref, ShowRequest{Path: "other/index.html"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(ref, first.Frame.ID); err != nil {
		t.Fatal(err)
	}
	recs := trust.Store.Records(ws)
	if len(recs) != 1 || strings.Join(recs[0].Shown, ",") != "other/index.html" {
		t.Fatalf("records %+v, want only the page still on the board", recs)
	}
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"nodes":[`, `"nodes":[{"id":"eeeeeeeeeeeeeeee","type":"file","file":"site/index.html","x":0,"y":900,"width":390,"height":844},`, 1)
	})
	if got := held(t, ref); strings.Join(got, ",") != "site/index.html" {
		t.Fatalf("held %v, want the page the file brought back", got)
	}
}

// A page the person let run leaves the receipt when it leaves the board, as a
// page Coddy put there does: the approval does not outlive the frame, so the
// page asks again when the file brings it back.
func TestAnApprovedPageTakenOffTheBoardLeavesTheReceipt(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	trustAll(t, ref)
	if got := held(t, ref); len(got) != 0 {
		t.Fatalf("held %v right after the approval", got)
	}
	if _, err := Remove(ref, "bbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	recs := ref.Trust.Store.Records(ref.Workspace)
	if len(recs) != 1 || strings.Join(recs[0].Approved, ",") != "site/index.html" {
		t.Fatalf("records %+v, want the approval of the page still on the board only", recs)
	}
	// The file brings the page back: it is not the page that was approved.
	editFile(t, ref, func(s string) string {
		return strings.Replace(s, `"nodes":[`,
			`"nodes":[{"id":"cccccccccccccccc","type":"file","file":"site/about.html","x":1400,"y":0,"width":390,"height":844},`, 1)
	})
	if got := held(t, ref); strings.Join(got, ",") != "site/about.html" {
		t.Fatalf("held %v, want the page the file brought back", got)
	}
}

// A page Coddy put on a board runs in the checkout it was put in. The board
// at that path in a linked worktree is another branch's file, so a show in
// one checkout never releases a page on the other's board - which is what
// deny, where a show is the only thing that runs a page, rests on.
func TestAShownPageStaysInItsCheckout(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	trust := NewTrust(t.TempDir(), config.ProjectTrustDeny)
	for _, ws := range []string{main, wt} {
		for _, f := range []string{"site/index.html", "site/about.html"} {
			p := filepath.Join(ws, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("<!doctype html>"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	refMain := checkoutBoardIn(t, main, filepath.Join(t.TempDir(), "sess_main"), trust)
	refWT := checkoutBoardIn(t, wt, filepath.Join(t.TempDir(), "sess_wt"), trust)
	if _, err := Show(refMain, ShowRequest{Path: "site/index.html"}); err != nil {
		t.Fatal(err)
	}
	if got := held(t, refMain); strings.Join(got, ",") != "site/about.html" {
		t.Fatalf("held %v in the checkout of the show, want the page it did not show", got)
	}
	if got := held(t, refWT); len(got) != 2 {
		t.Fatalf("held %v in the worktree, want both pages of its own board", got)
	}
}

// A receipts file that cannot be parsed is not "nothing approved": every page
// waits, and the next approval refuses instead of writing over what is there,
// as the MCP store refuses.
func TestADamagedReceiptsFileIsNeverWrittenOver(t *testing.T) {
	ref := checkoutBoard(t, config.ProjectTrustAsk)
	trustAll(t, ref)
	store := ref.Trust.Store
	damaged := []byte("{\"version\":1,\"workspaces\":{")
	if err := os.WriteFile(store.Path(), damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := held(t, ref); len(got) != 2 {
		t.Fatalf("held %v over a damaged store, want every page to wait", got)
	}
	if err := store.Approve(ref.Workspace, ref.File, []string{"site/index.html"}); err == nil {
		t.Fatal("Approve wrote over a damaged receipts file")
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(damaged) {
		t.Fatalf("the damaged file was replaced: %s", raw)
	}
}

// The digest names one list of pages and no other, a page path holding a
// newline included.
func TestPagesDigestTellsAPageWithANewlineFromTwoPages(t *testing.T) {
	if PagesDigest([]string{"a\nb.html"}) == PagesDigest([]string{"a", "b.html"}) {
		t.Fatal("a page holding a newline digests as two pages")
	}
}
