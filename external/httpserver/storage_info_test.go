//go:build http

package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newStorageServer is a server over a real manager and a real store in a
// temporary folder, with sessions.min_free_mb set to minFreeMB (nil leaves it
// to the default).
func newStorageServer(t *testing.T, minFreeMB *int) (*httptest.Server, *Server, *session.Manager) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	sessions := filepath.Join(root, "sessions")
	for _, dir := range []string{cwd, sessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: "http://127.0.0.1:1", APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b"}},
		Agent:     config.Agent{Model: "local/qwen3-1.7b"},
		Sessions:  config.Sessions{Dir: sessions, MinFreeMB: minFreeMB},
	}
	log := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, nil, log, cwd, &session.FileStore{Root: sessions})
	srv := New(cfg, mgr, log, cwd)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts, srv, mgr
}

func storageOf(t *testing.T, info map[string]interface{}) map[string]interface{} {
	t.Helper()
	st, _ := info["storage"].(map[string]interface{})
	if st == nil {
		t.Fatalf("GET /coddy/info has no storage object: %v", info)
	}
	return st
}

// GET /coddy/info says how much room the disk that stores the sessions has:
// its state against sessions.min_free_mb, the figures of the disk, and the
// threshold, so a page can warn before a save fails.
func TestCoddyInfoReportsTheRoomOnTheSessionsDisk(t *testing.T) {
	one := 1
	ts, _, _ := newStorageServer(t, &one)
	st := storageOf(t, readInfo(t, ts))
	if st["state"] != "ok" || st["volume"] != "sessions" {
		t.Fatalf("a disk with more than 1 MB free reads as %v", st)
	}
	free, _ := st["freeBytes"].(float64)
	total, _ := st["totalBytes"].(float64)
	if free <= 0 || total < free {
		t.Fatalf("figures free=%v total=%v do not describe a disk", free, total)
	}
	if got, _ := st["minFreeBytes"].(float64); got != 1<<20 {
		t.Fatalf("minFreeBytes = %v, want %d", got, 1<<20)
	}
}

// A threshold above the size of the disk turns the state to low; 0 turns the
// warning off, and the figures stay.
func TestCoddyInfoStateFollowsTheThreshold(t *testing.T) {
	huge := 1 << 30 // MB: a petabyte, more than any disk a test runs on
	ts, _, _ := newStorageServer(t, &huge)
	if st := storageOf(t, readInfo(t, ts)); st["state"] != "low" {
		t.Fatalf("a disk under the threshold reads as %v, want low", st)
	}

	zero := 0
	ts, _, _ = newStorageServer(t, &zero)
	st := storageOf(t, readInfo(t, ts))
	if st["state"] != "ok" || st["minFreeBytes"].(float64) != 0 {
		t.Fatalf("with the warning off the disk reads as %v, want ok with no threshold", st)
	}
	if free, _ := st["freeBytes"].(float64); free <= 0 {
		t.Fatalf("the figures are still reported with the warning off: %v", st)
	}
}

// A manager that keeps no sessions on disk has no disk to speak of, and the
// answer stays what it was before the field existed.
func TestCoddyInfoHasNoStorageWithoutAStore(t *testing.T) {
	ts, _ := newInfoTestServer(t, config.PermModeAsk)
	if _, present := readInfo(t, ts)["storage"]; present {
		t.Fatal("GET /coddy/info reports storage for a manager with no store")
	}
}

// The shape of each state, as the page reads it.
func TestCoddyInfoStorageShapes(t *testing.T) {
	ts, srv, _ := newStorageServer(t, nil)
	cases := []struct {
		name string
		st   session.StorageStatus
		want map[string]interface{}
	}{
		{"low", session.StorageStatus{State: session.StorageLow, Measured: true, Volume: "home", FreeBytes: 300 << 20, TotalBytes: 1 << 40, MinFreeBytes: 512 << 20},
			map[string]interface{}{"state": "low", "volume": "home", "freeBytes": float64(300 << 20), "totalBytes": float64(1 << 40), "minFreeBytes": float64(512 << 20)}},
		{"full, nothing readable", session.StorageStatus{State: session.StorageFull, MinFreeBytes: 512 << 20},
			map[string]interface{}{"state": "full", "minFreeBytes": float64(512 << 20)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv.storageStatus = func() (session.StorageStatus, bool) { return tc.st, true }
			got := storageOf(t, readInfo(t, ts))
			if len(got) != len(tc.want) {
				t.Fatalf("storage = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("storage[%q] = %v, want %v (all: %v)", k, got[k], v, got)
				}
			}
		})
	}
	srv.storageStatus = func() (session.StorageStatus, bool) { return session.StorageStatus{}, false }
	if _, present := readInfo(t, ts)["storage"]; present {
		t.Fatal("storage is reported when there is nothing to report")
	}
}

// readStorageEvent reads frames until the next storage_status one and returns
// its data.
func readStorageEvent(t *testing.T, body *bufio.Reader) map[string]interface{} {
	t.Helper()
	frame := readEventFrames(t, body, "event: storage_status")
	var data string
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		t.Fatalf("storage_status data %q: %v", data, err)
	}
	return ev
}

// A request answered 507 puts the failure on the manager's record, and the
// events stream tells every client at once, without a poll. A client that
// asks afterwards gets the disk as it is: here a disk with room and a store
// that writes, which reads as healthy, and the stream says the failure is over.
func TestAFullDiskAnswerIsAnnouncedOnTheEventsStream(t *testing.T) {
	one := 1
	ts, srv, _ := newStorageServer(t, &one)
	srv.ensureSession = func(context.Context, string, string, string) (*session.State, error) {
		return nil, fmt.Errorf("session/new: layout: %w", &os.PathError{Op: "mkdir", Path: "sessions/sess_x", Err: diskFullErrno})
	}
	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"agent","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507", res.StatusCode)
	}

	ev := readStorageEvent(t, body)
	if ev["object"] != "coddy.storage_status" || ev["writeFailing"] != true || ev["at"] == "" {
		t.Fatalf("event = %v, want a storage_status that says writes are failing", ev)
	}

	// The disk has room and the store writes: asking clears the failure and
	// the stream says so.
	if st := storageOf(t, readInfo(t, ts)); st["state"] != "ok" {
		t.Fatalf("a disk with room reads as %v after the failure", st)
	}
	ev = readStorageEvent(t, body)
	if ev["writeFailing"] != false {
		t.Fatalf("event = %v, want a storage_status that says the failure is over", ev)
	}
}

// The workspace route that creates the session of a new chat answers 507 the
// same way and records the failure the same way.
func TestWorkspaceRouteFullDiskIsRecorded(t *testing.T) {
	one := 1
	ts, srv, _ := newStorageServer(t, &one)
	srv.ensureSession = func(context.Context, string, string, string) (*session.State, error) {
		return nil, fmt.Errorf("session/new: layout: %w", &os.PathError{Op: "mkdir", Path: "sessions/sess_x", Err: diskFullErrno})
	}
	body, closeEvents := subscribeEvents(t, ts, "")
	defer closeEvents()
	readEventFrames(t, body, "event: ready")

	code, _ := postWorkspace(t, ts, session.NewSessionID(), srv.defaultCWD)
	if code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507", code)
	}
	if ev := readStorageEvent(t, body); ev["writeFailing"] != true {
		t.Fatalf("event = %v, want writes failing", ev)
	}
}

// Any other answer says nothing about room.
func TestOtherSessionErrorsAreNotAnnouncedAsAFullDisk(t *testing.T) {
	one := 1
	ts, srv, mgr := newStorageServer(t, &one)
	heard := 0
	defer mgr.AddStorageObserver(func(session.StorageEvent) { heard++ })()
	srv.ensureSession = func(context.Context, string, string, string) (*session.State, error) {
		return nil, fmt.Errorf("boom")
	}
	res, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"agent","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	if heard != 0 {
		t.Fatal("a plain failure was announced as a full disk")
	}
}

// The served spec names the storage object and the event.
func TestOpenAPIDescribesTheInfoStorage(t *testing.T) {
	paths, _ := openAPISpec()["paths"].(map[string]interface{})
	entry, _ := paths["/coddy/info"].(map[string]interface{})
	get, _ := entry["get"].(map[string]interface{})
	responses, _ := get["responses"].(map[string]interface{})
	ok, _ := responses["200"].(map[string]interface{})
	content, _ := ok["content"].(map[string]interface{})
	media, _ := content["application/json"].(map[string]interface{})
	schema, _ := media["schema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	storage, _ := props["storage"].(map[string]interface{})
	if storage == nil {
		t.Fatalf("GET /coddy/info schema has no storage: %#v", props)
	}
	inner, _ := storage["properties"].(map[string]interface{})
	for _, key := range []string{"state", "volume", "freeBytes", "totalBytes", "minFreeBytes"} {
		if inner[key] == nil {
			t.Errorf("storage has no %s property", key)
		}
	}
	state, _ := inner["state"].(map[string]interface{})
	if enum, _ := state["enum"].([]string); len(enum) != 3 || enum[0] != "ok" || enum[1] != "low" || enum[2] != "full" {
		t.Errorf("state enum = %v, want ok, low, full", state["enum"])
	}
	for _, r := range schema["required"].([]string) {
		if r == "storage" {
			t.Error("storage must stay optional: an older server and a store-less manager do not send it")
		}
	}

	events, _ := paths["/coddy/events"].(map[string]interface{})
	evGet, _ := events["get"].(map[string]interface{})
	desc, _ := evGet["description"].(string)
	if !strings.Contains(desc, "storage_status") {
		t.Error("GET /coddy/events does not describe event: storage_status")
	}
}
