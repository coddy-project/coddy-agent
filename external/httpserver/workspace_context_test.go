//go:build http

package httpserver

// Unit coverage for the folder picker's volume level. The Windows-only half of
// the story (a drive root really has no parent) is exercised here with an
// injected drive list, so the behaviour is checked on every OS; the real
// enumeration lives in internal/platform and is tested on a Windows runner.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDrivesListingPayload(t *testing.T) {
	got := drivesListingPayload([]string{`C:\`, `D:\`})
	if got["path"] != workspaceDrivesPath || got["parent"] != workspaceDrivesPath {
		t.Errorf("drive level should have no level above it, got path=%v parent=%v", got["path"], got["parent"])
	}
	if got["drives"] != true {
		t.Errorf("drives flag = %v, want true", got["drives"])
	}
	folders, _ := got["folders"].([]map[string]string)
	if len(folders) != 2 {
		t.Fatalf("folders = %v, want 2 rows", folders)
	}
	if folders[0]["name"] != "C:" || folders[0]["path"] != `C:\` {
		t.Errorf("first row = %v, want name C: pointing at C:\\", folders[0])
	}
}

func TestFolderListingPayloadPromotesRootParentToDrives(t *testing.T) {
	root := string(filepath.Separator)
	withDrives := folderListingPayload(root, nil, []string{`X:\`})
	if withDrives["parent"] != workspaceDrivesPath {
		t.Errorf("parent of %q = %v, want the drive level %q", root, withDrives["parent"], workspaceDrivesPath)
	}
	// Without drives (Linux, macOS) the root keeps pointing at itself, so the
	// picker still hides its ".." row.
	noDrives := folderListingPayload(root, nil, nil)
	if noDrives["parent"] != root {
		t.Errorf("parent of %q = %v, want %q when the host has no drives", root, noDrives["parent"], root)
	}
}

func TestFolderListingPayloadKeepsOrdinaryParent(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "srv", "repos", "coddy")
	got := folderListingPayload(dir, nil, []string{`X:\`})
	if want := filepath.Dir(dir); got["parent"] != want {
		t.Errorf("parent of %q = %v, want %q", dir, got["parent"], want)
	}
}

// newFoldersTestServer builds a minimal server exposing only what the folder
// picker needs, with the drive list under test control.
func newFoldersTestServer(t *testing.T, drives []string) *httptest.Server {
	t.Helper()
	srv := &Server{drives: func() []string { return drives }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coddy/workspace/folders", srv.coddyWorkspaceFoldersGet)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func getFoldersJSON(t *testing.T, ts *httptest.Server, path string) (int, map[string]interface{}) {
	t.Helper()
	res, err := http.Get(ts.URL + "/coddy/workspace/folders?path=" + path)
	if err != nil {
		t.Fatalf("GET folders: %v", err)
	}
	defer res.Body.Close()
	var body map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body
}

func TestWorkspaceFoldersDriveLevel(t *testing.T) {
	ts := newFoldersTestServer(t, []string{`X:\`, `Y:\`})
	status, body := getFoldersJSON(t, ts, "%3Adrives%3A")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	if body["drives"] != true || body["path"] != workspaceDrivesPath {
		t.Fatalf("body = %v, want the drive level", body)
	}
	rows, _ := body["folders"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("folders = %v, want 2 drives", rows)
	}
}

func TestWorkspaceFoldersDriveSentinelIsAPlainPathWithoutDrives(t *testing.T) {
	// On a host with no drives the sentinel is just an unknown folder.
	ts := newFoldersTestServer(t, nil)
	status, _ := getFoldersJSON(t, ts, "%3Adrives%3A")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without drives", status)
	}
}

func TestWorkspaceFoldersDriveRootOffersDriveLevelAsParent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ts := newFoldersTestServer(t, []string{`X:\`})

	// A regular folder keeps its real parent...
	status, body := getFoldersJSON(t, ts, dir)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	if body["parent"] != filepath.Dir(dir) {
		t.Errorf("parent = %v, want %q", body["parent"], filepath.Dir(dir))
	}
	if body["drives"] != nil {
		t.Errorf("ordinary listing should not carry the drives flag, got %v", body["drives"])
	}

	// ...while the root of the volume points at the drive level instead of itself.
	root := filepath.VolumeName(dir) + string(filepath.Separator)
	status, body = getFoldersJSON(t, ts, root)
	if status != http.StatusOK {
		t.Fatalf("status = %d for %q, want 200 (body %v)", status, root, body)
	}
	if body["parent"] != workspaceDrivesPath {
		t.Errorf("parent of %q = %v, want %q", root, body["parent"], workspaceDrivesPath)
	}
}
