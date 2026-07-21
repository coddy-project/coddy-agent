package config

import (
	"os"
	"strings"
)

// expandEnvEscaped expands ${VAR} and $VAR references from the process environment,
// like os.ExpandEnv, but treats "$$" as an escape for a literal "$". This lets secrets
// that contain a dollar sign (e.g. a proxy password like "$2y$10$...") survive the
// load-time expansion pass instead of having their "$WORD"/"$N" fragments resolved to
// empty environment variables. Values are written to disk with "$" doubled to "$$"
// (see escapeYAMLDollar) and read back here as a single literal "$".
func expandEnvEscaped(s string) string {
	return os.Expand(s, func(name string) string {
		// os.Expand yields name == "$" for the "$$" sequence (via isShellSpecialVar).
		if name == "$" {
			return "$"
		}
		return os.Getenv(name)
	})
}

// escapeYAMLDollar doubles every "$" so that expandEnvEscaped restores the exact literal
// on the next load. Applied to always-literal secret fields (proxy URLs) before they are
// serialized to disk, so a value never gets mangled by environment-variable expansion.
func escapeYAMLDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
