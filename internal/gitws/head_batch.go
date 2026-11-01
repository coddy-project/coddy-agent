package gitws

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// errHeadMissing is the HEAD side of a path that HEAD does not hold, as
// `git show HEAD:./path` fails for it.
var errHeadMissing = errors.New("path is not in HEAD")

// gitBatchHEAD reads the HEAD side of every record that has one with a single
// `git cat-file --batch`, instead of a `git show` per file: the change list
// asks for both sides of each changed file, and the web UI asks for it after
// every tool call. Anything the batch cannot answer exactly as `git show
// HEAD:./path` would - a name holding a newline, an object that is not a blob,
// a batch that fails - falls back to that per-file read.
func gitBatchHEAD(dir string, records []changedRecord) headBlobReader {
	perFile := gitShowHEAD(dir)
	var need []string
	seen := map[string]bool{}
	for _, rec := range records {
		if rec.code == 'A' || rec.code == 'C' || seen[rec.oldPath] || strings.ContainsAny(rec.oldPath, "\n\r") {
			continue
		}
		seen[rec.oldPath] = true
		need = append(need, rec.oldPath)
	}
	if len(need) == 0 {
		return perFile
	}
	// The diff names paths relative to dir; the batch takes them from the top
	// of the repository, as `HEAD:./path` resolves them from dir.
	prefixOut, err := runGitRaw(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return perFile
	}
	prefix := strings.TrimSuffix(string(prefixOut), "\n")
	var in bytes.Buffer
	for _, rel := range need {
		in.WriteString("HEAD:" + prefix + rel + "\n")
	}
	out, err := runGitInput(dir, in.Bytes(), "cat-file", "--batch")
	if err != nil {
		return perFile
	}
	blobs, missing, ok := parseCatFileBatch(out, need)
	if !ok {
		return perFile
	}
	return func(rel string) ([]byte, error) {
		if b, found := blobs[rel]; found {
			return b, nil
		}
		if missing[rel] {
			return nil, errHeadMissing
		}
		return perFile(rel)
	}
}

// parseCatFileBatch reads `git cat-file --batch` output for the requests in
// order. A blob is kept, a missing object is recorded, and anything else is
// left to the per-file read. ok is false when the output does not parse.
func parseCatFileBatch(out []byte, want []string) (blobs map[string][]byte, missing map[string]bool, ok bool) {
	blobs = map[string][]byte{}
	missing = map[string]bool{}
	r := bufio.NewReader(bytes.NewReader(out))
	for _, rel := range want {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, nil, false
		}
		fields := strings.Fields(strings.TrimSuffix(header, "\n"))
		if len(fields) == 2 && (fields[1] == "missing" || fields[1] == "ambiguous") {
			missing[rel] = fields[1] == "missing"
			continue
		}
		if len(fields) != 3 {
			return nil, nil, false
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 {
			return nil, nil, false
		}
		body := make([]byte, size+1)
		if _, err := io.ReadFull(r, body); err != nil || body[size] != '\n' {
			return nil, nil, false
		}
		if fields[1] == "blob" {
			blobs[rel] = body[:size]
		}
	}
	return blobs, missing, true
}

// runGitInput runs git in dir with input on its stdin, under the same rules
// as runGitRaw.
func runGitInput(dir string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = bytes.NewReader(input)
	platform.AdaptCommand(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
