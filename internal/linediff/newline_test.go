package linediff

import (
	"strings"
	"testing"
)

func TestDiffIncludesFinalNewlineChanges(t *testing.T) {
	for _, sides := range [][2]string{{"one\n", "one"}, {"one", "one\n"}} {
		add, del := Stat(sides[0], sides[1])
		if add != 1 || del != 1 {
			t.Errorf("stats = +%d -%d; want +1 -1", add, del)
		}
		patch, _ := Unified("a.txt", sides[0], sides[1], DefaultContext)
		if !strings.Contains(patch, "@@") || !strings.Contains(patch, "\\ No newline at end of file") {
			t.Errorf("missing newline change: %q", patch)
		}
	}
}
