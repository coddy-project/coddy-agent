import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, ReactNode } from "react";
import { copyText } from "../clipboard";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import { useApiImageState } from "../env/apiImage";
import { Markdown } from "../markdown/Markdown";
import type { ParsedDiffLine } from "../messages/parseDiff";
import { IconCopy } from "../settings/icons";
import { FileActionsMenu } from "./FileActionsMenu";
import {
  EMPTY_DIFF_CONTEXT,
  expandAllContext,
  expandContextAt,
  expandDiffContext,
} from "./contextExpansion";
import type {
  ContextDirection,
  ContextGap,
  DiffContextState,
} from "./contextExpansion";
import { lineNumberDigits, toSplitRows, toUnifiedRows } from "./diffRows";
import type { SplitRow, UnifiedRow } from "./diffRows";
import type { DiffView } from "./diffViewPrefs";
import { languageForPath } from "./diffLanguage";
import { highlightLine } from "./highlightLine";
import type { HighlightSpan } from "./highlightLine";
import { baseName, dirName, statusKey } from "./sessionChangesText";
import type { ChangedFile } from "./types";
import {
  formatDiffBytes,
  formatExactDiffBytes,
  richDiffKind,
  sessionChangeImageUrl,
} from "./richDiff";

/** Token spans per line, keyed by the line itself: both row builders hand back
 *  the same objects the parser produced, so one lookup serves either view. */
type HighlightMap = Map<ParsedDiffLine, HighlightSpan[]>;

/** The digits every number cell of the file makes room for, as the style
 *  of its diff: the stylesheet sizes `.dv-num` from it. */
function gutterStyle(digits: number): CSSProperties {
  return { "--dv-num-digits": String(digits) } as CSSProperties;
}

/** A line number cell; blank on the side a line does not exist. */
function LineNo({ value }: { value: number | null }) {
  return <span className="dv-num">{value === null ? "" : String(value)}</span>;
}

/** The code of one line, coloured when the file has a known language. */
function CodeCell({
  line,
  highlight,
}: {
  line: ParsedDiffLine;
  highlight: HighlightMap;
}) {
  const spans = highlight.get(line);
  if (!spans) {
    return <span className="dv-code">{line.content}</span>;
  }
  return (
    <span className="dv-code">
      {spans.map((span, i) => (
        <span key={i} className={span.className || undefined}>
          {span.text}
        </span>
      ))}
    </span>
  );
}

function ContextGapControl({
  gap,
  onExpand,
}: {
  gap: ContextGap;
  onExpand: (direction: ContextDirection) => void;
}) {
  const { t } = useT();
  return (
    <span className="dv-context-actions">
      {gap.canUp ? (
        <button
          type="button"
          className="dv-icon-btn"
          title={t("changes.viewer.expandUp")}
          aria-label={t("changes.viewer.expandUp")}
          onClick={() => onExpand("up")}
        >
          <IconContext direction="up" />
        </button>
      ) : null}
      {gap.canUp && gap.canDown ? (
        <button
          type="button"
          className="dv-icon-btn"
          title={t("changes.viewer.expandBoth")}
          aria-label={t("changes.viewer.expandBoth")}
          onClick={() => onExpand("both")}
        >
          <IconContext direction="both" />
        </button>
      ) : null}
      {gap.canDown ? (
        <button
          type="button"
          className="dv-icon-btn"
          title={t("changes.viewer.expandDown")}
          aria-label={t("changes.viewer.expandDown")}
          onClick={() => onExpand("down")}
        >
          <IconContext direction="down" />
        </button>
      ) : null}
    </span>
  );
}

function RichDiff({
  file,
  sessionId,
  kind,
}: {
  file: ChangedFile & {
    before?: string | undefined;
    after?: string | undefined;
  };
  sessionId: string;
  kind: "image" | "markdown";
}) {
  const { t } = useT();
  const showBefore =
    file.status === "deleted" || (kind === "image" && file.status !== "added");
  return (
    <div className={`dv-rich dv-rich--${kind}`}>
      {showBefore ? (
        <figure className="dv-rich-version dv-rich-version--before">
          {kind === "image" ? (
            <figcaption>{t("changes.viewer.before")}</figcaption>
          ) : null}
          {kind === "image" && (file.beforeImage ?? file.image) ? (
            <DiffImage sessionId={sessionId} path={file.path} side="before" />
          ) : kind === "image" ? (
            <pre className="dv-rich-text">{file.before ?? ""}</pre>
          ) : (
            <Markdown text={file.before ?? ""} />
          )}
        </figure>
      ) : null}
      {file.status !== "deleted" ? (
        <figure className="dv-rich-version dv-rich-version--after">
          {kind === "image" ? (
            <figcaption>{t("changes.viewer.after")}</figcaption>
          ) : null}
          {kind === "image" && (file.afterImage ?? file.image) ? (
            <DiffImage sessionId={sessionId} path={file.path} side="after" />
          ) : kind === "image" ? (
            <pre className="dv-rich-text">{file.after ?? ""}</pre>
          ) : (
            <Markdown text={file.after ?? ""} />
          )}
        </figure>
      ) : null}
    </div>
  );
}

function DiffImage({
  sessionId,
  path,
  side,
}: {
  sessionId: string;
  path: string;
  side: "before" | "after";
}) {
  const [dimensions, setDimensions] = useState<{ w: number; h: number } | null>(
    null,
  );
  const apiUrl = sessionId ? sessionChangeImageUrl(sessionId, path, side) : "";
  const { t } = useT();
  const { src, loading, failed } = useApiImageState(apiUrl);
  if (loading) {
    return <span className="dv-note">{t("changes.viewer.imageLoading")}</span>;
  }
  if (failed || !src) {
    return (
      <span className="dv-note">{t("changes.viewer.imageUnavailable")}</span>
    );
  }
  return (
    <>
      <span className="dv-image-frame">
        <img
          src={src}
          alt={`${t(`changes.viewer.${side}`)}: ${path}`}
          data-image-name={path}
          draggable={false}
          onLoad={(event) =>
            setDimensions({
              w: event.currentTarget.naturalWidth,
              h: event.currentTarget.naturalHeight,
            })
          }
        />
      </span>
      {dimensions ? (
        <span className="dv-image-dimensions">
          W {dimensions.w} · H {dimensions.h}
        </span>
      ) : null}
    </>
  );
}

function IconExpandLines({ collapsed = false }: { collapsed?: boolean }) {
  return (
    <IconContext
      direction="both"
      className={collapsed ? "dv-expand-lines-collapse" : undefined}
    />
  );
}

function IconSourceDiff() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      aria-hidden="true"
    >
      <path d="m4 4-3 4 3 4M12 4l3 4-3 4M9 2 7 14" />
    </svg>
  );
}

function IconRichDiff() {
  return (
    <svg
      viewBox="0 0 16 16"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      aria-hidden="true"
    >
      <rect x="1.5" y="2.5" width="13" height="11" rx="1.5" />
      <path d="M1.5 10.5 5 7l3 3 2-2 4.5 4M11 5.5h.01" />
    </svg>
  );
}

function IconContext({
  direction,
  className,
}: {
  direction: "up" | "down" | "both";
  className?: string | undefined;
}) {
  const path =
    direction === "up"
      ? "M7.823 1.677 4.927 4.573A.25.25 0 0 0 5.104 5H7.25v3.236a.75.75 0 1 0 1.5 0V5h2.146a.25.25 0 0 0 .177-.427L8.177 1.677a.25.25 0 0 0-.354 0ZM13.75 11a.75.75 0 0 0 0 1.5h.5a.75.75 0 0 0 0-1.5h-.5Zm-3.75.75a.75.75 0 0 1 .75-.75h.5a.75.75 0 0 1 0 1.5h-.5a.75.75 0 0 1-.75-.75ZM7.75 11a.75.75 0 0 0 0 1.5h.5a.75.75 0 0 0 0-1.5h-.5ZM4 11.75a.75.75 0 0 1 .75-.75h.5a.75.75 0 0 1 0 1.5h-.5a.75.75 0 0 1-.75-.75ZM1.75 11a.75.75 0 0 0 0 1.5h.5a.75.75 0 0 0 0-1.5h-.5Z"
      : direction === "down"
        ? "m8.177 14.323 2.896-2.896a.25.25 0 0 0-.177-.427H8.75V7.764a.75.75 0 1 0-1.5 0V11H5.104a.25.25 0 0 0-.177.427l2.896 2.896a.25.25 0 0 0 .354 0ZM2.25 5a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5ZM6 4.25a.75.75 0 0 1-.75.75h-.5a.75.75 0 0 1 0-1.5h.5a.75.75 0 0 1 .75.75ZM8.25 5a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5ZM12 4.25a.75.75 0 0 1-.75.75h-.5a.75.75 0 0 1 0-1.5h.5a.75.75 0 0 1 .75.75Zm2.25.75a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5Z"
        : "m8.177.677 2.896 2.896a.25.25 0 0 1-.177.427H8.75v1.25a.75.75 0 0 1-1.5 0V4H5.104a.25.25 0 0 1-.177-.427L7.823.677a.25.25 0 0 1 .354 0ZM7.25 10.75a.75.75 0 0 1 1.5 0V12h2.146a.25.25 0 0 1 .177.427l-2.896 2.896a.25.25 0 0 1-.354 0l-2.896-2.896A.25.25 0 0 1 5.104 12H7.25v-1.25Zm-5-2a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5ZM6 8a.75.75 0 0 1-.75.75h-.5a.75.75 0 0 1 0-1.5h.5A.75.75 0 0 1 6 8Zm2.25.75a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5ZM12 8a.75.75 0 0 1-.75.75h-.5a.75.75 0 0 1 0-1.5h.5A.75.75 0 0 1 12 8Zm2.25.75a.75.75 0 0 0 0-1.5h-.5a.75.75 0 0 0 0 1.5h.5Z";
  return (
    <svg
      viewBox="0 0 16 16"
      width="16"
      height="16"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path d={path} />
    </svg>
  );
}

function UnifiedBody({
  rows,
  highlight,
  digits,
  renderGap,
}: {
  rows: UnifiedRow[];
  highlight: HighlightMap;
  digits: number;
  renderGap?: (index: number) => ReactNode;
}) {
  let gapIndex = 0;
  return (
    <div className="dv-diff dv-diff--unified" style={gutterStyle(digits)}>
      {rows.map((row, i) =>
        row.kind === "gap" ? (
          <div key={`gap-${i}`} className="dv-gap">
            {renderGap?.(gapIndex++)}
          </div>
        ) : (
          <div key={i} className={"dv-line dv-line--" + row.line.kind}>
            <LineNo value={row.line.oldNo} />
            <LineNo value={row.line.newNo} />
            <CodeCell line={row.line} highlight={highlight} />
          </div>
        ),
      )}
    </div>
  );
}

/** One half of a split row. A missing line is a filler cell, not a blank line:
 *  it keeps the two columns aligned without pretending the file has content. */
function SplitSide({
  line,
  side,
  highlight,
}: {
  line: ParsedDiffLine | null;
  side: "old" | "new";
  highlight: HighlightMap;
}) {
  if (line === null) {
    return <span className="dv-side dv-side--empty" aria-hidden="true" />;
  }
  const changed = side === "old" ? line.kind === "del" : line.kind === "add";
  return (
    <span className={"dv-side dv-side--" + (changed ? line.kind : "ctx")}>
      <LineNo value={side === "old" ? line.oldNo : line.newNo} />
      <CodeCell line={line} highlight={highlight} />
    </span>
  );
}

function SplitBody({
  rows,
  highlight,
  digits,
  renderGap,
}: {
  rows: SplitRow[];
  highlight: HighlightMap;
  digits: number;
  renderGap?: (index: number) => ReactNode;
}) {
  let gapIndex = 0;
  return (
    <div className="dv-diff dv-diff--split" style={gutterStyle(digits)}>
      {rows.map((row, i) =>
        row.kind === "gap" ? (
          <div key={`gap-${i}`} className="dv-gap">
            {renderGap?.(gapIndex++)}
          </div>
        ) : (
          <div key={i} className="dv-split-row">
            <SplitSide line={row.left} side="old" highlight={highlight} />
            <SplitSide line={row.right} side="new" highlight={highlight} />
          </div>
        ),
      )}
    </div>
  );
}

/**
 * One file of the edits window: a header that names it and states its line
 * counts, over its diff.
 *
 * The header carries the per-file actions the window offers, revealed on
 * hover so a long list of files stays quiet until pointed at.
 */
export function DiffFileSection(props: {
  file: ChangedFile & {
    before?: string | undefined;
    after?: string | undefined;
  };
  sessionId?: string;
  /** Empty until the patch lands; binary files never get one. */
  patch: string;
  loading: boolean;
  error: string;
  collapsed: boolean;
  view: DiffView;
  onToggle: () => void;
  /** Registers the section element so the toolbar can scroll to it. */
  registerRef: (path: string, el: HTMLDivElement | null) => void;
  /** Puts this file back at HEAD, after asking. */
  onDiscard?: (() => void) | undefined;
  onDelete?: (() => void) | undefined;
  discardBusy?: boolean;
  viewed?: boolean;
  onViewedChange?: (viewed: boolean) => void;
  active?: boolean;
}) {
  const { t, locale } = useT();
  const [copied, setCopied] = useState(false);
  const mounted = useRef(false);
  const copyTimer = useRef<number | undefined>(undefined);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      window.clearTimeout(copyTimer.current);
      copyTimer.current = undefined;
    };
  }, []);
  const file = props.file;
  const dir = dirName(file.path);
  const [mode, setMode] = useState<"source" | "rich">(
    file.image ? "rich" : "source",
  );
  const [context, setContext] = useState<DiffContextState>(EMPTY_DIFF_CONTEXT);
  useEffect(() => {
    setMode(file.image ? "rich" : "source");
  }, [file.path, file.digest]);
  useEffect(() => {
    setContext(EMPTY_DIFF_CONTEXT);
  }, [file.path, file.digest, props.patch]);
  const expanded = useMemo(
    () =>
      expandDiffContext(
        props.patch,
        file.before ?? "",
        file.after ?? "",
        context,
      ),
    [props.patch, file.before, file.after, context],
  );

  // Parsing and colouring depend on the patch, not on how it is drawn, so
  // flipping between unified and split does not re-highlight the file.
  const parsed = useMemo(() => {
    if (!props.patch) {
      return null;
    }
    const hunks = expanded.hunks;
    const language = languageForPath(file.path);
    const highlight: HighlightMap = new Map();
    if (language) {
      for (const hunk of hunks) {
        for (const line of hunk.lines) {
          const spans = highlightLine(line.content, language);
          if (spans) {
            highlight.set(line, spans);
          }
        }
      }
    }
    return { hunks, highlight, digits: lineNumberDigits(hunks) };
  }, [expanded, file.path]);

  const rows = useMemo(() => {
    if (!parsed) {
      return null;
    }
    return props.view === "split"
      ? { split: toSplitRows(parsed.hunks) }
      : { unified: toUnifiedRows(parsed.hunks) };
  }, [parsed, props.view]);

  const copyPath = () => {
    void copyText(file.path).then((ok) => {
      if (!ok || !mounted.current) return;
      setCopied(true);
      window.clearTimeout(copyTimer.current);
      copyTimer.current = window.setTimeout(() => {
        copyTimer.current = undefined;
        setCopied(false);
      }, 1200);
    });
  };

  const richKind = richDiffKind(file);
  const richAvailable =
    !props.loading &&
    !props.error &&
    (richKind === "image" ||
      (!file.binary && file.before !== undefined && file.after !== undefined));
  const canExpand =
    !file.binary &&
    !file.truncated &&
    file.before !== undefined &&
    file.after !== undefined &&
    expanded.hunks.length > 0;
  const renderGap = (index: number) => {
    const gap = expanded.gaps[index];
    if (!canExpand || !gap || gap.remaining === 0) {
      return null;
    }
    return (
      <ContextGapControl
        gap={gap}
        onExpand={(direction) =>
          setContext((current) => expandContextAt(current, index, direction))
        }
      />
    );
  };

  return (
    <div
      className={
        "dv-file dv-file--" + file.status + (props.active ? " is-active" : "")
      }
      data-testid={`dv-file-${file.path}`}
      ref={(el) => props.registerRef(file.path, el)}
    >
      <div className="dv-file-head">
        <button
          type="button"
          className="dv-file-title"
          aria-expanded={!props.collapsed}
          data-testid={`dv-file-toggle-${file.path}`}
          onClick={props.onToggle}
          title={`${file.path} (${t(statusKey(file.status))})`}
        >
          <Chevron open={!props.collapsed} />
          <span className="dv-file-path">
            {dir ? <span className="dv-file-dir">{dir + "/"}</span> : null}
            <span className="dv-file-name">{baseName(file.path)}</span>
          </span>
        </button>

        <button
          type="button"
          className="dv-icon-btn"
          title={
            copied ? t("changes.viewer.copied") : t("changes.viewer.copyPath")
          }
          aria-label={t("changes.viewer.copyPath")}
          data-testid={`dv-copy-${file.path}`}
          onClick={copyPath}
        >
          {copied ? "✓" : <IconCopy />}
        </button>
        {canExpand ? (
          <button
            type="button"
            className="dv-icon-btn"
            data-testid={`dv-expand-lines-${file.path}`}
            aria-pressed={context.all}
            title={
              context.all
                ? t("changes.viewer.collapseLines")
                : t("changes.viewer.expandLines")
            }
            aria-label={
              context.all
                ? t("changes.viewer.collapseLines")
                : t("changes.viewer.expandLines")
            }
            onClick={() => {
              setMode("source");
              if (context.all) {
                setContext(EMPTY_DIFF_CONTEXT);
                return;
              }
              setContext((current) => expandAllContext(current));
              if (props.collapsed) {
                props.onToggle();
              }
            }}
          >
            <IconExpandLines collapsed={context.all} />
          </button>
        ) : null}
        <span className="dv-file-head-spacer" aria-hidden="true" />

        <span className="dv-file-stat">
          {file.binary ? (
            <>
              {file.status !== "added" ? (
                <span
                  className="changes-del"
                  title={formatExactDiffBytes(file.beforeSize ?? 0, locale)}
                  aria-label={formatExactDiffBytes(
                    file.beforeSize ?? 0,
                    locale,
                  )}
                >
                  {formatDiffBytes(file.beforeSize ?? 0, locale)}
                </span>
              ) : null}
              {file.status !== "deleted" ? (
                <span
                  className="changes-add"
                  title={formatExactDiffBytes(file.afterSize ?? 0, locale)}
                  aria-label={formatExactDiffBytes(file.afterSize ?? 0, locale)}
                >
                  {formatDiffBytes(file.afterSize ?? 0, locale)}
                </span>
              ) : null}
            </>
          ) : (
            <>
              <span className="changes-add">{"+" + file.additions}</span>
              <span className="changes-del">{"−" + file.deletions}</span>
            </>
          )}
          {/* Diff controls follow the counts; Viewed and file actions close the row. */}
          {richKind === "markdown" && richAvailable ? (
            <span className="dv-file-actions">
              {richKind === "markdown" && richAvailable ? (
                <span
                  className="dv-source-rich"
                  role="group"
                  aria-label={t("changes.viewer.diffMode")}
                >
                  <button
                    type="button"
                    className="dv-icon-btn"
                    aria-label={t("changes.viewer.sourceDiff")}
                    title={t("changes.viewer.sourceDiff")}
                    aria-pressed={mode === "source"}
                    onClick={() => setMode("source")}
                  >
                    <IconSourceDiff />
                  </button>
                  <button
                    type="button"
                    className="dv-icon-btn"
                    aria-label={t("changes.viewer.richDiff")}
                    title={t("changes.viewer.richDiff")}
                    aria-pressed={mode === "rich"}
                    onClick={() => setMode("rich")}
                  >
                    <IconRichDiff />
                  </button>
                </span>
              ) : null}
            </span>
          ) : null}

          <label className="dv-viewed">
            <input
              type="checkbox"
              checked={props.viewed ?? false}
              onChange={(event) => {
                const viewed = event.target.checked;
                props.onViewedChange?.(viewed);
                if (viewed && !props.collapsed) {
                  props.onToggle();
                } else if (!viewed && props.collapsed) {
                  props.onToggle();
                }
              }}
            />
            <span>{t("changes.viewer.viewed")}</span>
          </label>
          <FileActionsMenu
            path={file.path}
            busy={props.discardBusy ?? false}
            onDiscard={props.onDiscard}
            onDelete={props.onDelete}
          />
        </span>
      </div>

      {props.collapsed ? null : (
        <div className="dv-file-body">
          {props.loading && (!file.binary || file.image) ? (
            <div className="dv-note">{t("changes.viewer.loadingFile")}</div>
          ) : props.error ? (
            <div className="dv-note dv-note--error">{props.error}</div>
          ) : mode === "rich" && richKind && richAvailable ? (
            <RichDiff
              file={file}
              sessionId={props.sessionId ?? ""}
              kind={richKind}
            />
          ) : file.binary ? (
            <div className="dv-note">{t("changes.binaryBody")}</div>
          ) : rows && parsed ? (
            <>
              {renderGap(0)}
              {rows.unified ? (
                <UnifiedBody
                  rows={rows.unified}
                  highlight={parsed.highlight}
                  digits={parsed.digits}
                  renderGap={(index) => renderGap(index + 1)}
                />
              ) : null}
              {rows.split ? (
                <SplitBody
                  rows={rows.split}
                  highlight={parsed.highlight}
                  digits={parsed.digits}
                  renderGap={(index) => renderGap(index + 1)}
                />
              ) : null}
              {renderGap(expanded.gaps.length - 1)}
              {file.truncated ? (
                <div className="dv-note">{t("changes.truncated")}</div>
              ) : null}
            </>
          ) : null}
        </div>
      )}
    </div>
  );
}
