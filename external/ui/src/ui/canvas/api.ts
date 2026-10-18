import { remoteApiRequest } from "../env/remoteEnv";
import { readJson, resourceRequest } from "../files/api";

/** What the newest browser report said about a frame, as the board counts it. */
export interface CanvasReportSummary {
  stamp: string;
  loaded: boolean;
  closed?: boolean;
  error?: string;
  title?: string;
  errors: number;
  warnings: number;
  at: string;
}

/** One page on the canvas (GET /coddy/sessions/{id}/canvas). */
export interface CanvasFrame {
  id: string;
  path: string;
  root: string;
  title?: string;
  device?: "desktop" | "tablet" | "phone";
  width: number;
  height: number;
  x: number;
  y: number;
  added_by?: string;
  added_at: string;
  shown_at: string;
  gen: number;
  /** Moves when a file under the frame's root changes. */
  stamp: string;
  /** The frame's address, relative to the API base. */
  url: string;
  expires_at: string;
  report?: CanvasReportSummary;
}

/** A node of a linked .canvas file that is not a live frame: a text card, a
 *  file the canvas does not run, a link or a group (JSON Canvas 1.0). */
export interface CanvasItem {
  id: string;
  type: "text" | "file" | "link" | "group";
  x: number;
  y: number;
  width: number;
  height: number;
  /** A preset "1" to "6" or a hex colour. */
  color?: string;
  text?: string;
  file?: string;
  url?: string;
  label?: string;
  /** Why a file node naming a page does not run. */
  note?: string;
}

export type CanvasSide = "top" | "right" | "bottom" | "left";

/** A line between two nodes of a linked file. */
export interface CanvasEdge {
  id: string;
  fromNode: string;
  fromSide?: CanvasSide;
  fromEnd?: "none" | "arrow";
  toNode: string;
  toSide?: CanvasSide;
  toEnd?: "none" | "arrow";
  color?: string;
  label?: string;
}

/** The .canvas file of the workspace the board is, when it is one. */
export interface CanvasLink {
  path: string;
  /** Moves when the file changes; "missing" when it is gone. */
  stamp: string;
  error?: string;
}

export interface CanvasBoard {
  session_id: string;
  revision: number;
  frames: CanvasFrame[];
  link?: CanvasLink;
  items: CanvasItem[];
  edges: CanvasEdge[];
}

export type CanvasDevice = "desktop" | "tablet" | "phone";

/** The sizes the server gives a named device (internal/canvas). */
export const DEVICE_SIZES: Record<
  CanvasDevice,
  { width: number; height: number }
> = {
  desktop: { width: 1280, height: 800 },
  tablet: { width: 820, height: 1180 },
  phone: { width: 390, height: 844 },
};

function boardPath(sessionId: string): string {
  return `/coddy/sessions/${encodeURIComponent(sessionId)}/canvas`;
}

function framePath(sessionId: string, frameId: string): string {
  return `${boardPath(sessionId)}/frames/${encodeURIComponent(frameId)}`;
}

const json = { "Content-Type": "application/json" };

export async function readBoard(
  sessionId: string,
  signal?: AbortSignal,
): Promise<CanvasBoard> {
  const raw = await readJson<Partial<CanvasBoard> | null>(
    boardPath(sessionId),
    {
      signal: signal ?? null,
      cache: "no-store",
    },
  );
  return normalizeBoard(raw);
}

/** A board as the window can draw it, whatever came back: an older server or
 *  a proxy's answer may lack a field. */
export function normalizeBoard(
  raw: Partial<CanvasBoard> | null | undefined,
): CanvasBoard {
  const frames = Array.isArray(raw?.frames)
    ? raw.frames.filter(
        (f): f is CanvasFrame =>
          !!f && typeof f.id === "string" && typeof f.url === "string",
      )
    : [];
  const items = Array.isArray(raw?.items)
    ? raw.items.filter(
        (n): n is CanvasItem =>
          !!n &&
          typeof n.id === "string" &&
          ["text", "file", "link", "group"].includes(n.type) &&
          [n.x, n.y, n.width, n.height].every(Number.isFinite),
      )
    : [];
  const edges = Array.isArray(raw?.edges)
    ? raw.edges.filter(
        (e): e is CanvasEdge =>
          !!e &&
          typeof e.id === "string" &&
          typeof e.fromNode === "string" &&
          typeof e.toNode === "string",
      )
    : [];
  const link =
    raw?.link && typeof raw.link.path === "string"
      ? {
          path: raw.link.path,
          stamp: typeof raw.link.stamp === "string" ? raw.link.stamp : "",
          ...(typeof raw.link.error === "string" && raw.link.error
            ? { error: raw.link.error }
            : {}),
        }
      : undefined;
  return {
    session_id: typeof raw?.session_id === "string" ? raw.session_id : "",
    revision: typeof raw?.revision === "number" ? raw.revision : 0,
    frames,
    ...(link ? { link } : {}),
    items,
    edges,
  };
}

/** Moves or resizes nodes of the board: frames, and the cards, links and
 *  groups of a linked file, in one change (a group carries its nodes). */
export function patchNodes(
  sessionId: string,
  nodes: {
    id: string;
    x?: number;
    y?: number;
    width?: number;
    height?: number;
  }[],
): Promise<{ revision: number }> {
  return readJson(`${boardPath(sessionId)}/nodes`, {
    method: "PATCH",
    headers: json,
    body: JSON.stringify({ nodes }),
  });
}

/** Makes a .canvas file of the workspace the chat's canvas. */
export async function linkCanvasFile(
  sessionId: string,
  path: string,
): Promise<CanvasBoard> {
  return normalizeBoard(
    await readJson<Partial<CanvasBoard>>(`${boardPath(sessionId)}/link`, {
      method: "POST",
      headers: json,
      body: JSON.stringify({ path }),
    }),
  );
}

/** Gives the chat its own canvas back; the file stays. */
export async function unlinkCanvas(sessionId: string): Promise<CanvasBoard> {
  return normalizeBoard(
    await readJson<Partial<CanvasBoard>>(`${boardPath(sessionId)}/link`, {
      method: "DELETE",
    }),
  );
}

/** Saves the chat's own canvas as a new .canvas file and links it. */
export async function exportCanvas(
  sessionId: string,
  path: string,
): Promise<CanvasBoard> {
  return normalizeBoard(
    await readJson<Partial<CanvasBoard>>(`${boardPath(sessionId)}/export`, {
      method: "POST",
      headers: json,
      body: JSON.stringify({ path }),
    }),
  );
}

/** A page the canvas runs, by its name: the extensions the server's
 *  webfs.IsHTML takes (an .xhtml file is previewed as HTML in the Files window
 *  but not run on the canvas). */
export function isCanvasPage(path: string): boolean {
  return /\.html?$/i.test(path);
}

/** A JSON Canvas file, by its name. */
export function isCanvasFile(path: string): boolean {
  return /\.canvas$/i.test(path);
}

export function addFrame(
  sessionId: string,
  body: { path: string; device?: CanvasDevice; title?: string },
): Promise<{ reason: string; frame: CanvasFrame; revision: number }> {
  return readJson(`${boardPath(sessionId)}/frames`, {
    method: "POST",
    headers: json,
    body: JSON.stringify(body),
  });
}

export function patchFrame(
  sessionId: string,
  frameId: string,
  body: {
    title?: string;
    device?: CanvasDevice | "";
    width?: number;
    height?: number;
    x?: number;
    y?: number;
  },
): Promise<{ frame: CanvasFrame; revision: number }> {
  return readJson(framePath(sessionId, frameId), {
    method: "PATCH",
    headers: json,
    body: JSON.stringify(body),
  });
}

export function removeFrame(
  sessionId: string,
  frameId: string,
): Promise<{ revision: number }> {
  return readJson(framePath(sessionId, frameId), { method: "DELETE" });
}

export function rotateFrame(
  sessionId: string,
  frameId: string,
): Promise<{ frame: CanvasFrame; revision: number }> {
  return readJson(`${framePath(sessionId, frameId)}/rotate`, {
    method: "POST",
  });
}

/** One line of a frame's console, as the bridge reported it. */
export interface CanvasConsoleEntry {
  level: "error" | "warning";
  message: string;
  source?: string;
  line?: number;
  column?: number;
}

export interface CanvasReport {
  stamp: string;
  loaded: boolean;
  closed?: boolean;
  title?: string;
  console: CanvasConsoleEntry[];
}

/** Posts what the browser saw in a frame. Best effort: a report that does
 *  not arrive costs the agent a reading, never the person anything. */
export async function postReport(
  sessionId: string,
  frameId: string,
  report: CanvasReport,
  keepalive = false,
): Promise<void> {
  try {
    await resourceRequest(`${framePath(sessionId, frameId)}/report`, {
      method: "POST",
      headers: json,
      body: JSON.stringify(report),
      keepalive,
    });
  } catch {
    /* the server is away; the next report carries the state */
  }
}

/** The absolute address a frame's iframe loads: the environment's API base
 *  in front (a remote server, a relay's mount), the capability in the path
 *  and never a token. */
export function frameSrc(url: string): string {
  return (
    remoteApiRequest(url)?.url || new URL(url, window.location.origin).href
  );
}

/** The root address of a frame: everything up to and with its capability,
 *  where the page's own navigations stay. */
export function frameBase(src: string): string {
  const m = /^(.*\/coddy\/canvas\/[^/?#]+\/)/.exec(src);
  return m?.[1] ?? src;
}
