//go:build memory

package memstorage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The two roots are reached differently.
//
// The global root is the operator's: it lives where memory.dir says, and a
// link inside it is followed, so a folder of notes can live in a synced
// folder elsewhere.
//
// The project root, .coddy/memory of the session's folder, comes with the
// workspace, a checkout anyone may have written. It is reached through
// os.Root, so no read, search, listing, write or delete leaves it: a link
// inside it is followed only while its target stays inside, and .coddy or
// .coddy/memory being a link is refused outright. Without that, a clone could
// carry a note that is a link to ~/.aws/credentials, and recall would put the
// file in the model's context, or a folder that is a link to another project,
// and a save would write into it.

// Errors the store answers with, so a caller (the HTTP routes) can tell them
// apart from a failure of the disk.
var (
	// ErrInvalidPath refuses a path that would leave its root by its own
	// spelling: "..", an absolute path, a volume name.
	ErrInvalidPath = errors.New("invalid path: it must stay inside the memory root")
	// ErrNotNote refuses a file that is not a .md or .txt note.
	ErrNotNote = errors.New("memory file must end with .md or .txt")
	// ErrNotDir answers a listing of something that is not a folder.
	ErrNotDir = errors.New("not a directory")
	// ErrScopeRoot refuses deleting a root itself.
	ErrScopeRoot = errors.New("cannot delete scope root")
	// ErrOutsideRoot refuses a path of the project root that a link inside
	// it would lead out of.
	ErrOutsideRoot = errors.New("the path leads out of the project's memory folder through a link")
)

// rootFS is one memory root opened for an operation. Names are slash paths
// relative to the root, "." for the root itself, as cleanRel returns them.
// *os.Root is one; dirRoot is the other.
type rootFS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldname, newname string) error
	FS() fs.FS
	Close() error
}

// dirRoot is the global root: plain paths under dir, links followed.
type dirRoot struct{ dir string }

func (d dirRoot) path(name string) string { return filepath.Join(d.dir, filepath.FromSlash(name)) }

func (d dirRoot) ReadFile(name string) ([]byte, error) { return os.ReadFile(d.path(name)) }

func (d dirRoot) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(d.path(name), data, perm)
}

func (d dirRoot) MkdirAll(name string, perm fs.FileMode) error {
	return os.MkdirAll(d.path(name), perm)
}

func (d dirRoot) Stat(name string) (fs.FileInfo, error)  { return os.Stat(d.path(name)) }
func (d dirRoot) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(d.path(name)) }
func (d dirRoot) Remove(name string) error               { return os.Remove(d.path(name)) }
func (d dirRoot) RemoveAll(name string) error            { return os.RemoveAll(d.path(name)) }

func (d dirRoot) Rename(oldname, newname string) error {
	return os.Rename(d.path(oldname), d.path(newname))
}

func (d dirRoot) FS() fs.FS    { return os.DirFS(d.dir) }
func (d dirRoot) Close() error { return nil }

// projectRoot is the project root as an os.Root, its refusals of a link out
// of it answered as ErrOutsideRoot.
type projectRoot struct{ *os.Root }

// outside turns os.Root's refusal of a path that leaves it into
// ErrOutsideRoot. os keeps that error to itself; its text is the same in every
// release since os.Root came, and the store's tests hold it.
func outside(err error) error {
	if err != nil && strings.Contains(err.Error(), "path escapes from parent") {
		return fmt.Errorf("%w: %v", ErrOutsideRoot, err)
	}
	return err
}

func (r projectRoot) ReadFile(name string) ([]byte, error) {
	b, err := r.Root.ReadFile(name)
	return b, outside(err)
}

func (r projectRoot) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return outside(r.Root.WriteFile(name, data, perm))
}

func (r projectRoot) MkdirAll(name string, perm fs.FileMode) error {
	return outside(r.Root.MkdirAll(name, perm))
}

func (r projectRoot) Stat(name string) (fs.FileInfo, error) {
	info, err := r.Root.Stat(name)
	return info, outside(err)
}

func (r projectRoot) Lstat(name string) (fs.FileInfo, error) {
	info, err := r.Root.Lstat(name)
	return info, outside(err)
}

func (r projectRoot) Remove(name string) error    { return outside(r.Root.Remove(name)) }
func (r projectRoot) RemoveAll(name string) error { return outside(r.Root.RemoveAll(name)) }

func (r projectRoot) Rename(oldname, newname string) error {
	return outside(r.Root.Rename(oldname, newname))
}

// cleanRel turns the part of a memory path after its scope into a name
// relative to the root: slash-separated, cleaned, "." for the root. A name
// that would leave the root by its spelling is ErrInvalidPath; a link that
// would is the project root's os.Root to refuse.
func cleanRel(rest string) (string, error) {
	p := strings.TrimSpace(filepath.ToSlash(rest))
	if p == "" {
		return ".", nil
	}
	if filepath.VolumeName(rest) != "" || path.IsAbs(p) {
		return "", ErrInvalidPath
	}
	p = path.Clean(p)
	if p == "." {
		return ".", nil
	}
	if !filepath.IsLocal(filepath.FromSlash(p)) {
		return "", ErrInvalidPath
	}
	return p, nil
}

// openRoot opens the root of label for one operation; create makes the root
// first when it does not exist yet (a write), otherwise a missing root
// answers fs.ErrNotExist from the operation itself.
func (s *Store) openRoot(label string, create bool) (rootFS, error) {
	switch label {
	case "global":
		if create {
			if err := s.ensureDir(s.globalRoot); err != nil {
				return nil, err
			}
		}
		return dirRoot{dir: s.globalRoot}, nil
	case "project":
		return openProjectRoot(s.projectRoot, create)
	default:
		return nil, fmt.Errorf("unknown scope %q", label)
	}
}

// openProjectRoot opens root, the project's .coddy/memory, as an os.Root from
// the workspace that holds it, one folder at a time, so that neither it nor
// the folder holding it is a link.
func openProjectRoot(root string, create bool) (rootFS, error) {
	holder := filepath.Dir(root)
	holderName, rootName := filepath.Base(holder), filepath.Base(root)
	ws, err := os.OpenRoot(filepath.Dir(holder))
	if err != nil {
		return nil, err
	}
	defer func() { _ = ws.Close() }()
	h, err := openRealDir(ws, holderName, holderName, create)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.Close() }()
	mem, err := openRealDir(h, rootName, holderName+"/"+rootName, create)
	if err != nil {
		return nil, err
	}
	return projectRoot{Root: mem}, nil
}

// beforeOpenRealDir, set by tests only, runs between openRealDir's check of a
// folder and its open, the window a link swapped in would use.
var beforeOpenRealDir func(display string)

// openRealDir opens name, one element under parent, as a root of its own,
// creating it first when create is set and it does not exist. A link is
// ErrProjectRootLink and anything but a folder ErrNotDir, shown as display.
// os.Root would follow a link that stays inside parent, so the folder opened
// is held to the one Lstat saw: a link swapped in between is refused too.
func openRealDir(parent *os.Root, name, display string, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if create && errors.Is(err, fs.ErrNotExist) {
		if err := parent.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, outside(err)
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, outside(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrProjectRootLink
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project memory: %s is not a folder: %w", display, ErrNotDir)
	}
	if beforeOpenRealDir != nil {
		beforeOpenRealDir(display)
	}
	dir, err := parent.OpenRoot(name)
	if err != nil {
		return nil, outside(err)
	}
	opened, err := dir.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = ErrProjectRootLink
	}
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	return dir, nil
}

// collectFiles lists the notes under a root as slash paths relative to it.
// Folders that are links are not descended into, as before; a note that is
// a link is listed, and reading it is the root's to allow.
func collectFiles(fsys fs.FS) []string {
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isMemoryFile(d.Name()) {
			out = append(out, p)
		}
		return nil
	})
	return out
}
