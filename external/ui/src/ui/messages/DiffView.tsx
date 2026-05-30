import { useCallback, useMemo, useState } from "react";
import {
  flattenDiffLines,
  parseDiffPatch,
  type ParsedDiffLine,
} from "./parseDiff";

/** Number of diff lines visible before "Load more results" appears. */
export const DIFF_INITIAL_LINES = 15;

function DiffLineRow({ line }: { line: ParsedDiffLine }) {
  const sign = line.kind === "add" ? "+" : line.kind === "del" ? "-" : " ";
  return (
    <div className={`diff-line diff-line--${line.kind}`}>
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

export function DiffView(props: {
  patch: string;
  filePath: string;
}) {
  const parsed = useMemo(
    () => parseDiffPatch(props.patch, props.filePath),
    [props.patch, props.filePath],
  );

  const allLines = useMemo(() => flattenDiffLines(parsed), [parsed]);
  const isTruncated = allLines.length > DIFF_INITIAL_LINES;

  const [showAll, setShowAll] = useState(false);
  const onLoadMore = useCallback(() => setShowAll(true), []);
  const onHide = useCallback(() => setShowAll(false), []);

  const visibleLines = showAll ? allLines : allLines.slice(0, DIFF_INITIAL_LINES);

  const viewportMode = showAll ? "scroll" : "clip";

  if (parsed.hunks.length === 0) {
    return null;
  }

  const blockClass = [
    "tool-block diff-block",
    isTruncated && "tool-result-viewport tool-result-viewport--tall",
    isTruncated && `tool-result-viewport--${viewportMode}`,
  ]
    .filter(Boolean)
    .join(" ");

  // Map allLines index → hunk header, so we can inject headers at the right place.
  const hunkBreaks = new Map<number, string>();
  let lineIdx = 0;
  for (const hunk of parsed.hunks) {
    hunkBreaks.set(lineIdx, hunk.header);
    lineIdx += hunk.lines.length;
  }

  // Collect visible rows: hunk headers + diff lines, up to visibleLines count.
  const rows: Array<
    | { type: "hunk"; header: string; key: string }
    | { type: "line"; line: ParsedDiffLine; key: string }
  > = [];
  let consumed = 0;
  for (const hunk of parsed.hunks) {
    // Only emit the hunk header if we still have budget or just started this hunk.
    if (consumed >= visibleLines.length) break;
    rows.push({ type: "hunk", header: hunk.header, key: `h-${consumed}` });
    const take = Math.min(hunk.lines.length, visibleLines.length - consumed);
    for (let i = 0; i < take; i++) {
      rows.push({ type: "line", line: hunk.lines[i], key: `l-${consumed + i}` });
    }
    consumed += take;
  }

  void hunkBreaks; // computed for future use; rows loop above inlines headers per-hunk

  const displayPath = parsed.filePath || props.filePath;

  return (
    <>
      <div className={blockClass} aria-label="Diff view">
        {displayPath ? (
          <div className="diff-file-header">
            <span className="diff-file-path" aria-label="Patched file">
              {displayPath}
            </span>
          </div>
        ) : null}
        <div>
          {rows.map((row) =>
            row.type === "hunk" ? (
              <div key={row.key} className="diff-hunk-header" aria-hidden>
                {row.header}
              </div>
            ) : (
              <DiffLineRow key={row.key} line={row.line} />
            ),
          )}
        </div>
      </div>
      {isTruncated ? (
        <div className="tool-result-toggle-row">
          {showAll ? (
            <button
              type="button"
              className="tool-result-text-link"
              data-testid="diff-hide-link"
              onClick={onHide}
            >
              Hide
            </button>
          ) : (
            <button
              type="button"
              className="tool-result-text-link"
              data-testid="diff-load-more"
              onClick={onLoadMore}
            >
              Load more results
            </button>
          )}
        </div>
      ) : null}
    </>
  );
}
