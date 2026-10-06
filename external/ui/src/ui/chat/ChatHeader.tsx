import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { countTasks } from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { filesShortcutLabel } from "../files/filesHotkey";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";

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
        <HeaderViewsControl
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
 * The views of a chat - its background tasks, its edits, its files - and the
 * one place it says how much runs in its background. The header is sticky, so
 * the control stays in reach however far the reader has scrolled, and it is
 * there from the first message: a chat that has not run a task yet reads
 * "Tasks", one that has reads how many run out of how many there are.
 *
 * The control opens a menu rather than a panel, the way the views of a session
 * are picked in Claude's app: the tasks and the edits share the dock beside the
 * chat, the files open in a window of their own, and none of them carries a tab
 * strip to reach the others. A row of a view on show is checked, and picking it
 * puts that view away.
 */
function HeaderViewsControl(props: {
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
  const aria =
    total === 0
      ? t("tasks.header.ariaEmpty")
      : t("tasks.header.aria", { running, total });
  const useSheet = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const controlRef = useRef<HTMLButtonElement | null>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const close = () => {
    setOpen(false);
    controlRef.current?.focus({ preventScroll: true });
  };
  useEscapeCloses(open, close);

  // A view that opens or closes some other way - its key, a link, the card -
  // puts the menu away, so the next Escape is the view's. The focus stays
  // where that way left it.
  const viewsKey = `${props.tasksOpen}:${props.editsOpen}:${props.filesOpen}`;
  const viewsKeyRef = useRef(viewsKey);
  useEffect(() => {
    if (viewsKeyRef.current === viewsKey) return;
    viewsKeyRef.current = viewsKey;
    setOpen(false);
  }, [viewsKey]);

  // The menu takes the focus as it opens, so the arrow keys walk it at once.
  useEffect(() => {
    if (open) {
      itemRefs.current.find(Boolean)?.focus({ preventScroll: true });
    }
  }, [open]);

  const views: {
    id: ViewId;
    label: string;
    checked: boolean;
    run: () => void;
  }[] = [
    {
      id: "tasks",
      label: t("tasks.panelTitle"),
      checked: props.tasksOpen,
      run: props.onOpenTasks,
    },
  ];
  if (props.onOpenEdits) {
    views.push({
      id: "edits",
      label: t("changes.panelTitle"),
      checked: props.editsOpen,
      run: props.onOpenEdits,
    });
  }
  if (props.onOpenFiles) {
    views.push({
      id: "files",
      label: t("files.title"),
      checked: props.filesOpen,
      run: props.onOpenFiles,
    });
  }
  itemRefs.current.length = views.length;

  const pick = (run: () => void) => {
    close();
    run();
  };

  const onMenuKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = itemRefs.current.filter(
      (el): el is HTMLButtonElement => !!el,
    );
    if (items.length === 0) {
      return;
    }
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (at + 1) % items.length;
    else if (e.key === "ArrowUp")
      next = (at <= 0 ? items.length : at) - 1;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    else if (e.key === "Tab") {
      // Tab puts the menu away and moves on from its control in the same
      // press: the focus goes back to the control and the browser's own Tab
      // takes it from there.
      close();
      return;
    }
    if (next >= 0) {
      e.preventDefault();
      items[next]!.focus({ preventScroll: true });
    }
  };

  const anyOpen = props.tasksOpen || props.editsOpen || props.filesOpen;

  return (
    <>
      <button
        type="button"
        ref={controlRef}
        className={[
          "chat-header-tasks",
          live ? "is-running" : "",
          total > 0 ? "has-tasks" : "",
          anyOpen ? "is-active" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        data-testid="chat-header-tasks"
        aria-label={aria}
        aria-haspopup="menu"
        aria-expanded={open}
        title={aria}
        onClick={() => {
          if (open) {
            close();
            return;
          }
          if (controlRef.current) {
            setAnchor(controlRef.current.getBoundingClientRect());
          }
          setOpen(true);
        }}
      >
        <span
          className={`bgtask-dot ${live ? "bgtask-dot--running" : "bgtask-dot--muted"}`}
          aria-hidden="true"
        />
        <span className="chat-header-tasks-label">
          {t("tasks.header.label")}
        </span>
        {total > 0 ? (
          <span
            className="chat-header-tasks-counts"
            data-testid="chat-header-tasks-counts"
          >
            {t("tasks.header.counts", { running, total })}
          </span>
        ) : null}
      </button>
      {open && (useSheet || anchor)
        ? createPortal(
            <>
              <button
                type="button"
                className={`mode-menu-backdrop ${useSheet ? "mode-menu-backdrop--scrim" : ""}`}
                aria-hidden="true"
                tabIndex={-1}
                onMouseDown={(e) => {
                  e.preventDefault();
                  close();
                }}
              />
              <div
                className={`mode-menu mode-menu--views ${
                  useSheet ? "mode-menu--sheet" : "mode-menu--portal opens-down"
                }`}
                role="menu"
                aria-label={t("chat.views.menu")}
                data-testid="chat-views-menu"
                onKeyDown={onMenuKeyDown}
                style={
                  useSheet || !anchor
                    ? undefined
                    : {
                        top: anchor.bottom + 8,
                        right: Math.max(8, window.innerWidth - anchor.right),
                      }
                }
              >
                {views.map((view, i) => (
                  <button
                    key={view.id}
                    type="button"
                    ref={(el) => {
                      itemRefs.current[i] = el;
                    }}
                    role="menuitemcheckbox"
                    aria-checked={view.checked}
                    className={`mode-item views-menu-item ${view.checked ? "is-selected" : ""}`}
                    data-testid={`chat-views-${view.id}`}
                    onClick={() => pick(view.run)}
                  >
                    <ViewIcon id={view.id} />
                    <span className="views-menu-label">{view.label}</span>
                    {view.id === "tasks" && total > 0 ? (
                      <span
                        className="views-menu-count"
                        aria-label={aria}
                      >
                        {t("tasks.header.counts", { running, total })}
                      </span>
                    ) : null}
                    {view.id === "files" ? (
                      <kbd
                        className="views-menu-key"
                        data-testid="chat-views-files-shortcut"
                      >
                        {filesShortcutLabel()}
                      </kbd>
                    ) : null}
                  </button>
                ))}
              </div>
            </>,
            document.body,
          )
        : null}
    </>
  );
}

/** A 16px line icon per view, drawn in the row's text colour. */
function ViewIcon(props: { id: ViewId }) {
  const common = {
    className: "views-menu-icon",
    width: 16,
    height: 16,
    viewBox: "0 0 16 16",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.4,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
  };
  if (props.id === "tasks") {
    // Stacked layers: work that runs under the conversation.
    return (
      <svg {...common}>
        <path d="M8 2.5 2.5 5.25 8 8l5.5-2.75L8 2.5Z" />
        <path d="m2.5 8.25 5.5 2.75 5.5-2.75" />
        <path d="m2.5 11 5.5 2.75L13.5 11" />
      </svg>
    );
  }
  if (props.id === "edits") {
    // A page with a plus over a minus: what the session changed.
    return (
      <svg {...common}>
        <path d="M4 1.75h5.5L12.5 4.75v9.5H4z" />
        <path d="M8.25 5.5v3.5M6.5 7.25h3.5M6.5 11.25h3.5" />
      </svg>
    );
  }
  return (
    <svg {...common}>
      <path d="M1.75 4.25c0-.83.67-1.5 1.5-1.5h3l1.5 1.75h5c.83 0 1.5.67 1.5 1.5v6.25c0 .83-.67 1.5-1.5 1.5h-9.5c-.83 0-1.5-.67-1.5-1.5z" />
    </svg>
  );
}
