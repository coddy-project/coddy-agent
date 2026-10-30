/**
 * The picture markup editor without the screen: the shapes, the history
 * (marks, crops, quarter turns) and undo, the sizes a picture is drawn with.
 * The console's editor (external/cli/markup) follows the same rules, so a
 * picture marked up in either looks the same, but for what only a pen gives
 * the web editor: the pressure of a stroke.
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
 * its whole path, and the pressure of each point when a pen drew it (a mouse
 * or a finger keeps none, and draws at one width); ellipse, rectangle and
 * arrow keep two corners (tail and tip for the arrow); a label keeps its
 * top-left corner.
 */
export type MarkupShape =
  | {
      kind: "pen";
      color: string;
      width: number;
      points: Pt[];
      pressures?: number[];
    }
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

/**
 * The pressure a pointer event gives a pen stroke: a pen's own reading, or
 * none for a mouse or a finger, whose pressure says only that a button is
 * down.
 */
export function penPressure(e: {
  pointerType?: string;
  pressure?: number;
}): number | undefined {
  if (e.pointerType !== "pen") return undefined;
  const p = e.pressure;
  return typeof p === "number" && p > 0 ? clamp(p, 0, 1) : undefined;
}

/**
 * How much wider than its base width a stroke is drawn at a pressure: half
 * pressure, a pen's resting press, draws at the base width, as a mouse does.
 */
export function penWidthFactor(pressure: number): number {
  return clamp(0.35 + 1.3 * pressure, 0.35, 1.65);
}

/** One piece of a pen stroke: a straight line when ctrl is null. */
export type PenSegment = {
  from: Pt;
  ctrl: Pt | null;
  to: Pt;
  width: number;
};

/**
 * A pen stroke as pieces to draw: from midpoint to midpoint of its points,
 * each bent through the point between them, so the stroke runs smooth rather
 * than as a chain of straight lines, with straight pieces at both ends. Each
 * piece takes its width from the pressure at its point, or the stroke's
 * width when the stroke has none.
 */
export function penSegments(s: {
  width: number;
  points: readonly Pt[];
  pressures?: readonly number[];
}): PenSegment[] {
  const pts = s.points;
  const widthAt = (i: number): number => {
    const p = s.pressures?.[i];
    return typeof p === "number" ? s.width * penWidthFactor(p) : s.width;
  };
  const mid = (a: Pt, b: Pt): Pt => ({
    x: (a.x + b.x) / 2,
    y: (a.y + b.y) / 2,
  });
  const out: PenSegment[] = [];
  const n = pts.length;
  for (let i = 0; i + 1 < n; i++) {
    const a = pts[i];
    const b = pts[i + 1];
    if (!a || !b) continue;
    if (n === 2) {
      out.push({ from: a, ctrl: null, to: b, width: widthAt(1) });
      continue;
    }
    // The first piece runs straight from the start to the first midpoint,
    // and the last straight from the last midpoint to the end.
    if (i === 0)
      out.push({ from: a, ctrl: null, to: mid(a, b), width: widthAt(0) });
    const next = pts[i + 2];
    if (next) {
      out.push({
        from: mid(a, b),
        ctrl: b,
        to: mid(b, next),
        width: widthAt(i + 1),
      });
    } else {
      out.push({ from: mid(a, b), ctrl: null, to: b, width: widthAt(i + 1) });
    }
  }
  return out;
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
