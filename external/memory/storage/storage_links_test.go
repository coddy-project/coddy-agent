//go:build memory

package memstorage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// linkStand is a home with a secret and another project in it, and a
// workspace whose .coddy/memory a clone could have brought.
type linkStand struct {
	home, workspace, global, project string
	secret, other                    string
}

func newLinkStand(t *testing.T) linkStand {
	t.Helper()
	s := linkStand{home: t.TempDir(), workspace: t.TempDir()}
	s.global = filepath.Join(s.home, ".coddy", "memory")
	s.project = filepath.Join(s.workspace, ".coddy", "memory")
	for _, d := range []string{s.global, s.project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.secret = filepath.Join(s.home, ".aws", "credentials")
	if err := os.MkdirAll(filepath.Dir(s.secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.secret, []byte("aws_secret_access_key = OUTSIDE-SECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.other = filepath.Join(s.home, "other-project")
	if err := os.MkdirAll(filepath.Join(s.other, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.other, "docs", "keep.md"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s linkStand) store(t *testing.T) *Store {
	t.Helper()
	st, err := NewStore(&config.MemoryConfig{}, config.Paths{Home: filepath.Join(s.home, ".coddy")}, s.workspace)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func noSecretIn(t *testing.T, what string, hits []Hit) {
	t.Helper()
	for _, h := range hits {
		if strings.Contains(h.Snippet, "OUTSIDE-SECRET") {
			t.Fatalf("%s returned the file behind a link out of the project root: %+v", what, h)
		}
	}
}

// A note of the project root that is a link to a file outside it reads as
// nothing: not through read, and not through search, which would put it in
// the model's context without anyone asking for that file.
func TestProjectRootRefusesALinkedFileOutOfIt(t *testing.T) {
	s := newLinkStand(t)
	symlinkOrSkip(t, s.secret, filepath.Join(s.project, "setup.md"))
	st := s.store(t)

	if got, err := st.Read("project:setup.md"); !errors.Is(err, ErrOutsideRoot) || strings.Contains(got, "OUTSIDE-SECRET") {
		t.Fatalf("Read through a linked note = %q, %v; want ErrOutsideRoot", got, err)
	}
	for _, scope := range []string{"project", "both"} {
		hits, _ := st.Search("aws secret access key", scope, 8)
		noSecretIn(t, "Search "+scope, hits)
	}
	// The link itself can be taken away: deleting it removes the link only.
	if err := st.Delete("project:setup.md"); err != nil {
		t.Fatalf("Delete of the link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(s.project, "setup.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link is still there: %v", err)
	}
	if _, err := os.Stat(s.secret); err != nil {
		t.Fatalf("deleting the link removed what it pointed at: %v", err)
	}
}

// The guide at the root of the project memory is a note like the others: when
// it is a link to a file outside the root, reading it is refused, and a search
// neither returns it nor reaches the file.
func TestProjectRootRefusesAGuideThatIsALinkOutOfIt(t *testing.T) {
	s := newLinkStand(t)
	symlinkOrSkip(t, s.secret, filepath.Join(s.project, GuideName))
	st := s.store(t)

	if got, err := st.ReadNote("project", GuideName); !errors.Is(err, ErrOutsideRoot) || strings.Contains(string(got), "OUTSIDE-SECRET") {
		t.Fatalf("ReadNote through a linked guide = %q, %v; want ErrOutsideRoot", got, err)
	}
	for _, scope := range []string{"project", "both"} {
		hits, _ := st.Search("aws secret access key", scope, 8)
		noSecretIn(t, "Search "+scope, hits)
	}
}

// A folder of the project root that is a link to a folder outside it leads
// nowhere: nothing is read, listed, created, written or deleted behind it.
func TestProjectRootRefusesALinkedFolderOutOfIt(t *testing.T) {
	s := newLinkStand(t)
	symlinkOrSkip(t, s.other, filepath.Join(s.project, "shared"))
	st := s.store(t)

	if got, err := st.Read("project:shared/docs/keep.md"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Read through a linked folder = %q, %v; want ErrOutsideRoot", got, err)
	}
	if _, err := st.ListOneLevel("project", "shared/docs"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("ListOneLevel through a linked folder: %v; want ErrOutsideRoot", err)
	}
	if err := st.Mkdir("project", "shared/new"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Mkdir through a linked folder: %v; want ErrOutsideRoot", err)
	}
	if _, err := os.Stat(filepath.Join(s.other, "new")); err == nil {
		t.Fatal("Mkdir created a folder outside the project root")
	}
	if _, err := st.WriteFlexible("project", "x", "shared/AGENTS.md", "planted"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("WriteFlexible through a linked folder: %v; want ErrOutsideRoot", err)
	}
	if _, err := os.Stat(filepath.Join(s.other, "AGENTS.md")); err == nil {
		t.Fatal("a note was written outside the project root")
	}
	if err := st.Delete("project:shared/docs"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Delete through a linked folder: %v; want ErrOutsideRoot", err)
	}
	if _, err := os.Stat(filepath.Join(s.other, "docs", "keep.md")); err != nil {
		t.Fatalf("Delete removed a folder outside the project root: %v", err)
	}
}

// The project root itself must be a real folder, and so must .coddy that holds
// it: a clone whose .coddy/memory, or whose .coddy, is a link to a folder
// outside reaches nothing behind it.
func TestProjectRootRefusesARootThatIsALink(t *testing.T) {
	for name, link := range map[string]func(t *testing.T, s linkStand){
		"memory": func(t *testing.T, s linkStand) {
			_ = os.RemoveAll(s.project)
			symlinkOrSkip(t, filepath.Join(s.other, "docs"), s.project)
		},
		".coddy": func(t *testing.T, s linkStand) {
			_ = os.RemoveAll(filepath.Dir(s.project))
			if err := os.MkdirAll(filepath.Join(s.other, "memory"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.other, "memory", "keep.md"), []byte("keep me\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, s.other, filepath.Dir(s.project))
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newLinkStand(t)
			link(t, s)
			st := s.store(t)
			if got, err := st.Read("project:keep.md"); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("Read = %q, %v; want ErrProjectRootLink", got, err)
			}
			if _, err := st.ListOneLevel("project", ""); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("ListOneLevel: %v; want ErrProjectRootLink", err)
			}
			if _, err := st.WriteFlexible("project", "planted", "", "planted"); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("WriteFlexible: %v; want ErrProjectRootLink", err)
			}
			if err := st.Delete("project:keep.md"); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("Delete: %v; want ErrProjectRootLink", err)
			}
			if _, err := st.Search("keep", "project", 8); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("Search of the project root: %v; want ErrProjectRootLink", err)
			}
			// Searching both roots still answers from the global one.
			if err := os.WriteFile(filepath.Join(s.global, "keep-global.md"), []byte("keep global\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			hits, err := st.Search("keep", "both", 8)
			if err != nil || len(hits) != 1 || hits[0].Scope != "global" {
				t.Fatalf("Search of both roots = %+v, %v; want the global note alone", hits, err)
			}
			if _, err := os.Stat(filepath.Join(s.other, "docs", "keep.md")); err != nil {
				t.Fatalf("something behind the linked root was removed: %v", err)
			}
		})
	}
}

// A project root, or the .coddy holding it, that is a file is not a folder of
// notes: every operation on it says so, and a search of both roots still
// answers from the global one.
func TestProjectRootRefusesARootThatIsAFile(t *testing.T) {
	for name, path := range map[string]func(s linkStand) string{
		"memory": func(s linkStand) string { return s.project },
		".coddy": func(s linkStand) string { return filepath.Dir(s.project) },
	} {
		t.Run(name, func(t *testing.T) {
			s := newLinkStand(t)
			p := path(s)
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("keep\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.global, "keep-global.md"), []byte("keep global\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			st := s.store(t)
			if _, err := st.List("project", ""); !errors.Is(err, ErrNotDir) {
				t.Fatalf("List: %v; want ErrNotDir", err)
			}
			if _, err := st.WriteFlexible("project", "planted", "", "planted"); !errors.Is(err, ErrNotDir) {
				t.Fatalf("WriteFlexible: %v; want ErrNotDir", err)
			}
			if _, err := st.Search("keep", "project", 8); !errors.Is(err, ErrNotDir) {
				t.Fatalf("Search of the project root: %v; want ErrNotDir", err)
			}
			hits, err := st.Search("keep", "both", 8)
			if err != nil || len(hits) != 1 || hits[0].Scope != "global" {
				t.Fatalf("Search of both roots = %+v, %v; want the global note alone", hits, err)
			}
		})
	}
}

// A link swapped in for .coddy or .coddy/memory between the check that it is
// a folder and its open is refused, even when it stays inside the workspace,
// where os.Root alone would follow it.
func TestProjectRootRefusesALinkSwappedInAfterTheCheck(t *testing.T) {
	for _, tc := range []struct {
		name, at string
		swap     func(t *testing.T, s linkStand)
	}{
		{".coddy to another folder of the workspace", ".coddy", func(t *testing.T, s linkStand) {
			holder := filepath.Dir(s.project)
			if err := os.Rename(holder, holder+"-checked"); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, "src", holder)
		}},
		{".coddy/memory to another folder of .coddy", ".coddy/memory", func(t *testing.T, s linkStand) {
			if err := os.Rename(s.project, s.project+"-checked"); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, "other", s.project)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLinkStand(t)
			// The folders the swapped-in links name: src/memory beside
			// .coddy, and other inside it, each with a note of its own.
			for _, d := range []string{
				filepath.Join(s.workspace, "src", "memory"),
				filepath.Join(filepath.Dir(s.project), "other"),
			} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(d, "keep.md"), []byte("swapped\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			beforeOpenRealDir = func(display string) {
				if display == tc.at {
					tc.swap(t, s)
				}
			}
			t.Cleanup(func() { beforeOpenRealDir = nil })
			if got, err := s.store(t).Read("project:keep.md"); !errors.Is(err, ErrProjectRootLink) {
				t.Fatalf("Read = %q, %v; want ErrProjectRootLink", got, err)
			}
		})
	}
}

// A link inside the project root that stays inside it is an ordinary alias.
func TestProjectRootFollowsALinkThatStaysInside(t *testing.T) {
	s := newLinkStand(t)
	if err := os.WriteFile(filepath.Join(s.project, "note.md"), []byte("inside note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, "note.md", filepath.Join(s.project, "alias.md"))
	got, err := s.store(t).Read("project:alias.md")
	if err != nil || strings.TrimSpace(got) != "inside note" {
		t.Fatalf("Read of an inside alias = %q, %v", got, err)
	}
}

// The global root is the operator's own: a note folder may live in a synced
// folder elsewhere, so its links are followed as before.
func TestGlobalRootFollowsLinks(t *testing.T) {
	s := newLinkStand(t)
	symlinkOrSkip(t, filepath.Join(s.other, "docs"), filepath.Join(s.global, "synced"))
	st := s.store(t)
	if got, err := st.Read("global:synced/keep.md"); err != nil || strings.TrimSpace(got) != "keep me" {
		t.Fatalf("Read through a linked global folder = %q, %v", got, err)
	}
	if _, err := st.WriteFlexible("global", "x", "synced/new.md", "written through the link"); err != nil {
		t.Fatalf("WriteFlexible through a linked global folder: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(s.other, "docs", "new.md")); err != nil || !strings.Contains(string(b), "written through the link") {
		t.Fatalf("the note did not land behind the link: %q, %v", b, err)
	}
}
