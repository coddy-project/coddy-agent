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
		// ${CWD} is the session placeholder, not an environment reference: it
		// stays in the document for whoever resolves it against a session
		// working directory, even when the environment has a CWD variable.
		if name == sessionCWDVar {
			return sessionCWDPlaceholder
		}
		return os.Getenv(name)
	})
}

const (
	sessionCWDVar         = "CWD"
	sessionCWDPlaceholder = "${" + sessionCWDVar + "}"
)

// expandConfigBody prepares raw config.yaml text for parsing: ${CODDY_HOME} is
// substituted (with forward slashes, see ExpandPathVars) and environment
// references are expanded, while ${CWD} survives verbatim. Per-session paths
// (skills.dirs, subagents.dirs, hooks.files, prompts.dir, mcp_servers) resolve
// it against the workspace of the session that uses them; the process-scoped
// directories expand it against Paths.CWD in applyDefaults.
func expandConfigBody(s string, p Paths) string {
	s = strings.ReplaceAll(s, "${CODDY_HOME}", yamlSafePath(p.Home))
	return expandEnvEscaped(s)
}

// escapeYAMLDollar doubles every "$" so that expandEnvEscaped restores the exact literal
// on the next load. Applied to always-literal secret fields (proxy URLs) before they are
// serialized to disk, so a value never gets mangled by environment-variable expansion.
func escapeYAMLDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
