//go:build scheduler

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const jobBody = "---\nschedule: \"0 3 * * *\"\n---\nreport\n"

func testRoots(t *testing.T) (Roots, string) {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	home := filepath.Join(root, "home")
	ws := filepath.Join(root, "ws")
	for _, d := range []string{home, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Roots{Home: home, User: filepath.Join(home, "scheduler")}, ws
}

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
}

func TestProjectStateLivesInTheHomeNotTheCheckout(t *testing.T) {
	rt, ws := testRoots(t)
	ref := rt.ProjectRef(ws, "nightly")
	if strings.HasPrefix(ref.StatePath, ws) {
		t.Fatalf("project state %s is inside the workspace", ref.StatePath)
	}
	if !strings.HasPrefix(ref.StatePath, filepath.Join(rt.User, ".projects")) {
		t.Fatalf("project state %s is not under the home", ref.StatePath)
	}
	other := rt.ProjectRef(ws+"2", "nightly")
	if other.StatePath == ref.StatePath || other.Key() == ref.Key() {
		t.Fatal("two workspaces share a job's state or key")
	}
	if rt.UserRef("nightly").Key() == ref.Key() {
		t.Fatal("a user job and a project job share a key")
	}
}

func TestCreateAndReadAProjectJob(t *testing.T) {
	rt, ws := testRoots(t)
	ref := rt.ProjectRef(ws, "nightly")
	if err := rt.CreateJobFile(ref, []byte(jobBody)); err != nil {
		t.Fatal(err)
	}
	if err := rt.CreateJobFile(ref, []byte(jobBody)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("a second create = %v, want ErrExist", err)
	}
	snap, err := ReadSnapshot(ref)
	if err != nil || snap.Err != nil {
		t.Fatalf("read = %v / %v", err, snap.Err)
	}
	if snap.Digest != Digest([]byte(jobBody)) || snap.Body != "report" {
		t.Fatalf("snapshot = %+v", snap)
	}
	refs, err := rt.ListProject(ws)
	if err != nil || len(refs) != 1 || refs[0].ID != "nightly" {
		t.Fatalf("list = %v, %v", refs, err)
	}
}

func TestAProjectFolderInsideTheHomeHasNoJobs(t *testing.T) {
	rt, _ := testRoots(t)
	// A daemon started in $HOME with CODDY_HOME=$HOME/.coddy would read
	// $HOME/.coddy/scheduler as its project folder: the user root itself.
	ws := filepath.Dir(rt.Home)
	rt.Home = filepath.Join(ws, ".coddy")
	rt.User = filepath.Join(rt.Home, "scheduler")
	if err := os.MkdirAll(rt.User, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.User, "nightly.md"), []byte(jobBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CheckProjectDir(ws); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("check = %v, want ErrUnsafePath", err)
	}
	if refs, _ := rt.ListProject(ws); len(refs) != 0 {
		t.Fatalf("the user root was listed as project jobs: %v", refs)
	}
}

func TestLinksInTheProjectFolderAreRefused(t *testing.T) {
	skipWithoutSymlinks(t)
	t.Run("scheduler folder linked to the user root", func(t *testing.T) {
		rt, ws := testRoots(t)
		if err := os.MkdirAll(rt.User, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(ws, ".coddy"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(rt.User, ProjectDir(ws)); err != nil {
			t.Fatal(err)
		}
		err := rt.CreateJobFile(rt.ProjectRef(ws, "evil"), []byte(jobBody))
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("create through a linked folder = %v", err)
		}
		if _, err := os.Stat(filepath.Join(rt.User, "evil.md")); err == nil {
			t.Fatal("a project create landed in the user root")
		}
	})
	t.Run(".coddy linked elsewhere", func(t *testing.T) {
		rt, ws := testRoots(t)
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(ws, ".coddy")); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.CheckProjectDir(ws); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("check = %v", err)
		}
	})
	t.Run("job file linked to an operator file", func(t *testing.T) {
		rt, ws := testRoots(t)
		if err := os.MkdirAll(ProjectDir(ws), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(target, []byte(jobBody), 0o644); err != nil {
			t.Fatal(err)
		}
		ref := rt.ProjectRef(ws, "linked")
		if err := os.Symlink(target, ref.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSnapshot(ref); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("read of a linked job = %v", err)
		}
		if err := rt.ReplaceJobFile(ref, []byte("overwritten")); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(target); string(got) != jobBody {
			t.Fatalf("the replace wrote through the link: %q", got)
		}
	})
	t.Run("dangling job link blocks a create", func(t *testing.T) {
		rt, ws := testRoots(t)
		if err := os.MkdirAll(ProjectDir(ws), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "created-through-link.md")
		ref := rt.ProjectRef(ws, "dangling")
		if err := os.Symlink(target, ref.Path); err != nil {
			t.Fatal(err)
		}
		if err := rt.CreateJobFile(ref, []byte(jobBody)); err == nil {
			t.Fatal("a create followed a dangling link")
		}
		if _, err := os.Stat(target); err == nil {
			t.Fatal("the dangling link's target was created")
		}
	})
}

func TestProjectCWDStaysInTheWorkspace(t *testing.T) {
	_, ws := testRoots(t)
	if got, err := ProjectCWD(ws, ""); err != nil || got != ws {
		t.Fatalf("empty cwd = %q, %v", got, err)
	}
	if got, err := ProjectCWD(ws, "sub/dir"); err != nil || got != filepath.Join(ws, "sub", "dir") {
		t.Fatalf("relative cwd = %q, %v", got, err)
	}
	for _, bad := range []string{"/etc", "../other", "sub/../../other"} {
		if _, err := ProjectCWD(ws, bad); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("cwd %q = %v, want ErrUnsafePath", bad, err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(ws, "out")); err != nil {
			t.Fatal(err)
		}
		if _, err := ProjectCWD(ws, "out"); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("a linked cwd leaving the workspace was accepted: %v", err)
		}
	}
}

// An absolute cwd that names the workspace or a folder inside it, even through
// a link to the workspace, is the same place a relative one names: a job
// written before project jobs existed carries one, and refusing it left the
// job impossible to enable.
func TestProjectCWDAcceptsAnAbsolutePathInsideTheWorkspace(t *testing.T) {
	_, ws := testRoots(t)
	if err := os.MkdirAll(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ProjectCWD(ws, ws); err != nil || got != ws {
		t.Fatalf("cwd = workspace: %q, %v", got, err)
	}
	if got, err := ProjectCWD(ws, filepath.Join(ws, "sub")); err != nil || got != filepath.Join(ws, "sub") {
		t.Fatalf("cwd = workspace/sub: %q, %v", got, err)
	}
	if _, err := ProjectCWD(ws, filepath.Join(ws, "..", "other")); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("an absolute cwd leaving the workspace was accepted: %v", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "ws-link")
		if err := os.Symlink(ws, link); err != nil {
			t.Fatal(err)
		}
		if got, err := ProjectCWD(ws, link); err != nil || got != link {
			t.Fatalf("cwd through a link to the workspace: %q, %v", got, err)
		}
		if _, err := ProjectCWD(link, ws); err != nil {
			t.Fatalf("cwd = real path of a linked workspace: %v", err)
		}
	}
}

func TestASnapshotWithAnEscapingCWDIsInvalid(t *testing.T) {
	rt, ws := testRoots(t)
	ref := rt.ProjectRef(ws, "escape")
	if err := rt.CreateJobFile(ref, []byte("---\nschedule: \"0 3 * * *\"\ncwd: /etc\n---\nx\n")); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadSnapshot(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(snap.Err, ErrUnsafePath) {
		t.Fatalf("snapshot err = %v, want ErrUnsafePath", snap.Err)
	}
}

func TestRegistryKeepsWorkspaces(t *testing.T) {
	home := t.TempDir()
	reg := NewRegistry(home)
	if err := reg.Add("/a"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Add("/b"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Add("/a"); err != nil {
		t.Fatal(err)
	}
	if got := NewRegistry(home).List(); len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("registry = %v", got)
	}
}

// A schedule that does not parse makes the snapshot invalid, so the list, an
// approval and a manual run agree with the tick, which never fires it.
func TestASnapshotWithABadScheduleIsInvalid(t *testing.T) {
	rt, ws := testRoots(t)
	ref := rt.ProjectRef(ws, "bad")
	if err := rt.CreateJobFile(ref, []byte("---\nschedule: \"not a cron\"\n---\nx\n")); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadSnapshot(ref)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Err == nil {
		t.Fatal("a schedule that does not parse was accepted")
	}
}
