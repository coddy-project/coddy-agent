export type DiffLineKind = "add" | "del" | "ctx";

export interface ParsedDiffLine {
  kind: DiffLineKind;
  /** Null for added lines (no old-file line number). */
  oldNo: number | null;
  /** Null for removed lines (no new-file line number). */
  newNo: number | null;
  /** Line content without the leading +/-/space character. */
  content: string;
}

export interface ParsedDiffHunk {
  header: string;
  lines: ParsedDiffLine[];
}

export interface ParsedDiff {
  filePath: string;
  hunks: ParsedDiffHunk[];
}

// ---------------------------------------------------------------------------
// V4A (Codex / OpenAI "*** Begin Patch") format detection — mirrors Go backend
// ---------------------------------------------------------------------------

function isV4AHunkHeader(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed.startsWith("@@")) return false;
  let inner = trimmed.slice(2).trim();
  if (inner.endsWith("@@")) inner = inner.slice(0, -2).trim();
  if (inner === "") return true; // bare @@
  if (inner.startsWith("-")) {
    const oldPart = inner.split(" ")[0]!.slice(1);
    const startStr = oldPart.split(",")[0]!;
    if (!Number.isNaN(parseInt(startStr, 10))) return false; // valid unified-diff header
  }
  return true;
}

function isV4APatch(patch: string): boolean {
  if (patch.includes("*** Begin Patch")) return true;
  for (const line of patch.split("\n")) {
    if (isV4AHunkHeader(line)) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// V4A parser — hunks use relative line numbers (1-based within each hunk)
// ---------------------------------------------------------------------------

function parseV4APatch(patch: string, fallbackPath: string): ParsedDiff {
  let filePath = fallbackPath;
  const hunks: ParsedDiffHunk[] = [];
  let currentLines: ParsedDiffLine[] | null = null;
  let currentHeader = "";
  let oldNo = 1;
  let newNo = 1;

  const flush = () => {
    if (currentLines && currentLines.length > 0) {
      hunks.push({ header: currentHeader, lines: currentLines });
    }
    currentLines = null;
  };

  for (const line of patch.split("\n")) {
    const trimmed = line.trim();

    if (
      trimmed === "*** Begin Patch" ||
      trimmed === "*** End Patch" ||
      trimmed === "*** End of File"
    )
      continue;

    if (
      trimmed.startsWith("*** Update File:") ||
      trimmed.startsWith("*** Add File:")
    ) {
      const path = trimmed.split(":").slice(1).join(":").trim();
      if (path) filePath = path;
      continue;
    }

    if (
      trimmed.startsWith("*** Delete File:") ||
      trimmed.startsWith("*** Move to:") ||
      trimmed.startsWith("*** Environment ID:")
    )
      continue;

    if (trimmed.startsWith("---") || trimmed.startsWith("+++")) continue;

    if (isV4AHunkHeader(line)) {
      flush();
      // V4A @@ lines may carry a context string instead of line numbers
      const ctx = trimmed === "@@" ? "" : trimmed.slice(2).trim();
      currentHeader = ctx ? `@@ ${ctx}` : "@@";
      currentLines = [];
      oldNo = 1;
      newNo = 1;
      continue;
    }

    if (currentLines === null) continue;

    if (line.length === 0) {
      currentLines.push({ kind: "ctx", oldNo: oldNo++, newNo: newNo++, content: "" });
      continue;
    }

    const sign = line[0];
    const content = line.slice(1);

    if (sign === " ") {
      currentLines.push({ kind: "ctx", oldNo: oldNo++, newNo: newNo++, content });
    } else if (sign === "+") {
      currentLines.push({ kind: "add", oldNo: null, newNo: newNo++, content });
    } else if (sign === "-") {
      currentLines.push({ kind: "del", oldNo: oldNo++, newNo: null, content });
    }
  }
  flush();

  return { filePath, hunks };
}

// ---------------------------------------------------------------------------
// Unified diff parser (git diff / diff -u)
// ---------------------------------------------------------------------------

function parseUnifiedDiff(patch: string, fallbackPath: string): ParsedDiff {
  const rawLines = patch.split("\n");
  let filePath = fallbackPath;
  const hunks: ParsedDiffHunk[] = [];
  let currentHunk: ParsedDiffHunk | null = null;
  let oldNo = 1;
  let newNo = 1;

  for (const raw of rawLines) {
    if (raw.startsWith("---")) {
      const m = /^---\s+(?:a\/)?(.+)$/.exec(raw);
      if (m && !filePath) filePath = m[1].trim();
      continue;
    }
    if (raw.startsWith("+++")) {
      const m = /^\+\+\+\s+(?:b\/)?(.+)$/.exec(raw);
      if (m) filePath = m[1].trim();
      continue;
    }
    if (raw.startsWith("@@")) {
      const m = /^@@\s+-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/.exec(raw);
      oldNo = m ? parseInt(m[1], 10) : 1;
      newNo = m ? parseInt(m[2], 10) : 1;
      currentHunk = { header: raw, lines: [] };
      hunks.push(currentHunk);
      continue;
    }
    if (!currentHunk) continue;
    if (raw.startsWith("\\")) continue; // "\ No newline at end of file"

    if (raw.startsWith("+")) {
      currentHunk.lines.push({ kind: "add", oldNo: null, newNo: newNo++, content: raw.slice(1) });
    } else if (raw.startsWith("-")) {
      currentHunk.lines.push({ kind: "del", oldNo: oldNo++, newNo: null, content: raw.slice(1) });
    } else if (raw.startsWith(" ")) {
      currentHunk.lines.push({ kind: "ctx", oldNo: oldNo++, newNo: newNo++, content: raw.slice(1) });
    }
  }

  return { filePath, hunks };
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/** Parse a unified diff or V4A patch into structured hunks and lines. */
export function parseDiffPatch(patch: string, fallbackPath = ""): ParsedDiff {
  if (isV4APatch(patch)) {
    return parseV4APatch(patch, fallbackPath);
  }
  return parseUnifiedDiff(patch, fallbackPath);
}

/** All diff lines from all hunks, in order. */
export function flattenDiffLines(parsed: ParsedDiff): ParsedDiffLine[] {
  return parsed.hunks.flatMap((h) => h.lines);
}

/** Total number of content lines across all hunks. */
export function totalDiffLines(parsed: ParsedDiff): number {
  return parsed.hunks.reduce((sum, h) => sum + h.lines.length, 0);
}
