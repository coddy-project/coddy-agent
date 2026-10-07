//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:     config.Agent{Model: "p1/gpt-4o"},
	}
	root := t.TempDir()
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
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
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
