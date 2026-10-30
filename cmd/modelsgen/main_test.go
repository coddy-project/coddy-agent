package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

const testCatalog = `{"openai":{"models":{"gpt-a":{"limit":{"context":1000,"output":100}},"gpt-b":{"limit":{"context":2000,"output":200}}}}}`

func compact(t *testing.T, api string) []byte {
	t.Helper()
	out, err := models.Compact([]byte(api))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRun(t *testing.T) {
	current := compact(t, testCatalog)
	behind := compact(t, `{"openai":{"models":{"gpt-a":{"limit":{"context":1000,"output":100}}}}}`)
	for _, tc := range []struct {
		name      string
		opts      options
		api       string // empty: the catalog is not there at all
		file      []byte
		wantExit  int
		wantOut   string
		wantErr   string
		wantWrite []byte
	}{
		{
			name: "up to date", opts: options{check: true}, api: testCatalog, file: current,
			wantExit: 0, wantOut: "up to date",
		},
		{
			name: "behind", opts: options{check: true}, api: testCatalog, file: behind,
			wantExit: exitBehind, wantOut: "added: gpt-b", wantErr: "run make models-catalog",
		},
		{
			name: "behind is still behind with unreachable-ok", opts: options{check: true, unreachableOK: true}, api: testCatalog, file: behind,
			wantExit: exitBehind, wantOut: "added: gpt-b",
		},
		{
			name: "refresh writes", api: testCatalog, file: behind,
			wantExit: 0, wantOut: "wrote", wantWrite: current,
		},
		{
			name: "no catalog", opts: options{check: true}, file: behind,
			wantExit: exitFailed,
		},
		{
			name: "no catalog with unreachable-ok", opts: options{check: true, unreachableOK: true}, file: behind,
			wantExit: 0, wantErr: "not checked",
		},
		{
			name: "a page that is no catalog with unreachable-ok", opts: options{check: true, unreachableOK: true}, api: "<html>sign in</html>", file: behind,
			wantExit: 0, wantErr: "not checked",
		},
		{
			name: "a refresh without a catalog fails whatever the flag", opts: options{unreachableOK: true}, file: behind,
			wantExit: exitFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := tc.opts
			opts.source = filepath.Join(dir, "api.json")
			opts.out = filepath.Join(dir, "models.json")
			opts.timeout = time.Minute
			if tc.api != "" {
				writeFile(t, opts.source, tc.api, 0o644)
			}
			writeFile(t, opts.out, string(tc.file), 0o644)

			var stdout, stderr bytes.Buffer
			if got := run(opts, &stdout, &stderr); got != tc.wantExit {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, tc.wantExit, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want %q in it", stdout.String(), tc.wantOut)
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want %q in it", stderr.String(), tc.wantErr)
			}
			want := tc.wantWrite
			if want == nil {
				want = tc.file
			}
			got, err := os.ReadFile(opts.out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("models.json =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// A server that takes the connection and never answers is what a stalled proxy
// looks like: the timeout is what gets a commit past it.
func TestRunStopsAtTheTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = l.Close()
	})
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		<-done
		_ = conn.Close()
	}()

	dir := t.TempDir()
	opts := options{
		check:         true,
		unreachableOK: true,
		source:        "https://" + l.Addr().String() + "/api.json",
		out:           filepath.Join(dir, "models.json"),
		timeout:       200 * time.Millisecond,
	}
	var stdout, stderr bytes.Buffer
	if got := run(opts, &stdout, &stderr); got != 0 {
		t.Errorf("exit = %d, want 0\nstderr: %s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not checked") {
		t.Errorf("stderr = %q, want the skipped check named", stderr.String())
	}
}
