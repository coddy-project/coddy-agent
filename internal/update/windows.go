package update

import "io"

const (
	applyUpdateCommand = "__apply-update"
	// afterUpdateCommand deletes the helper once the new build is in place. It
	// keeps the name older releases call it by: their helper hands over to the
	// build it just installed, and that build may be this one.
	afterUpdateCommand = "__restart-after-update"
)

type windowsUpdateRequest struct {
	ParentPID  int
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
	case afterUpdateCommand:
		return true, runWindowsAfterUpdate(args[1:], out)
	default:
		return false, nil
	}
}
