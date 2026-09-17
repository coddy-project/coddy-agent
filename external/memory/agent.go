//go:build memory

// Package memory is the long-term memory subagent: what a user turn's memory
// child runs on. The notes live under external/memory/storage, the tools
// under external/memory/tools; this package provides the child's system
// prompt template, its task message, its tool set and its tool names. The
// child itself is launched and driven by internal/agent (memory_hooks.go):
// it is an ordinary subagent run in the background task pool, with its own
// session bundle inside the parent's and its own task log.
package memory

import (
	_ "embed"
	"strings"
	"unicode/utf8"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

//go:embed prompts/memory_agent.md
var promptTemplate string

// readOnlyAddendum tells the child why the mutating tools are absent.
const readOnlyAddendum = "This turn runs in the read-only ask mode: recall only. " +
	"Saving, creating or deleting notes is unavailable; answer from what you find."

// taskPreamble opens the child's task: the user message it works from.
const taskPreamble = "User message for this turn:\n"

// cutMarker replaces the tail of a user message over the task bound.
const cutMarker = "\n\n[the rest of the message was cut: it exceeded the memory subagent's task bound]"

// PromptTemplate is the child's system prompt template source, rendered by
// the agent with the working directory and the tool list. readOnly appends
// the addendum for a recall-only child (an ask-mode turn).
func PromptTemplate(readOnly bool) string {
	if readOnly {
		return promptTemplate + "\n\n" + readOnlyAddendum
	}
	return promptTemplate
}

// TaskMessage is the child's task: the user message behind a preamble, cut
// at the bound a spawn_agent prompt has (32 KiB) with a marker, so the pass
// sees a bounded input (issue #266).
func TaskMessage(userText string) string {
	text := strings.TrimSpace(userText)
	if len(text) > subagents.MaxPromptBytes {
		cut := text[:subagents.MaxPromptBytes]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		text = strings.TrimSpace(cut) + cutMarker
	}
	return taskPreamble + text
}

// ToolNames is the child's tool allowlist: the six memory tools, or the
// three recall tools for a read-only child.
func ToolNames(readOnly bool) []string {
	if readOnly {
		return []string{memtools.NameSearch, memtools.NameList, memtools.NameRead}
	}
	return []string{memtools.NameSearch, memtools.NameList, memtools.NameRead, memtools.NameMkdir, memtools.NameSave, memtools.NameDelete}
}

// Tools builds the six memory tools over the two note roots of cwd: the
// global root of memory.dir and the project root under cwd. The child's
// allowlist decides which of them it may call.
func Tools(cfg *config.Config, cwd string) ([]*tooling.Tool, error) {
	store, err := memstorage.NewStore(&cfg.Memory, cfg.Paths, cwd)
	if err != nil {
		return nil, err
	}
	return memtools.PersistTools(store, &cfg.Memory), nil
}
