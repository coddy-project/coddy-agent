package agent

import (
	"context"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// PluginCommandName is the built-in slash command that manages skill plugins and
// marketplaces from chat, mirroring the `coddy plugin ...` CLI.
const PluginCommandName = "plugin"

// parsePluginCommand detects the built-in /plugin command and returns the
// argument tokens after it (whitespace-split). ok is false for any other input.
func parsePluginCommand(text string) (args []string, ok bool) {
	t := strings.TrimSpace(text)
	const cmd = "/" + PluginCommandName
	if t == cmd {
		return nil, true
	}
	for _, sep := range []string{" ", "\t", "\n"} {
		if rest, found := strings.CutPrefix(t, cmd+sep); found {
			return strings.Fields(strings.TrimSpace(rest)), true
		}
	}
	return nil, false
}

// pluginCommandRefused says whether the turn may not run this /plugin
// invocation: a surface that took the tools away from the turn - a messenger
// user who is not the bot's admin - keeps it to the usage screen and the list
// of installed skills, since installing, updating or removing a skill changes
// what every session of the home runs.
func (a *Agent) pluginCommandRefused(args []string) (string, bool) {
	st := sessionStatePtr(a.state)
	if st == nil {
		return "", false
	}
	r := st.GetTurnRestriction()
	if r == nil || r.AllowedTools == nil {
		return "", false
	}
	if len(args) == 0 {
		return "", false
	}
	switch args[0] {
	case "help", "-h", "--help", "list":
		return "", false
	}
	note := strings.TrimSpace(r.Note)
	if note == "" {
		note = "this conversation cannot change the installed skills"
	}
	return note, true
}

// runPluginCommand executes a /plugin invocation deterministically (no LLM
// call), shares the dispatcher with the CLI, and surfaces the result as an
// assistant message. The command text is persisted as a user message so it
// shows in the transcript like any other input.
func (a *Agent) runPluginCommand(ctx context.Context, args []string, rawCommand string) (string, error) {
	a.addUserCommandMessage(rawCommand)
	var text string
	if note, refused := a.pluginCommandRefused(args); refused {
		text = "/plugin: " + note + "."
	} else {
		out, err := skills.RunPluginCommand(ctx, a.cfg, a.state.GetCWD(), args)
		text = out
		if err != nil {
			text = err.Error()
		}
	}
	if a.server != nil {
		_ = a.server.SendSessionUpdate(a.state.GetID(), acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: text},
		})
	}
	a.state.AddMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   text,
		Model:     a.state.EffectiveModelID(a.cfg),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return string(acp.StopReasonEndTurn), nil
}
