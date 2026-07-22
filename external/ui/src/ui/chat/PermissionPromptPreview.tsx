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
}: {
  preview: Preview;
  /** Transcript foldouts render the full preview and omit nested controls. */
  interactive?: boolean;
}) {
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const hasBody =
    preview.kind !== "path" &&
    !(preview.kind === "diff" && preview.lines.length === 0);
  const previewIdentity = [
    preview.toolName,
    preview.header,
    preview.copyText,
  ].join("\0");

  const measure = useCallback(() => {
    if (!interactive || expanded) return;
    const node = viewportRef.current;
    if (!node) {
      setOverflows(false);
      return;
    }
    setOverflows(node.scrollHeight > node.clientHeight + 1);
  }, [expanded, interactive]);

  useLayoutEffect(() => {
    if (interactive) setExpanded(false);
  }, [interactive, previewIdentity]);

  useLayoutEffect(() => {
    if (!interactive || !hasBody || expanded) return;
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    const node = viewportRef.current;
    if (node) observer.observe(node);
    return () => observer.disconnect();
  }, [expanded, hasBody, interactive, measure, previewIdentity]);

  const showFullBody = !interactive || expanded;

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
              showFullBody && "permission-preview-viewport--expanded",
            ]
              .filter(Boolean)
              .join(" ")}
            data-testid="permission-preview-viewport"
          >
            <PreviewBody preview={preview} />
            {interactive && overflows && !expanded ? (
              <span className="permission-preview-fade" aria-hidden />
            ) : null}
          </div>
          {interactive && overflows ? (
            <button
              type="button"
              className="permission-preview-more"
              aria-expanded={expanded}
              onClick={() => setExpanded((value) => !value)}
            >
              {expanded ? "Less" : "More…"}
            </button>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
