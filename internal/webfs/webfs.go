// Package webfs holds the rules Coddy applies whenever it serves a folder of
// the workspace to a browser: the content types a page cannot work without,
// and the dot-named paths (.git, .env, the Coddy store) that are never served.
// The preview_server tool and the canvas both follow them, so a file one of
// them refuses the other refuses too.
package webfs

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// contentTypes pins the types a page cannot work without. Go asks the host for
// the rest, and on Windows the host is the registry, where an installed editor
// routinely maps .js to text/plain - a type browsers refuse for an ES module.
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".htm":   "text/html; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json",
	".map":   "application/json",
	".mjs":   "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".wasm":  "application/wasm",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// ContentType returns the pinned type of a file name's extension, if any.
func ContentType(name string) (string, bool) {
	ct, ok := contentTypes[strings.ToLower(path.Ext(name))]
	return ct, ok
}

// IsHTML reports whether a file name is an HTML page by its extension.
func IsHTML(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return true
	}
	return false
}

// HasDotSegment reports whether any element of the URL path starts with a dot:
// .git, .env, the Coddy store. Project secrets live behind such names, and a
// page under preview has no business with them.
func HasDotSegment(urlPath string) bool {
	// A backslash separates path elements on Windows, where os.Root opens
	// "a\.env" as a/.env.
	for _, seg := range strings.FieldsFunc(urlPath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// HiddenPathElement reports whether a slash-separated path holds an element
// that starts with a dot, where "." alone is just the current directory.
func HiddenPathElement(rel string) bool {
	for _, e := range strings.Split(rel, "/") {
		if len(e) > 1 && strings.HasPrefix(e, ".") {
			return true
		}
	}
	return false
}

// HiddenTarget reports whether the request path resolves, inside the served
// directory, to a dot-named element - the names HasDotSegment refuses on the
// URL itself. A symlink inside the directory can point at one: the file open
// stays confined by os.Root, so an escape still cannot be served; this only
// applies the same dot policy to what the symlink points at. rootDir must be
// resolved already (filepath.EvalSymlinks), or the check quietly skips.
func HiddenTarget(rootDir, urlPath string) bool {
	resolved, err := filepath.EvalSymlinks(filepath.Join(rootDir, strings.TrimPrefix(path.Clean("/"+urlPath), "/")))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// Outside the directory: os.Root refuses the open itself.
		return false
	}
	return HiddenPathElement(filepath.ToSlash(rel))
}

// DotHiddenFS wraps a served filesystem so a directory listing never shows a
// dot-named entry - the same names HasDotSegment refuses on a request path.
type DotHiddenFS struct{ Files fs.FS }

// Open returns the file the wrapped filesystem has, with the listing of a
// directory filtered of dot names. Only directories are wrapped: a regular
// file must keep its Seek for Range requests (a video or a download the
// browser reads in parts), which an interface-typed wrapper would strip.
func (d DotHiddenFS) Open(name string) (fs.File, error) {
	f, err := d.Files.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return f, err
	}
	if dir, ok := f.(fs.ReadDirFile); ok {
		return dotHiddenDir{File: f, dir: dir}, nil
	}
	return f, nil
}

// dotHiddenDir filters dot-named entries out of a directory read.
type dotHiddenDir struct {
	fs.File
	dir fs.ReadDirFile
}

// ReadDir drops the entries whose name starts with a dot. A page that comes
// back all dots is not the end of the directory - the next page is read, so a
// positive count never gets an empty result without an error.
func (d dotHiddenDir) ReadDir(count int) ([]fs.DirEntry, error) {
	for {
		entries, err := d.dir.ReadDir(count)
		if err != nil {
			return entries, err
		}
		kept := entries[:0]
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				kept = append(kept, e)
			}
		}
		if len(kept) > 0 || count <= 0 {
			return kept, nil
		}
	}
}

var cspSourceRe = regexp.MustCompile(`^https?://(\*\.)?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`)

// ValidateCSPSource checks one source a served page may load from: a scheme
// and host (a leading *. allowed), an optional port and path. A bare scheme,
// keywords, wildcards and anything that could end the directive are refused:
// each entry names the hosts it opens.
func ValidateCSPSource(s string) error {
	if !cspSourceRe.MatchString(s) {
		return fmt.Errorf("%q is not a source a page may load from; write https://host or https://*.host", s)
	}
	return nil
}
