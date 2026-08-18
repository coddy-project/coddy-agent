//go:build !windows

package update

import (
	"fmt"
	"io"
)

func scheduleWindowsUpdate(windowsUpdateRequest) error {
	return fmt.Errorf("Windows self-update can only run on Windows")
}

func runWindowsUpdateHelper(_ []string, _ io.Writer) error {
	return fmt.Errorf("Windows update helper can only run on Windows")
}

func runWindowsRestartAfterUpdate(_ []string, _ io.Writer) error {
	return fmt.Errorf("Windows update helper can only run on Windows")
}
