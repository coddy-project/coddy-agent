import { useT } from "../i18n/I18nProvider";
import { getLocale } from "../i18n/i18n";
import { hasEdits, type WorkingCopy } from "../changes/workingCopy";
import {
  BRANCH_CHARS,
  folderChipLabel,
  middleTruncate,
  type WorkspaceContext,
} from "./workspaceContext";
import { BranchIcon, FolderIcon } from "./workspaceIcons";
import { WorkspaceChips } from "./WorkspaceChips";

export { middleTruncate } from "./workspaceContext";

/** What the plate needs to offer the folder, the branch and the worktree as a choice. */
export type WorkspacePick = {
  worktreePref: boolean;
  onPickFolder: (path: string) => void;
  onPickBranch: (branch: string, worktree: boolean) => void;
  onWorktreeToggle: () => void;
  /** The menus open upward from a docked composer, downward on the start screen. */
  opensUp: boolean;
};

/**
 * The plate over the composer, joined to the top of the composer card: where
 * the chat works. Before the chat starts (`pick`) that is still a choice - the
 * folder and the branch are buttons that open their menus and the worktree a
 * checkbox (WorkspaceChips), the branch and the worktree only in a git
 * repository. Once the chat runs it is a fact: the repository, its branch -
 * the tooltip naming the worktree when the chat runs in a linked one - and, at
 * the right edge in a light frame, what git reports as changed there, which
 * opens the edits window. Git's count waits for a session.
 */
export function WorkspaceBar(props: {
  context: WorkspaceContext;
  workingCopy?: WorkingCopy | undefined;
  onOpenEdits?: (() => void) | undefined;
  pick?: WorkspacePick | undefined;
}) {
  const { t, tp } = useT();
  const ctx = props.context;
  const number = new Intl.NumberFormat(getLocale());
  const branch = ctx.branch || t("workspace.detached");
  const copy = props.workingCopy;
  const showEdits =
    !props.pick && !!copy && props.onOpenEdits !== undefined && hasEdits(copy);
  const totals = copy?.changes.totals;

  return (
    <div
      className={`workspace-bar${props.pick ? " workspace-bar--pick" : ""}`}
      role="group"
      aria-label={t("workspaceBar.label")}
      data-testid="workspace-bar"
    >
      {props.pick ? (
        <WorkspaceChips
          context={ctx}
          worktreePref={props.pick.worktreePref}
          onPickFolder={props.pick.onPickFolder}
          onPickBranch={props.pick.onPickBranch}
          onWorktreeToggle={props.pick.onWorktreeToggle}
          opensUp={props.pick.opensUp}
        />
      ) : (
        <>
          <span
            className="workspace-bar-item workspace-bar-repo"
            title={ctx.path}
            data-testid="workspace-bar-repo"
          >
            <FolderIcon />
            <span className="workspace-bar-text">{folderChipLabel(ctx)}</span>
          </span>
          {ctx.is_git_repo ? (
            <span
              className="workspace-bar-item workspace-bar-branch"
              title={
                ctx.is_worktree
                  ? t("workspaceBar.worktreeTitle", { path: ctx.path, branch })
                  : t("workspaceBar.branchTitle", { branch })
              }
              data-testid="workspace-bar-branch"
            >
              <BranchIcon worktree={ctx.is_worktree === true} />
              <span className="workspace-bar-text">
                {middleTruncate(branch, BRANCH_CHARS)}
              </span>
            </span>
          ) : null}
        </>
      )}
      {showEdits && totals ? (
        <button
          type="button"
          className="workspace-bar-edits"
          aria-label={tp("workspaceBar.editsLabel", totals.files)}
          title={tp("workspaceBar.editsLabel", totals.files)}
          data-testid="workspace-bar-edits"
          onClick={() => props.onOpenEdits?.()}
        >
          <span className="changes-add">{"+" + number.format(totals.additions)}</span>
          <span className="changes-del">{"−" + number.format(totals.deletions)}</span>
        </button>
      ) : null}
    </div>
  );
}
