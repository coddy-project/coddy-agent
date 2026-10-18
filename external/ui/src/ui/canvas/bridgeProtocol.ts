/**
 * The window's half of the canvas bridge (internal/canvas/bridge.js,
 * protocol coddy-canvas/1). Everything a frame says is untrusted: a message
 * is read only into the shapes below, every string cut to its limit, and
 * nothing of it is ever rendered as HTML.
 */

export const BRIDGE_PROTOCOL = "coddy-canvas/1";

const MAX_TEXT = 300;
const MAX_HTML = 2000;
const MAX_MESSAGE = 1000;

export interface PickedElement {
  selector: string;
  tag: string;
  id: string;
  classes: string[];
  text: string;
  html: string;
  rect: { x: number; y: number; width: number; height: number };
  styles: Record<string, string>;
}

export type BridgeMessage =
  | { type: "ready" }
  | { type: "location"; gen: number; href: string }
  | { type: "loaded"; gen: number; title: string }
  | {
      type: "console";
      gen: number;
      level: "error" | "warning";
      message: string;
      source: string;
      line: number;
      column: number;
    }
  | { type: "pick"; gen: number; element: PickedElement }
  | { type: "escape"; gen: number }
  | { type: "leaving"; gen: number; frame: string };

function str(v: unknown, max: number): string {
  return typeof v === "string" ? v.slice(0, max) : "";
}

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

/** Reads one message event's data, or null for anything that is not a
 *  well-formed message of the protocol. */
export function parseBridgeMessage(data: unknown): BridgeMessage | null {
  if (!data || typeof data !== "object") return null;
  const d = data as Record<string, unknown>;
  if (d.proto !== BRIDGE_PROTOCOL || typeof d.type !== "string") return null;
  if (d.type === "ready") return { type: "ready" };
  const gen = num(d.gen);
  switch (d.type) {
    case "location":
      return { type: "location", gen, href: str(d.href, 4096) };
    case "loaded":
      return { type: "loaded", gen, title: str(d.title, MAX_TEXT) };
    case "console":
      return {
        type: "console",
        gen,
        level: d.level === "warning" ? "warning" : "error",
        message: str(d.message, MAX_MESSAGE),
        source: str(d.source, MAX_TEXT),
        line: num(d.line),
        column: num(d.column),
      };
    case "pick": {
      const rect =
        d.rect && typeof d.rect === "object"
          ? (d.rect as Record<string, unknown>)
          : {};
      const styles: Record<string, string> = {};
      if (d.styles && typeof d.styles === "object") {
        for (const [k, v] of Object.entries(
          d.styles as Record<string, unknown>,
        ).slice(0, 16)) {
          if (/^[a-zA-Z]{1,32}$/.test(k)) styles[k] = str(v, 120);
        }
      }
      return {
        type: "pick",
        gen,
        element: {
          selector: str(d.selector, MAX_TEXT),
          tag: str(d.tag, 32),
          id: str(d.id, 120),
          classes: Array.isArray(d.classes)
            ? d.classes.slice(0, 5).map((c) => str(c, 60))
            : [],
          text: str(d.text, MAX_TEXT),
          html: str(d.html, MAX_HTML),
          rect: {
            x: num(rect.x),
            y: num(rect.y),
            width: num(rect.width),
            height: num(rect.height),
          },
          styles,
        },
      };
    }
    case "escape":
      return { type: "escape", gen };
    case "leaving":
      return { type: "leaving", gen, frame: str(d.frame, 64) };
    default:
      return null;
  }
}

/** Whether an address the page reports is still a page of its frame. */
export function staysInFrame(href: string, base: string): boolean {
  return base !== "" && href.startsWith(base);
}

/** The message the window sends a frame. */
export function bridgeCommand(
  type: "hello" | "mode" | "highlight",
  body: Record<string, unknown> = {},
): Record<string, unknown> {
  return { proto: BRIDGE_PROTOCOL, type, ...body };
}

/** Keeps a frame's console lines for its report: the newest 20. */
export function keepConsole<T>(lines: T[], line: T, max = 20): T[] {
  const next = [...lines, line];
  return next.length > max ? next.slice(next.length - max) : next;
}
