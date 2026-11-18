//go:build http

package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type changesEnv struct {
	srv *Server
	id  string
	cwd string
}

func newChangesEnv(t *testing.T) *changesEnv {
	t.Helper()
	return newChangesEnvWithRunner(t, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
}

// newChangesEnvWithRunner is newChangesEnv with the turn a test needs the agent
// to run when a prompt is posted.
func newChangesEnvWithRunner(t *testing.T, runner func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error)) *changesEnv {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		// A home of its own: the receipts that let a page on the canvas run
		// live there.
		Paths:     config.Paths{Home: filepath.Join(root, "home")},
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	cwd := t.TempDir()
	newRes, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	return &changesEnv{srv: srv, id: newRes.SessionID, cwd: cwd}
}

func (e *changesEnv) do(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	return e.doBody(t, method, target, "")
}

func (e *changesEnv) doBody(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func writeInWorkspace(t *testing.T, cwd, rel, content string) {
	t.Helper()
	p := filepath.Join(cwd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readInWorkspace(t *testing.T, cwd, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cwd, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitWorkspace makes the session's folder a repository whose HEAD holds
// files. A machine without git cannot build the fixture and skips.
func (e *changesEnv) commitWorkspace(t *testing.T, files map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	gitInRepo(t, e.cwd, "init", "-b", "main")
	gitInRepo(t, e.cwd, "config", "core.autocrlf", "false")
	for name, body := range files {
		writeInWorkspace(t, e.cwd, name, body)
	}
	gitInRepo(t, e.cwd, "add", "-A")
	gitInRepo(t, e.cwd, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "init")
}

func changedFiles(t *testing.T, rec *httptest.ResponseRecorder) map[string]map[string]interface{} {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	files, _ := body["files"].([]interface{})
	out := make(map[string]map[string]interface{}, len(files))
	for _, f := range files {
		row, _ := f.(map[string]interface{})
		p, _ := row["path"].(string)
		out[filepath.ToSlash(p)] = row
	}
	return out
}

// waitForSessionChanges reads frames until the one that says the session's
// working copy may have moved, and fails the test if none arrives.
func waitForSessionChanges(t *testing.T, frames <-chan []byte, sessionID string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-frames:
			s := string(f)
			if strings.HasPrefix(s, "event: session_changes\n") && strings.Contains(s, `"sessionId":"`+sessionID+`"`) {
				return
			}
		case <-deadline:
			t.Fatal("no session_changes event for the session")
		}
	}
}

// A folder that is not a repository has no edits: the list is empty and names
// no version control, so the client hides the Edits view rather than erroring.
func TestSessionChangesOutsideARepo(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.txt", "a\n")
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["object"] != "coddy.session_changes" || body["vcs"] != "" {
		t.Fatalf("want an empty answer naming no vcs: %v", body)
	}
	if files := body["files"].([]interface{}); len(files) != 0 {
		t.Fatalf("want no files: %v", files)
	}
	totals := body["totals"].(map[string]interface{})
	if totals["files"].(float64) != 0 || totals["additions"].(float64) != 0 || totals["deletions"].(float64) != 0 {
		t.Fatalf("totals %v", totals)
	}
}

// The list is git's report of the working copy: tracked edits and the new
// files git does not ignore, with their line counts, whoever made them.
func TestSessionChangesReportTheWorkingCopy(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{".gitignore": "*.log\n", "tracked.txt": "old\n", "keep.txt": "same\n"})
	writeInWorkspace(t, e.cwd, "tracked.txt", "new\n")
	writeInWorkspace(t, e.cwd, "loose.txt", "x\ny\n")
	writeInWorkspace(t, e.cwd, "debug.log", "noise\n")

	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes")
	body := decodeJSON(t, rec)
	if body["vcs"] != "git" {
		t.Fatalf("want vcs git: %v", body)
	}
	if body["skipped"].(float64) != 0 {
		t.Fatalf("nothing is skipped here: %v", body["skipped"])
	}
	files := changedFiles(t, rec)
	if len(files) != 2 {
		t.Fatalf("want tracked.txt and loose.txt, got %v", files)
	}
	if f := files["tracked.txt"]; f["status"] != "modified" || f["additions"].(float64) != 1 || f["deletions"].(float64) != 1 {
		t.Fatalf("tracked.txt %v", f)
	}
	if f := files["loose.txt"]; f["status"] != "added" || f["additions"].(float64) != 2 || f["deletions"].(float64) != 0 {
		t.Fatalf("loose.txt %v", f)
	}
	totals := body["totals"].(map[string]interface{})
	if totals["files"].(float64) != 2 || totals["additions"].(float64) != 3 || totals["deletions"].(float64) != 1 {
		t.Fatalf("totals %v", totals)
	}
	// The list carries no patch unless asked.
	if _, ok := files["tracked.txt"]["patch"]; ok {
		t.Fatalf("patch sent without include: %v", files["tracked.txt"])
	}
}

func TestSessionChangesIncludePatchAndContent(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes?include=patch,content"))
	f := files["a.txt"]
	patch, _ := f["patch"].(string)
	if !strings.Contains(patch, "\n-old\n") || !strings.Contains(patch, "\n+new\n") {
		t.Fatalf("patch %q", patch)
	}
	if f["before"] != "old\n" || f["after"] != "new\n" {
		t.Fatalf("sides %v / %v", f["before"], f["after"])
	}
}

// The detail route reads one file of the change set, by either separator; a
// path git does not report - unchanged, ignored, outside the folder - is 404.
func TestSessionChangeFileDetail(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{".gitignore": "*.log\n", "dir/sub/a.txt": "one\n", "keep.txt": "same\n"})
	writeInWorkspace(t, e.cwd, "dir/sub/a.txt", "two\n")
	writeInWorkspace(t, e.cwd, "debug.log", "noise\n")

	for _, p := range []string{"dir/sub/a.txt", "dir%5Csub%5Ca.txt"} {
		rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path="+p)
		if rec.Code != http.StatusOK {
			t.Fatalf("path %q returned %d: %s", p, rec.Code, rec.Body.String())
		}
		body := decodeJSON(t, rec)
		if body["object"] != "coddy.session_change" || body["status"] != "modified" ||
			!strings.Contains(body["patch"].(string), "+two") || body["before"] != "one\n" {
			t.Fatalf("detail %v", body)
		}
	}
	for _, p := range []string{"keep.txt", "debug.log", "../escape.txt", "missing.txt"} {
		rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path="+p)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q must be 404, got %d: %s", p, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file"); rec.Code != http.StatusBadRequest {
		t.Fatalf("a missing path must be 400, got %d", rec.Code)
	}
}

// A binary file has no line diff; it is listed so the reader knows it changed,
// with no line counts.
func TestSessionChangesMarkBinary(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "a\n"})
	writeInWorkspace(t, e.cwd, "blob.bin", "\x00\x01\x02binary\x00")

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	f := files["blob.bin"]
	if f == nil || f["binary"] != true || f["additions"].(float64) != 0 {
		t.Fatalf("blob.bin %v", f)
	}
}

func TestSessionChangesDiscardOneFile(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	writeInWorkspace(t, e.cwd, "loose.txt", "x\n")

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"paths":["a.txt"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeJSON(t, rec); body["object"] != "coddy.session_changes_reverted" || body["sessionId"] != e.id {
		t.Fatalf("answer %v", body)
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "old\n" {
		t.Fatalf("a.txt = %q, want HEAD's content", got)
	}
	if got := readInWorkspace(t, e.cwd, "loose.txt"); got != "x\n" {
		t.Fatalf("loose.txt was not named and must stay: %q", got)
	}
}

func TestSessionChangesDiscardEverything(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	writeInWorkspace(t, e.cwd, "loose.txt", "x\n")

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"all":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "old\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.cwd, "loose.txt")); !os.IsNotExist(err) {
		t.Fatalf("loose.txt must be deleted: %v", err)
	}
	if files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes")); len(files) != 0 {
		t.Fatalf("want a clean working copy: %v", files)
	}
}

// Discarding deletes files, so the request says what it means: a list of paths
// or all of them. An empty or malformed body is refused and touches nothing.
func TestSessionChangesDiscardNeedsAnExplicitSelection(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	for _, body := range []string{"", "{}", `{"paths":[]}`, `{"all":false}`, "{bad", `{"all":true,"paths":["a.txt"]}`} {
		rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
}

// A path that is not in the change set - it was committed meanwhile, it is
// unchanged or ignored - is a conflict with what the client saw, and nothing
// of the request is applied.
func TestSessionChangesDiscardRefusesAPathOutsideTheSet(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n", "keep.txt": "same\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"paths":["a.txt","keep.txt"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
}

func TestSessionChangesDiscardOutsideARepo(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.txt", "a\n")
	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"all":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "a\n" {
		t.Fatalf("a plain folder was touched: %q", got)
	}
}

// Every window showing the session reads its working copy again after a
// discard.
func TestSessionChangesDiscardAnnouncesTheChange(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	frames, unsubscribe := e.srv.events.subscribe()
	defer unsubscribe()

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"all":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	waitForSessionChanges(t, frames, e.id)
}

// Rewriting files under an agent that is mid-edit would leave the folder in a
// state neither side expects, so a discard waits for the turn to end.
func TestSessionChangesDiscardRefusedWhileATurnRuns(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	e := newChangesEnvWithRunner(t, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		close(started)
		<-release
		return string(acp.StopReasonEndTurn), nil
	})
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses",
			strings.NewReader(`{"model":"agent","input":"work","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", e.id)
		e.srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	defer func() {
		close(release)
		<-done
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never started")
	}

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"all":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 while a turn runs, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused discard touched a.txt: %q", got)
	}
}

func TestSessionChangesFrameShape(t *testing.T) {
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	frame := string(sessionChangesFrame("sess_x", at))
	if !strings.HasPrefix(frame, "event: session_changes\ndata: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("frame %q", frame)
	}
	var body map[string]interface{}
	data := strings.TrimSuffix(strings.TrimPrefix(frame, "event: session_changes\ndata: "), "\n\n")
	if err := json.Unmarshal([]byte(data), &body); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	if body["object"] != "coddy.session_changes" || body["sessionId"] != "sess_x" || body["at"] != "2026-09-27T12:00:00Z" {
		t.Fatalf("body %v", body)
	}
}

// Only one JSON object: anything after it makes the request ambiguous, and a
// destructive one is refused rather than read as its first half.
func TestSessionChangesDiscardRefusesTrailingData(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n", "b.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	writeInWorkspace(t, e.cwd, "b.txt", "new\n")

	for _, body := range []string{`{"all":true}{"paths":["a.txt"]}`, `{"all":true} trailing`, `{"paths":["a.txt"]}]`} {
		rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
	// Whitespace after the object is not data.
	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", "{\"paths\":[\"a.txt\"]}\n  ")
	if rec.Code != http.StatusOK {
		t.Fatalf("trailing whitespace: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A discard rewrites the folder, not a session: a turn running in another chat
// of the same folder refuses it as much as one of its own.
func TestSessionChangesDiscardRefusedWhileAnotherChatOfTheFolderRuns(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	e := newChangesEnvWithRunner(t, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		close(started)
		<-release
		return string(acp.StopReasonEndTurn), nil
	})
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	other, err := e.srv.mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: e.cwd})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses",
			strings.NewReader(`{"model":"agent","input":"work","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", other.SessionID)
		e.srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	defer func() {
		close(release)
		<-done
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the other chat's turn never started")
	}

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", `{"all":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 while another chat of the folder runs, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused discard touched a.txt: %q", got)
	}
}

// A selection says one thing: both keys, an empty list, or a key the route
// does not know is refused, never read as "all".
func TestSessionChangesDiscardRefusesAnUnclearSelection(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	for _, body := range []string{`{"all":true,"paths":[]}`, `{"all":true,"paths":null}`, `{"all":true,"path":["a.txt"]}`, `{"paths":null}`} {
		rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
}

// Deleting removes the named files without restoring HEAD: a file git does
// not hold goes away, and an edited tracked file becomes the deletion git
// then reports.
func TestSessionChangesDeleteNamedFiles(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n", "keep.txt": "same\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	writeInWorkspace(t, e.cwd, "loose.txt", "x\n")

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", `{"paths":["a.txt","loose.txt"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeJSON(t, rec); body["object"] != "coddy.session_changes_deleted" || body["sessionId"] != e.id {
		t.Fatalf("answer %v", body)
	}
	if _, err := os.Lstat(filepath.Join(e.cwd, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt must be deleted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(e.cwd, "loose.txt")); !os.IsNotExist(err) {
		t.Fatalf("loose.txt must be deleted: %v", err)
	}
	// The tracked file is not put back at HEAD: it is gone and git reports it
	// as a deletion.
	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	if f := files["a.txt"]; f == nil || f["status"] != "deleted" {
		t.Fatalf("a.txt must be reported as deleted: %v", files)
	}
	if _, ok := files["loose.txt"]; ok {
		t.Fatalf("the deleted new file is still reported: %v", files)
	}
	if got := readInWorkspace(t, e.cwd, "keep.txt"); got != "same\n" {
		t.Fatalf("keep.txt was not named and must stay: %q", got)
	}
}

// Deleting files needs the paths it deletes: an empty or malformed body, a
// selection the route does not know, or an all-of-them flag is refused and
// touches nothing.
func TestSessionChangesDeleteNeedsAnExplicitSelection(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	for _, body := range []string{"", "{}", `{"paths":[]}`, `{"paths":null}`, `{"all":true}`, "{bad", `{"paths":["a.txt"],"all":true}`, `{"path":["a.txt"]}`, `{"paths":["a.txt"]} trailing`} {
		rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
}

// A path that is not in the change set - unchanged, ignored, outside the
// folder or missing - refuses the whole delete before a single file goes.
func TestSessionChangesDeleteRefusesAPathOutsideTheSet(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n", "keep.txt": "same\n", ".gitignore": "*.log\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	writeInWorkspace(t, e.cwd, "debug.log", "noise\n")

	for _, paths := range []string{
		`{"paths":["keep.txt"]}`,
		`{"paths":["debug.log"]}`,
		`{"paths":["../escape.txt"]}`,
		`{"paths":["missing.txt"]}`,
		`{"paths":["a.txt","keep.txt"]}`,
	} {
		rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", paths)
		if rec.Code != http.StatusConflict {
			t.Fatalf("body %s: want 409, got %d: %s", paths, rec.Code, rec.Body.String())
		}
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused request touched a.txt: %q", got)
	}
	if got := readInWorkspace(t, e.cwd, "debug.log"); got != "noise\n" {
		t.Fatalf("a refused request touched debug.log: %q", got)
	}
}

func TestSessionChangesDeleteOutsideARepo(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.txt", "a\n")
	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", `{"paths":["a.txt"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "a\n" {
		t.Fatalf("a plain folder was touched: %q", got)
	}
}

// Every window showing the session reads its working copy again after a
// delete, the same as after a discard.
func TestSessionChangesDeleteAnnouncesTheChange(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")
	frames, unsubscribe := e.srv.events.subscribe()
	defer unsubscribe()

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", `{"paths":["a.txt"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	waitForSessionChanges(t, frames, e.id)
}

// Removing files under an agent that is mid-edit would leave the folder in a
// state neither side expects, so a delete waits for the turn to end.
func TestSessionChangesDeleteRefusedWhileATurnRuns(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	e := newChangesEnvWithRunner(t, func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		close(started)
		<-release
		return string(acp.StopReasonEndTurn), nil
	})
	e.commitWorkspace(t, map[string]string{"a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses",
			strings.NewReader(`{"model":"agent","input":"work","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", e.id)
		e.srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	defer func() {
		close(release)
		<-done
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never started")
	}

	rec := e.doBody(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/delete", `{"paths":["a.txt"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 while a turn runs, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readInWorkspace(t, e.cwd, "a.txt"); got != "new\n" {
		t.Fatalf("a refused delete touched a.txt: %q", got)
	}
}

// A file row carries a digest of its status and both raw sides: identical
// content hashes the same on every read, and a rewrite that keeps the line
// counts still moves it.
func TestSessionChangesFileDigest(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "one\ntwo\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "one\nTWO\n")

	first := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	digest, _ := first["a.txt"]["digest"].(string)
	if len(digest) != 64 {
		t.Fatalf("digest must be a SHA-256 hex fingerprint, got %q", digest)
	}
	second := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	if second["a.txt"]["digest"] != digest {
		t.Fatalf("identical content changed the digest: %q then %q", digest, second["a.txt"]["digest"])
	}
	// Same counts, other bytes: the digest still moves.
	writeInWorkspace(t, e.cwd, "a.txt", "one\nTRE\n")
	third := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	if third["a.txt"]["additions"].(float64) != 1 || third["a.txt"]["deletions"].(float64) != 1 {
		t.Fatalf("the rewrite kept the counts, fixture broken: %v", third["a.txt"])
	}
	if third["a.txt"]["digest"] == digest {
		t.Fatalf("a same-count rewrite must move the digest: %q", digest)
	}
}

// Every row says whether the file is a raster image and how long each raw side
// is, so the viewer can pick its rendering without fetching content. An absent
// side is zero; the sizes are the bytes as they are, line endings included.
func TestSessionChangesFileImageAndSizes(t *testing.T) {
	png := string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02})
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"pic.png": png, "a.txt": "alpha\nbeta\n"})
	writeInWorkspace(t, e.cwd, "pic.png", png+"x")
	writeInWorkspace(t, e.cwd, "a.txt", "alpha\r\nbeta\r\ngamma\r\n")

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	pic := files["pic.png"]
	if pic["image"] != true {
		t.Fatalf("a PNG must be marked as an image: %v", pic)
	}
	if pic["beforeSize"].(float64) != float64(len(png)) || pic["afterSize"].(float64) != float64(len(png)+1) {
		t.Fatalf("sizes must be exact raw lengths, got %v", pic)
	}
	txt := files["a.txt"]
	if txt["image"] != false {
		t.Fatalf("a text file is not an image: %v", txt)
	}
	// The raw after side is 20 bytes: the CRLFs were not levelled before sizing.
	if txt["beforeSize"].(float64) != 11 || txt["afterSize"].(float64) != 20 {
		t.Fatalf("sizes must count raw bytes, got %v", txt)
	}
}

// raw=1 serves the exact bytes of one image side of the file, so the viewer
// can render it. The missing side of an added file is 404, a raw request for
// text is 400, and the JSON answer keeps its shape either way.
func TestSessionChangeFileRawSides(t *testing.T) {
	pngBefore := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'o', 'l', 'd'}
	pngAfter := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'n', 'e', 'w', '!'}
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"pic.png": string(pngBefore), "a.txt": "old\n"})
	writeInWorkspace(t, e.cwd, "pic.png", string(pngAfter))
	writeInWorkspace(t, e.cwd, "fresh.png", string(pngAfter))
	writeInWorkspace(t, e.cwd, "a.txt", "new\n")

	for side, want := range map[string][]byte{"before": pngBefore, "after": pngAfter} {
		rec := e.do(t, http.MethodGet,
			"/coddy/sessions/"+e.id+"/changes/file?path=pic.png&side="+side+"&raw=1")
		if rec.Code != http.StatusOK {
			t.Fatalf("side %s: status %d: %s", side, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Fatalf("side %s: Content-Type %q, want image/png", side, ct)
		}
		if !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatalf("side %s: bytes %x, want %x", side, rec.Body.Bytes(), want)
		}
	}
	// The default side is before.
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=pic.png&raw=1")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngBefore) {
		t.Fatalf("default side must be before: %d %x", rec.Code, rec.Body.Bytes())
	}
	// An added file has no before side.
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=fresh.png&side=before&raw=1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing side must be 404, got %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=fresh.png&side=after&raw=1")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pngAfter) {
		t.Fatalf("the after side of an added image: %d %x", rec.Code, rec.Body.Bytes())
	}
	// Text is not served raw.
	for _, target := range []string{
		"changes/file?path=a.txt&side=before&raw=1",
		"changes/file?path=a.txt&side=after&raw=1",
		"changes/file?path=pic.png&side=sideways&raw=1",
	} {
		rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/"+target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d: %s", target, rec.Code, rec.Body.String())
		}
	}
	// Without raw=1 the answer is still the JSON document, metadata included -
	// and side means nothing there, so even a bad one is simply ignored.
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=pic.png&side=after")
	body := decodeJSON(t, rec)
	if body["object"] != "coddy.session_change" || body["image"] != true || body["digest"] == "" {
		t.Fatalf("ordinary response: %v", body)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=pic.png&side=sideways")
	if rec.Code != http.StatusOK {
		t.Fatalf("a side value only matters for raw=1: %d %s", rec.Code, rec.Body.String())
	}
	// An unknown path is 404 whether the side is valid or not.
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=nope.png&side=sideways&raw=1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown path must stay 404 even for raw: %d", rec.Code)
	}
}

func TestSessionChangeFileDeletedTextAndBinary(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{
		"gone.txt": "last line\n",
		"gone.bin": "\x00\x01old\x00",
	})
	for _, path := range []string{"gone.txt", "gone.bin"} {
		if err := os.Remove(filepath.Join(e.cwd, path)); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		path      string
		before    string
		binary    bool
		deletions float64
	}{
		{path: "gone.txt", before: "last line\n", deletions: 1},
		{path: "gone.bin", before: "\x00\x01old\x00", binary: true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path="+tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			body := decodeJSON(t, rec)
			if body["status"] != "deleted" || body["binary"] != tc.binary || body["image"] != false {
				t.Fatalf("deleted metadata: %v", body)
			}
			if body["after"] != "" {
				t.Fatalf("deleted after side: %v", body)
			}
			wantBefore := tc.before
			if tc.binary {
				wantBefore = ""
			}
			if body["before"] != wantBefore {
				t.Fatalf("deleted before side: %v", body)
			}
			if body["beforeSize"] != float64(len(tc.before)) || body["afterSize"] != float64(0) {
				t.Fatalf("deleted sizes: %v", body)
			}
			if body["additions"] != float64(0) || body["deletions"] != tc.deletions {
				t.Fatalf("deleted counts: %v", body)
			}
			patch, _ := body["patch"].(string)
			if tc.binary && patch != "" {
				t.Fatalf("binary deletion has a text patch: %q", patch)
			}
			if !tc.binary && !strings.Contains(patch, "-last line") {
				t.Fatalf("text deletion patch: %q", patch)
			}
			for _, side := range []string{"before", "after"} {
				rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path="+tc.path+"&side="+side+"&raw=1")
				want := http.StatusBadRequest
				if side == "after" {
					want = http.StatusNotFound
				}
				if rec.Code != want {
					t.Fatalf("raw %s: want %d, got %d: %s", side, want, rec.Code, rec.Body.String())
				}
			}
		})
	}
}

// A deleted image keeps a before side and no after side: the raw route serves
// the bytes HEAD held and answers 404 for the side the file no longer has.
func TestSessionChangeFileRawDeletedImage(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'g', 'o', 'n', 'e'}
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"gone.png": string(png)})
	if err := os.Remove(filepath.Join(e.cwd, "gone.png")); err != nil {
		t.Fatal(err)
	}

	detailRec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gone.png")
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", detailRec.Code, detailRec.Body.String())
	}
	detail := decodeJSON(t, detailRec)
	if detail["status"] != "deleted" || detail["binary"] != true || detail["image"] != true ||
		detail["beforeImage"] != true || detail["afterImage"] != false ||
		detail["beforeSize"] != float64(len(png)) || detail["afterSize"] != float64(0) ||
		detail["patch"] != "" {
		t.Fatalf("deleted image detail: %v", detail)
	}

	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gone.png&side=before&raw=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("bytes %x, want %x", rec.Body.Bytes(), png)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gone.png&side=after&raw=1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("the after side of a deleted file must be 404, got %d", rec.Code)
	}
}

// The raw answer is content, and content that might not be what its name or
// its first bytes suggest is served under the same restrictions the workspace
// routes use: a file that leads with a PNG signature and continues as HTML is
// image/png to the route, and it is the headers - nosniff, the sandboxed CSP
// the file routes use, no-store - that stop the browser from rendering it.
func TestSessionChangeFileRawHeaders(t *testing.T) {
	crafted := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		[]byte("<html><body><script>alert(1)</script></body></html>")...)
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"pic.png": string(crafted)})
	writeInWorkspace(t, e.cwd, "pic.png", string(append(crafted, 'x')))
	writeInWorkspace(t, e.cwd, "p.htm", "<html><body>x</body></html>")

	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=pic.png&side=after&raw=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if ct := h.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if cl := h.Get("Content-Length"); cl != strconv.Itoa(len(crafted)+1) {
		t.Fatalf("Content-Length = %q, want %d", cl, len(crafted)+1)
	}
	if v := h.Get("X-Content-Type-Options"); v != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if v := h.Get("Content-Security-Policy"); v != "sandbox; default-src 'none'" {
		t.Fatalf("Content-Security-Policy = %q", v)
	}
	if v := h.Get("Cache-Control"); v != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", v)
	}
	// Markup is never an image, whatever the request asks.
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=p.htm&side=after&raw=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an HTML page must not be served as an image: %d", rec.Code)
	}
}

// The row marks each side for what it is: a file that was text and is now a
// PNG has beforeImage=false and afterImage=true, and the other way round, and
// raw=1 is validated against the side it serves - a text side is 400 even
// when the row is an image row.
func TestSessionChangesPerSideImages(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'p', 'i', 'x'}
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{
		"became.png": "was text\n",
		"gone.txt":   string(png),
	})
	writeInWorkspace(t, e.cwd, "became.png", string(png))
	writeInWorkspace(t, e.cwd, "gone.txt", "now text\n")

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	became := files["became.png"]
	if became["image"] != true || became["beforeImage"] != false || became["afterImage"] != true {
		t.Fatalf("text to image: %v", became)
	}
	gone := files["gone.txt"]
	if gone["image"] != true || gone["beforeImage"] != true || gone["afterImage"] != false {
		t.Fatalf("image to text: %v", gone)
	}
	// raw=1 follows the side, not the row: the text side of an image row is 400.
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=became.png&side=before&raw=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the text before side must not be served as an image: %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=became.png&side=after&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("the image after side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gone.txt&side=before&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("the image before side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gone.txt&side=after&raw=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the text after side must not be served as an image: %d", rec.Code)
	}
}

// A BMP is told by its header's structure, not by two letters: "BM" followed
// by prose is text, while a file whose size field, reserved word, offset and
// DIB header all agree is image/bmp.
func TestSessionChangesBmpDetection(t *testing.T) {
	bmp := make([]byte, 58) // 14-byte file header + 40-byte DIB + 4 bytes of pixels
	bmp[0], bmp[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(bmp[2:6], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:14], 54)
	binary.LittleEndian.PutUint32(bmp[14:18], 40)
	binary.LittleEndian.PutUint32(bmp[18:22], 1)  // width
	binary.LittleEndian.PutUint32(bmp[22:26], 1)  // height
	binary.LittleEndian.PutUint16(bmp[26:28], 1)  // planes
	binary.LittleEndian.PutUint16(bmp[28:30], 24) // bpp

	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "a\n"})
	writeInWorkspace(t, e.cwd, "bm.txt", "BM is just a text file that starts with two letters\n")
	writeInWorkspace(t, e.cwd, "real.bmp", string(bmp))

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	if files["bm.txt"]["image"] != false {
		t.Fatalf("BM-prefixed text is not an image: %v", files["bm.txt"])
	}
	if files["real.bmp"]["image"] != true {
		t.Fatalf("a structurally valid BMP is an image: %v", files["real.bmp"])
	}
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=bm.txt&side=after&raw=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("BM-prefixed text must not be served as an image: %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=real.bmp&side=after&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/bmp" {
		t.Fatalf("the BMP side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), bmp) {
		t.Fatalf("bytes %x, want %x", rec.Body.Bytes(), bmp)
	}
}

// A GIF is told by its whole header, not by six letters: the signature opens
// the file, but a real one goes on with a block marker where text keeps a
// word, so prose that only begins "GIF89a" is not an image while a tiny but
// well-formed GIF is.
func TestSessionChangesGifDetection(t *testing.T) {
	// Header + logical screen descriptor + image descriptor introducer: all
	// ASCII, no NUL byte anywhere.
	gif := []byte("GIF89a0000000,000000000;")
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "a\n"})
	writeInWorkspace(t, e.cwd, "gif.txt", "GIF89a is just prose that happens to open like a GIF\n")
	writeInWorkspace(t, e.cwd, "real.gif", string(gif))

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes"))
	if files["gif.txt"]["image"] != false {
		t.Fatalf("GIF-prefixed prose is not an image: %v", files["gif.txt"])
	}
	if files["real.gif"]["image"] != true {
		t.Fatalf("a structurally valid GIF is an image: %v", files["real.gif"])
	}
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=gif.txt&side=after&raw=1")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GIF-prefixed prose must not be served as an image: %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=real.gif&side=after&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("the GIF side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !bytes.Equal(rec.Body.Bytes(), gif) {
		t.Fatalf("bytes %x, want %x", rec.Body.Bytes(), gif)
	}
}

// An image row is binary even when its bytes happen to read as text: a GIF or
// a WebP can be all ASCII and hold no NUL, and a line diff of it would still
// be noise. The row then carries no line counts and no patch.
func TestSessionChangesAsciiImagesReadAsBinary(t *testing.T) {
	gifBefore := []byte("GIF89a0000000,000000000;")
	gifAfter := []byte("GIF89a0000000,000000000;more")
	webpBefore := []byte("RIFF0000WEBP")
	webpAfter := []byte("RIFF0000WEBPmore")
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{
		"a.gif":  string(gifBefore),
		"a.webp": string(webpBefore),
	})
	writeInWorkspace(t, e.cwd, "a.gif", string(gifAfter))
	writeInWorkspace(t, e.cwd, "a.webp", string(webpAfter))

	files := changedFiles(t, e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes?include=patch"))
	for _, name := range []string{"a.gif", "a.webp"} {
		row := files[name]
		if row == nil {
			t.Fatalf("%s not reported: %v", name, files)
		}
		if row["binary"] != true || row["image"] != true {
			t.Fatalf("%s must read as a binary image: %v", name, row)
		}
		if row["additions"].(float64) != 0 || row["deletions"].(float64) != 0 {
			t.Fatalf("%s must carry no line counts: %v", name, row)
		}
		if patch, _ := row["patch"].(string); patch != "" {
			t.Fatalf("%s must carry no patch: %q", name, patch)
		}
	}
	// And the raw bytes still come back for rendering.
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=a.gif&side=after&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("the GIF side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=a.webp&side=after&raw=1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("the WebP side: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// The JSON document still shows text the way the diff does - LF endings - but
// the digest and the sizes describe the bytes as they stand, so a rewrite
// that only adds CRs moves the fingerprint without changing a line.
func TestSessionChangeFileCrlfTextAndRawDigest(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{"a.txt": "one\ntwo\n"})
	writeInWorkspace(t, e.cwd, "a.txt", "one\r\nTWO\r\n")

	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=a.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if after, _ := body["after"].(string); strings.Contains(after, "\r") {
		t.Fatalf("the text side must be levelled for the diff: %q", after)
	}
	if body["afterSize"].(float64) != 10 {
		t.Fatalf("afterSize must count raw bytes, got %v", body["afterSize"])
	}
	// The digest is over the raw sides: recompute it here the way the route
	// documents it and require equality.
	h := sha256.New()
	h.Write([]byte("modified"))
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], 8)
	h.Write(lenBuf[:])
	h.Write([]byte("one\ntwo\n"))
	binary.BigEndian.PutUint64(lenBuf[:], 10)
	h.Write(lenBuf[:])
	h.Write([]byte("one\r\nTWO\r\n"))
	if body["digest"] != hex.EncodeToString(h.Sum(nil)) {
		t.Fatalf("digest must cover the raw sides, got %v", body["digest"])
	}
}
