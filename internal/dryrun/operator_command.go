package dryrun

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// operatorCommand looks up what tools.operator_command runs commands with on
// this host: the operator shell (an empty setting stands for the platform's
// default, which is reported too) and the multiplexer. Only the host that runs
// the commands can tell, which is why coddy -t does not judge them.
func (r *runner) operatorCommand() {
	oc := r.req.Cfg.Tools.OperatorCommand.ToolSettings()
	if !oc.Enabled {
		return
	}
	const shellPath = "tools.operator_command.shell"
	if sh, err := platform.ResolveShell(oc.Shell, runtime.GOOS, nil); err != nil {
		r.rep.add(r.check(StatusError, shellPath, shellPath, err.Error(),
			"install it, write its absolute path, or leave tools.operator_command.shell empty for the platform's default shell"))
	} else {
		what := fmt.Sprintf("operator commands run in %s (%s)", sh.Kind, sh.Path)
		if oc.Shell == "" {
			what += ", the default of this platform"
		}
		r.rep.add(r.check(StatusOK, shellPath, shellPath, what, ""))
	}
	if oc.Multiplexer == "" {
		return
	}
	const muxPath = "tools.operator_command.multiplexer"
	if runtime.GOOS == "windows" {
		r.rep.add(r.check(StatusWarning, muxPath, muxPath, oc.Multiplexer+" is ignored on Windows: commands run directly",
			"remove tools.operator_command.multiplexer, or keep it for the macOS and Linux hosts that share this file"))
		return
	}
	resolved, err := exec.LookPath(oc.Multiplexer)
	if err != nil {
		r.rep.add(r.check(StatusError, muxPath, muxPath, fmt.Sprintf("%s is not installed on this host", oc.Multiplexer),
			"install "+oc.Multiplexer+", or leave tools.operator_command.multiplexer empty to run commands directly"))
		return
	}
	r.rep.add(r.check(StatusOK, muxPath, muxPath, fmt.Sprintf("operator commands run inside %s (%s)", oc.Multiplexer, resolved), ""))
}
