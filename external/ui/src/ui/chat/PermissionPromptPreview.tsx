import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";

import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import type { ParsedDiffLine } from "../messages/parseDiff";
import type { PermissionToolPreview as Preview } from "./permissionToolPreview";

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
  const headers = useMemo(
    () => new Map(preview.hunkHeaders.map((row) => [row.at, row.text])),
    [preview.hunkHeaders],
  );
  return (
    <div
      className="permission-preview-diff"
      aria-label={
        preview.toolName === "apply_patch" ? "Patch preview" : "Edit preview"
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

function PreviewBody({ preview }: { preview: Preview }) {
  if (preview.kind === "diff") return <DiffPreview preview={preview} />;
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
}: {
  preview: Preview;
  /** Permission prompts include copy and overflow controls. */
  interactive?: boolean;
  /** Selected transcript previews keep overflow controls without adding copy. */
  overflowControls?: boolean;
}) {
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
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    const node = viewportRef.current;
    if (node) observer.observe(node);
    return () => observer.disconnect();
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
            <PreviewBody preview={preview} />
            {canToggleOverflow && overflows && !expanded ? (
              <span className="permission-preview-fade" aria-hidden />
            ) : null}
          </div>
          {canToggleOverflow && overflows ? (
            <button
              type="button"
              className="tool-overflow-toggle"
              aria-expanded={expanded}
              onClick={() =>
                setExpanded((value) => {
                  if (value && viewportRef.current) {
                    viewportRef.current.scrollTop = 0;
                  }
                  return !value;
                })
              }
            >
              {expanded ? "Less" : "More…"}
            </button>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
