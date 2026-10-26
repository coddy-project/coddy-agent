/**
 * The glyphs of the plate over the composer (WorkspaceBar, and the picks of
 * WorkspaceChips on it): a folder outline for the repository and the git
 * branch for the branch, 14px in the plate's colour.
 */

export function FolderIcon() {
  return (
    <svg
      className="workspace-bar-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      aria-hidden="true"
    >
      <path
        d="M1.75 3.25h4.1l1.5 1.5h6.9c.28 0 .5.22.5.5v7.5c0 .28-.22.5-.5.5H1.75a.5.5 0 0 1-.5-.5v-9c0-.28.22-.5.5-.5Z"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * The git branch glyph. In a linked worktree it is the worktree mark too,
 * drawn the same: the tooltip of the branch says which folder it is.
 */
export function BranchIcon(props: { worktree?: boolean }) {
  return (
    <svg
      className="workspace-bar-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      aria-hidden="true"
      {...(props.worktree ? { "data-testid": "workspace-bar-worktree" } : {})}
    >
      <circle cx="4.5" cy="3.5" r="1.5" />
      <circle cx="4.5" cy="12.5" r="1.5" />
      <circle cx="11.5" cy="5.5" r="1.5" />
      <path d="M4.5 5v6M11.5 7c0 2.5-2 3.5-5.5 4" strokeLinecap="round" />
    </svg>
  );
}

/**
 * A cloud: the mark of a branch that is only on a remote, at the start of its
 * row in the branch list.
 */
export function RemoteBranchIcon(props: { label: string }) {
  return (
    <svg
      className="workspace-branch-remote-icon"
      data-testid="workspace-branch-remote-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      role="img"
      aria-label={props.label}
    >
      <path
        d="M4.5 12.5h7.25a2.75 2.75 0 0 0 .4-5.47A4 4 0 0 0 4.5 6.1a3.2 3.2 0 0 0 0 6.4Z"
        strokeLinejoin="round"
      />
    </svg>
  );
}
