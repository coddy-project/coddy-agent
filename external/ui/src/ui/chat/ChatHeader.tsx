import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { countTasks } from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";

export function ChatHeader(props: {
  title: string;
  editable?: boolean;
  onTitleSave?: (title: string) => void;
  /** Background tasks of this chat, counted on the control `onOpenTasks` puts at the right edge. */
  tasks?: BackgroundTask[];
  onOpenTasks?: () => void;
  /** The Tasks panel is showing. */
  tasksOpen?: boolean;
}) {
  const { t } = useT();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(props.title || "");
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (!editing) {
      setValue(props.title || "");
    }
  }, [props.title, editing]);

  useEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [editing]);

  const canEdit = !!props.editable && !!props.onTitleSave;

  return (
    <header className="chat-header">
      <div className="chat-title" id="chat-title">
        {canEdit && editing ? (
          <input
            ref={inputRef}
            className="chat-title-input"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onBlur={() => {
              setEditing(false);
              const t = value.trim();
              if (t && t !== (props.title || "").trim()) {
                props.onTitleSave?.(t);
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                (e.target as HTMLInputElement).blur();
              }
              if (e.key === "Escape") {
                setValue(props.title || "");
                setEditing(false);
              }
            }}
          />
        ) : (
          <button
            type="button"
            className={`chat-title-btn ${canEdit ? "is-editable" : ""}`}
            onClick={() => {
              if (canEdit) {
                setEditing(true);
              }
            }}
            aria-label={t("chat.chatTitleAriaLabel")}
          >
            {props.title || t("chat.newChat")}
          </button>
        )}
      </div>
      {props.onOpenTasks ? (
        <HeaderTasksControl
          tasks={props.tasks ?? []}
          onOpen={props.onOpenTasks}
          open={props.tasksOpen === true}
        />
      ) : null}
    </header>
  );
}

/**
 * The opener of the Tasks panel, and the one place a chat says how much runs in its
 * background. The header is sticky, so it stays in reach however far the reader has
 * scrolled, and it is there from the first message: a chat that has not run a task yet
 * reads "Tasks", one that has reads how many run out of how many there are.
 */
function HeaderTasksControl(props: {
  tasks: BackgroundTask[];
  onOpen: () => void;
  open: boolean;
}) {
  const { t } = useT();
  const { running, total } = countTasks(props.tasks);
  const live = running > 0;
  const aria =
    total === 0
      ? t("tasks.header.ariaEmpty")
      : t("tasks.header.aria", { running, total });
  return (
    <button
      type="button"
      className={[
        "chat-header-tasks",
        live ? "is-running" : "",
        total > 0 ? "has-tasks" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid="chat-header-tasks"
      aria-label={aria}
      aria-expanded={props.open}
      title={aria}
      onClick={props.onOpen}
    >
      <span
        className={`bgtask-dot ${live ? "bgtask-dot--running" : "bgtask-dot--muted"}`}
        aria-hidden="true"
      />
      <span className="chat-header-tasks-label">{t("tasks.header.label")}</span>
      {total > 0 ? (
        <span
          className="chat-header-tasks-counts"
          data-testid="chat-header-tasks-counts"
        >
          {t("tasks.header.counts", { running, total })}
        </span>
      ) : null}
    </button>
  );
}
