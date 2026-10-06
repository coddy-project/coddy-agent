//go:build http && unix

package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWorkspaceViewerRejectsSymlinksAndFIFOs(t *testing.T) {
	e := newChangesEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.cwd, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(e.cwd, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := workspaceRequest(e, "GET", "tree", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"symlink"`) || !strings.Contains(w.Body.String(), `"kind":"special"`) {
		t.Fatalf("link kinds: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"link.txt", "fifo"} {
		for _, route := range []string{"raw", "text"} {
			w := workspaceRequest(e, "GET", route+"?path_rel="+path, nil, nil)
			if w.Code != 404 {
				t.Fatalf("%s %s: %d", route, path, w.Code)
			}
		}
	}
}

// On a POSIX system a backslash is a character of a file name: the detail
// route reads the file git lists under that very name.
func TestSessionChangeFileWithABackslashInItsName(t *testing.T) {
	e := newChangesEnv(t)
	e.commitWorkspace(t, map[string]string{`dir\file.txt`: "old\n", "dir/file.txt": "other old\n"})
	writeInWorkspace(t, e.cwd, `dir\file.txt`, "new\n")
	writeInWorkspace(t, e.cwd, "dir/file.txt", "other new\n")
	rec := e.do(t, http.MethodGet, "/coddy/sessions/"+e.id+"/changes/file?path=dir%5Cfile.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeJSON(t, rec); body["after"] != "new\n" {
		t.Fatalf("read the wrong file: %v", body["after"])
	}
}
