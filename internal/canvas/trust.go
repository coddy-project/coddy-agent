package canvas

// Workspace trust for the pages of a board.
//
// A .canvas file is repository content: a board that came with a checkout
// names HTML pages beside it, and a page on the canvas runs scripts that read
// every file under its root and can send what they read anywhere by
// navigating their own frame. So a page runs only as tools.canvas.project_trust
// allows: under ask (the default) a page runs once Coddy put it on the board
// (canvas_show, the files window) or the person trusted the board's pages;
// under deny only the pages Coddy put run; under allow every page runs.
//
// A receipt binds a board file and the pages that may run on it, by their
// workspace-relative path. It does not bind what a page holds: the agent edits
// pages all the time, and a page edited in place is still the page the person
// let run. A page the file names that no receipt holds - one a branch or a
// pull added - waits, while the board's other pages keep running.
//
// Receipts live in <home>/canvas-trust.json, keyed by the project
// (mcp.TrustWorkspace), so a linked worktree shares what its main checkout
// decided; each checkout keeps its own receipt under that key. The store is a
// sibling of the MCP, hooks and subagent stores: one kind of approval never
// reads as another.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// TrustFileName is the receipts file inside the coddy home directory.
const TrustFileName = "canvas-trust.json"

const trustFileVersion = 1

var (
	// ErrTrustDenied refuses the person's approval under project_trust deny.
	ErrTrustDenied = errors.New("tools.canvas.project_trust is deny: the pages of a board Coddy did not make never run")
	// ErrPagesMoved refuses an approval of a page list the board no longer
	// holds: the person approved what the dialog showed, not what is there now.
	ErrPagesMoved = errors.New("the pages of this board changed since they were shown; read the board again")
	// ErrFrameHeld is a frame whose page waits for the person.
	ErrFrameHeld = errors.New("this page waits for the person to trust the pages of its board")
)

// Trust is how a Ref decides which pages of a board run: the policy and the
// receipts. A Ref without one runs no page of any board, so a caller that
// forgets it fails closed.
type Trust struct {
	// Policy is tools.canvas.project_trust: ask, allow or deny ("" is ask).
	Policy string
	// Store holds the receipts; without one no page is recorded or approved.
	Store *TrustStore
}

// NewTrust is the trust of a process: the receipts under its home and the
// policy of its configuration.
func NewTrust(home, policy string) *Trust {
	t := &Trust{Policy: policy}
	if strings.TrimSpace(home) != "" {
		t.Store = NewTrustStore(home)
	}
	return t
}

func (t *Trust) policy() string {
	if t == nil {
		return config.ProjectTrustAsk
	}
	switch p := strings.ToLower(strings.TrimSpace(t.Policy)); p {
	case config.ProjectTrustAllow, config.ProjectTrustDeny:
		return p
	default:
		return config.ProjectTrustAsk
	}
}

// runs answers, for one board file, whether a page may run.
func (t *Trust) runs(workspace, file string) func(page string) bool {
	switch {
	case t == nil:
		return func(string) bool { return false }
	case t.policy() == config.ProjectTrustAllow:
		return func(string) bool { return true }
	case t.Store == nil:
		return func(string) bool { return false }
	}
	shown, approved := t.Store.pages(workspace, file)
	deny := t.policy() == config.ProjectTrustDeny
	return func(page string) bool {
		return shown[page] || (!deny && approved[page])
	}
}

// PagesDigest names a list of pages: what the person approves and what the
// approval is checked against, so a dialog that showed one list never approves
// another.
func PagesDigest(pages []string) string {
	h := sha256.New()
	for _, p := range uniqueSorted(pages) {
		// Length-prefixed, not joined by a separator: a file name may hold a
		// newline, and ["a\nb.html"] would otherwise digest the same as
		// ["a", "b.html"] - a board rewritten between the dialog and the
		// approval could keep the digest while changing the list.
		// A hash writer never fails.
		_, _ = fmt.Fprintf(h, "%d:%s", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Pages are the pages a board's frames show, each once, sorted.
func (b Board) Pages() []string {
	out := make([]string, 0, len(b.Frames))
	for _, f := range b.Frames {
		out = append(out, f.Path)
	}
	return uniqueSorted(out)
}

// Held reports whether a page of the board waits for the person.
func (b Board) Held() bool {
	for _, f := range b.Frames {
		if f.Held {
			return true
		}
	}
	return false
}

// Policy is the project_trust the board was read under.
func (b Board) Policy() string {
	return b.policy
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// applyTrust marks the frames whose pages may not run.
func applyTrust(b *Board, t *Trust) {
	b.policy = t.policy()
	runs := t.runs(b.Workspace, b.File)
	for i := range b.Frames {
		b.Frames[i].Held = !runs(b.Frames[i].Path)
	}
}

// TrustBoard records the person's approval of the pages a board shows now.
// digest is PagesDigest of the list the person was shown; a board whose pages
// moved since is refused with ErrPagesMoved.
func TrustBoard(ref Ref, digest string) (Board, error) {
	if ref.Trust.policy() == config.ProjectTrustDeny {
		return Board{}, ErrTrustDenied
	}
	if ref.Trust == nil || ref.Trust.Store == nil {
		return Board{}, errors.New("this process keeps no canvas trust receipts")
	}
	var board Board
	err := mutate(ref, nil, func(b *Board) (string, error) {
		if !b.fileOK {
			return "", fmt.Errorf("%s cannot be read: %s", b.File, b.Error)
		}
		pages := b.Pages()
		if strings.TrimSpace(digest) != PagesDigest(pages) {
			return "", ErrPagesMoved
		}
		if err := ref.Trust.Store.Approve(b.Workspace, b.File, pages); err != nil {
			return "", err
		}
		applyTrust(b, ref.Trust)
		return "", nil
	}, func() string { return ReasonTrusted }, &board, nil)
	return board, err
}

// UntrustBoard takes back every approval and every page Coddy put on a board,
// in every checkout of the project: its pages wait for the person again.
func UntrustBoard(ref Ref) (Board, error) {
	if ref.Trust == nil || ref.Trust.Store == nil {
		return Board{}, errors.New("this process keeps no canvas trust receipts")
	}
	var board Board
	err := mutate(ref, nil, func(b *Board) (string, error) {
		if _, err := ref.Trust.Store.Revoke(b.Workspace, b.File); err != nil {
			return "", err
		}
		applyTrust(b, ref.Trust)
		return "", nil
	}, func() string { return ReasonUntrusted }, &board, nil)
	return board, err
}

// TrustRecord is one checkout's receipt for one board file.
type TrustRecord struct {
	// File is the board, slash-separated and relative to the workspace.
	File string `json:"file"`
	// Shown are the pages Coddy put on the board (canvas_show, the files
	// window): they run under ask and deny.
	Shown []string `json:"shown,omitempty"`
	// Approved are the pages the person let run: they run under ask.
	Approved   []string `json:"approved,omitempty"`
	ApprovedAt string   `json:"approved_at"`
	// Checkout is the canonical folder the receipt was given in: the
	// checkouts of a project share one key but keep a receipt each
	// (mcp.ReceiptCheckout reads an empty one as its key's).
	Checkout string `json:"checkout,omitempty"`
}

type trustFile struct {
	Version    int                      `json:"version"`
	Workspaces map[string][]TrustRecord `json:"workspaces"`
}

// TrustStore persists receipts at <home>/canvas-trust.json. Every operation
// re-reads the file, so a decision made through another process reaches this
// one on its next read of a board. A write is a transaction under an
// in-process mutex shared by every instance of the same path plus a file lock
// shared with other processes, and the file is replaced atomically.
type TrustStore struct {
	path string
}

// trustStoreLocks holds one lock per receipts file path for the process.
var trustStoreLocks sync.Map

// NewTrustStore returns the store backed by <home>/canvas-trust.json.
func NewTrustStore(home string) *TrustStore {
	return &TrustStore{path: filepath.Join(home, TrustFileName)}
}

// Path returns the receipts file path.
func (s *TrustStore) Path() string { return s.path }

func (s *TrustStore) lock() *sync.RWMutex {
	m, _ := trustStoreLocks.LoadOrStore(s.path, &sync.RWMutex{})
	return m.(*sync.RWMutex)
}

// read loads the receipts. A missing file is not an error; a corrupt one is,
// so a damaged store never silently reads as "nothing approved" and gets
// written back over, as the MCP store refuses the same way.
func (s *TrustStore) read() (trustFile, error) {
	empty := trustFile{Version: trustFileVersion, Workspaces: map[string][]TrustRecord{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return trustFile{}, fmt.Errorf("canvas trust store: read %s: %w", s.path, err)
	}
	var parsed trustFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return trustFile{}, fmt.Errorf("canvas trust store: %s is damaged: %w", s.path, err)
	}
	if parsed.Workspaces == nil {
		parsed.Workspaces = map[string][]TrustRecord{}
	}
	parsed.Version = trustFileVersion
	return parsed, nil
}

func (s *TrustStore) write(file trustFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("canvas trust store: %w", err)
	}
	// A unique temporary file: two processes writing at once never share one.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), TrustFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("canvas trust store: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("canvas trust store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("canvas trust store: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("canvas trust store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("canvas trust store: %w", err)
	}
	return nil
}

// transaction serialises a read-modify-write against every other writer of
// the file: the mutex covers goroutines, the file lock other processes.
func (s *TrustStore) transaction() (func(), error) {
	mu := s.lock()
	mu.Lock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		mu.Unlock()
		return nil, fmt.Errorf("canvas trust store: %w", err)
	}
	unlockFile, err := platform.LockFile(s.path + ".lock")
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		unlockFile()
		mu.Unlock()
	}, nil
}

// pages are the pages of one board file that Coddy put on it and that the
// person approved, in any checkout of the project.
func (s *TrustStore) pages(workspace, file string) (shown, approved map[string]bool) {
	shown, approved = map[string]bool{}, map[string]bool{}
	key := mcp.TrustWorkspace(workspace)
	if key == "" || file == "" {
		return shown, approved
	}
	checkout := mcp.CanonicalWorkspace(workspace)
	// A read lock, so the file requests of a page's assets do not queue behind
	// one another, while a write still never renames the file under a reader.
	mu := s.lock()
	mu.RLock()
	defer mu.RUnlock()
	tf, err := s.read()
	if err != nil {
		// A store that cannot be read approves nothing: every page waits.
		return shown, approved
	}
	for _, rec := range tf.Workspaces[key] {
		if rec.File != file {
			continue
		}
		// Approved is the person's decision about the project, so a linked
		// worktree runs what the main checkout let run. Shown is not a
		// decision - it records that Coddy put a page on this checkout's
		// board - and the board at that path in a sibling checkout is another
		// branch's file, so it never leaves the checkout it was made in.
		for _, p := range rec.Approved {
			approved[p] = true
		}
		if mcp.ReceiptCheckout(rec.Checkout, key) != checkout {
			continue
		}
		for _, p := range rec.Shown {
			shown[p] = true
		}
	}
	return shown, approved
}

// Records returns the receipts recorded for a project, sorted by file.
func (s *TrustStore) Records(workspace string) []TrustRecord {
	key := mcp.TrustWorkspace(workspace)
	mu := s.lock()
	mu.RLock()
	tf, err := s.read()
	mu.RUnlock()
	if err != nil {
		return nil
	}
	out := append([]TrustRecord(nil), tf.Workspaces[key]...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Checkout < out[j].Checkout
	})
	return out
}

// edit changes this checkout's receipt for a board file in one transaction.
func (s *TrustStore) edit(workspace, file string, fn func(*TrustRecord)) error {
	checkout := mcp.CanonicalWorkspace(workspace)
	key := mcp.TrustWorkspace(workspace)
	if key == "" || strings.TrimSpace(file) == "" {
		return errors.New("canvas trust store: workspace and board file are required")
	}
	release, err := s.transaction()
	if err != nil {
		return err
	}
	defer release()
	tf, err := s.read()
	if err != nil {
		return err
	}
	records := tf.Workspaces[key]
	at := -1
	for i, rec := range records {
		if rec.File == file && mcp.ReceiptCheckout(rec.Checkout, key) == checkout {
			at = i
			break
		}
	}
	if at < 0 {
		records = append(records, TrustRecord{File: file, Checkout: checkout})
		at = len(records) - 1
	}
	rec := &records[at]
	fn(rec)
	rec.Checkout = checkout
	rec.ApprovedAt = time.Now().UTC().Format(time.RFC3339)
	tf.Workspaces[key] = records
	return s.write(tf)
}

// onBoardSet is the lookup of the pages a board shows now.
func onBoardSet(onBoard []string) map[string]bool {
	keep := make(map[string]bool, len(onBoard))
	for _, p := range onBoard {
		keep[p] = true
	}
	return keep
}

// keepPages drops the pages a board no longer shows. Both halves of a receipt
// are trimmed alike: a page the person let run leaves with its frame just as
// one Coddy put there does, or an approval would outlive the board and a page
// the file brings back later would run unasked.
func keepPages(pages []string, keep map[string]bool) ([]string, bool) {
	kept := pages[:0:0]
	for _, p := range pages {
		if keep[p] {
			kept = append(kept, p)
		}
	}
	return kept, len(kept) != len(pages)
}

// Show records pages Coddy put on a board: they run from now on. onBoard are
// the pages the board shows after the change: a page recorded before that is
// no longer on it is dropped, so the receipt never outgrows the board, and a
// page that comes back through the file asks again.
func (s *TrustStore) Show(workspace, file string, onBoard []string, pages ...string) error {
	keep := onBoardSet(onBoard)
	for _, p := range pages {
		keep[p] = true
	}
	return s.edit(workspace, file, func(rec *TrustRecord) {
		shown, _ := keepPages(rec.Shown, keep)
		rec.Shown = uniqueSorted(append(shown, pages...))
		rec.Approved, _ = keepPages(rec.Approved, keep)
	})
}

// Prune drops from this checkout's receipt of a board the pages the board no
// longer shows, whether Coddy put them there or the person let them run. A
// board without a receipt keeps none.
func (s *TrustStore) Prune(workspace, file string, onBoard []string) error {
	checkout := mcp.CanonicalWorkspace(workspace)
	key := mcp.TrustWorkspace(workspace)
	if key == "" {
		return nil
	}
	keep := onBoardSet(onBoard)
	release, err := s.transaction()
	if err != nil {
		return err
	}
	defer release()
	tf, err := s.read()
	if err != nil {
		return err
	}
	changed := false
	for i, rec := range tf.Workspaces[key] {
		if rec.File != file || mcp.ReceiptCheckout(rec.Checkout, key) != checkout {
			continue
		}
		shown, movedShown := keepPages(rec.Shown, keep)
		approved, movedApproved := keepPages(rec.Approved, keep)
		if movedShown || movedApproved {
			tf.Workspaces[key][i].Shown = shown
			tf.Workspaces[key][i].Approved = approved
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.write(tf)
}

// Approve records the person's approval of exactly these pages of a board in
// this checkout, replacing the checkout's earlier approval; the other
// checkouts of the project keep theirs.
func (s *TrustStore) Approve(workspace, file string, pages []string) error {
	return s.edit(workspace, file, func(rec *TrustRecord) {
		rec.Approved = uniqueSorted(pages)
	})
}

// Revoke removes every receipt of a board file in the project and reports
// whether one was on file.
func (s *TrustStore) Revoke(workspace, file string) (bool, error) {
	key := mcp.TrustWorkspace(workspace)
	release, err := s.transaction()
	if err != nil {
		return false, err
	}
	defer release()
	tf, err := s.read()
	if err != nil {
		return false, err
	}
	records := tf.Workspaces[key]
	kept := make([]TrustRecord, 0, len(records))
	for _, rec := range records {
		if rec.File != file {
			kept = append(kept, rec)
		}
	}
	if len(kept) == len(records) {
		return false, nil
	}
	if len(kept) == 0 {
		delete(tf.Workspaces, key)
	} else {
		tf.Workspaces[key] = kept
	}
	return true, s.write(tf)
}
