/**
 * Paints the page backdrop (the two glows over the canvas gradient that the
 * `html` rule of styles.css draws) into a fixed <canvas> under the app, with
 * random dither.
 *
 * The glows span only a few dozen of a channel's 256 levels, so each level is
 * a band tens of pixels wide. Chromium and Firefox hide the bands with an
 * ordered dither: one fixed pattern per level, and where one pattern meets the
 * next a contrasty screen (a VA panel) shows a ring. Random dither has no
 * pattern to meet, so the same gradient reads smooth there. CSS cannot ask for
 * it, hence the canvas.
 *
 * The CSS background stays as it is and shows until the first paint, and
 * whenever the canvas cannot be used. The geometry below mirrors that rule;
 * change both together.
 */
import { subscribeAppliedUiTheme } from "./uiTheme";

export type Rgba = [number, number, number, number];

export interface BackdropColors {
  top: Rgba;
  bottom: Rgba;
  purple: Rgba;
  indigo: Rgba;
}

/** One `radial-gradient(<rx>px <ry>px at <x>% <y>%, color, transparent <end>%)`. */
interface Glow {
  rx: number;
  ry: number;
  x: number;
  y: number;
  end: number;
}

const PURPLE: Glow = { rx: 1200, ry: 600, x: 0.3, y: 0.1, end: 0.55 };
const INDIGO: Glow = { rx: 900, ry: 500, x: 0.8, y: 0.7, end: 0.5 };

const TOKENS = {
  top: "--coddy-canvas-gradient-top",
  bottom: "--coddy-canvas-gradient-bottom",
  purple: "--coddy-canvas-glow-purple",
  indigo: "--coddy-canvas-glow-indigo",
} as const;

/** parseCssColor reads the hex and rgb()/rgba() spellings the theme tokens use. */
export function parseCssColor(raw: string): Rgba | null {
  const s = raw.trim().toLowerCase();
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/.exec(s);
  const digits = hex?.[1];
  if (digits) {
    const h =
      digits.length === 3
        ? digits
            .split("")
            .map((c) => c + c)
            .join("")
        : digits;
    return [
      parseInt(h.slice(0, 2), 16),
      parseInt(h.slice(2, 4), 16),
      parseInt(h.slice(4, 6), 16),
      1,
    ];
  }
  const body = /^rgba?\(([^)]*)\)$/.exec(s)?.[1];
  if (body === undefined) {
    return null;
  }
  const parts = body.split(/[\s,/]+/).filter(Boolean);
  if (parts.length !== 3 && parts.length !== 4) {
    return null;
  }
  const [r, g, b] = parts.slice(0, 3).map(Number) as [number, number, number];
  const alpha = parts[3] === undefined ? 1 : parseAlpha(parts[3]);
  if (![r, g, b].every(Number.isFinite) || alpha === null) {
    return null;
  }
  return [r, g, b, alpha];
}

function parseAlpha(part: string): number | null {
  const v = part.endsWith("%") ? Number(part.slice(0, -1)) / 100 : Number(part);
  return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : null;
}

/** readBackdropColors reads the four tokens of the applied theme, or null when one does not parse. */
export function readBackdropColors(doc: Document): BackdropColors | null {
  const style = getComputedStyle(doc.documentElement);
  const out: Partial<BackdropColors> = {};
  for (const key of Object.keys(TOKENS) as (keyof BackdropColors)[]) {
    const c = parseCssColor(style.getPropertyValue(TOKENS[key]));
    if (!c) {
      return null;
    }
    out[key] = c;
  }
  return out as BackdropColors;
}

/** One picture paintBackdropLayers fills: RGBA pixels, and the panel tint laid over the backdrop, if any. */
export interface BackdropLayer {
  data: Uint8ClampedArray;
  tint?: Rgba | undefined;
}

/** A rectangle of the viewport in device pixels. */
export interface BackdropRegion {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Columns between exact samples of a glow; the alpha between them is interpolated. */
const GLOW_STEP = 8;

/**
 * paintBackdropLayers fills every layer (RGBA, `region.width` x
 * `region.height`) with the part of the backdrop that `region` covers in a
 * viewport of `viewWidth` x `viewHeight` device pixels: the vertical gradient,
 * the indigo glow over it, the purple glow on top, each glow fading linearly
 * in alpha to nothing at its end stop. A layer with a tint gets it laid over
 * the backdrop, which is how the backdrop shows through a translucent panel of
 * that colour. Each pixel of each layer then gets a triangular dither of about
 * one level. The backdrop is computed once per row for all the layers, and a
 * glow's alpha exactly every GLOW_STEP columns (it changes far too slowly for
 * the difference to reach a level). `scale` is device pixels per CSS pixel.
 * `random` is for tests; by default a xorshift generator gives each pixel its
 * noise, far cheaper than two calls of Math.random.
 */
export function paintBackdropLayers(
  layers: readonly BackdropLayer[],
  region: BackdropRegion,
  viewWidth: number,
  viewHeight: number,
  scale: number,
  colors: BackdropColors,
  random?: () => number,
): void {
  const glows: { g: Glow; c: Rgba }[] = [
    { g: INDIGO, c: colors.indigo },
    { g: PURPLE, c: colors.purple },
  ];
  const { x: rx, y: ry, width, height } = region;
  const [tr, tg, tb] = colors.top;
  const [br, bg, bb] = colors.bottom;
  const rs = new Float32Array(width);
  const gs = new Float32Array(width);
  const bs = new Float32Array(width);
  let seed = (Math.random() * 0x100000000) >>> 0 || 1;
  const noise = random
    ? () => random() - random()
    : () => {
        seed ^= seed << 13;
        seed ^= seed >>> 17;
        seed ^= seed << 5;
        // two 16-bit uniforms from one draw: their difference is triangular on (-1, 1)
        return ((seed & 0xffff) - (seed >>> 16)) / 65536;
      };
  for (let row = 0; row < height; row++) {
    const y = ry + row;
    const ty = viewHeight > 1 ? y / (viewHeight - 1) : 0;
    rs.fill(tr + (br - tr) * ty);
    gs.fill(tg + (bg - tg) * ty);
    bs.fill(tb + (bb - tb) * ty);
    for (const { g, c } of glows) {
      const [cr, cg, cb, ca] = c;
      // The end stop is an ellipse of radii end*rx, end*ry; only the columns
      // inside it on this row are touched.
      const ex = g.end * g.rx * scale;
      const ey = g.end * g.ry * scale;
      const cx = g.x * viewWidth;
      const dy = (y - g.y * viewHeight) / ey;
      if (dy <= -1 || dy >= 1) {
        continue;
      }
      const half = ex * Math.sqrt(1 - dy * dy);
      const x0 = Math.max(rx, Math.ceil(cx - half));
      const x1 = Math.min(rx + width - 1, Math.floor(cx + half));
      const alpha = (x: number) => {
        const dx = (x - cx) / ex;
        return ca * Math.max(0, 1 - Math.sqrt(dx * dx + dy * dy));
      };
      // exact alpha at every GLOW_STEP-th column, a straight line between
      let s = x0;
      let a0 = alpha(s);
      while (s <= x1) {
        const e = Math.min(x1 + 1, s + GLOW_STEP);
        const a1 = alpha(e);
        for (let x = s; x < e; x++) {
          const a = a0 + ((a1 - a0) * (x - s)) / (e - s);
          const i = x - rx;
          rs[i] = rs[i]! + (cr - rs[i]!) * a;
          gs[i] = gs[i]! + (cg - gs[i]!) * a;
          bs[i] = bs[i]! + (cb - bs[i]!) * a;
        }
        s = e;
        a0 = a1;
      }
    }
    const base = row * width * 4;
    for (const { data, tint } of layers) {
      const [kr, kg, kb, ka] = tint ?? [0, 0, 0, 0];
      if (random) {
        for (let i = 0, p = base; i < width; i++, p += 4) {
          const n = noise();
          const r = rs[i]!;
          const g = gs[i]!;
          const b = bs[i]!;
          data[p] = r + (kr - r) * ka + n;
          data[p + 1] = g + (kg - g) * ka + n;
          data[p + 2] = b + (kb - b) * ka + n;
          data[p + 3] = 255;
        }
        continue;
      }
      // The hot path: the generator inline and one 32-bit store per pixel
      // (RGBA bytes read as a little-endian word, which every engine that
      // runs this uses), rounded and clamped by hand as the clamped array would.
      const words = new Uint32Array(
        data.buffer,
        data.byteOffset,
        data.length >> 2,
      );
      const ia = 1 - ka;
      const or = kr * ka + 0.5;
      const og = kg * ka + 0.5;
      const ob = kb * ka + 0.5;
      for (let i = 0, w = base >> 2; i < width; i++, w++) {
        seed ^= seed << 13;
        seed ^= seed >>> 17;
        seed ^= seed << 5;
        const n = ((seed & 0xffff) - (seed >>> 16)) / 65536;
        let r = (rs[i]! * ia + or + n) | 0;
        let g = (gs[i]! * ia + og + n) | 0;
        let b = (bs[i]! * ia + ob + n) | 0;
        r = r < 0 ? 0 : r > 255 ? 255 : r;
        g = g < 0 ? 0 : g > 255 ? 255 : g;
        b = b < 0 ? 0 : b > 255 ? 255 : b;
        words[w] = (0xff000000 | (b << 16) | (g << 8) | r) >>> 0;
      }
    }
  }
}

/** paintBackdrop paints the whole viewport into one picture. */
export function paintBackdrop(
  data: Uint8ClampedArray,
  width: number,
  height: number,
  scale: number,
  colors: BackdropColors,
  random?: () => number,
  tint?: Rgba,
): void {
  paintBackdropLayers(
    [{ data, tint }],
    { x: 0, y: 0, width, height },
    width,
    height,
    scale,
    colors,
    random,
  );
}

const RESIZE_DELAY_MS = 150;
const PANEL_DELAY_MS = 100;

/**
 * The translucent panels that show the backdrop through glass: where to find
 * them, the tint token each one is drawn with, and the custom property its
 * painted image goes to. A browser blur would wipe the dither out and band
 * again, so in this mode a panel shows the backdrop with its tint baked in
 * instead (styles.css, `html[data-smooth-backdrop]`). Each image covers only
 * its panel's box, fixed to the viewport where the panel stands. The blur
 * itself is left out: the glows are far wider than any of these blurs, so it
 * would change nothing visible.
 */
export const BACKDROP_PANELS = [
  {
    selector: ".chat-header",
    tint: "--coddy-chat-header-bg",
    image: "--coddy-smooth-header",
  },
  { selector: ".rail-pill", tint: "--nav", image: "--coddy-smooth-nav" },
  {
    selector: ".composer-card",
    tint: "--coddy-glass-panel-bg",
    image: "--coddy-smooth-glass",
  },
] as const;

/**
 * followCanvasBackdrop mounts the canvas and paints it now, on every theme
 * change and after a resize (a page zoom included), and keeps an image for
 * every panel in BACKDROP_PANELS over the box the panel stands in, painted
 * again when a panel appears, goes or changes size. Returns the teardown,
 * which removes the canvas and the images, or null where a 2D context is not
 * available and nothing was mounted.
 */
export function followCanvasBackdrop(
  doc: Document = document,
): (() => void) | null {
  const win = doc.defaultView;
  if (!win) {
    return null;
  }
  const canvas = doc.createElement("canvas");
  const ctx = canvas.getContext?.("2d") ?? null;
  if (!ctx) {
    return null;
  }
  canvas.className = "canvas-backdrop";
  canvas.setAttribute("aria-hidden", "true");
  doc.body.prepend(canvas);
  const root = doc.documentElement;
  const urls = new Map<string, string>();
  const painted = new Map<string, string>(); // image property -> box it was painted for
  // A paint that a newer one overtook drops its images when they arrive.
  let generation = 0;

  const dropPanel = (name: string) => {
    root.style.removeProperty(name);
    const url = urls.get(name);
    if (url) {
      URL.revokeObjectURL(url);
    }
    urls.delete(name);
    painted.delete(name);
  };

  const view = () => {
    const scale = win.devicePixelRatio || 1;
    return {
      scale,
      width: Math.max(1, Math.round(win.innerWidth * scale)),
      height: Math.max(1, Math.round(win.innerHeight * scale)),
    };
  };

  /** The box (device pixels, inside the viewport) every element of a panel covers. */
  const panelBox = (selector: string, scale: number, w: number, h: number) => {
    let x0 = Infinity;
    let y0 = Infinity;
    let x1 = -Infinity;
    let y1 = -Infinity;
    for (const el of doc.querySelectorAll(selector)) {
      const r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) {
        continue;
      }
      x0 = Math.min(x0, r.left);
      y0 = Math.min(y0, r.top);
      x1 = Math.max(x1, r.right);
      y1 = Math.max(y1, r.bottom);
    }
    if (x0 === Infinity) {
      return null;
    }
    const x = Math.max(0, Math.floor(x0 * scale));
    const y = Math.max(0, Math.floor(y0 * scale));
    const box = {
      x,
      y,
      width: Math.min(w, Math.ceil(x1 * scale)) - x,
      height: Math.min(h, Math.ceil(y1 * scale)) - y,
    };
    return box.width > 0 && box.height > 0 ? box : null;
  };

  const paintPanels = (force: boolean) => {
    const colors = readBackdropColors(doc);
    if (!colors) {
      return;
    }
    const gen = generation;
    const { scale, width, height } = view();
    const style = getComputedStyle(root);
    for (const panel of BACKDROP_PANELS) {
      const tint = parseCssColor(style.getPropertyValue(panel.tint));
      const box = tint ? panelBox(panel.selector, scale, width, height) : null;
      if (!tint || !box) {
        dropPanel(panel.image);
        continue;
      }
      const key = `${box.x},${box.y},${box.width},${box.height},${width},${height}`;
      if (!force && painted.get(panel.image) === key) {
        continue;
      }
      const off = doc.createElement("canvas");
      off.width = box.width;
      off.height = box.height;
      const offCtx = off.getContext("2d");
      if (!offCtx) {
        continue;
      }
      const image = offCtx.createImageData(box.width, box.height);
      paintBackdropLayers(
        [{ data: image.data, tint }],
        box,
        width,
        height,
        scale,
        colors,
      );
      offCtx.putImageData(image, 0, 0);
      painted.set(panel.image, key);
      off.toBlob((blob) => {
        if (!blob || gen !== generation) {
          return;
        }
        const url = URL.createObjectURL(blob);
        const old = urls.get(panel.image);
        if (old) {
          URL.revokeObjectURL(old);
        }
        urls.set(panel.image, url);
        // fixed to the viewport: the box's place and size in CSS pixels
        root.style.setProperty(
          panel.image,
          `url(${url}) ${box.x / scale}px ${box.y / scale}px / ${box.width / scale}px ${box.height / scale}px`,
        );
      });
    }
  };

  const paint = () => {
    generation++;
    const colors = readBackdropColors(doc);
    if (!colors) {
      canvas.hidden = true;
      return;
    }
    const { scale, width, height } = view();
    canvas.width = width;
    canvas.height = height;
    const image = ctx.createImageData(width, height);
    paintBackdrop(image.data, width, height, scale, colors);
    ctx.putImageData(image, 0, 0);
    canvas.hidden = false;
    paintPanels(true);
  };

  // Panels come and go with the screen (the start screen has no title bar)
  // and the composer grows with its text: watch which elements there are and
  // their size, and paint only the panels whose box moved.
  let panelTimer: ReturnType<typeof setTimeout> | undefined;
  const schedulePanels = () => {
    clearTimeout(panelTimer);
    panelTimer = setTimeout(() => {
      observePanels();
      paintPanels(false);
    }, PANEL_DELAY_MS);
  };
  const sizes =
    typeof ResizeObserver === "function"
      ? new ResizeObserver(schedulePanels)
      : null;
  let observed: Element[] = [];
  const observePanels = () => {
    const now = BACKDROP_PANELS.flatMap((p) => [
      ...doc.querySelectorAll(p.selector),
    ]);
    if (
      now.length === observed.length &&
      now.every((el, i) => el === observed[i])
    ) {
      return;
    }
    sizes?.disconnect();
    for (const el of now) sizes?.observe(el);
    observed = now;
  };
  const tree =
    typeof MutationObserver === "function"
      ? new MutationObserver(() => {
          const now = BACKDROP_PANELS.flatMap((p) => [
            ...doc.querySelectorAll(p.selector),
          ]);
          if (
            now.length !== observed.length ||
            now.some((el, i) => el !== observed[i])
          ) {
            schedulePanels();
          }
        })
      : null;

  let timer: ReturnType<typeof setTimeout> | undefined;
  const onResize = () => {
    clearTimeout(timer);
    timer = setTimeout(paint, RESIZE_DELAY_MS);
  };
  observePanels();
  paint();
  tree?.observe(doc.body, { childList: true, subtree: true });
  win.addEventListener("resize", onResize);
  const stopTheme = subscribeAppliedUiTheme(paint);
  return () => {
    generation++;
    clearTimeout(timer);
    clearTimeout(panelTimer);
    win.removeEventListener("resize", onResize);
    stopTheme();
    sizes?.disconnect();
    tree?.disconnect();
    canvas.remove();
    for (const panel of BACKDROP_PANELS) dropPanel(panel.image);
  };
}
