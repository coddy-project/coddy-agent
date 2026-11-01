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
	"errors"
	"fmt"
	"io/fs"
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

// guideMaxBytes bounds one guide. A guide over it is cut with guideCutMarker,
// so the task is at most the user message's bound (subagents.MaxPromptBytes)
// plus two guides of this size and their headings.
const guideMaxBytes = 8 * 1024

// guideCutMarker replaces the tail of a guide over guideMaxBytes.
const guideCutMarker = "\n\n[the rest of the guide was cut: it exceeded the memory subagent's guide bound]"

// Guide is the AGENTS.md at the root of one memory scope, the text that
// tells the child how the notes of that root are kept.
type Guide struct {
	// Path is the guide's scope:relative path, global:AGENTS.md.
	Path string
	// Text is the file, trimmed.
	Text string
}

// Guides reads the guide of the global memory root and then the one of the
// project root of cwd, through the store, so a guide is read under the rules
// of any note of its root (a project guide that is a link out of the root is
// refused). A missing, empty or refused guide is absent from the result; a
// refused one is also reported in the error, and the other level is still
// returned. The two levels stay apart: neither is merged into the other.
func Guides(cfg *config.Config, cwd string) ([]Guide, error) {
	store, err := memstorage.NewStore(&cfg.Memory, cfg.Paths, cwd)
	if err != nil {
		return nil, err
	}
	var guides []Guide
	var refused []error
	for _, scope := range []string{"global", "project"} {
		path := scope + ":" + memstorage.GuideName
		data, err := store.ReadNote(scope, memstorage.GuideName)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				refused = append(refused, fmt.Errorf("%s: %w", path, err))
			}
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			guides = append(guides, Guide{Path: path, Text: text})
		}
	}
	return guides, errors.Join(refused...)
}

// TaskMessage is the child's task: the guides of the memory roots, each under
// a heading that names it and quoted line by line, then the user message
// behind a preamble. The user
// message is cut at the bound a spawn_agent prompt has (32 KiB) and each
// guide at guideMaxBytes, with a marker, so the pass sees a bounded input
// (issue #266). Without guides the task is the preamble and the message alone.
func TaskMessage(userText string, guides []Guide) string {
	var b strings.Builder
	for _, g := range guides {
		text, cut := cutBytes(g.Text, guideMaxBytes)
		if cut {
			text += guideCutMarker
		}
		b.WriteString("Memory guide (" + g.Path + "):\n" + quoteLines(text) + "\n\n")
	}
	text, cut := cutBytes(strings.TrimSpace(userText), subagents.MaxPromptBytes)
	if cut {
		text += cutMarker
	}
	b.WriteString(taskPreamble + text)
	return b.String()
}

// quoteLines writes text as a Markdown blockquote, "> " before every line and
// ">" alone for an empty one. The child's task is resolved for @ references
// like any prompt, and a guide arrives with a checkout: a reference in it
// ("@~/.aws/credentials") would attach that file to the child's request. A
// quoted line is prose to the mention grammar, a fence the guide opens and
// never closes cannot swallow the user message after it, and the guide stays
// readable text.
func quoteLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

// cutBytes cuts s to at most max bytes without splitting a character, the
// cut end trimmed, and says whether it cut anything.
func cutBytes(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut), true
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
