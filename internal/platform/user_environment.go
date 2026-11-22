package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	userEnvironmentTimeout   = 10 * time.Second
	userEnvironmentWaitDelay = time.Second
)

// UserEnvironment returns the exported environment of the user's current
// interactive Bash session configuration. It falls back to the process
// environment when Bash is unavailable or its startup files fail.
func UserEnvironment(parent context.Context, dir string) []string {
	fallback := os.Environ()
	shell := CurrentShell()
	if shell.Kind != ShellBash {
		return fallback
	}
	ctx, cancel := context.WithTimeout(parent, userEnvironmentTimeout)
	defer cancel()

	// A random boundary keeps startup output, including NUL-delimited text,
	// separate from the environment stream. Bash builtins avoid depending on
	// the host's env implementation or a PATH that the startup file replaced.
	var boundary [16]byte
	if _, err := rand.Read(boundary[:]); err != nil {
		return fallback
	}
	marker := "CODDY_ENVIRONMENT_" + hex.EncodeToString(boundary[:])
	loopVariable := "__coddy_env_" + hex.EncodeToString(boundary[:])
	probe := fmt.Sprintf(`\builtin printf '%s\0'; while IFS= \builtin read -r %s; do \builtin printf '%%s=%%s\0' "$%s" "${!%s}"; done < <(\builtin compgen -e)`, marker, loopVariable, loopVariable, loopVariable)
	cmd := exec.CommandContext(ctx, shell.Path, "-ic", probe)
	cmd.Dir = dir
	cmd.Stderr = nil
	cmd.WaitDelay = userEnvironmentWaitDelay
	DetachProcessGroup(cmd)
	cmd.Cancel = func() error {
		return TerminateProcessGroup(cmd, 0)
	}
	AdaptCommand(cmd)
	output, err := cmd.Output()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return fallback
	}
	if ctx.Err() != nil {
		return fallback
	}
	_, environment, ok := bytes.Cut(output, append([]byte(marker), 0))
	if !ok {
		return fallback
	}
	entries := bytes.Split(bytes.TrimSuffix(environment, []byte{0}), []byte{0})
	env := make([]string, 0, len(entries)+len(fallback))
	for _, entry := range fallback {
		name, _, _ := strings.Cut(entry, "=")
		if !shellVariableName(name) && !strings.HasPrefix(name, "BASH_FUNC_") {
			env = append(env, entry)
		}
	}
	for _, entry := range entries {
		if bytes.Contains(entry, []byte{'='}) {
			env = append(env, string(entry))
		}
	}
	if len(env) == 0 {
		return fallback
	}
	return env
}

func shellVariableName(name string) bool {
	if name == "" || !isShellIdentifierStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isShellIdentifierStart(name[i]) && (name[i] < '0' || name[i] > '9') {
			return false
		}
	}
	return true
}

func isShellIdentifierStart(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}
