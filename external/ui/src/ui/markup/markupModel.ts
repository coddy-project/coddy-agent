/**
 * The picture markup editor without the screen: the shapes, the history
 * (marks, crops, quarter turns) and undo, the sizes a picture is drawn with.
 * The console's editor (external/cli/markup) follows the same rules, so a
 * picture marked up in either looks the same.
 */

export type MarkupTool = "pen" | "ellipse" | "rect" | "arrow" | "text" | "crop";

export type Pt = { x: number; y: number };

/** The palette of both editors: red, orange, yellow, green, blue, white, black. */
export const MARKUP_PALETTE = [
  { id: "red", color: "#ff3b30" },
  { id: "orange", color: "#ff9500" },
  { id: "yellow", color: "#ffcc00" },
  { id: "green", color: "#34c759" },
  { id: "blue", color: "#0a84ff" },
  { id: "white", color: "#ffffff" },
  { id: "black", color: "#000000" },
] as const;

export type MarkupColorId = (typeof MARKUP_PALETTE)[number]["id"];

/**
 * One mark, in the picture's pixels at the moment it was drawn. A pen keeps
 * its whole path; ellipse, rectangle and arrow keep two corners (tail and tip
 * for the arrow); a label keeps its top-left corner.
 */
export type MarkupShape =
  | { kind: "pen"; color: string; width: number; points: Pt[] }
  | {
      kind: "ellipse" | "rect" | "arrow";
      color: string;
      width: number;
      a: Pt;
      b: Pt;
    }
  | { kind: "text"; color: string; size: number; at: Pt; text: string };

export type MarkupOp =
  | { kind: "shape"; shape: MarkupShape }
  | { kind: "crop"; x: number; y: number; w: number; h: number }
  | { kind: "rotate" };

export type Size = { w: number; h: number };

/** Line width that reads well on a picture of this size. */
export function strokeWidthFor(size: Size): number {
  return clamp(Math.max(size.w, size.h) / 240, 3, 14);
}

/** Font size of a label on a picture of this size. */
export function textSizeFor(size: Size): number {
  return clamp(Math.max(size.w, size.h) / 36, 16, 96);
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/** Size of the picture after every step of the history. */
export function sizeAfter(base: Size, ops: readonly MarkupOp[]): Size {
  let s = base;
  for (const op of ops) {
    if (op.kind === "crop") s = { w: op.w, h: op.h };
    else if (op.kind === "rotate") s = { w: s.h, h: s.w };
  }
  return s;
}

/**
 * The crop a drag from a to b asks for, clipped to the picture; null when it
 * keeps less than 2x2 pixels or all of the picture.
 */
export function cropFor(size: Size, a: Pt, b: Pt): MarkupOp | null {
  const x0 = Math.max(0, Math.round(Math.min(a.x, b.x)));
  const y0 = Math.max(0, Math.round(Math.min(a.y, b.y)));
  const x1 = Math.min(size.w, Math.round(Math.max(a.x, b.x)));
  const y1 = Math.min(size.h, Math.round(Math.max(a.y, b.y)));
  const w = x1 - x0;
  const h = y1 - y0;
  if (w < 2 || h < 2) return null;
  if (x0 === 0 && y0 === 0 && w === size.w && h === size.h) return null;
  return { kind: "crop", x: x0, y: y0, w, h };
}

/**
 * Whether a drag with a shape tool drew anything: a click (under 3 screen
 * pixels of travel) does not.
 */
export function dragDrew(a: Pt, b: Pt, screenPerPixel: number): boolean {
  return Math.hypot(b.x - a.x, b.y - a.y) * screenPerPixel >= 3;
}

/** The three corners of an arrow's head and where its shaft stops. */
export function arrowGeometry(
  a: Pt,
  b: Pt,
  width: number,
): { shaftEnd: Pt; head: [Pt, Pt, Pt] } {
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy) || 1;
  const ux = dx / len;
  const uy = dy / len;
  const head = Math.min(width * 4 + 6, len * 0.6);
  const half = head * 0.55;
  const bx = b.x - ux * head;
  const by = b.y - uy * head;
  return {
    shaftEnd: { x: b.x - ux * head * 0.5, y: b.y - uy * head * 0.5 },
    head: [
      { x: b.x, y: b.y },
      { x: bx - uy * half, y: by + ux * half },
      { x: bx + uy * half, y: by - ux * half },
    ],
  };
}

/**
 * Where a picture of size `pic` goes in a stage of size `stage`: fitted,
 * never enlarged, centred. k is screen pixels per picture pixel.
 */
export function fitView(
  pic: Size,
  stage: Size,
): { x: number; y: number; w: number; h: number; k: number } {
  if (pic.w <= 0 || pic.h <= 0 || stage.w <= 0 || stage.h <= 0) {
    return { x: 0, y: 0, w: pic.w, h: pic.h, k: 1 };
  }
  const k = Math.min(1, stage.w / pic.w, stage.h / pic.h);
  const w = pic.w * k;
  const h = pic.h * k;
  return { x: (stage.w - w) / 2, y: (stage.h - h) / 2, w, h, k };
}

/** The keys that pick a tool when no label is being typed. */
export const MARKUP_TOOL_KEYS: Record<string, MarkupTool> = {
  p: "pen",
  o: "ellipse",
  r: "rect",
  a: "arrow",
  t: "text",
  c: "crop",
};
