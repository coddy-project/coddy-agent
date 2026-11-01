import {
  arrowGeometry,
  penSegments,
  penWidthFactor,
  type MarkupOp,
  type MarkupShape,
  type Size,
} from "./markupModel";

/** Makes a canvas of a size; tests hand in a fake. */
export type CanvasFactory = (w: number, h: number) => HTMLCanvasElement;

export const makeCanvas: CanvasFactory = (w, h) => {
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(w));
  c.height = Math.max(1, Math.round(h));
  return c;
};

/** The font a label is drawn in: bold, the platform's sans. */
export function labelFont(size: number): string {
  return `bold ${size}px system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`;
}

/**
 * Gives a canvas's pixels back at once rather than at garbage collection:
 * Safari caps the memory of every canvas of a page together.
 */
export function releaseCanvas(c: HTMLCanvasElement): void {
  c.width = 0;
  c.height = 0;
}

/** Draws one mark onto a context in picture coordinates. */
export function drawShape(ctx: CanvasRenderingContext2D, s: MarkupShape): void {
  ctx.save();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.strokeStyle = s.color;
  ctx.fillStyle = s.color;
  switch (s.kind) {
    case "pen": {
      const [first] = s.points;
      if (!first) break;
      if (s.points.length === 1) {
        // A click is a dot.
        const p = s.pressures?.[0];
        const w = typeof p === "number" ? s.width * penWidthFactor(p) : s.width;
        ctx.beginPath();
        ctx.arc(first.x, first.y, w / 2, 0, Math.PI * 2);
        ctx.fill();
        break;
      }
      const segments = penSegments(s);
      if (!s.pressures) {
        // One width: one path, so the joins stay round and nothing overlaps.
        ctx.lineWidth = s.width;
        ctx.beginPath();
        ctx.moveTo(first.x, first.y);
        for (const g of segments) {
          if (g.ctrl) ctx.quadraticCurveTo(g.ctrl.x, g.ctrl.y, g.to.x, g.to.y);
          else ctx.lineTo(g.to.x, g.to.y);
        }
        ctx.stroke();
        break;
      }
      // A width per piece: each is a path of its own, joined by round caps.
      for (const g of segments) {
        ctx.lineWidth = g.width;
        ctx.beginPath();
        ctx.moveTo(g.from.x, g.from.y);
        if (g.ctrl) ctx.quadraticCurveTo(g.ctrl.x, g.ctrl.y, g.to.x, g.to.y);
        else ctx.lineTo(g.to.x, g.to.y);
        ctx.stroke();
      }
      break;
    }
    case "ellipse": {
      ctx.lineWidth = s.width;
      const cx = (s.a.x + s.b.x) / 2;
      const cy = (s.a.y + s.b.y) / 2;
      ctx.beginPath();
      ctx.ellipse(
        cx,
        cy,
        Math.abs(s.b.x - s.a.x) / 2,
        Math.abs(s.b.y - s.a.y) / 2,
        0,
        0,
        Math.PI * 2,
      );
      ctx.stroke();
      break;
    }
    case "rect": {
      ctx.lineWidth = s.width;
      ctx.lineJoin = "miter";
      ctx.strokeRect(
        Math.min(s.a.x, s.b.x),
        Math.min(s.a.y, s.b.y),
        Math.abs(s.b.x - s.a.x),
        Math.abs(s.b.y - s.a.y),
      );
      break;
    }
    case "arrow": {
      ctx.lineWidth = s.width;
      const g = arrowGeometry(s.a, s.b, s.width);
      ctx.beginPath();
      ctx.moveTo(s.a.x, s.a.y);
      ctx.lineTo(g.shaftEnd.x, g.shaftEnd.y);
      ctx.stroke();
      ctx.beginPath();
      ctx.moveTo(g.head[0].x, g.head[0].y);
      ctx.lineTo(g.head[1].x, g.head[1].y);
      ctx.lineTo(g.head[2].x, g.head[2].y);
      ctx.closePath();
      ctx.fill();
      break;
    }
    case "text": {
      ctx.font = labelFont(s.size);
      ctx.textBaseline = "top";
      ctx.fillText(s.text, s.at.x, s.at.y);
      break;
    }
  }
  ctx.restore();
}

/**
 * The picture with every step of the history applied, on a canvas of its
 * own. Nothing is lost to a crop: the original is replayed every time.
 */
export function renderPicture(
  base: CanvasImageSource,
  baseSize: Size,
  ops: readonly MarkupOp[],
  make: CanvasFactory = makeCanvas,
): HTMLCanvasElement {
  let canvas = make(baseSize.w, baseSize.h);
  let ctx = canvas.getContext("2d");
  if (!ctx) return canvas;
  ctx.drawImage(base, 0, 0);
  for (const op of ops) {
    if (op.kind === "shape") {
      drawShape(ctx, op.shape);
      continue;
    }
    const prev = canvas;
    if (op.kind === "crop") {
      canvas = make(op.w, op.h);
      ctx = canvas.getContext("2d");
      if (!ctx) return prev;
      ctx.drawImage(prev, op.x, op.y, op.w, op.h, 0, 0, op.w, op.h);
      releaseCanvas(prev);
    } else {
      // A quarter turn clockwise: (x, y) moves to (h - y, x).
      canvas = make(prev.height, prev.width);
      ctx = canvas.getContext("2d");
      if (!ctx) return prev;
      ctx.translate(prev.height, 0);
      ctx.rotate(Math.PI / 2);
      ctx.drawImage(prev, 0, 0);
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      releaseCanvas(prev);
    }
  }
  return canvas;
}

/** Encodes a canvas as PNG. */
export function canvasToPng(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("the picture could not be encoded"));
    }, "image/png");
  });
}

export type LoadedPicture = {
  source: CanvasImageSource;
  width: number;
  height: number;
  release?: () => void;
};

/** Decodes a picture file for drawing. */
export async function loadPicture(file: Blob): Promise<LoadedPicture> {
  if (typeof createImageBitmap === "function") {
    const bmp = await createImageBitmap(file);
    return {
      source: bmp,
      width: bmp.width,
      height: bmp.height,
      release: () => bmp.close(),
    };
  }
  const url = URL.createObjectURL(file);
  const img = new Image();
  try {
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve();
      img.onerror = () => reject(new Error("the picture could not be read"));
      img.src = url;
    });
  } catch (err) {
    URL.revokeObjectURL(url);
    throw err;
  }
  return {
    source: img,
    width: img.naturalWidth,
    height: img.naturalHeight,
    release: () => URL.revokeObjectURL(url),
  };
}
