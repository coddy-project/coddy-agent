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
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// WorktreeCommandName is the built-in slash command that moves the chat
// between the main checkout and a worktree, the operator's side of
// worktree_create and worktree_exit.
const WorktreeCommandName = "worktree"

// worktreeUsage closes every reply that could not run the command as typed.
const worktreeUsage = "Usage: /worktree shows where the chat works and the worktrees of its repository; " +
	"/worktree <branch> [--from-head] opens a worktree for the branch and moves the chat there (a new branch starts at " +
	"origin's default branch, or at the current commit with --from-head); /worktree exit [--remove [--force]] brings " +
	"the chat back to the main checkout, --remove removes the worktree too and --force drops its uncommitted changes."

// worktreeCommand is the parsed form of one /worktree invocation.
type worktreeCommand struct {
	// Branch is the branch to open; Exit brings the chat back instead; neither
	// asks where the chat works.
	Branch   string
	Exit     bool
	FromHead bool
	Remove   bool
	Force    bool
	// Problem says why the words do not make a command.
	Problem string
}

// parseWorktreeCommand detects the built-in /worktree command. The first word
// that is not an option is "exit" or the branch; --from-head goes with a
// branch, --remove and --force with exit.
func parseWorktreeCommand(text string) (worktreeCommand, bool) {
	t := strings.TrimSpace(text)
	const cmd = "/" + WorktreeCommandName
	rest, found := strings.CutPrefix(t, cmd)
	if !found || (rest != "" && !strings.ContainsAny(rest[:1], " \t\r\n")) {
		return worktreeCommand{}, false
	}
	var c worktreeCommand
	var words []string
	for _, w := range strings.Fields(rest) {
		switch w {
		case "--from-head":
			c.FromHead = true
		case "--remove":
			c.Remove = true
		case "--force":
			c.Force = true
		default:
			if strings.HasPrefix(w, "-") {
				c.Problem = fmt.Sprintf("unknown option %s", w)
				return c, true
			}
			words = append(words, w)
		}
	}
	switch {
	case len(words) > 1:
		c.Problem = "one branch at a time"
	case len(words) == 1 && words[0] == "exit":
		c.Exit = true
	case len(words) == 1:
		c.Branch = words[0]
	}
	switch {
	case c.Problem != "":
	case c.FromHead && c.Branch == "":
		c.Problem = "--from-head goes with a branch"
	case (c.Remove || c.Force) && !c.Exit:
		c.Problem = "--remove and --force go with exit"
	case c.Force && !c.Remove:
		c.Problem = "--force goes with --remove"
	}
	return c, true
}

// runWorktreeCommand executes /worktree deterministically (no LLM call): it
// moves the session the way worktree_create and worktree_exit move it, and
// answers with one assistant message, like the other built-ins. A surface
// that took the worktree tools away from the turn (a messenger user who is
// not the bot's admin) takes the command away too.
func (a *Agent) runWorktreeCommand(ctx context.Context, c worktreeCommand, rawCommand string) (string, error) {
	a.addUserCommandMessage(rawCommand)
	text := a.worktreeCommandReply(ctx, c)
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

func (a *Agent) worktreeCommandReply(ctx context.Context, c worktreeCommand) string {
	if c.Problem != "" {
		return "/worktree: " + c.Problem + ".\n\n" + worktreeUsage
	}
	if st := sessionStatePtr(a.state); st != nil {
		if r := st.GetTurnRestriction(); r != nil && (!r.Allows(tools.ToolWorktreeCreate) || !r.Allows(tools.ToolWorktreeExit)) {
			note := strings.TrimSpace(r.Note)
			if note == "" {
				note = "this conversation cannot move to another folder"
			}
			return "/worktree: " + note + "."
		}
	}
	if c.Branch == "" && !c.Exit {
		return worktreeStatus(a.state.GetCWD())
	}
	if a.workspaceSwitcher == nil {
		return "/worktree: this surface cannot move the chat to another folder."
	}
	env := &tools.Env{
		CWD:        a.state.GetCWD(),
		SessionID:  a.state.GetID(),
		Background: a.backgroundPool(strings.TrimSpace(a.state.GetPersistedSessionDir())),
	}
	a.wireWorkspaceTool(env)
	if c.Exit {
		left, err := tools.LeaveWorktree(ctx, env, c.Remove, c.Force)
		if err != nil {
			return "/worktree exit: " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "The chat is back in the main checkout %s.", left.Path)
		switch {
		case left.Removed && left.BranchDeleted:
			fmt.Fprintf(&b, " The worktree %s and its branch %s are removed.", left.Left, left.Branch)
		case left.Removed && left.BranchKept != "":
			fmt.Fprintf(&b, " The worktree %s is removed; %s.", left.Left, left.BranchKept)
		case left.Removed:
			fmt.Fprintf(&b, " The worktree %s is removed.", left.Left)
		case left.Branch != "":
			fmt.Fprintf(&b, " The worktree %s stays; /worktree %s goes back to it.", left.Left, left.Branch)
		default:
			fmt.Fprintf(&b, " The worktree %s stays.", left.Left)
		}
		return b.String()
	}
	from := gitws.BaseDefault
	if c.FromHead {
		from = gitws.BaseHead
	}
	opened, err := tools.OpenWorktree(ctx, env, c.Branch, from)
	if err != nil {
		return "/worktree " + c.Branch + ": " + err.Error()
	}
	verb := "opened"
	if opened.Created {
		verb = "created"
	}
	return fmt.Sprintf("The chat now works in the worktree %s on branch %s (%s). The main checkout is %s; /worktree exit brings the chat back.",
		opened.Path, opened.Branch, verb, opened.MainCheckout)
}

// worktreeStatus answers /worktree alone: where the chat works and the
// worktrees of its repository.
func worktreeStatus(cwd string) string {
	info := gitws.Describe(cwd)
	if !info.IsGitRepo {
		return fmt.Sprintf("The chat works in %s, which is not in a git repository.\n\n%s", cwd, worktreeUsage)
	}
	var b strings.Builder
	if wt, err := gitws.WorktreeOf(cwd); err == nil {
		fmt.Fprintf(&b, "The chat works in the worktree %s on branch %s; the main checkout is %s.", wt.Path, worktreeBranchLabel(wt.Branch), wt.MainCheckout)
	} else if errors.Is(err, gitws.ErrNotLinkedWorktree) {
		fmt.Fprintf(&b, "The chat works in the main checkout %s on branch %s.", info.RepoRoot, worktreeBranchLabel(info.Branch))
	} else {
		fmt.Fprintf(&b, "The chat works in %s.", cwd)
	}
	if len(info.Worktrees) > 1 {
		b.WriteString("\nWorktrees of this repository:")
		for _, wt := range info.Worktrees {
			fmt.Fprintf(&b, "\n- %s (%s)", wt.Path, worktreeBranchLabel(wt.Branch))
			if wt.Main {
				b.WriteString(", the main checkout")
			}
		}
	}
	// A blank line ends the list: Markdown would take the usage for a
	// continuation of its last item.
	b.WriteString("\n\n" + worktreeUsage)
	return b.String()
}

// worktreeBranchLabel names a branch, or says there is none checked out.
func worktreeBranchLabel(branch string) string {
	if strings.TrimSpace(branch) == "" {
		return "a detached HEAD"
	}
	return branch
}
