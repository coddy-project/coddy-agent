// Package hooks loads, matches and runs operator-defined lifecycle hooks:
// commands that receive one JSON document on stdin at a point of a session
// (before a tool call, after it, when the user submits a prompt, ...) and
// answer with an exit code plus optional JSON on stdout. Definitions use the
// file shape of Claude Code, so a hooks file written for it loads unchanged.
package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Event names use the Claude Code spelling so a definition ports both ways.
const (
	EventSessionStart       = "SessionStart"
	EventUserPromptSubmit   = "UserPromptSubmit"
	EventPreToolUse         = "PreToolUse"
	EventPostToolUse        = "PostToolUse"
	EventPostToolUseFailure = "PostToolUseFailure"
	EventStop               = "Stop"
	EventSubagentStart      = "SubagentStart"
	EventSubagentStop       = "SubagentStop"
	EventPreCompact         = "PreCompact"
	EventPostCompact        = "PostCompact"
	EventNotification       = "Notification"
)

// Events lists every event Coddy fires, in the order the catalog shows them.
var Events = []string{
	EventSessionStart,
	EventUserPromptSubmit,
	EventPreToolUse,
	EventPostToolUse,
	EventPostToolUseFailure,
	EventStop,
	EventSubagentStart,
	EventSubagentStop,
	EventPreCompact,
	EventPostCompact,
	EventNotification,
}

// KnownEvent reports whether name is an event Coddy fires.
func KnownEvent(name string) bool {
	for _, e := range Events {
		if e == name {
			return true
		}
	}
	return false
}

// HandlerCommand is the only handler type the runner executes. Other types
// from Claude Code and Codex (http, prompt, agent, mcp_tool) parse but are
// flagged unsupported and never run.
const HandlerCommand = "command"

// Handler is one command run for a matching event.
type Handler struct {
	// Type is the handler type; only HandlerCommand runs.
	Type string
	// Command is the command line (shell form) or the executable (exec form).
	Command string
	// CommandWindows replaces Command when Coddy runs on Windows (Codex's
	// commandWindows / command_windows key).
	CommandWindows string
	// Args, when present in the definition, switch the handler to the exec
	// form: Command is spawned directly with these arguments and no shell.
	Args []string
	// ExecForm records that the args key was present, even when empty.
	ExecForm bool
	// TimeoutSeconds bounds the process; 0 uses the runner's default.
	TimeoutSeconds int
	// Async runs the hook detached: its output is ignored and it can never
	// block.
	Async bool
	// FailClosed turns a crash, a timeout or invalid output into a block
	// instead of a non-blocking error (Cursor's failClosed).
	FailClosed bool
	// Unsupported explains why the handler never runs (an unsupported type).
	Unsupported string
}

// Group is one matcher group under an event: the matcher and its handlers.
type Group struct {
	Matcher  string
	Handlers []Handler
}

// Definition is the parsed content of one hooks file.
type Definition struct {
	// Events maps an event name to its matcher groups in file order.
	Events map[string][]Group
	// Warnings lists what was skipped or ignored while parsing.
	Warnings []string
}

// rawHandler is the JSON shape of one handler. Pointer fields tell presence
// apart from a zero value where that matters (args, failClosed, timeout).
type rawHandler struct {
	Type                   string    `json:"type"`
	Command                string    `json:"command"`
	CommandWindows         string    `json:"commandWindows"`
	CommandWindowsSnake    string    `json:"command_windows"`
	Args                   *[]string `json:"args"`
	Timeout                *float64  `json:"timeout"`
	Async                  bool      `json:"async"`
	FailClosed             *bool     `json:"failClosed"`
	FailClosedSnake        *bool     `json:"fail_closed"`
	If                     string    `json:"if"`
	StatusMessage          string    `json:"statusMessage"`
	Once                   bool      `json:"once"`
	Shell                  string    `json:"shell"`
	AdditionalContextLimit *float64  `json:"additionalContextLimit"`
}

type rawGroup struct {
	Matcher string       `json:"matcher"`
	Hooks   []rawHandler `json:"hooks"`
}

// Parse reads a hooks file. The document must be a JSON object; only its
// "hooks" key is read, so a Claude Code settings file with permissions and
// other keys parses to just its hooks. A missing "hooks" key is an empty
// definition. Unknown events and unsupported handler types are dropped or
// flagged with a warning; a malformed group or an empty command is an error,
// because a file that fails to parse must show up as invalid rather than
// silently lose a policy hook.
func Parse(data []byte) (*Definition, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("hooks definition must be a JSON object")
	}
	var top struct {
		Hooks json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return nil, fmt.Errorf("parse hooks definition: %w", err)
	}
	def := &Definition{Events: map[string][]Group{}}
	body := bytes.TrimSpace(top.Hooks)
	if len(body) == 0 || string(body) == "null" {
		return def, nil
	}
	if body[0] != '{' {
		return nil, fmt.Errorf("hooks must be an object keyed by event name")
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("parse hooks: %w", err)
	}
	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !KnownEvent(name) {
			def.Warnings = append(def.Warnings, fmt.Sprintf("event %q is not supported and was skipped", name))
			continue
		}
		var groups []rawGroup
		if err := json.Unmarshal(events[name], &groups); err != nil {
			return nil, fmt.Errorf("event %s: expected an array of matcher groups: %w", name, err)
		}
		for gi, g := range groups {
			group := Group{Matcher: strings.TrimSpace(g.Matcher)}
			for hi, rh := range g.Hooks {
				h, warn, err := rh.handler()
				if err != nil {
					return nil, fmt.Errorf("event %s group %d hook %d: %w", name, gi, hi, err)
				}
				if warn != "" {
					def.Warnings = append(def.Warnings, fmt.Sprintf("event %s group %d hook %d: %s", name, gi, hi, warn))
				}
				group.Handlers = append(group.Handlers, h)
			}
			def.Events[name] = append(def.Events[name], group)
		}
	}
	return def, nil
}

func (rh rawHandler) handler() (Handler, string, error) {
	typ := strings.ToLower(strings.TrimSpace(rh.Type))
	if typ == "" {
		typ = HandlerCommand
	}
	h := Handler{
		Type:           typ,
		Command:        strings.TrimSpace(rh.Command),
		CommandWindows: strings.TrimSpace(rh.CommandWindows),
		Async:          rh.Async,
	}
	if h.CommandWindows == "" {
		h.CommandWindows = strings.TrimSpace(rh.CommandWindowsSnake)
	}
	if rh.Args != nil {
		h.ExecForm = true
		h.Args = append([]string(nil), (*rh.Args)...)
	}
	if rh.Timeout != nil && *rh.Timeout > 0 {
		h.TimeoutSeconds = int(math.Ceil(*rh.Timeout))
	}
	switch {
	case rh.FailClosed != nil:
		h.FailClosed = *rh.FailClosed
	case rh.FailClosedSnake != nil:
		h.FailClosed = *rh.FailClosedSnake
	}
	if typ != HandlerCommand {
		h.Unsupported = fmt.Sprintf("handler type %q is not supported (only command handlers run)", typ)
		return h, h.Unsupported, nil
	}
	if h.Command == "" {
		return h, "", fmt.Errorf("command handler needs a non-empty command")
	}
	if h.Async && h.FailClosed {
		// A detached handler is never awaited, so nothing it does can block;
		// a failClosed flag on it would promise a gate that cannot exist.
		h.FailClosed = false
		return h, "failClosed is ignored on an async handler: a detached hook can never block", nil
	}
	if strings.TrimSpace(rh.If) != "" {
		return h, fmt.Sprintf("the \"if\" filter %q is not supported and is ignored: the hook runs for every matching call", rh.If), nil
	}
	return h, "", nil
}
