package modelcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CacheFileName is the copy of the catalog kept in the agent home.
const CacheFileName = "models-catalog.json"

// maxDocumentBytes bounds what is read of a catalog download and of the copy
// on disk. models.dev's api.json is about 6 MB; a mirror answering with more
// than this is not serving the catalog.
const maxDocumentBytes = 32 << 20

// errNotAFile reports a cache path that names a pipe, a socket or a device,
// whose read could block or never end.
var errNotAFile = errors.New("is a pipe, a socket or a device, not a file")

// cacheFile is the copy kept on disk: the catalog in the compact form a
// suggestion needs, and what is needed to revalidate it.
type cacheFile struct {
	// URL is the address the copy was downloaded from: a copy of another
	// address is not the catalog the configuration names.
	URL       string    `json:"url"`
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Providers Index     `json:"providers"`
}

// specialFile reports whether mode names a file whose read may block or never
// end. It lists what to refuse rather than what to read, since Windows
// reports a OneDrive placeholder as irregular rather than regular.
func specialFile(mode fs.FileMode) bool {
	return mode&(fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice|fs.ModeCharDevice) != 0
}

// readCache reads the copy at path.
func readCache(path string) (*cacheFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() || specialFile(info.Mode()) {
		return nil, fmt.Errorf("%s %w", path, errNotAFile)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDocumentBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxDocumentBytes)
	}
	var c cacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.URL == "" || len(c.Providers) == 0 {
		return nil, fmt.Errorf("%s holds no catalog", path)
	}
	return &c, nil
}

// writeCache replaces the copy at path in one step, so a reader sees the old
// copy or the new one and never half of either.
func writeCache(path string, c *cacheFile) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".models-catalog-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}
