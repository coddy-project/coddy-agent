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
	// "$$" is the documented escape for a literal "$". It is resolved first,
	// pairing left to right exactly as os.Expand would, so that "$${CWD}"
	// still yields the literal "${CWD}" and escapeYAMLDollar stays an inverse.
	s = strings.ReplaceAll(s, "$$", escapedDollarSentinel)
	// ${CWD} is the session placeholder, not an environment reference: it is
	// hidden from os.Expand and restored afterwards, so it stays in the
	// document for whoever resolves it against a session working directory,
	// even when the environment has a CWD variable. Only the braced spelling
	// is the placeholder; a bare $CWD is an ordinary reference as before.
	s = strings.ReplaceAll(s, sessionCWDPlaceholder, sessionCWDSentinel)
	s = os.Expand(s, os.Getenv)
	s = strings.ReplaceAll(s, sessionCWDSentinel, sessionCWDPlaceholder)
	return strings.ReplaceAll(s, escapedDollarSentinel, "$")
}

// The sentinels stand in for "$$" and "${CWD}" while the environment is
// expanded; neither carries a "$", so os.Expand passes them through.
const (
	sessionCWDPlaceholder = "${CWD}"
	sessionCWDSentinel    = "\x00coddy-session-cwd\x00"
	escapedDollarSentinel = "\x00coddy-escaped-dollar\x00"
)

// expandConfigBody prepares raw config.yaml text for parsing: ${CODDY_HOME} is
// substituted (with forward slashes, see ExpandPathVars) and environment
// references are expanded, while ${CWD} survives verbatim. Per-session paths
// (skills.dirs, subagents.dirs, hooks.files, prompts.dir, mcp_servers) resolve
// it against the workspace of the session that uses them; the process-scoped
// directories expand it against Paths.CWD in applyDefaults. A leading ~ inside
// a value is left to those consumers as well, as it always was: the previous
// body-level pass only looked at the first byte of the whole document.
func expandConfigBody(s string, p Paths) string {
	// "$$" is resolved before ${CODDY_HOME} is substituted so that
	// "$${CODDY_HOME}" keeps a literal placeholder, like "$${CWD}" does.
	s = strings.ReplaceAll(s, "$$", escapedDollarSentinel)
	s = strings.ReplaceAll(s, "${CODDY_HOME}", yamlSafePath(p.Home))
	return expandEnvEscaped(s)
}

// escapeYAMLDollar doubles every "$" so that expandEnvEscaped restores the exact literal
// on the next load. Applied to always-literal secret fields (proxy URLs) before they are
// serialized to disk, so a value never gets mangled by environment-variable expansion.
func escapeYAMLDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}
