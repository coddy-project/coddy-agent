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
  gen: number;
  /** Moves when a file under the frame's root changes. */
  stamp: string;
  /** The frame's address, relative to the API base. */
  url: string;
  expires_at: string;
  report?: CanvasReportSummary;
}

/** A node of a .canvas file that is not a live frame: a text card, a file
 *  the canvas does not run, a link or a group (JSON Canvas 1.0). */
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

/** A line between two nodes of the file. */
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

/** A .canvas file of the workspace as the canvas shows it
 *  (GET /coddy/sessions/{id}/canvas?file=). */
export interface CanvasBoard {
  session_id: string;
  /** The file, relative to the workspace; "" while the chat has no canvas. */
  file: string;
  /** The agent shows pages on this file when it names none. */
  current: boolean;
  revision: number;
  /** Moves when the file changes, whoever changed it; "missing" when gone. */
  stamp: string;
  /** Why the file cannot be read; nothing of it is shown then. */
  error?: string;
  frames: CanvasFrame[];
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

function fileQuery(file: string): string {
  return file ? `?file=${encodeURIComponent(file)}` : "";
}

function framePath(sessionId: string, frameId: string): string {
  return `${boardPath(sessionId)}/frames/${encodeURIComponent(frameId)}`;
}

const json = { "Content-Type": "application/json" };

/** Reads a .canvas file as a board; without a file, the chat's current one. */
export async function readBoard(
  sessionId: string,
  file: string,
  signal?: AbortSignal,
): Promise<CanvasBoard> {
  const raw = await readJson<Partial<CanvasBoard> | null>(
    boardPath(sessionId) + fileQuery(file),
    {
      signal: signal ?? null,
      cache: "no-store",
    },
  );
  return normalizeBoard(raw);
}

/** Makes a .canvas file the one the agent shows pages on: the person opened
 *  it. Answers the board. */
export async function openCanvasFile(
  sessionId: string,
  file: string,
): Promise<CanvasBoard> {
  return normalizeBoard(
    await readJson<Partial<CanvasBoard>>(
      `${boardPath(sessionId)}/open${fileQuery(file)}`,
      { method: "POST" },
    ),
  );
}

/** A board as the window can draw it, whatever came back: a proxy's answer
 *  may lack a field. */
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
  return {
    session_id: typeof raw?.session_id === "string" ? raw.session_id : "",
    file: typeof raw?.file === "string" ? raw.file : "",
    current: raw?.current === true,
    revision: typeof raw?.revision === "number" ? raw.revision : 0,
    stamp: typeof raw?.stamp === "string" ? raw.stamp : "",
    ...(typeof raw?.error === "string" && raw.error
      ? { error: raw.error }
      : {}),
    frames,
    items,
    edges,
  };
}

/** One node moved or resized: a frame (by a size or a device), a card, a link
 *  or a group. */
export interface NodeChange {
  id: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  device?: CanvasDevice;
}

/** Moves or resizes nodes of a board in one change (a group carries its
 *  nodes). */
export function patchNodes(
  sessionId: string,
  file: string,
  nodes: NodeChange[],
): Promise<{ revision: number }> {
  return readJson(`${boardPath(sessionId)}/nodes${fileQuery(file)}`, {
    method: "PATCH",
    headers: json,
    body: JSON.stringify({ nodes }),
  });
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

/** Puts a page on a board: the file named, or the chat's current one, or a
 *  new canvas/<folder>.canvas made for it (created says so). */
export function addFrame(
  sessionId: string,
  file: string,
  body: { path: string; device?: CanvasDevice; title?: string },
): Promise<{
  reason: string;
  file: string;
  created: boolean;
  frame: CanvasFrame;
  revision: number;
}> {
  return readJson(`${boardPath(sessionId)}/frames${fileQuery(file)}`, {
    method: "POST",
    headers: json,
    body: JSON.stringify(body),
  });
}

/** Takes a frame off its board: its node and the edges at it leave the file. */
export function removeFrame(
  sessionId: string,
  file: string,
  frameId: string,
): Promise<{ revision: number }> {
  return readJson(framePath(sessionId, frameId) + fileQuery(file), {
    method: "DELETE",
  });
}

export function rotateFrame(
  sessionId: string,
  file: string,
  frameId: string,
): Promise<{ frame: CanvasFrame; revision: number }> {
  return readJson(`${framePath(sessionId, frameId)}/rotate${fileQuery(file)}`, {
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
  file: string,
  frameId: string,
  report: CanvasReport,
  keepalive = false,
): Promise<void> {
  try {
    await resourceRequest(
      `${framePath(sessionId, frameId)}/report${fileQuery(file)}`,
      {
        method: "POST",
        headers: json,
        body: JSON.stringify(report),
        keepalive,
      },
    );
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
