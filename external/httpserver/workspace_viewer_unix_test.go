//go:build http && unix

package httpserver

import (
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
