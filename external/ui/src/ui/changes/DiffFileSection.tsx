import { useMemo, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { Chevron } from "../components/Chevron";
import { parseDiffPatch } from "../messages/parseDiff";
import type { ParsedDiffLine } from "../messages/parseDiff";
import { toSplitRows, toUnifiedRows } from "./diffRows";
import type { SplitRow, UnifiedRow } from "./diffRows";
import type { DiffView } from "./diffViewPrefs";
import { languageForPath } from "./diffLanguage";
import { highlightLine } from "./highlightLine";
import type { HighlightSpan } from "./highlightLine";
import { baseName, dirName, statusKey } from "./sessionChangesText";
import type { ChangedFile } from "./types";

/** Token spans per line, keyed by the line itself: both row builders hand back
 *  the same objects the parser produced, so one lookup serves either view. */
type HighlightMap = Map<ParsedDiffLine, HighlightSpan[]>;

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

function UnifiedBody({
  rows,
  highlight,
}: {
  rows: UnifiedRow[];
  highlight: HighlightMap;
}) {
  return (
    <div className="dv-diff dv-diff--unified">
      {rows.map((row, i) =>
        row.kind === "gap" ? (
          <div key={`gap-${i}`} className="dv-gap" aria-hidden="true" />
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
}: {
  rows: SplitRow[];
  highlight: HighlightMap;
}) {
  return (
    <div className="dv-diff dv-diff--split">
      {rows.map((row, i) =>
        row.kind === "gap" ? (
          <div key={`gap-${i}`} className="dv-gap" aria-hidden="true" />
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
  file: ChangedFile;
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
  discardBusy?: boolean;
}) {
  const { t } = useT();
  const [copied, setCopied] = useState(false);
  const file = props.file;
  const dir = dirName(file.path);

  // Parsing and colouring depend on the patch, not on how it is drawn, so
  // flipping between unified and split does not re-highlight the file.
  const parsed = useMemo(() => {
    if (!props.patch) {
      return null;
    }
    const hunks = parseDiffPatch(props.patch, file.path).hunks;
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
    return { hunks, highlight };
  }, [props.patch, file.path]);

  const rows = useMemo(() => {
    if (!parsed) {
      return null;
    }
    return props.view === "split"
      ? { split: toSplitRows(parsed.hunks) }
      : { unified: toUnifiedRows(parsed.hunks) };
  }, [parsed, props.view]);

  const copyPath = () => {
    void navigator.clipboard
      .writeText(file.path)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      })
      .catch(() => {
        /* clipboard denied; the path is still selectable in the header */
      });
  };

  return (
    <div
      className={"dv-file dv-file--" + file.status}
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
          title={file.path}
        >
          <Chevron open={!props.collapsed} />
          <span
            className={"dv-file-badge dv-file-badge--" + file.status}
            aria-label={t(statusKey(file.status))}
          />
          <span className="dv-file-path">
            {dir ? <span className="dv-file-dir">{dir + "/"}</span> : null}
            <span className="dv-file-name">{baseName(file.path)}</span>
          </span>
        </button>

        {file.binary ? (
          <span className="dv-file-stat dv-file-stat--binary">
            {t("changes.binary")}
          </span>
        ) : (
          <span className="dv-file-stat">
            <span className="changes-add">{"+" + file.additions}</span>
            <span className="changes-del">{"−" + file.deletions}</span>
          </span>
        )}

        <span className="dv-file-actions">
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
            {copied ? "✓" : "⧉"}
          </button>
          {props.onDiscard ? (
            <button
              type="button"
              className="dv-icon-btn dv-icon-btn--danger"
              title={t("changes.discardFileTitle", { name: file.path })}
              aria-label={t("changes.discardFileTitle", { name: file.path })}
              disabled={props.discardBusy === true}
              data-testid={`dv-discard-${file.path}`}
              onClick={props.onDiscard}
            >
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
                <path d="M3.5 6.5h6a3.5 3.5 0 0 1 0 7H7M3.5 6.5 6 4M3.5 6.5 6 9" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          ) : null}
          <button
            type="button"
            className="dv-icon-btn"
            title={
              props.collapsed
                ? t("changes.viewer.expandFile")
                : t("changes.viewer.collapseFile")
            }
            aria-label={
              props.collapsed
                ? t("changes.viewer.expandFile")
                : t("changes.viewer.collapseFile")
            }
            onClick={props.onToggle}
          >
            <Chevron open={!props.collapsed} />
          </button>
        </span>
      </div>

      {props.collapsed ? null : (
        <div className="dv-file-body">
          {file.binary ? (
            <div className="dv-note">{t("changes.binaryBody")}</div>
          ) : props.error ? (
            <div className="dv-note dv-note--error">{props.error}</div>
          ) : rows && parsed ? (
            <>
              {rows.unified ? (
                <UnifiedBody rows={rows.unified} highlight={parsed.highlight} />
              ) : null}
              {rows.split ? (
                <SplitBody rows={rows.split} highlight={parsed.highlight} />
              ) : null}
              {file.truncated ? (
                <div className="dv-note">{t("changes.truncated")}</div>
              ) : null}
            </>
          ) : props.loading ? (
            <div className="dv-note">{t("changes.viewer.loadingFile")}</div>
          ) : null}
        </div>
      )}
    </div>
  );
}
