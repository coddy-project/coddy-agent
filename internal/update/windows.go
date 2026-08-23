package update

import "io"

const (
	applyUpdateCommand        = "__apply-update"
	restartAfterUpdateCommand = "__restart-after-update"
)

type windowsUpdateRequest struct {
	ParentPID  int
	Restart    bool
	StagedPath string
	TargetPath string
}

type windowsUpdateInstaller func(windowsUpdateRequest) error

// RunHelper handles the private commands used by a copied Windows executable to
// install a staged self-update after its parent has exited. On every other
// platform they are not commands at all, so the caller reports them the same
// way it reports any other unknown argument.
func RunHelper(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 || !helperCommandsAvailable {
		return false, nil
	}
	switch args[0] {
	case applyUpdateCommand:
		return true, runWindowsUpdateHelper(args[1:], out)
	case restartAfterUpdateCommand:
		return true, runWindowsRestartAfterUpdate(args[1:], out)
	default:
		return false, nil
	}
}
