package gitws

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorktreeChangesWithNewlinePrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses newlines in paths")
	}
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	// Each newline would start another request in the line-delimited protocol.
	const folder = "decoy\nHEAD:decoy\nsafe"
	dir := commitRepo(t, map[string]string{folder + "/a.txt": "real before\n", "decoy": "wrong before\n"})
	writeFile(t, dir, folder+"/a.txt", "after\n")
	changes, _, err := WorktreeChanges(filepath.Join(dir, folder))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || string(changes[0].Before) != "real before\n" {
		t.Fatalf("before-image from newline prefix: %+v", changes)
	}
}

func TestRunGitInputBoundsOutput(t *testing.T) {
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	dir := commitRepo(t, map[string]string{"large": strings.Repeat("x", 1<<20)})
	_, err := runGitInput(dir, []byte(strings.Repeat("HEAD:large\n", 33)), "cat-file", "--batch")
	if err == nil {
		t.Fatal("accepted batch output over 32 MiB")
	}
}

func TestParseCatFileBatch(t *testing.T) {
	const blob = "0123456789012345678901234567890123456789 blob 3\nold\n"
	for _, tc := range []struct {
		name, out string
		wantOK    bool
	}{
		{"blob", blob, true},
		{"missing", "HEAD:gone missing\n", true},
		{"non-blob", "abc tree 0\n\n", true},
		{"extra response", blob + blob, false},
		{"truncated header", "abc blob 3", false},
		{"truncated body", "abc blob 3\nol", false},
		{"bad separator", "abc blob 3\nold!", false},
		{"negative size", "abc blob -1\n", false},
		{"overflow size", fmt.Sprintf("abc blob %d\n", int(^uint(0)>>1)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("parser panicked: %v", p)
				}
			}()
			blobs, missing, ok := parseCatFileBatch([]byte(tc.out), []string{"a"})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.name == "blob" && string(blobs["a"]) != "old" {
				t.Fatalf("blobs = %q", blobs)
			}
			if tc.name == "missing" && !missing["a"] {
				t.Fatal("missing path not recorded")
			}
		})
	}
}
