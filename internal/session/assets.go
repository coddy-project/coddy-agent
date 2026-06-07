package session

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// SavePartsToAssets decodes every ImagePart DataURL and writes it to
// <sessionDir>/assets/<name> (read-only, 0o444).  Duplicate base names are
// disambiguated by inserting _1, _2, … before the extension.  The FilePath
// field of each part is populated in-place; parts without a DataURL are left
// unchanged.  When sessionDir is empty the function is a no-op.
func SavePartsToAssets(parts []llm.ImagePart, sessionDir string) error {
	if sessionDir == "" || len(parts) == 0 {
		return nil
	}
	assetsDir := AssetsPath(sessionDir)
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return fmt.Errorf("assets dir: %w", err)
	}
	seen := make(map[string]int, len(parts))
	for i := range parts {
		p := &parts[i]
		if p.DataURL == "" {
			continue
		}
		data, err := decodeDataURLBytes(p.DataURL)
		if err != nil {
			continue // best-effort; leave FilePath empty
		}
		name := uniqueAssetName(assetsDir, p.Name, seen)
		dest := filepath.Join(assetsDir, name)
		if err := writeReadOnly(dest, data); err != nil {
			return fmt.Errorf("write asset %s: %w", name, err)
		}
		p.FilePath = dest
	}
	return nil
}

// decodeDataURLBytes decodes the payload of a data URI.
// Supports base64-encoded and plain (URL-encoded) data URIs.
func decodeDataURLBytes(dataURL string) ([]byte, error) {
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 {
		return []byte(dataURL), nil
	}
	header := dataURL[:comma]
	payload := dataURL[comma+1:]
	if strings.Contains(header, ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	return []byte(payload), nil
}

// uniqueAssetName returns a base name that does not collide with an existing
// file in assetsDir.  seen tracks how many times each base name was requested.
func uniqueAssetName(assetsDir, name string, seen map[string]int) string {
	if name == "" {
		name = "file"
	}
	// Sanitise: strip path separators so the name stays under assetsDir.
	name = filepath.Base(filepath.Clean(name))
	if name == "." || name == "/" {
		name = "file"
	}

	base := name
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	seen[base]++
	if seen[base] == 1 {
		if _, err := os.Stat(filepath.Join(assetsDir, name)); err == nil {
			// File already present from a previous request — use counter.
			seen[base]++
		} else {
			return name
		}
	}
	for n := seen[base] - 1; ; n++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, n, ext)
		if _, err := os.Stat(filepath.Join(assetsDir, candidate)); os.IsNotExist(err) {
			seen[base] = n + 1
			return candidate
		}
	}
}

// writeReadOnly writes data to path atomically and sets permissions to 0o444.
func writeReadOnly(path string, data []byte) error {
	// Write via a temp file in the same directory for atomicity.
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".asset.tmp.")
	if err != nil {
		return err
	}
	tmp := f.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o444); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}
