//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDrivesListsVolumeRoots(t *testing.T) {
	drives := Drives()
	if len(drives) == 0 {
		t.Fatal("Drives() returned no volume roots on Windows")
	}
	shape := regexp.MustCompile(`^[A-Z]:\\$`)
	for _, d := range drives {
		if !shape.MatchString(d) {
			t.Errorf("drive %q is not a bare volume root", d)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	want := strings.ToUpper(filepath.VolumeName(cwd)) + `\`
	found := false
	for _, d := range drives {
		if d == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Drives() = %v, missing the volume of the working directory %q", drives, want)
	}
}
