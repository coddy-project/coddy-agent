import type { BackgroundTask } from "./types";

/**
 * The opener for the background tasks panel, sitting under the last message the
 * way a task line does in Claude Code.
 *
 * It belongs at the bottom of the transcript rather than in the nav rail
 * because the tasks belong to this chat: the thing that started them is right
 * above it.
 */
export function BackgroundTasksChip(props: {
  tasks: BackgroundTask[];
  onOpen: () => void;
}) {
  const running = props.tasks.filter((t) => t.running).length;
  const total = props.tasks.length;

  // Nothing has ever run in this chat, so there is nothing to open.
  if (total === 0) {
    return null;
  }

  const live = running > 0;
  const label = live
    ? `${running} running task${running === 1 ? "" : "s"}`
    : `${total} background task${total === 1 ? "" : "s"}`;

  return (
    <div className="bgtask-chip-row">
      <button
        type="button"
        className={["bgtask-chip", live ? "is-running" : ""]
          .filter(Boolean)
          .join(" ")}
        data-testid="bgtask-chip"
        aria-label={`Open background tasks: ${label}`}
        onClick={props.onOpen}
      >
        <span
          className={`bgtask-dot ${live ? "bgtask-dot--running" : "bgtask-dot--muted"}`}
          aria-hidden="true"
        />
        <span className="bgtask-chip-text">{label}</span>
      </button>
    </div>
  );
}
