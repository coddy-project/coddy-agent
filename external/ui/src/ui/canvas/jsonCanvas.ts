/**
 * What the canvas draws of a JSON Canvas file (https://jsoncanvas.org, the
 * format of Obsidian's canvases) besides its live frames: the colours of its
 * nodes and edges, where an edge leaves and enters a node, the curve between
 * them and the arrows at its ends. Pure functions over screen rectangles, so
 * the geometry is tested without a layout.
 */
import type { CanvasEdge, CanvasItem, CanvasSide } from "./api";
import type { Rect } from "./camera";

/** The six preset colours of JSON Canvas, as Obsidian paints them: red,
 *  orange, yellow, green, cyan, purple. */
export const PRESET_COLORS: Record<string, string> = {
  "1": "#fb464c",
  "2": "#e9973f",
  "3": "#e0de71",
  "4": "#44cf6e",
  "5": "#53dfdd",
  "6": "#a882ff",
};

/** A node's or an edge's colour as CSS, or undefined for the default one. A
 *  value that is neither a preset nor a hex colour is ignored, never put into
 *  a style as it was written. */
export function canvasColor(color?: string): string | undefined {
  if (!color) return undefined;
  const preset = PRESET_COLORS[color];
  if (preset) return preset;
  return /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i.test(color) ? color : undefined;
}

export interface Point {
  x: number;
  y: number;
}

const NORMALS: Record<CanvasSide, Point> = {
  top: { x: 0, y: -1 },
  right: { x: 1, y: 0 },
  bottom: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
};

function isSide(s: unknown): s is CanvasSide {
  return s === "top" || s === "right" || s === "bottom" || s === "left";
}

/** The middle of a side of a rectangle. */
export function sideAnchor(r: Rect, side: CanvasSide): Point {
  switch (side) {
    case "top":
      return { x: r.x + r.width / 2, y: r.y };
    case "right":
      return { x: r.x + r.width, y: r.y + r.height / 2 };
    case "bottom":
      return { x: r.x + r.width / 2, y: r.y + r.height };
    default:
      return { x: r.x, y: r.y + r.height / 2 };
  }
}

/** The side of `from` that faces `to`, for an edge that names none. */
export function facingSide(from: Rect, to: Rect): CanvasSide {
  const dx = to.x + to.width / 2 - (from.x + from.width / 2);
  const dy = to.y + to.height / 2 - (from.y + from.height / 2);
  if (Math.abs(dx) >= Math.abs(dy)) return dx >= 0 ? "right" : "left";
  return dy >= 0 ? "bottom" : "top";
}

export interface EdgeShape {
  /** The SVG path of the curve. */
  d: string;
  /** The arrowheads, as SVG polygon points, at the ends that have one. */
  arrows: string[];
  /** Where the label goes: the middle of the curve. */
  mid: Point;
}

function bezierAt(p: Point[], t: number): Point {
  const u = 1 - t;
  const a = u * u * u;
  const b = 3 * u * u * t;
  const c = 3 * u * t * t;
  const d = t * t * t;
  return {
    x: a * p[0]!.x + b * p[1]!.x + c * p[2]!.x + d * p[3]!.x,
    y: a * p[0]!.y + b * p[1]!.y + c * p[2]!.y + d * p[3]!.y,
  };
}

function arrowAt(tip: Point, from: Point, size: number): string {
  let ux = tip.x - from.x;
  let uy = tip.y - from.y;
  const len = Math.hypot(ux, uy) || 1;
  ux /= len;
  uy /= len;
  const bx = tip.x - ux * size;
  const by = tip.y - uy * size;
  const half = size * 0.55;
  const pts = [
    [tip.x, tip.y],
    [bx - uy * half, by + ux * half],
    [bx + uy * half, by - ux * half],
  ];
  return pts.map(([x, y]) => `${round(x!)},${round(y!)}`).join(" ");
}

function round(n: number): number {
  return Math.round(n * 10) / 10;
}

/**
 * The curve of an edge between two nodes on the screen: from the middle of
 * the side it names (or the side facing the other node) out along that
 * side's normal and into the other node the same way, as Obsidian draws it.
 * `scale` is the camera's, so the bend and the arrows grow with the board.
 * Null when either node is not on the board.
 */
export function edgeShape(
  edge: CanvasEdge,
  rects: ReadonlyMap<string, Rect>,
  scale: number,
): EdgeShape | null {
  const a = rects.get(edge.fromNode);
  const b = rects.get(edge.toNode);
  if (!a || !b) return null;
  const fromSide = isSide(edge.fromSide) ? edge.fromSide : facingSide(a, b);
  const toSide = isSide(edge.toSide) ? edge.toSide : facingSide(b, a);
  const p0 = sideAnchor(a, fromSide);
  const p3 = sideAnchor(b, toSide);
  const dist = Math.hypot(p3.x - p0.x, p3.y - p0.y);
  const bend = Math.min(Math.max(dist * 0.4, 24 * scale), 240 * scale);
  const n0 = NORMALS[fromSide];
  const n3 = NORMALS[toSide];
  const p1 = { x: p0.x + n0.x * bend, y: p0.y + n0.y * bend };
  const p2 = { x: p3.x + n3.x * bend, y: p3.y + n3.y * bend };
  const pts = [p0, p1, p2, p3];
  const size = Math.min(16, Math.max(6, 14 * scale));
  const arrows: string[] = [];
  // JSON Canvas: an edge ends in an arrow unless it says none, and starts
  // without one unless it says arrow.
  if ((edge.toEnd ?? "arrow") === "arrow") arrows.push(arrowAt(p3, p2, size));
  if (edge.fromEnd === "arrow") arrows.push(arrowAt(p0, p1, size));
  const r = (p: Point) => `${round(p.x)} ${round(p.y)}`;
  return {
    d: `M ${r(p0)} C ${r(p1)}, ${r(p2)}, ${r(p3)}`,
    arrows,
    mid: bezierAt(pts, 0.5),
  };
}

/** The nodes a group carries when it is dragged: every frame and item whose
 *  rectangle lies inside the group's, as Obsidian moves them. */
export function insideGroup(
  group: Rect,
  nodes: ReadonlyArray<{ id: string } & Rect>,
): string[] {
  return nodes
    .filter(
      (n) =>
        n.x >= group.x &&
        n.y >= group.y &&
        n.x + n.width <= group.x + group.width &&
        n.y + n.height <= group.y + group.height,
    )
    .map((n) => n.id);
}

/** Groups first, so the cards and pages stand over them, as in Obsidian. */
export function drawOrder(items: CanvasItem[]): CanvasItem[] {
  return [
    ...items.filter((i) => i.type === "group"),
    ...items.filter((i) => i.type !== "group"),
  ];
}

const IMAGE_RE = /\.(png|jpe?g|gif|webp|avif|svg|bmp)$/i;

/** A file node the canvas shows as a picture. */
export function isImageFile(path: string): boolean {
  return IMAGE_RE.test(path);
}

/** A file node the canvas shows as a note: its Markdown, rendered. */
export function isNoteFile(path: string): boolean {
  return /\.md$/i.test(path);
}

/** A link node's address, when it is one the page may open. */
export function safeLinkUrl(url?: string): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url);
    return u.protocol === "https:" || u.protocol === "http:"
      ? u.href
      : undefined;
  } catch {
    return undefined;
  }
}
