package shell

import (
	"fmt"
	"runtime"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// operatorMux runs operator commands inside a terminal multiplexer.
type operatorMux interface {
	// Name is "tmux" or "screen".
	Name() string
	// AttachCommand is how the operator reaches the command's terminal.
	AttachCommand() string
	// Start runs the command in a window of its own.
	Start(command, cwd string, commandShell platform.Shell) (operatorRun, error)
}

// operatorMultiplexer returns the multiplexer tools.operator_command names
// for this request, nil to run the command directly. Windows has neither, so
// the setting is ignored there.
func operatorMultiplexer(name string, env *tooling.Env, req acp.OperatorCommandRequest) (operatorMux, error) {
	if name == "" || runtime.GOOS == "windows" {
		return nil, nil
	}
	return nil, fmt.Errorf("tools.operator_command.multiplexer %q is not supported yet", name)
}
