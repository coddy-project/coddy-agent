package session

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// Command kinds.
const (
	// CommandKindSetting changes a session setting; it runs no turn and adds
	// nothing to the model's history.
	CommandKindSetting = "setting"
	// CommandKindAction runs deterministically outside the model (/compact,
	// /export, /plugin).
	CommandKindAction = "action"
)

// Argument shapes of a settings command.
const (
	argRequired = "required"
	argOptional = "optional"
	argNone     = "none"
)

// reasoningOn is what /think without a level asks for: thinking on at the
// model's default level. It is resolved against the model the change leaves
// the session (or the turns) on, never stored.
const reasoningOn = "on"

// Command is one built-in settings command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	// Hint is the argument hint (ACP AvailableCommand.Input.Hint).
	Hint string
	// Setting is the setting the command changes.
	Setting string
	// arg says whether the command takes a value; value is what a command
	// without one sets (/nothink -> off, /plan -> plan).
	arg   string
	value string
}

// scopeHint is appended to every hint: the flags each settings command takes.
const scopeHint = "[--once|--count=N]"

// settingsCommands is the registry of settings commands, in menu order.
var settingsCommands = []Command{
	{Name: "model", Setting: SettingModel, arg: argRequired, Hint: "<model id> " + scopeHint,
		Description: "Switch the model for this session, or for the next turns with --once / --count=N"},
	{Name: "reasoning", Aliases: []string{"effort"}, Setting: SettingReasoning, arg: argRequired, Hint: "<level|off|default> " + scopeHint,
		Description: "Set the reasoning level (off turns thinking off, default is the model's own level)"},
	{Name: "think", Setting: SettingReasoning, arg: argOptional, value: reasoningOn, Hint: "[level] " + scopeHint,
		Description: "Turn thinking on, at the model's default level or the one named"},
	{Name: "nothink", Aliases: []string{"no_think"}, Setting: SettingReasoning, arg: argNone, value: config.ReasoningOff, Hint: scopeHint,
		Description: "Turn thinking off where the model's provider can"},
	{Name: "agent", Setting: SettingMode, arg: argNone, value: string(ModeAgent), Hint: scopeHint,
		Description: "Agent mode: every tool, for implementing and running things"},
	{Name: "plan", Setting: SettingMode, arg: argNone, value: string(ModePlan), Hint: scopeHint,
		Description: "Plan mode: read-only tools and the plan document, nothing is changed"},
	{Name: "ask", Setting: SettingMode, arg: argNone, value: string(ModeAsk), Hint: scopeHint,
		Description: "Ask mode: read-only research and answers"},
	{Name: "permissions", Setting: SettingPermissionMode, arg: argRequired, Hint: "<ask|accept_edits|bypass> " + scopeHint,
		Description: "Set when tools ask for approval: ask, accept_edits (file writes pass) or bypass (nothing asks)"},
}

// SettingsCommands returns the registry of settings commands, in menu order.
func SettingsCommands() []Command {
	out := make([]Command, len(settingsCommands))
	copy(out, settingsCommands)
	return out
}

// LookupSettingsCommand finds a settings command by its name or an alias,
// with or without the leading slash.
func LookupSettingsCommand(name string) (Command, bool) {
	n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	if n == "" {
		return Command{}, false
	}
	for _, c := range settingsCommands {
		if c.Name == n {
			return c, true
		}
		for _, a := range c.Aliases {
			if a == n {
				return c, true
			}
		}
	}
	return Command{}, false
}

// Usage is the one-line usage of the command.
func (c Command) Usage() string {
	return "/" + c.Name + " " + c.Hint
}

// TakesValue reports whether the command is followed by a value (required or
// optional).
func (c Command) TakesValue() bool {
	return c.arg != argNone
}

// Choices lists the values the command's argument may take for the session
// on cfg: the configured models, the reasoning levels of the session's model,
// the modes, the permission modes. Empty for a command without an argument.
func (c Command) Choices(cfg *config.Config, st *State) []string {
	if !c.TakesValue() {
		return nil
	}
	switch c.Setting {
	case SettingModel:
		return configuredModelIDs(cfg)
	case SettingReasoning:
		if cfg == nil || st == nil {
			return nil
		}
		choices := cfg.ReasoningChoicesFor(cfg.FindModelEntry(st.EffectiveModelID(cfg)))
		if c.Name == "think" {
			// Off is /nothink's business.
			out := make([]string, 0, len(choices))
			for _, lv := range choices {
				if lv != config.ReasoningOff {
					out = append(out, lv)
				}
			}
			return out
		}
		if len(choices) > 0 {
			choices = append(choices, config.ReasoningDefault)
		}
		return choices
	case SettingPermissionMode:
		return []string{config.PermModeAsk, config.PermModeAcceptEdits, config.PermModeBypass}
	}
	return nil
}

// CommandLine is what the leading settings commands of a prompt asked for.
type CommandLine struct {
	// Session is the change for the session (Turns 0); empty when no command
	// was session-wide.
	Session SettingsChange
	// Turns are the changes for a number of turns, one per distinct count.
	Turns []SettingsChange
	// Rest is the text after the commands: the prompt, if any.
	Rest string
	// Names are the commands taken, in order, for the log.
	Names []string
}

// Empty reports whether the line held no settings command at all.
func (l *CommandLine) Empty() bool {
	return l == nil || len(l.Names) == 0
}

// ParseSettingsCommands takes the settings commands off the start of text.
//
// Only the start counts: a command in the middle of a sentence is prose. Each
// command's value and flags (--once, --count=N) are words on its own line;
// several commands may follow each other, on one line or on consecutive ones.
// The first word that belongs to no command starts the prompt, which is
// returned verbatim from there. A text that does not start with a settings
// command comes back as a line with no names and the whole text as Rest.
func ParseSettingsCommands(text string) (*CommandLine, error) {
	line := &CommandLine{}
	pos := skipSpace(text, 0, true)
	for pos < len(text) && text[pos] == '/' {
		nameEnd := wordEnd(text, pos)
		cmd, ok := LookupSettingsCommand(text[pos:nameEnd])
		if !ok {
			break
		}
		next, value, turns, err := parseCommandWords(text, nameEnd, cmd)
		if err != nil {
			return nil, err
		}
		line.add(cmd, value, turns)
		pos = next
		// The line is over: the next command may start the following line.
		if pos < len(text) && (text[pos] == '\n' || text[pos] == '\r') {
			pos = skipSpace(text, pos, true)
		}
	}
	if len(line.Names) == 0 {
		line.Rest = text
		return line, nil
	}
	line.Rest = strings.TrimLeft(text[pos:], " \t\r\n")
	return line, nil
}

// parseCommandWords reads the value and the flags of cmd from the words that
// follow it on its line. It stops before the first word that is neither, or
// before the next settings command, and returns where it stopped.
func parseCommandWords(text string, pos int, cmd Command) (next int, value string, turns int, err error) {
	haveValue := false
	for {
		start := skipSpace(text, pos, false)
		if start >= len(text) || text[start] == '\n' || text[start] == '\r' {
			pos = start
			break
		}
		end := wordEnd(text, start)
		word := text[start:end]
		switch {
		case word == "--once":
			turns = 1
			pos = end
			continue
		case word == "--count" || strings.HasPrefix(word, "--count="):
			raw := strings.TrimPrefix(word, "--count=")
			after := end
			if word == "--count" {
				vs := skipSpace(text, end, false)
				after = wordEnd(text, vs)
				raw = text[vs:after]
			}
			n, convErr := strconv.Atoi(raw)
			if convErr != nil || n < 1 || n > MaxOverrideTurns {
				return 0, "", 0, fmt.Errorf("/%s: --count takes a number of turns from 1 to %d, got %q", cmd.Name, MaxOverrideTurns, raw)
			}
			turns = n
			pos = after
			continue
		case strings.HasPrefix(word, "--"):
			return 0, "", 0, fmt.Errorf("/%s: unknown flag %s (usage: %s)", cmd.Name, word, cmd.Usage())
		}
		if strings.HasPrefix(word, "/") {
			if _, isCmd := LookupSettingsCommand(word); isCmd {
				pos = start
				break
			}
		}
		if !haveValue && cmd.arg == argRequired {
			value, haveValue = word, true
			pos = end
			continue
		}
		if !haveValue && cmd.arg == argOptional && isReasoningLevelWord(word) {
			value, haveValue = strings.ToLower(word), true
			pos = end
			continue
		}
		// The prompt starts here.
		pos = start
		break
	}
	if cmd.arg == argRequired && !haveValue {
		return 0, "", 0, fmt.Errorf("/%s needs a value (usage: %s)", cmd.Name, cmd.Usage())
	}
	if !haveValue {
		value = cmd.value
	}
	if cmd.Setting == SettingPermissionMode {
		value = normalizePermissionMode(value)
	}
	if cmd.Setting == SettingReasoning {
		value = strings.ToLower(value)
	}
	return pos, value, turns, nil
}

// add files one command's change under the session or under its count; a
// later command on the same setting wins.
func (l *CommandLine) add(cmd Command, value string, turns int) {
	l.Names = append(l.Names, cmd.Name)
	target := &l.Session
	if turns > 0 {
		target = nil
		for i := range l.Turns {
			if l.Turns[i].Turns == turns {
				target = &l.Turns[i]
				break
			}
		}
		if target == nil {
			l.Turns = append(l.Turns, SettingsChange{Turns: turns})
			target = &l.Turns[len(l.Turns)-1]
		}
	}
	v := value
	switch cmd.Setting {
	case SettingModel:
		target.Model = &v
	case SettingReasoning:
		target.Reasoning = &v
	case SettingMode:
		target.Mode = &v
	case SettingPermissionMode:
		target.PermissionMode = &v
	}
}

// reasoningLevelWords are the level names any provider uses; /think takes the
// word after it as a level only when it is one of them.
var reasoningLevelWords = map[string]bool{
	"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

func isReasoningLevelWord(w string) bool {
	return reasoningLevelWords[strings.ToLower(w)]
}

// normalizePermissionMode folds the spellings an operator types.
func normalizePermissionMode(v string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), "-", "_")) {
	case "acceptedits", "accept_edits", "edits":
		return config.PermModeAcceptEdits
	case "ask", "default":
		return config.PermModeAsk
	case "bypass", "bypasspermissions", "bypass_permissions", "yolo":
		return config.PermModeBypass
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// skipSpace moves past spaces and tabs, and past line breaks too when
// newlines is set.
func skipSpace(text string, pos int, newlines bool) int {
	for pos < len(text) {
		switch text[pos] {
		case ' ', '\t':
			pos++
		case '\n', '\r':
			if !newlines {
				return pos
			}
			pos++
		default:
			return pos
		}
	}
	return pos
}

// wordEnd returns the end of the word starting at pos.
func wordEnd(text string, pos int) int {
	for pos < len(text) {
		switch text[pos] {
		case ' ', '\t', '\n', '\r':
			return pos
		}
		pos++
	}
	return pos
}

// CommandRow is one built-in command as a catalog lists it: the settings
// commands and the deterministic actions (/compact, /export, /plugin). The
// web composer, the console menu and the ACP command list all read it.
type CommandRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Kind is CommandKindSetting or CommandKindAction.
	Kind string `json:"kind"`
	// Setting is the setting a settings command changes.
	Setting string `json:"setting,omitempty"`
	// Hint is the argument hint, for example "<model id> [--once|--count=N]".
	Hint    string   `json:"hint,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
	// Choices are the values the argument may take for the session asked
	// about: the models, the reasoning levels of its model, the modes.
	Choices []string `json:"choices,omitempty"`
	// Value is what a command without an argument sets (/plan -> plan).
	Value string `json:"value,omitempty"`
	// DuringTurn says the command may be sent while a turn runs: a settings
	// command applies at once, an action waits for the turn to end.
	DuringTurn bool `json:"duringTurn"`
}

// BuiltinCommandRows lists the built-in commands for a session: the settings
// commands first, then the actions skills.BuiltinCommands names. st may be
// nil (no session yet): the reasoning choices then stay empty.
func BuiltinCommandRows(cfg *config.Config, st *State, actions []CommandRow) []CommandRow {
	out := make([]CommandRow, 0, len(settingsCommands)+len(actions))
	for _, c := range settingsCommands {
		row := CommandRow{
			Name:        c.Name,
			Description: c.Description,
			Kind:        CommandKindSetting,
			Setting:     c.Setting,
			Hint:        c.Hint,
			Aliases:     append([]string(nil), c.Aliases...),
			DuringTurn:  true,
		}
		if c.TakesValue() {
			row.Choices = c.Choices(cfg, st)
		} else {
			row.Value = c.value
		}
		out = append(out, row)
	}
	return append(out, actions...)
}

// actionHints are the argument hints of the deterministic actions that take
// options, keyed by command name.
var actionHints = map[string]string{
	"compact": "[--model <id>] [instructions]",
}

// ActionCommandRows lists the deterministic actions (skills.BuiltinCommands)
// as catalog rows: /compact while compaction is enabled, /export, /plugin.
func ActionCommandRows(cfg *config.Config) []CommandRow {
	sums := skills.BuiltinCommands(cfg != nil && cfg.Compaction.IsEnabled())
	out := make([]CommandRow, 0, len(sums))
	for _, s := range sums {
		out = append(out, CommandRow{Name: s.Name, Description: s.Description, Kind: CommandKindAction, Hint: actionHints[s.Name]})
	}
	return out
}

// changeValues lists the values a change names, keyed by setting.
func changeValues(ch SettingsChange) map[string]string {
	values := make(map[string]string, 4)
	if ch.Model != nil {
		values[SettingModel] = *ch.Model
	}
	if ch.Reasoning != nil {
		v := *ch.Reasoning
		if v == "" {
			v = config.ReasoningDefault
		}
		values[SettingReasoning] = v
	}
	if ch.Mode != nil {
		values[SettingMode] = *ch.Mode
	}
	if ch.PermissionMode != nil {
		values[SettingPermissionMode] = *ch.PermissionMode
	}
	return values
}

// SettingsChangeNotice describes a change the way the manager's notice does,
// without validating it: for a client that has to say what it asked for
// before a server confirms it (a remote session the server has not created).
func SettingsChangeNotice(ch SettingsChange) string {
	values := changeValues(ch)
	scope := "for this session"
	if ch.Turns > 0 {
		scope = turnsScope(ch.Turns)
	}
	return settingsNotice(values, sortedSettings(values), scope)
}

// FormatSettingsCommands writes changes back as the command lines that ask
// for them, one per setting: what a client that holds a change for a session
// the server has not created yet puts at the start of that session's first
// prompt, where the server takes it like any other.
func FormatSettingsCommands(changes []SettingsChange) string {
	var lines []string
	for _, ch := range changes {
		flag := ""
		if ch.Turns > 0 {
			flag = " --count=" + strconv.Itoa(ch.Turns)
		}
		values := changeValues(ch)
		for _, name := range sortedSettings(values) {
			v := values[name]
			switch name {
			case SettingModel:
				lines = append(lines, "/model "+v+flag)
			case SettingReasoning:
				lines = append(lines, "/reasoning "+v+flag)
			case SettingMode:
				lines = append(lines, "/"+v+flag)
			case SettingPermissionMode:
				lines = append(lines, "/permissions "+v+flag)
			}
		}
	}
	return strings.Join(lines, "\n")
}
