import { useT } from "../i18n/I18nProvider";
import { getLocale } from "../i18n/i18n";
import { hasEdits, type WorkingCopy } from "../changes/workingCopy";
import { folderChipLabel, type WorkspaceContext } from "./workspaceContext";

/** How many characters of a branch name the bar shows before it cuts the middle. */
const BRANCH_CHARS = 24;

/**
 * Cuts the middle out of a long name, keeping both ends: a branch is told
 * apart by its prefix (feat/, fix/) and by its last words alike.
 */
export function middleTruncate(text: string, max: number): string {
  if (text.length <= max) {
    return text;
  }
  if (max < 5) {
    // Too little room for both ends: the start alone, cut.
    return text.slice(0, Math.max(1, max));
  }
  const keep = max - 1;
  // A little more of the end than of the start: the last words tell
  // branches of one prefix apart.
  const head = Math.max(1, Math.ceil(keep / 2) - 2);
  const tail = keep - head;
  return text.slice(0, head) + "…" + text.slice(text.length - tail);
}

function FolderIcon() {
  return (
    <svg className="workspace-bar-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <path d="M1.75 3.25h4.1l1.5 1.5h6.9c.28 0 .5.22.5.5v7.5c0 .28-.22.5-.5.5H1.75a.5.5 0 0 1-.5-.5v-9c0-.28.22-.5.5-.5Z" strokeLinejoin="round" />
    </svg>
  );
}

function BranchIcon() {
  return (
    <svg className="workspace-bar-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true">
      <circle cx="4.5" cy="3.5" r="1.5" />
      <circle cx="4.5" cy="12.5" r="1.5" />
      <circle cx="11.5" cy="5.5" r="1.5" />
      <path d="M4.5 5v6M11.5 7c0 2.5-2 3.5-5.5 4" strokeLinecap="round" />
    </svg>
  );
}

/** A folder holding a branch: this chat works in a checkout of its own. */
function WorktreeIcon() {
  return (
    <svg className="workspace-bar-icon workspace-bar-icon--worktree" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.3" aria-hidden="true" data-testid="workspace-bar-worktree">
      <path d="M1.75 2.75h4.1l1.5 1.5h6.9c.28 0 .5.22.5.5v8c0 .28-.22.5-.5.5H1.75a.5.5 0 0 1-.5-.5v-9.5c0-.28.22-.5.5-.5Z" strokeLinejoin="round" />
      <circle cx="6" cy="7.25" r="1" />
      <circle cx="6" cy="11" r="1" />
      <circle cx="10" cy="8.25" r="1" />
      <path d="M6 8.25v1.75M10 9.25c0 1-1 1.5-3 1.75" strokeLinecap="round" />
    </svg>
  );
}

/**
 * The bar over the composer of a running chat, a plate joined to the top of
 * the composer card: the repository the chat works in, its branch - with a
 * worktree mark when the chat runs in a linked worktree - and, at the right
 * edge, what git reports as changed there, which opens the edits window. The
 * start screen has the folder, branch and worktree chips in the composer
 * instead, where they are still a choice; once the chat runs they are a fact.
 */
export function WorkspaceBar(props: {
  context: WorkspaceContext;
  workingCopy: WorkingCopy;
  onOpenEdits?: (() => void) | undefined;
}) {
  const { t, tp } = useT();
  const ctx = props.context;
  const totals = props.workingCopy.changes.totals;
  const number = new Intl.NumberFormat(getLocale());
  const branch = ctx.branch || t("workspace.detached");
  const showEdits = props.onOpenEdits !== undefined && hasEdits(props.workingCopy);

  return (
    <div className="workspace-bar" role="group" aria-label={t("workspaceBar.label")} data-testid="workspace-bar">
      <span className="workspace-bar-item workspace-bar-repo" title={ctx.path} data-testid="workspace-bar-repo">
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
          {ctx.is_worktree ? <WorktreeIcon /> : <BranchIcon />}
          <span className="workspace-bar-text">{middleTruncate(branch, BRANCH_CHARS)}</span>
        </span>
      ) : null}
      {showEdits ? (
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
