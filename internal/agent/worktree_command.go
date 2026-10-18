package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// WorktreeCommandName is the built-in slash command that moves the chat
// between the main checkout and a worktree, the operator's side of
// worktree_open and worktree_exit.
const WorktreeCommandName = "worktree"

// worktreeUsage closes every reply that could not run the command as typed.
const worktreeUsage = "Usage: /worktree shows where the chat works and the worktrees of its repository; " +
	"/worktree <branch> [--from-head] moves the chat into the worktree of the branch, opening one when there is none (a new branch starts at " +
	"origin's default branch, or at the current commit with --from-head); /worktree <path> moves it into an existing worktree " +
	"(a path starts with /, ./, ../ or ~ and is read from the chat's folder); /worktree exit [--remove [--force]] brings the chat back to the main checkout, " +
	"--remove removes the worktree too and --force drops its uncommitted changes, kept as a snapshot."

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
// moves the session the way worktree_open and worktree_exit move it, and
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
		if r := st.GetTurnRestriction(); r != nil && (!r.Allows(tools.ToolWorktreeOpen) || !r.Allows(tools.ToolWorktreeExit)) {
			note := strings.TrimSpace(r.Note)
			if note == "" {
				note = "this conversation cannot move to another folder"
			}
			return "/worktree: " + note + "."
		}
	}
	env := &tools.Env{
		CWD:        a.state.GetCWD(),
		SessionID:  a.state.GetID(),
		Background: a.backgroundPool(strings.TrimSpace(a.state.GetPersistedSessionDir())),
	}
	a.wireWorkspaceTool(env)
	if c.Branch == "" && !c.Exit {
		return worktreeStatus(env)
	}
	if a.workspaceSwitcher == nil {
		return "/worktree: this surface cannot move the chat to another folder."
	}
	if c.Exit {
		left, err := tools.LeaveWorktree(ctx, env, c.Remove, c.Force)
		if err != nil {
			return "/worktree exit: " + err.Error()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "The chat is back in the main checkout %s.", left.Path)
		switch {
		case left.Gone:
			fmt.Fprintf(&b, " The worktree folder %s was already gone.", left.Left)
		case left.Removed && left.Snapshot != "" && left.BranchDeleted:
			fmt.Fprintf(&b, " The worktree %s and its branch %s are removed; its uncommitted work is kept as %s (git cherry-pick --no-commit %s brings it back).",
				left.Left, left.Branch, left.Snapshot, left.Snapshot)
		case left.Removed && left.Snapshot != "":
			fmt.Fprintf(&b, " The worktree %s is removed; its uncommitted work is kept as %s (git cherry-pick --no-commit %s brings it back)%s.",
				left.Left, left.Snapshot, left.Snapshot, keptNote(left.BranchKept))
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
	target := tools.WorktreeTarget{Branch: c.Branch, Base: from}
	if looksLikePath(c.Branch) {
		path := expandHome(c.Branch)
		// A path the operator types is read from the chat's folder.
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.CWD, path)
		}
		target = tools.WorktreeTarget{Path: path}
		if c.FromHead {
			return "/worktree: --from-head goes with a branch, not a path.\n\n" + worktreeUsage
		}
	}
	opened, err := tools.OpenWorktree(ctx, env, target)
	if err != nil {
		return "/worktree " + c.Branch + ": " + err.Error()
	}
	verb := "opened"
	if opened.Created {
		verb = "created"
	}
	return fmt.Sprintf("The chat now works in the worktree %s on %s (%s). The main checkout is %s; /worktree exit brings the chat back.",
		opened.Path, worktreeBranchLabelOf(opened.Branch), verb, opened.MainCheckout)
}

// worktreeStatus answers /worktree alone: where the chat works, and every
// worktree of its repository with where it stands (tools.ListWorktrees, what
// worktree_list answers the model).
func worktreeStatus(env *tools.Env) string {
	listing, err := tools.ListWorktrees(env)
	if err != nil {
		return fmt.Sprintf("The chat works in %s, which is not in a git repository.\n\n%s", env.CWD, worktreeUsage)
	}
	var b strings.Builder
	for _, wt := range listing.Worktrees {
		if !wt.Current {
			continue
		}
		if wt.Main {
			fmt.Fprintf(&b, "The chat works in the main checkout %s on %s.", wt.Path, worktreeBranchLabelOf(wt.Branch))
		} else {
			fmt.Fprintf(&b, "The chat works in the worktree %s on %s; the main checkout is %s.", wt.Path, worktreeBranchLabelOf(wt.Branch), listing.MainCheckout)
		}
	}
	b.WriteString("\n\nWorktrees of this repository:")
	for _, wt := range listing.Worktrees {
		fmt.Fprintf(&b, "\n- %s (%s)", wt.Path, worktreeBranchLabel(wt.Branch))
		var facts []string
		if wt.Main {
			facts = append(facts, "the main checkout")
		}
		if wt.Current {
			facts = append(facts, "this chat")
		}
		if wt.Missing {
			facts = append(facts, "its folder is gone")
		} else {
			if wt.Head != "" {
				facts = append(facts, fmt.Sprintf("at %s %q", wt.Head, wt.Subject))
			}
			if wt.Uncommitted > 0 {
				facts = append(facts, fmt.Sprintf("%d uncommitted", wt.Uncommitted))
			}
			if wt.Ahead > 0 || wt.Behind > 0 {
				facts = append(facts, fmt.Sprintf("%d ahead, %d behind origin/%s", wt.Ahead, wt.Behind, listing.BaseBranch))
			}
			if wt.Unpushed > 0 {
				facts = append(facts, fmt.Sprintf("%d unpushed", wt.Unpushed))
			}
			if wt.Locked {
				facts = append(facts, "locked")
			}
		}
		if len(wt.Sessions) > 0 {
			facts = append(facts, fmt.Sprintf("%d other chat(s)", len(wt.Sessions)))
		}
		if len(facts) > 0 {
			b.WriteString(": " + strings.Join(facts, ", "))
		}
	}
	// A blank line ends the list: Markdown would take the usage for a
	// continuation of its last item.
	b.WriteString("\n\n" + worktreeUsage)
	return b.String()
}

// keptNote adds why a branch stayed to a sentence, when it did.
func keptNote(kept string) string {
	if kept == "" {
		return ""
	}
	return "; " + kept
}

// worktreeBranchLabelOf names the branch a worktree is on for a reply.
func worktreeBranchLabelOf(branch string) string {
	if strings.TrimSpace(branch) == "" {
		return "a detached HEAD"
	}
	return "branch " + branch
}

// looksLikePath tells a folder from a branch in /worktree's argument: a
// branch name may hold slashes too, so only these starts make a path.
func looksLikePath(word string) bool {
	return strings.HasPrefix(word, "/") || strings.HasPrefix(word, "./") ||
		strings.HasPrefix(word, "../") || word == "~" || strings.HasPrefix(word, "~/")
}

// expandHome turns a leading ~ into the home folder.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// worktreeBranchLabel names a branch, or says there is none checked out.
func worktreeBranchLabel(branch string) string {
	if strings.TrimSpace(branch) == "" {
		return "a detached HEAD"
	}
	return branch
}
