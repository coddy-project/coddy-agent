import { memo, useEffect, useId, useState } from "react";

import {
  todoStatusLabel,
  todoStatusMark,
} from "../chat/PermissionPromptPreview";
import {
  nextTodoChecklistTurn,
  todoChecklistFocus,
  todoChecklistProgress,
  todoChecklistShown,
  todoPlanOpen,
  type TodoChecklistTurn,
} from "../chat/todoChecklist";
import {
  readTodoChecklistCollapsed,
  writeTodoChecklistCollapsed,
} from "../chat/todoChecklistPrefs";
import type { TodoPlanEntry } from "../chat/todoToolPreview";
import { useT } from "../i18n/I18nProvider";

/** How long a card that stops showing stays for its exit (styles.css). */
export const TODO_CHECKLIST_EXIT_MS = 240;

/**
 * The session's todo checklist at the newest end of the transcript (DESIGN.md
 * "Todo checklist under the transcript"): every item with the status mark the
 * todo tool rows use, the item in progress as the current step. The mark of
 * every item in progress spins and its text shimmers (styles.css), so the list
 * says which item the agent is on now.
 *
 * The head folds the list down to the items in progress, a choice this
 * browser keeps for every chat. A card that stops showing
 * (`shown` turns false) stays with the list it had for its exit, hidden from
 * assistive technology, and goes once the exit is over.
 */
function TodoChecklistMessageImpl(props: {
  sessionId: string;
  entries: readonly TodoPlanEntry[] | undefined;
  shown: boolean;
}) {
  const { t } = useT();
  const listId = useId();
  const [collapsed, setCollapsed] = useState(readTodoChecklistCollapsed);
  // The list the card shows while it leaves: the last one it was shown with,
  // in this chat. The transcript stays mounted across a switch of chats, and
  // another chat must not play the exit of the list it left.
  const [last, setLast] = useState<{
    sessionId: string;
    entries: readonly TodoPlanEntry[] | undefined;
  }>(() => ({
    sessionId: props.sessionId,
    entries: props.shown ? props.entries : undefined,
  }));
  if (last.sessionId !== props.sessionId) {
    setLast({
      sessionId: props.sessionId,
      entries: props.shown ? props.entries : undefined,
    });
  } else if (props.shown && props.entries && props.entries !== last.entries) {
    setLast({ sessionId: props.sessionId, entries: props.entries });
  }
  const held = last.sessionId === props.sessionId ? last.entries : undefined;
  const leaving = !props.shown && held !== undefined;
  useEffect(() => {
    if (!leaving) return;
    const timer = window.setTimeout(
      () => setLast((was) => ({ ...was, entries: undefined })),
      TODO_CHECKLIST_EXIT_MS,
    );
    return () => window.clearTimeout(timer);
  }, [leaving]);

  const entries = props.shown ? props.entries : held;
  if (!entries || entries.length === 0) return null;
  const { done, total } = todoChecklistProgress(entries);
  const focus = todoChecklistFocus(entries);
  const seen = new Map<string, number>();
  const toggle = () => {
    const next = !collapsed;
    writeTodoChecklistCollapsed(next);
    setCollapsed(next);
  };
  return (
    <div
      className={[
        "message-row",
        "message-row-todo",
        leaving ? "is-leaving" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid="todo-checklist"
      {...(leaving ? { "aria-hidden": true as const, inert: true } : {})}
    >
      {/* The clip has no padding or border, so the row's grid track can close
          to nothing while the card leaves. */}
      <div className="todo-checklist-clip">
        <div className="todo-checklist">
          <button
            type="button"
            className="todo-checklist-head"
            aria-expanded={!collapsed}
            aria-controls={listId}
            onClick={toggle}
          >
            <span className="todo-checklist-title">
              {t("todo.checklist.title")}
            </span>{" "}
            <span className="todo-checklist-count">
              {done}/{total}
            </span>
          </button>
          <ul
            id={listId}
            className="todo-checklist-list"
            aria-label={t("todo.checklist.label", { done, total })}
          >
            {entries.map((entry, index) => {
              // A row is known by its text (and which of equal texts it is),
              // not by its place: an item added or removed above leaves it the
              // same row, so it does not come in again.
              const nth = seen.get(entry.content) ?? 0;
              seen.set(entry.content, nth + 1);
              const current = entry.status === "in_progress";
              const folded = collapsed && !focus.has(index);
              const glyph = todoStatusMark(entry.status);
              return (
                <li
                  key={`${nth}:${entry.content}`}
                  className={[
                    "todo-checklist-row",
                    `todo-checklist-row--${entry.status}`,
                    folded ? "is-folded" : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  aria-label={`${todoStatusLabel(entry.status, t)}: ${entry.content}`}
                  {...(current ? { "aria-current": "step" as const } : {})}
                  {...(folded ? { "aria-hidden": true as const } : {})}
                >
                  <div className="todo-checklist-row-body">
                    <span
                      className={`todo-tool-preview-mark todo-tool-preview-mark--${entry.status}`}
                      aria-hidden="true"
                    >
                      {glyph ? (
                        // Keyed by the status, so a new glyph pops in where the
                        // mark itself only changes its look.
                        <span
                          key={entry.status}
                          className="todo-checklist-glyph"
                        >
                          {glyph}
                        </span>
                      ) : null}
                    </span>
                    <span className="todo-checklist-content">
                      {entry.content}
                    </span>
                  </div>
                </li>
              );
            })}
          </ul>
        </div>
      </div>
    </div>
  );
}

export const TodoChecklistMessage = memo(TodoChecklistMessageImpl);

/**
 * Whether the transcript shows the session's list now (todoChecklistShown),
 * remembering across renders whether the turn on screen saw the list with
 * work left. The memory is adjusted while rendering, from the previous
 * render's inputs, so the render that closes the list already knows.
 */
export function useTodoChecklistShown(
  entries: readonly TodoPlanEntry[] | undefined,
  generating: boolean,
  sessionId: string,
  turnKey: number,
): boolean {
  const open = todoPlanOpen(entries);
  const [turn, setTurn] = useState<TodoChecklistTurn>(() => ({
    sessionId,
    generating,
    turnKey,
    sawOpen: generating && open,
  }));
  const next = nextTodoChecklistTurn(
    turn,
    sessionId,
    generating,
    turnKey,
    open,
  );
  if (next !== turn) setTurn(next);
  return todoChecklistShown(entries, generating, next.sawOpen);
}
