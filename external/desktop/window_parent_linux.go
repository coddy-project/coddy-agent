//go:build desktop && linux && !android

package desktop

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parentOf reads a process's parent out of /proc. The field is the fourth of
// stat, after the executable name, which is in brackets and may itself hold
// spaces or brackets - so the line is read from its last bracket on.
func parentOf(pid int) (int, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	line := string(b)
	i := strings.LastIndex(line, ")")
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(line[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return parent, true
}
