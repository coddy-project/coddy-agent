package dryrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

const mib = 1 << 20

// diskTable answers a probe from a table keyed by the folder asked about.
type diskTable map[string]platform.DiskSpace

func (d diskTable) probe(path string) (platform.DiskSpace, error) {
	if ds, ok := d[path]; ok {
		return ds, nil
	}
	return platform.DiskSpace{}, errors.New("not in the table: " + path)
}

func disk(volume string, free, total uint64) platform.DiskSpace {
	return platform.DiskSpace{Volume: volume, FreeBytes: free, TotalBytes: total}
}

// runWithDisks runs the probes over a config whose sessions folder and home
// folder read as the table says.
func runWithDisks(t *testing.T, body string, sessionsFree, homeFree uint64, sameVolume bool) *Report {
	t.Helper()
	prep, home := prepare(t, body)
	root := prep.Cfg.ResolvedSessionsRoot()
	homeVolume := "dev:home"
	if sameVolume {
		homeVolume = "dev:sessions"
	}
	table := diskTable{
		root: disk("dev:sessions", sessionsFree, 500*1024*mib),
		home: disk(homeVolume, homeFree, 500*1024*mib),
	}
	req := Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator, DiskSpace: table.probe}
	return Run(context.Background(), req)
}

func storageChecks(rep *Report) []Check {
	var out []Check
	for _, c := range rep.Checks {
		if c.Path == "sessions.min_free_mb" {
			out = append(out, c)
		}
	}
	return out
}

// Plenty of room on the disk the sessions go to is an ok that says how much.
func TestSessionsDiskWithRoomIsOK(t *testing.T) {
	rep := runWithDisks(t, "agent:\n  model: x/y\n", 8*1024*mib, 8*1024*mib, true)
	checks := storageChecks(rep)
	if len(checks) != 1 {
		t.Fatalf("one volume, %d checks: %+v", len(checks), checks)
	}
	c := checks[0]
	if c.Status != StatusOK || !strings.Contains(c.Message, "8 GiB free of 500 GiB") || !strings.Contains(c.Message, "512 MiB") {
		t.Fatalf("got %+v", c)
	}
	if !strings.Contains(c.Message, "sessions") {
		t.Fatalf("the check does not say which folder it read: %+v", c)
	}
}

// Under the threshold is a warning, with the fix: the room to make or the
// threshold to lower. The line is the key's when the file sets it.
func TestSessionsDiskUnderTheThresholdIsAWarning(t *testing.T) {
	rep := runWithDisks(t, "sessions:\n  min_free_mb: 1024\n", 700*mib, 8*1024*mib, true)
	c := storageChecks(rep)[0]
	if c.Status != StatusWarning {
		t.Fatalf("got %+v, want a warning", c)
	}
	for _, want := range []string{"700 MiB free", "1 GiB"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("message %q lacks %q", c.Message, want)
		}
	}
	if !strings.Contains(c.Fix, "free space") || !strings.Contains(c.Fix, "sessions.min_free_mb") {
		t.Errorf("fix %q must say to free space or lower the key", c.Fix)
	}
	if c.Line == 0 {
		t.Errorf("a key the file writes is located: %+v", c)
	}
}

// A disk with no byte free is an error: nothing can be saved.
func TestSessionsDiskWithNothingFreeIsAnError(t *testing.T) {
	rep := runWithDisks(t, "agent:\n  model: x/y\n", 0, 8*1024*mib, true)
	c := storageChecks(rep)[0]
	if c.Status != StatusError || !strings.Contains(c.Message, "no free space") || !strings.Contains(c.Fix, "free space") {
		t.Fatalf("got %+v, want an error that names the lack of space", c)
	}
	if rep.Errors() == 0 {
		t.Fatal("the dry run did not fail")
	}
}

// 0 is the user's choice to hear nothing: skipped, with the figures kept.
func TestSessionsDiskWarningOffIsSkipped(t *testing.T) {
	rep := runWithDisks(t, "sessions:\n  min_free_mb: 0\n", 10*mib, 8*1024*mib, true)
	c := storageChecks(rep)[0]
	if c.Status != StatusSkipped || !strings.Contains(c.Message, "off") || !strings.Contains(c.Message, "10 MiB free") {
		t.Fatalf("got %+v, want skipped, saying the warning is off and how much is free", c)
	}
}

// The home folder is read too, when it is on another disk; the sessions
// folder is always first.
func TestHomeOnAnotherDiskIsCheckedToo(t *testing.T) {
	rep := runWithDisks(t, "agent:\n  model: x/y\n", 8*1024*mib, 100*mib, false)
	checks := storageChecks(rep)
	if len(checks) != 2 {
		t.Fatalf("two volumes, %d checks: %+v", len(checks), checks)
	}
	if checks[0].Status != StatusOK || !strings.Contains(checks[0].Message, "sessions") {
		t.Errorf("first check %+v, want the sessions volume", checks[0])
	}
	if checks[1].Status != StatusWarning || !strings.Contains(checks[1].Message, "home") {
		t.Errorf("second check %+v, want the home volume as a warning", checks[1])
	}
}

// A platform or a filesystem that cannot say is not a problem of the config.
func TestUnreadableDiskIsSkippedNotAnError(t *testing.T) {
	prep, _ := prepare(t, "agent:\n  model: x/y\n")
	unsupported := func(string) (platform.DiskSpace, error) {
		return platform.DiskSpace{}, fmt.Errorf("%w", platform.ErrDiskSpaceUnavailable)
	}
	rep := Run(context.Background(), Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator, DiskSpace: unsupported})
	checks := storageChecks(rep)
	if len(checks) == 0 {
		t.Fatal("no check at all for the sessions disk")
	}
	for _, c := range checks {
		if c.Status != StatusSkipped || !strings.Contains(c.Message, "cannot be read") {
			t.Errorf("got %+v, want skipped", c)
		}
	}
}

// With no probe given the real call is made, against the real folder: the
// sessions folder of a first start does not exist yet and is read through its
// nearest ancestor.
func TestRealProbeReadsAFolderThatDoesNotExistYet(t *testing.T) {
	later := filepath.Join(t.TempDir(), "later")
	prep, _ := prepare(t, "sessions:\n  dir: "+fmt.Sprintf("%q", later)+"\n  min_free_mb: 0\n")
	rep := Run(context.Background(), Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator})
	checks := storageChecks(rep)
	if len(checks) == 0 {
		t.Fatal("no check for the sessions disk")
	}
	c := checks[0]
	off := strings.Contains(c.Message, "off")
	if c.Status != StatusSkipped || (!off && !strings.Contains(c.Message, "cannot be read")) {
		t.Fatalf("got %+v, want skipped (warning off, or this platform cannot say)", c)
	}
	if off && !strings.Contains(c.Message, " free of ") {
		t.Fatalf("a real read reports the figures: %+v", c)
	}
}
