import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";

import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import type { ParsedDiffLine } from "../messages/parseDiff";
import type { PermissionToolPreview as Preview } from "./permissionToolPreview";
import { useT } from "../i18n/I18nProvider";

function DiffLineRow({ line }: { line: ParsedDiffLine }) {
  const sign = line.kind === "add" ? "+" : line.kind === "del" ? "−" : " ";
  return (
    <div className={"diff-line diff-line--" + line.kind}>
      <div className="diff-gutter">
        <span className="diff-no diff-no--old">
          {line.oldNo !== null ? line.oldNo : ""}
        </span>
        <span className="diff-no diff-no--new">
          {line.newNo !== null ? line.newNo : ""}
        </span>
      </div>
      <span className="diff-sign" aria-hidden>
        {sign}
      </span>
      <span className="diff-content">{line.content}</span>
    </div>
  );
}

function DiffPreview({
  preview,
}: {
  preview: Extract<Preview, { kind: "diff" }>;
}) {
  const { t } = useT();
  const headers = useMemo(
    () => new Map(preview.hunkHeaders.map((row) => [row.at, row.text])),
    [preview.hunkHeaders],
  );
  return (
    <div
      className="permission-preview-diff"
      aria-label={
        preview.toolName === "apply_patch"
          ? t("permission.preview.patch")
          : t("permission.preview.edit")
      }
    >
      {preview.lines.map((line, index) => (
        <div key={[index, line.kind, line.oldNo, line.newNo].join("-")}>
          {headers.has(index) ? (
            <div className="diff-hunk-header" aria-hidden>
              {headers.get(index)}
            </div>
          ) : null}
          <DiffLineRow line={line} />
        </div>
      ))}
    </div>
  );
}

function todoStatusLabel(status: string, t: (key: string) => string): string {
  switch (status) {
    case "in_progress":
      return t("todo.status.inProgress");
    case "completed":
      return t("todo.status.completed");
    case "failed":
      return t("todo.status.failed");
    case "cancelled":
      return t("todo.status.cancelled");
    default:
      return t("todo.status.pending");
  }
}

function todoStatusMark(status: string): string {
  switch (status) {
    case "completed":
      return "✓";
    case "failed":
      return "×";
    case "cancelled":
      return "−";
    default:
      return "";
  }
}

function TodoPreview({
  preview,
}: {
  preview: Extract<Preview, { kind: "todo" }>;
}) {
  const { t } = useT();
  return (
    <ul className="todo-tool-preview-list" aria-label={preview.header}>
      {preview.entries.map((entry, index) => {
        const status = todoStatusLabel(entry.status, t);
        return (
          <li
            key={`${index}-${entry.status}-${entry.content}`}
            className={`todo-tool-preview-row todo-tool-preview-row--${entry.status}`}
            aria-label={`${status}: ${entry.content}`}
          >
            <span className="todo-tool-preview-mark" aria-hidden="true">
              {todoStatusMark(entry.status)}
            </span>
            <span className="todo-tool-preview-content">{entry.content}</span>
            {entry.status === "in_progress" ? (
              <span className="todo-tool-preview-status">{status}</span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

function PlanExitPreview({ completed }: { completed: boolean }) {
  const { t } = useT();
  return (
    <div
      className={[
        "plan-exit-preview",
        completed && "plan-exit-preview--completed",
      ]
        .filter(Boolean)
        .join(" ")}
      role="status"
    >
      <span className="plan-exit-preview-icon" aria-hidden="true">
        {completed ? "✓" : "→"}
      </span>
      <div className="plan-exit-preview-copy">
        <div className="plan-exit-preview-modes" aria-hidden="true">
          <span className="plan-exit-preview-mode">
            {t("planExit.preview.planMode")}
          </span>
          <span className="plan-exit-preview-arrow">→</span>
          <span className="plan-exit-preview-mode plan-exit-preview-mode--agent">
            {t("planExit.preview.agentMode")}
          </span>
        </div>
        <div className="plan-exit-preview-message">
          {t(
            completed
              ? "planExit.preview.completed"
              : "planExit.preview.inProgress",
          )}
        </div>
      </div>
    </div>
  );
}

function PreviewBody({
  preview,
  completed = false,
}: {
  preview: Preview;
  completed?: boolean;
}) {
  if (preview.kind === "diff") return <DiffPreview preview={preview} />;
  if (preview.kind === "todo") return <TodoPreview preview={preview} />;
  if (preview.kind === "plan_exit") {
    return <PlanExitPreview completed={completed} />;
  }
  if (preview.kind === "move") {
    return (
      <div className="permission-preview-move">
        <code>{preview.sourcePath}</code>
        <span aria-hidden>→</span>
        <code>{preview.destinationPath}</code>
      </div>
    );
  }
  if (preview.kind === "code") {
    return <pre className="permission-preview-code">{preview.text}</pre>;
  }
  return null;
}

export function PermissionToolPreview({
  preview,
  interactive = true,
  overflowControls = false,
  toolStatus,
}: {
  preview: Preview;
  /** Permission prompts include copy and overflow controls. */
  interactive?: boolean;
  /** Selected transcript previews keep overflow controls without adding copy. */
  overflowControls?: boolean;
  /** Transcript rows can distinguish an in-flight transition from a completed one. */
  toolStatus?: string | undefined;
}) {
  const { t } = useT();
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const hasBody =
    preview.kind !== "path" &&
    !(preview.kind === "diff" && preview.lines.length === 0);
  const canToggleOverflow = interactive || overflowControls;
  const previewIdentity = [
    preview.toolName,
    preview.header,
    preview.copyText,
  ].join("\0");

  const measure = useCallback(() => {
    if (!canToggleOverflow || expanded) return;
    const node = viewportRef.current;
    if (!node) {
      setOverflows(false);
      return;
    }
    setOverflows(node.scrollHeight > node.clientHeight + 1);
  }, [canToggleOverflow, expanded]);

  useLayoutEffect(() => {
    if (canToggleOverflow) setExpanded(false);
  }, [canToggleOverflow, previewIdentity]);

  useLayoutEffect(() => {
    if (!canToggleOverflow || !hasBody || expanded) return;
    measure();
    const node = viewportRef.current;
    // Transcript foldouts keep the body display:none until the <details> opens;
    // the mount-time measure then sees zero heights. Re-measure on the toggle
    // event itself, because not every engine reports the un-hide as a resize.
    const details = node ? node.closest("details") : null;
    if (details) details.addEventListener("toggle", measure);
    let observer: ResizeObserver | undefined;
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(measure);
      if (node) observer.observe(node);
    }
    return () => {
      if (details) details.removeEventListener("toggle", measure);
      observer?.disconnect();
    };
  }, [canToggleOverflow, expanded, hasBody, measure, previewIdentity]);

  const viewportMode = !canToggleOverflow
    ? "static"
    : expanded
      ? "scroll"
      : "clip";

  return (
    <div className="permission-preview">
      <div
        className={
          "permission-preview-bar" +
          (hasBody ? "" : " permission-preview-bar--standalone")
        }
      >
        <div className="permission-preview-location" title={preview.header}>
          {preview.header}
        </div>
        {preview.meta.length > 0 ? (
          <div className="permission-preview-meta">
            {preview.meta.map((item) => (
              <span key={item}>{item}</span>
            ))}
          </div>
        ) : null}
        {interactive && preview.copyText ? (
          <CodeBlockCopyButton
            textToCopy={preview.copyText}
            dataTestId="permission-prompt-copy"
          />
        ) : null}
      </div>
      {hasBody ? (
        <>
          <div
            ref={viewportRef}
            className={[
              "permission-preview-viewport",
              `permission-preview-viewport--${viewportMode}`,
            ]
              .filter(Boolean)
              .join(" ")}
            data-testid="permission-preview-viewport"
          >
            <PreviewBody
              preview={preview}
              completed={toolStatus?.toLowerCase() === "completed"}
            />
            {canToggleOverflow && overflows && !expanded ? (
              <span className="permission-preview-fade" aria-hidden />
            ) : null}
          </div>
          {canToggleOverflow && overflows ? (
            <button
              type="button"
              className="tool-overflow-toggle"
              aria-expanded={expanded}
              data-testid={expanded ? "tool-preview-less" : "tool-preview-more"}
              onClick={(e) => {
                e.preventDefault();
                // Collapsing keeps the scrolled position, so reset it before clipping.
                if (expanded && viewportRef.current) {
                  viewportRef.current.scrollTop = 0;
                }
                setExpanded((value) => !value);
              }}
            >
              {expanded
                ? t("permission.preview.less")
                : t("permission.preview.more")}
            </button>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
