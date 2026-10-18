package canvas

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"strings"
)

// Bounds of the walk a stamp takes: a frame's root is a page and its assets,
// and a root that turns out to be a whole repository is cut short rather than
// walked on every read of the board.
const (
	stampMaxFiles = 2000
	stampMaxDepth = 8
)

// skippedDirs are folders a page's stamp never looks into: dependencies and
// vendored code change on installs, not on edits of the page.
var skippedDirs = map[string]bool{"node_modules": true, "vendor": true}

// Stamp digests the names, sizes and modification times of the files under a
// frame's root. The web UI reloads a frame when its stamp moves, and leaves it
// alone otherwise, so a page someone is clicking through is not reset by an
// edit somewhere else. Dot-named entries (never served) and dependency folders
// are skipped; past stampMaxFiles files the walk stops, so a change beyond
// that bound does not move the stamp.
func Stamp(workspace, root string) string {
	h := sha256.New()
	dir := filepath.Join(workspace, filepath.FromSlash(root))
	count := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skippedDirs[name] || strings.Count(filepath.ToSlash(rel), "/") >= stampMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		count++
		if count > stampMaxFiles {
			return filepath.SkipAll
		}
		var buf [16]byte
		binary.LittleEndian.PutUint64(buf[:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(buf[8:], uint64(info.ModTime().UnixNano()))
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(buf[:])
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:16]
}
