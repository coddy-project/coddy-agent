package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ExportCommandName is the built-in slash command that writes the session
// transcript to a file inside the workspace (an analog of qwen-code's /export).
const ExportCommandName = "export"

// exportUsage closes every reply that could not run the command as typed.
const exportUsage = "Usage: /export [md|html|json|jsonl] [path] [--no-tools] [--no-thinking]. The path is relative to " +
	"the session workspace: a directory receives coddy-export-<timestamp>.<ext>, any other path names the file, and " +
	"when the format is omitted it follows the file extension (markdown by default). --no-tools leaves out tool calls " +
	"and results, --no-thinking leaves out the model's reasoning."

// exportCommandArgs is the parsed form of one /export invocation.
type exportCommandArgs = session.ExportRequest

// parseExportCommand detects the built-in /export command. Words after the
// command are whitespace-separated: --options may appear anywhere, the first
// remaining word is the format when it names one, and the rest is the target
// path (joined by single spaces, so paths with spaces survive).
func parseExportCommand(text string) (exportCommandArgs, bool) {
	t := strings.TrimSpace(text)
	const cmd = "/" + ExportCommandName
	if t == cmd {
		return exportCommandArgs{}, true
	}
	for _, sep := range []string{" ", "\t", "\n", "\r"} {
		rest, found := strings.CutPrefix(t, cmd+sep)
		if !found {
			continue
		}
		var args exportCommandArgs
		var words []string
		for _, w := range strings.Fields(rest) {
			switch {
			case w == "--no-tools":
				args.Options.NoTools = true
			case w == "--no-thinking":
				args.Options.NoThinking = true
			case strings.HasPrefix(w, "--"):
				args.UnknownOptions = append(args.UnknownOptions, w)
			default:
				words = append(words, w)
			}
		}
		if len(words) > 0 {
			if _, ok := session.ParseExportFormat(words[0]); ok {
				args.Format = words[0]
				words = words[1:]
			}
		}
		args.Target = strings.Join(words, " ")
		return args, true
	}
	return exportCommandArgs{}, false
}

// runExportCommand executes /export deterministically (no LLM call). The
// export is built before the command row is persisted, so the file holds the
// conversation up to the command; the outcome is streamed as one agent
// message chunk and stored as an assistant message like the other built-ins.
func (a *Agent) runExportCommand(_ context.Context, args exportCommandArgs, rawCommand string) (string, error) {
	text := a.exportTranscript(args)
	a.addUserCommandMessage(rawCommand)
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

// exportTranscript gathers the session metadata, writes the export, and
// returns the reply text for the transcript.
func (a *Agent) exportTranscript(args exportCommandArgs) string {
	cwd := a.state.GetCWD()
	in := session.ExportInput{
		SessionID:  a.state.GetID(),
		CWD:        cwd,
		Model:      a.state.EffectiveModelID(a.cfg),
		Messages:   a.state.GetMessages(),
		ExportedAt: time.Now().UTC(),
	}
	if st := sessionStatePtr(a.state); st != nil {
		in.Title = st.ConversationTitle()
	}
	if dir := strings.TrimSpace(a.state.GetPersistedSessionDir()); dir != "" {
		if stats, err := session.ReadSessionStats(dir); err == nil {
			in.Stats = stats
		}
	}
	in.GitBranch = gitws.Describe(cwd).Branch

	res, err := session.ExportSession(in, args)
	if err != nil {
		if errors.Is(err, session.ErrExportOutsideWorkspace) {
			return fmt.Sprintf("Export failed: the target must stay inside the session workspace (%s).\n%s", cwd, exportUsage)
		}
		return "Export failed: " + err.Error() + "\n" + exportUsage
	}
	return fmt.Sprintf("Session exported to %s: %s\nFull path: %s (%d transcript entries)",
		res.Format.DisplayName(), res.Target.Display, res.Target.Path, res.Entries)
}
