package main

// --add-extension: an embedding that ships the binary together with extension
// ZIPs gets them installed and enabled before any surface serves. The named
// file is the operator's approval of that exact package, so nothing is
// prompted and nothing is ever replaced behind their back.

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
)

// stringList collects a repeatable flag: --add-extension a.zip
// --add-extension b.zip keeps both. The flag package stores one value per
// name, so the list is the value.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// addExtensionFlag registers --add-extension on fs and returns the paths it
// collects. The back-quoted ZIP is the placeholder `serve --help` prints after
// the flag name.
func addExtensionFlag(fs *flag.FlagSet) *stringList {
	var paths stringList
	fs.Var(&paths, "add-extension", "install and enable this extension package `ZIP` before the server serves; repeatable. The named file is the operator's approval of that exact package and is enabled with no prompt: the same id and SHA-256 already installed and enabled is left as it is, the same id with a different digest is skipped with a warning and never replaced, and a missing or invalid file warns and the startup continues (the server runs, exit code 0, even when every file fails)")
	return &paths
}

// absolutePackagePaths resolves package paths against the directory the
// command was typed in, before anything records them: the daemon replays the
// flags of a `coddy serve` from its own directory, and a `coddy serve restart`
// typed in another terminal would not find a package the operator named
// relative to their shell.
func absolutePackagePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			out[i] = abs
			continue
		}
		out[i] = p
	}
	return out
}

// installStartupExtensions installs and enables the packages named with
// --add-extension before any surface serves. Every problem is a warning on w
// and the startup goes on: a package the store refuses must not take down a
// server the operator asked for, so even a run where every file failed serves
// what it has and exits 0.
func installStartupExtensions(store *extensions.Store, paths []string, w io.Writer) {
	for _, p := range paths {
		if err := addExtension(store, p, w); err != nil {
			_, _ = fmt.Fprintf(w, "warning: --add-extension %s: %v\n", p, err)
		}
	}
}

// addExtension installs one package and enables it, and notes on w when that
// changes something. The same id and digest already installed and enabled is
// left exactly as it is; the same id under another digest is never replaced -
// an approval is for those bytes, not for whatever shares the id now.
func addExtension(store *extensions.Store, path string, w io.Writer) error {
	// The operator named this package on the command line; installing it is
	// the point of the flag.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read the package: %w", err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if id := extensionZIPID(data); id != "" {
		existing, err := findInstalled(store, id)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.SHA256 != digest {
				return fmt.Errorf("extension %q is already installed with a different digest (installed %.12s, this file %.12s); an approved package is never replaced - remove it first to install this file", id, existing.SHA256, digest)
			}
			if existing.Enabled {
				return nil
			}
			if _, err := store.SetEnabled(id, digest, true); err != nil {
				return fmt.Errorf("cannot enable %q: %w", id, err)
			}
			_, _ = fmt.Fprintf(w, "--add-extension %s: enabled extension %q\n", path, id)
			return nil
		}
	}
	// Nothing installed answers to this id (or the manifest was not readable
	// here): the store is the authority on what a valid package is, and its
	// refusal is what the warning says.
	entry, err := store.Install(data)
	if err != nil {
		return err
	}
	if _, err := store.SetEnabled(entry.ID, entry.SHA256, true); err != nil {
		return fmt.Errorf("installed %q but cannot enable it: %w", entry.ID, err)
	}
	_, _ = fmt.Fprintf(w, "--add-extension %s: installed and enabled extension %q\n", path, entry.ID)
	return nil
}

// findInstalled is the store's catalog as a lookup by id - a start with
// --add-extension asks about one package at a time - and nil when no installed
// package answers to the id.
func findInstalled(store *extensions.Store, id string) (*extensions.Entry, error) {
	rows, err := store.List()
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// extensionZIPID reads the id out of a package's manifest.json, or "" when the
// bytes carry no readable one. It exists to name an id before installing, so
// an already-installed copy of the same package is found and compared by
// digest; the store stays the authority on whether a package is valid at all.
func extensionZIPID(data []byte) string {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	f, err := z.Open("manifest.json")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	var m extensions.Manifest
	if err := json.NewDecoder(io.LimitReader(f, extensions.MaxArchive)).Decode(&m); err != nil {
		return ""
	}
	return m.ID
}
