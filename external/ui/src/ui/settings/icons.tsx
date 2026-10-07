/**
 * Icons the settings surfaces share (Skills, MCP servers, the provider model
 * list): lucide-style strokes drawn at the size of a 40px settings icon
 * button. One drawing per glyph, so a sync arrow looks the same on every tab.
 */

/** Two arrows chasing each other: a fetch or sync action. */
export function IconSync(props: { className?: string | undefined }) {
  return (
    <svg
      className={props.className}
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M21 2v6h-6" />
      <path d="M3 12a9 9 0 0 1 15-6.7L21 8" />
      <path d="M3 22v-6h6" />
      <path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
    </svg>
  );
}

/** Checkmark: done, or already present. */
export function IconCheck() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.1"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

/** Plus: add the row's item somewhere. */
export function IconPlus() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}

/**
 * Shield with a check: the workspace trust control of an entry that arrived
 * with the checkout (an MCP server, a subagent definition, a skill
 * marketplace), and the always-trusted mark of a built-in one.
 */
export function IconShield() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M12 3l7 3v6c0 4.4-3 8.2-7 9-4-.8-7-4.6-7-9V6Z" />
      <path d="M9 12l2 2 4-4" />
    </svg>
  );
}
