package config

import "strings"

// Instructions configures the files the operator adds to the system prompt
// (AGENTS.md convention), below the documents every session reads anyway: the
// AGENTS.md and DESIGN.md of the agent home and of the session folder, and the
// nested ones of the folders a tool enters (rules.LoadStanding,
// rules.AgentsForPaths), which no setting names or turns off. ${CODDY_HOME},
// ${CWD} and a leading ~ expand, and an absolute entry is read as it stands.
type Instructions struct {
	// Files is the list of instruction files to read, in the order listed,
	// after those documents. Empty by default: the list only adds.
	Files []string `yaml:"files"`
}

// DefaultInstructionFiles is the default of instructions.files: nothing. The
// AGENTS.md / DESIGN.md pair the list used to name is read whether or not it
// is listed, and a list naming it still works - an entry the layers already
// carry is not read twice. Kept as a function so the schema default, the UI
// defaults and the loader cannot drift apart.
func DefaultInstructionFiles() []string {
	return []string{}
}

// ApplyDefaults leaves an empty list empty: there is nothing to fill in.
func (c *Instructions) ApplyDefaults() {
	if c.Files == nil {
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
