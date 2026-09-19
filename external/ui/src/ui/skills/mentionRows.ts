/**
 * Rows of the composer's **`@`** picker. They come from **`GET /coddy/mentions`**
 * (the same search the console runs), plus the recent mentions this browser
 * remembers for the session (**`workspaceAtRecents`**).
 */

import type { WorkspaceAtRecentStored } from "./workspaceAtRecents";

/** Candidate kinds the server answers with. */
export type MentionKind =
  | "file"
  | "directory"
  | "session"
  | "rule"
  | "agent"
  | "plan"
  | "doc"
  | "scheme";

/** One picker row: **`insert`** replaces **`@`** plus the query in the draft. */
export type MentionRow = {
  kind: string;
  insert: string;
  label: string;
  detail?: string;
  /** Choosing it keeps the picker open: a folder to look into, a scheme hint. */
  continue?: boolean;
};

/** The answer of **`GET /coddy/mentions`**. */
export type MentionSearchBody = {
  items?: MentionRow[];
  total?: number;
  indexing?: boolean;
  index_truncated?: boolean;
};

/** The draft text for a path: quoted when it holds a space. */
export function mentionInsertForPath(path: string): string {
  if (/[\s]/u.test(path)) {
    return path.endsWith("/") ? `@"${path}` : `@"${path}"`;
  }
  return `@${path}`;
}

/** Parent folder shown next to a path, empty at the top of the workspace. */
function parentOf(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  const cut = trimmed.lastIndexOf("/");
  return cut < 0 ? "" : trimmed.slice(0, cut + 1);
}

/** A remembered mention as a picker row. */
export function mentionRowFromRecent(e: WorkspaceAtRecentStored): MentionRow {
  const dir = e.kind === "dir";
  const label = dir
    ? e.path_rel.endsWith("/")
      ? e.path_rel
      : `${e.path_rel}/`
    : e.path_rel.replace(/\/$/, "");
  return {
    kind: dir ? "directory" : "file",
    insert: mentionInsertForPath(label),
    label,
    detail: parentOf(label),
    ...(dir ? { continue: true } : {}),
  };
}

/** Recent rows first, then the server's, without repeating an insert. */
export function mergeMentionRows(
  recent: MentionRow[],
  server: MentionRow[],
): MentionRow[] {
  const seen = new Set<string>();
  const out: MentionRow[] = [];
  for (const row of [...recent, ...server]) {
    if (seen.has(row.insert)) {
      continue;
    }
    seen.add(row.insert);
    out.push(row);
  }
  return out;
}

/** Which remembered kind a picked row is, or null for a meta row. */
export function recentKindOf(row: MentionRow): "file" | "dir" | null {
  if (row.kind === "file") {
    return "file";
  }
  if (row.kind === "directory") {
    return "dir";
  }
  return null;
}

/**
 * The composer text after a picked row replaces the draft **`[from, to)`**,
 * and where the caret lands. A file or a meta row ends with a space; a folder
 * or a scheme hint is a step, so the caret stays inside it. A quoted folder
 * (**`@"my folder/`**) is a step inside an open quote: its closing quote goes
 * in ahead of the caret, so the text names the folder even if no file follows,
 * and the quoted row picked next takes that quote over instead of doubling it.
 * The console does the same in **`completionProvider.Apply`**.
 */
export function applyMentionRow(
  text: string,
  from: number,
  to: number,
  row: MentionRow,
): { text: string; caret: number } {
  const quoted = row.insert.startsWith('@"');
  let tail = text.slice(to);
  if (quoted && text.slice(from, to).startsWith('@"') && tail.startsWith('"')) {
    tail = tail.slice(1);
  }
  if (quoted && !row.insert.endsWith('"')) {
    const head = text.slice(0, from) + row.insert;
    return { text: `${head}"${tail}`, caret: head.length };
  }
  const insert = row.continue ? row.insert : `${row.insert} `;
  const head = text.slice(0, from) + insert;
  return { text: head + tail, caret: head.length };
}
