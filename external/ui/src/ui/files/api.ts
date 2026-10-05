import { remoteApiRequest } from "../env/remoteEnv";

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

export async function readJson<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await resourceRequest(path, init);
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as {
      error?: { message?: string };
    } | null;
    throw new Error(body?.error?.message || `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export function readTree(
  id: string,
  path: string,
  cursor = "",
  hidden = false,
  signal?: AbortSignal,
): Promise<TreePage> {
  return readJson(
    workspaceUrl(id, "tree", path) +
      `&cursor=${encodeURIComponent(cursor)}&include_hidden=${hidden ? 1 : 0}`,
    { signal: signal ?? null },
  );
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
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
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
