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

export interface CanvasBoard {
  session_id: string;
  revision: number;
  frames: CanvasFrame[];
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
  return {
    session_id: typeof raw?.session_id === "string" ? raw.session_id : "",
    revision: typeof raw?.revision === "number" ? raw.revision : 0,
    frames,
  };
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
