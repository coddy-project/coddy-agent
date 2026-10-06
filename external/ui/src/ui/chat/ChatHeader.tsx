import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { countTasks } from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";
import { filesShortcutLabel } from "../files/filesHotkey";

export function ChatHeader(props: {
  title: string;
  editable?: boolean;
  onTitleSave?: (title: string) => void;
  /** Background tasks of this chat, counted on the control `onOpenTasks` puts at the right edge. */
  tasks?: BackgroundTask[];
  /** Opens the background tasks beside the chat, or puts them away when they show. */
  onOpenTasks?: () => void;
  /** The Tasks panel is showing. */
  tasksOpen?: boolean;
  /** Opens the session's edits beside the chat, or puts them away when they show.
   *  Absent when the edits are switched off (`ui.session_changes: false`). */
  onOpenEdits?: () => void;
  /** The edits are showing. */
  editsOpen?: boolean;
  /** Opens the window of the session's files. */
  onOpenFiles?: () => void;
  /** The files window is open. */
  filesOpen?: boolean;
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
                // Leaving the title is this Escape's step: a drawer open
                // beside the chat stays (nav/railEscape.ts).
                e.preventDefault();
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
        <HeaderViews
          tasks={props.tasks ?? []}
          onOpenTasks={props.onOpenTasks}
          tasksOpen={props.tasksOpen === true}
          {...(props.onOpenEdits ? { onOpenEdits: props.onOpenEdits } : {})}
          editsOpen={props.editsOpen === true}
          {...(props.onOpenFiles ? { onOpenFiles: props.onOpenFiles } : {})}
          filesOpen={props.filesOpen === true}
        />
      ) : null}
    </header>
  );
}

type ViewId = "tasks" | "edits" | "files";

/**
 * The views of a chat - its edits, its files, its background tasks - as a row
 * of buttons at the right of the sticky header, the way the views of a session
 * sit at the top of Claude's app. Edits and Files are an icon the size of the
 * rail's, with a short name beside it on a desktop and a tablet; a phone has
 * room for the icon alone, the size of its top bar's. Background tasks come
 * last, at the right edge: the dot and the running / total count of the
 * control the header always had, the dot in the accent while work is in
 * flight. Edits is there only when the session has edits (or they are on
 * show). The full name is in the tooltip everywhere. A button opens its view
 * and, pressed again, puts it away; the button of a view on show is pressed
 * (aria-pressed, the accent).
 */
function HeaderViews(props: {
  tasks: BackgroundTask[];
  onOpenTasks: () => void;
  tasksOpen: boolean;
  onOpenEdits?: () => void;
  editsOpen: boolean;
  onOpenFiles?: () => void;
  filesOpen: boolean;
}) {
  const { t } = useT();
  const { running, total } = countTasks(props.tasks);
  const live = running > 0;
  const tasksName =
    total === 0
      ? t("tasks.header.ariaEmpty")
      : t("tasks.header.aria", { running, total });

  const views: {
    id: ViewId;
    label: string;
    name: string;
    pressed: boolean;
    run: () => void;
  }[] = [];
  if (props.onOpenEdits) {
    views.push({
      id: "edits",
      label: t("changes.panelTitle"),
      name: t("chat.views.editsTitle"),
      pressed: props.editsOpen,
      run: props.onOpenEdits,
    });
  }
  if (props.onOpenFiles) {
    views.push({
      id: "files",
      label: t("files.title"),
      name: t("chat.views.filesTitle", { key: filesShortcutLabel() }),
      pressed: props.filesOpen,
      run: props.onOpenFiles,
    });
  }
  views.push({
    id: "tasks",
    label: t("tasks.header.label"),
    name: tasksName,
    pressed: props.tasksOpen,
    run: props.onOpenTasks,
  });

  return (
    <div
      className="chat-views"
      role="toolbar"
      aria-label={t("chat.views.label")}
      data-testid="chat-views"
    >
      {views.map((view) => (
        <span key={view.id} className="chat-view-tip-host">
          <button
            type="button"
            className={[
              "chat-view-btn",
              view.id === "tasks" ? "chat-view-btn--tasks" : "",
              view.pressed ? "is-active" : "",
              view.id === "tasks" && live ? "is-running" : "",
              view.id === "tasks" && total > 0 ? "has-tasks" : "",
            ]
              .filter(Boolean)
              .join(" ")}
            data-testid={`chat-views-${view.id}`}
            aria-pressed={view.pressed}
            aria-label={view.name}
            onClick={view.run}
          >
            {view.id === "tasks" ? (
              <span
                className={`bgtask-dot ${live ? "bgtask-dot--running" : "bgtask-dot--muted"}`}
                aria-hidden="true"
              />
            ) : (
              <ViewIcon id={view.id} />
            )}
            <span className="chat-view-label" aria-hidden="true">
              {view.label}
            </span>
            {view.id === "tasks" && total > 0 ? (
              <span
                className="chat-view-count"
                data-testid="chat-views-tasks-count"
                aria-hidden="true"
              >
                {t("tasks.header.counts", { running, total })}
              </span>
            ) : null}
          </button>
          <span className="chat-view-tip" role="tooltip">
            {view.name}
          </span>
        </span>
      ))}
    </div>
  );
}

/** An 18px line icon per view, the size of the rail's, in the button's colour. */
function ViewIcon(props: { id: Exclude<ViewId, "tasks"> }) {
  const common = {
    className: "chat-view-icon",
    width: 18,
    height: 18,
    viewBox: "0 0 16 16",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.3,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
  };
  if (props.id === "edits") {
    // A plus over a minus: what the session changed.
    return (
      <svg {...common}>
        <rect x="1.75" y="1.75" width="12.5" height="12.5" rx="2" />
        <path d="M8 4.5v4M6 6.5h4M6 11h4" />
      </svg>
    );
  }
  return (
    <svg {...common}>
      <path d="M1.75 4.25c0-.83.67-1.5 1.5-1.5h3l1.5 1.75h5c.83 0 1.5.67 1.5 1.5v6.25c0 .83-.67 1.5-1.5 1.5h-9.5c-.83 0-1.5-.67-1.5-1.5z" />
    </svg>
  );
}
