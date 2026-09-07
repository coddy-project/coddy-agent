package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// writeMsgs writes a messages.json for a session directly (test helper).
func writeMsgs(t *testing.T, sessionDir string, msgs []llm.Message) {
	t.Helper()
	wrap := messagesFileData{Version: messagesLayout, Messages: msgs}
	p := filepath.Join(sessionDir, messagesFile)
	b, err := json.Marshal(wrap)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write messages: %v", err)
	}
}

// newTestManager returns a Manager and FileStore backed by a temp dir.
func newTestManager(t *testing.T) (*Manager, *FileStore) {
	t.Helper()
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := &Manager{store: fs}
	return mgr, fs
}

// userMsgs builds an alternating user/assistant message slice.
func userMsgs(contents ...string) []llm.Message {
	var out []llm.Message
	for _, c := range contents {
		out = append(out, llm.Message{Role: llm.RoleUser, Content: c})
		out = append(out, llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	}
	return out
}

// TestBranchSiblings verifies that branching at the same userMessageIndex
// always adds siblings in the same parent's branch file.
func TestBranchSiblings(t *testing.T) {
	mgr, fs := newTestManager(t)

	// Create root session with 2 user messages.
	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello", "world"))

	// Branch 1 from root at index 0.
	r1, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B1: %v", err)
	}
	if r1.TotalBranches != 2 {
		t.Errorf("after B1: want totalBranches=2, got %d", r1.TotalBranches)
	}

	// Branch 2 from root at index 0 → sibling of B1.
	r2, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B2: %v", err)
	}
	if r2.TotalBranches != 3 {
		t.Errorf("after B2: want totalBranches=3, got %d", r2.TotalBranches)
	}

	// Branch from B1 at same index (0) → sibling in root's file.
	r3, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: r1.NewSessionID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B1-sibling: %v", err)
	}
	if r3.TotalBranches != 4 {
		t.Errorf("sibling of B1: want totalBranches=4, got %d", r3.TotalBranches)
	}

	// B1 should show itself at its sibling position (no own branch points).
	views, err := mgr.LoadBranchPointViews(r1.NewSessionID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(B1): %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("B1: want 1 view, got %d", len(views))
	}
	if views[0].Total != 4 {
		t.Errorf("B1 view: want total=4, got %d", views[0].Total)
	}
}

// TestNestedBranching verifies that branching from a branch at a DIFFERENT
// userMessageIndex stores the new branch in the direct parent's own file.
func TestNestedBranching(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("r0", "r1", "r2"))

	// Level 1: B branches from root at index 1.
	resB, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 1})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	bID := resB.NewSessionID
	bDir := fs.SessionPath(bID)
	// Populate B with its own messages (inherits r0, then has b1 and b2).
	writeMsgs(t, bDir, userMsgs("r0", "b1", "b2"))

	// Level 2: C branches from B at index 2 (B's own message, different from B's origin index 1).
	resC, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: bID, UserMessageIndex: 2})
	if err != nil {
		t.Fatalf("create C (child of B): %v", err)
	}
	if resC.TotalBranches != 2 {
		t.Errorf("C: want totalBranches=2, got %d", resC.TotalBranches)
	}

	// C should see itself in B's branch file at index 2.
	viewsC, err := mgr.LoadBranchPointViews(resC.NewSessionID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(C): %v", err)
	}
	if len(viewsC) != 1 {
		t.Fatalf("C: want 1 view, got %d: %+v", len(viewsC), viewsC)
	}
	if viewsC[0].UserMessageIndex != 2 {
		t.Errorf("C view: want userMessageIndex=2, got %d", viewsC[0].UserMessageIndex)
	}
	if viewsC[0].Total != 2 {
		t.Errorf("C view: want total=2, got %d", viewsC[0].Total)
	}
	if viewsC[0].CurrentIndex != resC.BranchIndex {
		t.Errorf("C view: want currentIndex=%d, got %d", resC.BranchIndex, viewsC[0].CurrentIndex)
	}

	// B should see BOTH its sibling position (at index 1 from root) and its child (at index 2).
	viewsB, err := mgr.LoadBranchPointViews(bID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(B): %v", err)
	}
	if len(viewsB) != 2 {
		t.Fatalf("B: want 2 views (sibling+child), got %d: %+v", len(viewsB), viewsB)
	}

	sibView, childView := findByIdx(viewsB, 1), findByIdx(viewsB, 2)
	if sibView == nil {
		t.Error("B: missing sibling view at userMessageIndex=1")
	} else if sibView.CurrentIndex != resB.BranchIndex {
		t.Errorf("B sibling: want currentIndex=%d, got %d", resB.BranchIndex, sibView.CurrentIndex)
	}
	if childView == nil {
		t.Error("B: missing child view at userMessageIndex=2")
	} else if childView.Total != 2 {
		t.Errorf("B child: want total=2, got %d", childView.Total)
	}
}

// TestThreeLevelBranching tests R→B→C→D nesting.
func TestThreeLevelBranching(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("r0", "r1"))

	// Level 1: B branches from root at index 1.
	resB, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 1})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	bID := resB.NewSessionID
	writeMsgs(t, fs.SessionPath(bID), userMsgs("r0", "b1"))

	// Level 2: C branches from B at index 1 (B's own message b1, different from B's origin index 1).
	// Wait — B.Origin.UserMessageIndex == 1 and params.UserMessageIndex == 1, so C is a SIBLING.
	// Let's use index 1 for B but add a message at index 1 to branch from B's different position.
	// Actually B inherits r0 (index 0) and adds b1 (index 1). B.Origin.UserMessageIndex = 1 (root's index).
	// params.UserMessageIndex=1 from B == srcBF.Origin.UserMessageIndex=1 → SIBLING.
	// Let's give B a third message and branch at index 2.
	writeMsgs(t, fs.SessionPath(bID), userMsgs("r0", "b1", "b2"))

	resC, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: bID, UserMessageIndex: 2})
	if err != nil {
		t.Fatalf("create C: %v", err)
	}
	if resC.TotalBranches != 2 {
		t.Errorf("C: want totalBranches=2, got %d", resC.TotalBranches)
	}
	cID := resC.NewSessionID
	writeMsgs(t, fs.SessionPath(cID), userMsgs("r0", "b1", "c2", "c3"))

	// Level 3: D branches from C at index 3.
	resD, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: cID, UserMessageIndex: 3})
	if err != nil {
		t.Fatalf("create D: %v", err)
	}
	if resD.TotalBranches != 2 {
		t.Errorf("D: want totalBranches=2, got %d", resD.TotalBranches)
	}

	// D should only see C's child branch (at index 3).
	viewsD, err := mgr.LoadBranchPointViews(resD.NewSessionID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(D): %v", err)
	}
	if len(viewsD) != 1 {
		t.Fatalf("D: want 1 view, got %d", len(viewsD))
	}
	if viewsD[0].UserMessageIndex != 3 {
		t.Errorf("D view: want userMessageIndex=3, got %d", viewsD[0].UserMessageIndex)
	}

	// C should see: sibling view (from B at index 2) + child view (at index 3).
	viewsC, err := mgr.LoadBranchPointViews(cID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(C): %v", err)
	}
	if len(viewsC) != 2 {
		t.Fatalf("C: want 2 views, got %d: %+v", len(viewsC), viewsC)
	}
	if findByIdx(viewsC, 2) == nil {
		t.Error("C: missing sibling view at userMessageIndex=2")
	}
	if findByIdx(viewsC, 3) == nil {
		t.Error("C: missing child view at userMessageIndex=3")
	}

	// D should NOT appear in root's branch file.
	viewsRoot, err := mgr.LoadBranchPointViews(rootID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(root): %v", err)
	}
	for _, v := range viewsRoot {
		for _, s := range v.Sessions {
			if s.SessionID == resD.NewSessionID {
				t.Errorf("D must not appear in root branch views, but it does at userMessageIndex=%d", v.UserMessageIndex)
			}
		}
	}
}

// TestPruneBranchRefsDropsDeletedSibling verifies that retracting one branch of a
// three-way fork leaves the survivors listed, with navigator positions renumbered.
func TestPruneBranchRefsDropsDeletedSibling(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello", "world"))

	r1, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B1: %v", err)
	}
	r2, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B2: %v", err)
	}

	if err := mgr.PruneBranchRefs(r1.NewSessionID); err != nil {
		t.Fatalf("PruneBranchRefs(B1): %v", err)
	}
	if err := os.RemoveAll(fs.SessionPath(r1.NewSessionID)); err != nil {
		t.Fatalf("remove B1: %v", err)
	}

	bf, err := ReadBranchFile(rootDir)
	if err != nil {
		t.Fatalf("read root branches: %v", err)
	}
	if len(bf.BranchPoints) != 1 {
		t.Fatalf("root: want 1 branch point, got %d", len(bf.BranchPoints))
	}
	for _, ref := range bf.BranchPoints[0].Sessions {
		if ref.SessionID == r1.NewSessionID {
			t.Errorf("root still references deleted branch %q", r1.NewSessionID)
		}
	}

	// B2 keeps its navigator entry, now as the second of two threads.
	views, err := mgr.LoadBranchPointViews(r2.NewSessionID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(B2): %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("B2: want 1 view, got %d", len(views))
	}
	if views[0].Total != 2 {
		t.Errorf("B2 view: want total=2, got %d", views[0].Total)
	}
	if views[0].CurrentIndex != 1 {
		t.Errorf("B2 view: want currentIndex=1, got %d", views[0].CurrentIndex)
	}
	if got := views[0].Sessions[views[0].CurrentIndex].SessionID; got != r2.NewSessionID {
		t.Errorf("B2 view: currentIndex points at %q, want %q", got, r2.NewSessionID)
	}
}

// TestPruneBranchRefsCollapsesLastBranch verifies that deleting the only branch of a
// session removes the branch point entirely instead of leaving a one-sided fork.
func TestPruneBranchRefsCollapsesLastBranch(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello", "world"))

	res, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create branch: %v", err)
	}

	if err := mgr.PruneBranchRefs(res.NewSessionID); err != nil {
		t.Fatalf("PruneBranchRefs: %v", err)
	}
	if err := os.RemoveAll(fs.SessionPath(res.NewSessionID)); err != nil {
		t.Fatalf("remove branch: %v", err)
	}

	bf, err := ReadBranchFile(rootDir)
	if err != nil {
		t.Fatalf("read root branches: %v", err)
	}
	if len(bf.BranchPoints) != 0 {
		t.Errorf("root: want no branch points, got %+v", bf.BranchPoints)
	}
	views, err := mgr.LoadBranchPointViews(rootID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(root): %v", err)
	}
	if len(views) != 0 {
		t.Errorf("root: want no views, got %+v", views)
	}
}

// TestPruneBranchRefsWithoutOrigin verifies that pruning a session that never
// branched touches nothing and reports no error.
func TestPruneBranchRefsWithoutOrigin(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello"))

	if err := mgr.PruneBranchRefs(rootID); err != nil {
		t.Fatalf("PruneBranchRefs(root): %v", err)
	}
	if err := mgr.PruneBranchRefs("never-existed"); err != nil {
		t.Fatalf("PruneBranchRefs(missing): %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, branchesFile)); !os.IsNotExist(err) {
		t.Errorf("prune created %s for a session that never branched (err=%v)", branchesFile, err)
	}
}

// TestLoadBranchPointViewsSkipsMissingSessions verifies that a branch file left
// pointing at a deleted bundle (written before pruning existed) self-heals on read.
func TestLoadBranchPointViewsSkipsMissingSessions(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello", "world"))

	r1, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B1: %v", err)
	}
	r2, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create B2: %v", err)
	}

	// Delete the bundle behind the branch without touching root's branch file.
	if err := os.RemoveAll(fs.SessionPath(r1.NewSessionID)); err != nil {
		t.Fatalf("remove B1: %v", err)
	}

	views, err := mgr.LoadBranchPointViews(rootID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(root): %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("root: want 1 view, got %d", len(views))
	}
	if views[0].Total != 2 {
		t.Errorf("root view: want total=2, got %d", views[0].Total)
	}
	for _, ref := range views[0].Sessions {
		if ref.SessionID == r1.NewSessionID {
			t.Errorf("root view still offers deleted session %q", r1.NewSessionID)
		}
	}

	// The surviving sibling sees the same collapsed fork from the other side.
	viewsB2, err := mgr.LoadBranchPointViews(r2.NewSessionID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(B2): %v", err)
	}
	if len(viewsB2) != 1 {
		t.Fatalf("B2: want 1 view, got %d", len(viewsB2))
	}
	if viewsB2[0].Total != 2 || viewsB2[0].CurrentIndex != 1 {
		t.Errorf("B2 view: want currentIndex=1 total=2, got %d/%d",
			viewsB2[0].CurrentIndex, viewsB2[0].Total)
	}
}

// TestLoadBranchPointViewsDropsSoleSurvivor verifies that a two-session fork whose
// branch bundle is gone stops advertising a navigator on the session that remains.
// This is the shape of the reported bug: total stayed 2 with one dead id.
func TestLoadBranchPointViewsDropsSoleSurvivor(t *testing.T) {
	mgr, fs := newTestManager(t)

	rootID := "root"
	rootDir, _ := fs.EnsureLayout(rootID)
	writeMsgs(t, rootDir, userMsgs("hello", "world"))

	res, err := mgr.CreateBranchSession(CreateBranchParams{SourceSessionID: rootID, UserMessageIndex: 0})
	if err != nil {
		t.Fatalf("create branch: %v", err)
	}
	// Delete the bundle behind the branch without touching root's branch file.
	if err := os.RemoveAll(fs.SessionPath(res.NewSessionID)); err != nil {
		t.Fatalf("remove branch: %v", err)
	}

	views, err := mgr.LoadBranchPointViews(rootID)
	if err != nil {
		t.Fatalf("LoadBranchPointViews(root): %v", err)
	}
	if len(views) != 0 {
		t.Errorf("root: want no views once the only branch is gone, got %+v", views)
	}
}

func findByIdx(views []BranchPointView, idx int) *BranchPointView {
	for i := range views {
		if views[i].UserMessageIndex == idx {
			return &views[i]
		}
	}
	return nil
}
