import { remoteApiRequest } from "../env/remoteEnv";
import { applyWorkspaceQuery, workspaceScope } from "../chat/workspaceScope";

export interface FileEntry {
  name: string;
  path_rel: string;
  kind: "file" | "directory" | "symlink" | "special";
  size_bytes: number;
  mod_time: string;
}
export interface TreePage {
  entries: FileEntry[];
  next_cursor: string;
  has_more: boolean;
}
export interface TextPage {
  path_rel: string;
  lines: string[];
  offset: number;
  next_offset: number;
  has_more: boolean;
  etag: string;
  mod_time: string;
  size_bytes: number;
  charset: string;
}
export interface FileMeta {
  etag: string;
  modTime: string;
  size: number;
  type: string;
}

export function workspaceUrl(
  sessionId: string,
  route: string,
  path = "",
): string {
  return `/coddy/sessions/${encodeURIComponent(sessionId)}/workspace/${route}?path_rel=${encodeURIComponent(path)}`;
}

/** Maps only API paths, including a remote base's path prefix. Tokens stay in headers. */
export function resourceRequest(
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const remote = remoteApiRequest(path, init);
  return remote ? fetch(remote.url, remote.init) : fetch(path, init);
}

/** A failed API read, with the HTTP status the server answered. */
export class HttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export async function readJson<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await resourceRequest(path, init);
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as {
      error?: { message?: string };
    } | null;
    throw new HttpError(body?.error?.message || `HTTP ${res.status}`, res.status);
  }
  return res.json() as Promise<T>;
}

export function readTree(
  id: string,
  path: string,
  cursor = "",
  hidden = false,
  signal?: AbortSignal,
  limit = 0,
): Promise<TreePage> {
  return readJson(
    workspaceUrl(id, "tree", path) +
      `&cursor=${encodeURIComponent(cursor)}&include_hidden=${hidden ? 1 : 0}` +
      (limit > 0 ? `&limit=${limit}` : ""),
    { signal: signal ?? null },
  );
}

/**
 * Reads a folder again as far as it was read before: at least `rows` rows
 * when it has them, so the pages "Load more" added survive a refresh.
 */
export async function rereadTree(
  id: string,
  path: string,
  rows: number,
  hidden: boolean,
  signal?: AbortSignal,
): Promise<TreePage> {
  const limit = Math.min(1000, Math.max(200, rows));
  let page = await readTree(id, path, "", hidden, signal, limit);
  // A page that brings nothing, or names the cursor it was asked from, ends
  // the walk: a server that says "more" and gives nothing must not hold it.
  for (let i = 0; i < 50 && page.has_more && page.entries.length < rows; i++) {
    const next = await readTree(id, path, page.next_cursor, hidden, signal, limit);
    const stuck = next.entries.length === 0 || next.next_cursor === page.next_cursor;
    page = { ...next, entries: [...page.entries, ...next.entries] };
    if (stuck) break;
  }
  return page;
}

/** A path the "@" index answers that stays inside the workspace. */
function insideWorkspace(path: string): boolean {
  if (
    !path ||
    /^[\\/]/.test(path) ||
    path === "~" ||
    path.startsWith("~/") ||
    /^[a-zA-Z]:[\\/]/.test(path)
  )
    return false;
  return !path.split(/[\\/]/).some((segment) => segment === "..");
}

export async function readMeta(
  id: string,
  path: string,
  etag = "",
  signal?: AbortSignal,
): Promise<FileMeta | null> {
  const res = await resourceRequest(workspaceUrl(id, "raw", path), {
    method: "HEAD",
    signal: signal ?? null,
    headers: etag ? { "If-None-Match": etag } : {},
  });
  if (res.status === 304) return null;
  if (!res.ok) throw new HttpError(`HTTP ${res.status}`, res.status);
  return {
    etag: res.headers.get("ETag") || "",
    modTime: res.headers.get("Last-Modified") || "",
    size: Number(res.headers.get("Content-Length") || 0),
    type: res.headers.get("Content-Type") || "application/octet-stream",
  };
}

export function readText(
  id: string,
  path: string,
  offset: number,
  etag = "",
  signal?: AbortSignal,
): Promise<TextPage> {
  return readJson(
    workspaceUrl(id, "text", path) +
      `&offset=${offset}&max_lines=300&etag=${encodeURIComponent(etag)}`,
    { signal: signal ?? null },
  );
}

export async function mediaUrl(
  id: string,
  path: string,
  download = false,
  signal?: AbortSignal,
): Promise<string> {
  const token = await readJson<{ token: string }>(
    workspaceUrl(id, "media-token"),
    {
      method: "POST",
      signal: signal ?? null,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path_rel: path, download }),
    },
  );
  const raw =
    workspaceUrl(id, "raw", path) +
    `&download=${download ? 1 : 0}&access_token=${encodeURIComponent(token.token)}`;
  return (
    remoteApiRequest(raw)?.url || new URL(raw, window.location.origin).href
  );
}

/** One hit of the window's filter: a file or a folder of the workspace. */
export interface FileHit {
  path: string;
  kind: "file" | "directory";
}

/**
 * Searches the session's whole workspace by name, folders that were never
 * opened included: the same ranked index the composer's "@" picker reads
 * (`GET /coddy/mentions`). Only files and folders come back; the other things
 * an "@" can name (sessions, rules, pages) are not files of the workspace.
 */
export async function searchFiles(
  sessionId: string,
  workspacePath: string,
  query: string,
  signal?: AbortSignal,
): Promise<FileHit[]> {
  const sp = new URLSearchParams({ q: query, limit: "60" });
  const scope = workspaceScope(sessionId, workspacePath);
  applyWorkspaceQuery(sp, scope);
  const body = await readJson<{
    items?: { kind?: string; label?: string }[];
  }>(`/coddy/mentions?${sp.toString()}`, {
    headers: scope.headers,
    signal: signal ?? null,
  });
  const hits: FileHit[] = [];
  for (const item of body.items || []) {
    const label = (item.label || "").trim();
    // The index also browses the disk for "/", "~/" and "../" queries; the
    // window shows the session's workspace only.
    if (!insideWorkspace(label)) continue;
    if (item.kind === "file") hits.push({ path: label, kind: "file" });
    else if (item.kind === "directory")
      hits.push({ path: label.replace(/\/+$/, ""), kind: "directory" });
  }
  return hits;
}

/** Resolves Markdown's ../ links into the canonical paths the server accepts. */
export function relativeFilePath(
  path: string,
  baseFile = "",
  cwd = "",
): string | null {
  let value = path.replace(/\\/g, "/");
  const root = cwd.replace(/\\/g, "/").replace(/\/$/, "");
  if (root && value.toLowerCase().startsWith(root.toLowerCase() + "/"))
    value = value.slice(root.length + 1);
  else if (/^[a-zA-Z]:\//.test(value)) return null;
  else if (root && value.startsWith("/")) return null;
  const stack = value.startsWith("/") ? [] : baseFile.split("/").slice(0, -1);
  for (const segment of value.replace(/^\//, "").split("/")) {
    if (!segment || segment === ".") continue;
    if (segment === "..") {
      if (!stack.length) return null;
      stack.pop();
    } else stack.push(segment);
  }
  return stack.join("/");
}
