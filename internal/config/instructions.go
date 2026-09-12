package config

import "strings"

// Instructions configures which files are read as user-provided instructions and
// appended to the system prompt. Compatible with the AGENTS.md convention used by
// other AI coding agents, and not limited to the session CWD: ${CODDY_HOME},
// ${CWD} and a leading ~ expand, and an absolute entry is read as it stands.
type Instructions struct {
	// Files is the list of instruction files to read, in the order listed.
	// Defaults to DefaultInstructionFiles() when empty.
	Files []string `yaml:"files"`
}

// DefaultInstructionFiles is the pair a directory describes itself with, the
// same one the project docs preamble reads for the workspace root and for the
// agent home (rules.LoadProjectDocs). Kept as a function so the schema default,
// the UI defaults and the loader cannot drift apart.
func DefaultInstructionFiles() []string {
	return []string{"AGENTS.md", "DESIGN.md"}
}

// ApplyDefaults sets the default file list when empty.
func (c *Instructions) ApplyDefaults() {
	if len(c.Files) == 0 {
		c.Files = DefaultInstructionFiles()
	}
}

// Validate normalises the file list.
func (c *Instructions) Validate() error {
	for i := range c.Files {
		c.Files[i] = strings.TrimSpace(c.Files[i])
	}
	return nil
}
