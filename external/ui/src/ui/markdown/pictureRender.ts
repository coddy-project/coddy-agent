import { LIGHT_THEMES, type UiThemeMode } from "../theme/themeCookie";
import { readAppliedUiTheme } from "../theme/uiTheme";
import { loadMermaid } from "./renderers";

/** A picture ready for an <img>: the SVG text and its drawn size in CSS pixels. */
export type RenderedPicture = { svg: string; width: number; height: number };

export type PictureKind = "mermaid" | "svg";

/** Fence labels drawn as pictures, by the first word of the info string. */
export function pictureKindOf(
  className: string | undefined,
): PictureKind | null {
  const m = /(?:^|\s)language-([\w-]+)/.exec(className || "");
  const lang = (m?.[1] || "").toLowerCase();
  if (lang === "mermaid") return "mermaid";
  if (lang === "svg") return "svg";
  return null;
}

/** An <img> source for SVG text. Inside <img> an SVG runs no script and loads nothing. */
export function svgDataUrl(svg: string): string {
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

const SVG_NS = "http://www.w3.org/2000/svg";

/** A bigger picture is not something a reply should carry inline. */
const MAX_SVG_CHARS = 512 * 1024;

/**
 * Gives an SVG document what it needs to be drawn on its own, in an <img> or
 * as a downloaded file: the SVG namespace, and a width and height in pixels
 * (Mermaid writes width="100%" and a max-width style, which an <img> cannot
 * size). Throws on text that is not an SVG document.
 */
export function standaloneSvg(text: string): RenderedPicture {
  if (text.length > MAX_SVG_CHARS)
    throw new Error("the SVG is larger than 512 KB");
  const doc = new DOMParser().parseFromString(text, "image/svg+xml");
  const root = doc.documentElement;
  if (
    !root ||
    root.nodeName.toLowerCase() !== "svg" ||
    doc.getElementsByTagName("parsererror").length
  ) {
    throw new Error("not an SVG document");
  }
  if (!root.getAttribute("xmlns")) root.setAttribute("xmlns", SVG_NS);
  const box = (root.getAttribute("viewBox") || "")
    .trim()
    .split(/[\s,]+/)
    .map(Number);
  const vbW = box.length === 4 && box[2]! > 0 ? box[2]! : 0;
  const vbH = box.length === 4 && box[3]! > 0 ? box[3]! : 0;
  let width = pixels(root.getAttribute("width"));
  let height = pixels(root.getAttribute("height"));
  if (!width && !height && vbW && vbH) {
    width = vbW;
    height = vbH;
  } else if (width && !height && vbW && vbH) {
    height = (width * vbH) / vbW;
  } else if (height && !width && vbW && vbH) {
    width = (height * vbW) / vbH;
  }
  if (!width || !height) {
    width ||= 300;
    height ||= 150;
  }
  root.setAttribute("width", String(round(width)));
  root.setAttribute("height", String(round(height)));
  const style = root.getAttribute("style");
  if (style) {
    const kept = style
      .split(";")
      .filter((d) => d.trim() && !/^\s*max-(width|height)\s*:/i.test(d))
      .join(";");
    if (kept) root.setAttribute("style", kept);
    else root.removeAttribute("style");
  }
  return {
    svg: new XMLSerializer().serializeToString(root),
    width: round(width),
    height: round(height),
  };
}

function pixels(value: string | null): number {
  if (!value) return 0;
  const m = /^\s*([\d.]+)\s*(px)?\s*$/i.exec(value);
  return m ? Number(m[1]) || 0 : 0;
}

function round(n: number): number {
  return Math.round(n * 100) / 100;
}

/** An opaque colour, 0-255 per channel. */
type Rgb = [number, number, number];

/** The colours a diagram takes from the active appearance, every one opaque. */
export type DiagramPalette = {
  dark: boolean;
  /** What the picture is drawn on: the figure's surface. */
  background: Rgb;
  text: Rgb;
  accent: Rgb;
  /** Categorical colours, readable as marks on `background`: pie slices, branches, series. */
  series: Rgb[];
  font: string;
};

/** The syntax palette of every theme is tuned to be read on that theme's surface. */
const SERIES_TOKENS = [
  "--accent",
  "--syntax-string",
  "--syntax-number",
  "--syntax-title",
  "--syntax-meta",
  "--syntax-type",
  "--syntax-deletion",
  "--syntax-keyword",
  "--syntax-attribute",
  "--syntax-comment",
];

/**
 * Reads the active theme's colours. A token may be a color-mix() or carry
 * alpha, which Mermaid's colour code cannot read, so each one is resolved by
 * the browser on a probe element and composited onto the surface below it.
 * Returns null where the browser resolves nothing (jsdom): Mermaid then keeps
 * its own dark or light defaults.
 */
export function diagramPalette(theme: UiThemeMode): DiagramPalette | null {
  const probe = document.createElement("span");
  probe.style.display = "none";
  document.body.appendChild(probe);
  try {
    const resolve = (token: string, under?: Rgb): Rgb | null => {
      probe.style.color = "";
      probe.style.color = `var(${token})`;
      const c = parseCssColor(getComputedStyle(probe).color);
      if (!c) return null;
      const [r, g, b, alpha] = c;
      const base = under ?? [r, g, b];
      return mix([r, g, b], base, alpha);
    };
    const page = resolve("--bg");
    if (!page) return null;
    const background = resolve("--coddy-surface-inset", page) ?? page;
    const text = resolve("--text", background);
    const accent = resolve("--accent", background);
    if (!text || !accent) return null;
    const series = SERIES_TOKENS.map((t) => resolve(t, background)).filter(
      (c): c is Rgb => c !== null,
    );
    return {
      dark: !LIGHT_THEMES.has(theme),
      background,
      text,
      accent,
      series: series.length ? series : [accent],
      font: getComputedStyle(document.body).fontFamily || "sans-serif",
    };
  } finally {
    probe.remove();
  }
}

/** rgb(), rgba() and color(srgb ...) as a browser serialises a computed colour. */
export function parseCssColor(
  value: string,
): [number, number, number, number] | null {
  const v = value.trim();
  let m =
    /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)$/i.exec(
      v,
    );
  if (m) {
    return [Number(m[1]), Number(m[2]), Number(m[3]), alphaOf(m[4])];
  }
  m =
    /^color\(\s*srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+%?))?\s*\)$/i.exec(
      v,
    );
  if (m) {
    return [
      Number(m[1]) * 255,
      Number(m[2]) * 255,
      Number(m[3]) * 255,
      alphaOf(m[4]),
    ];
  }
  return null;
}

function alphaOf(raw: string | undefined): number {
  if (raw === undefined) return 1;
  return raw.endsWith("%") ? Number(raw.slice(0, -1)) / 100 : Number(raw);
}

/** WCAG's contrast for text: what a label in the surface colour needs on a mark. */
const MARK_CONTRAST = 4.5;

function luminance(c: Rgb): number {
  const [r, g, b] = c.map((n) => {
    const v = n / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

export function contrastRatio(a: Rgb, b: Rgb): number {
  const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m);
  return (x! + 0.05) / (y! + 0.05);
}

/** `c` moved toward white (on a dark surface) or black (on a light one) until it contrasts with `surface` by `ratio`. */
function awayFrom(c: Rgb, surface: Rgb, ratio: number): Rgb {
  const target: Rgb = luminance(surface) < 0.5 ? [255, 255, 255] : [0, 0, 0];
  let out = c;
  for (
    let step = 1;
    step <= 20 && contrastRatio(out, surface) < ratio;
    step++
  ) {
    out = mix(target, c, step / 20);
  }
  return out;
}

/** How far apart (in RGB) two categorical colours have to be. */
const DISTINCT = 48;

function distance(a: Rgb, b: Rgb): number {
  return Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);
}

/** `top` at weight `w` over `base`. */
function mix(top: Rgb, base: Rgb, w: number): Rgb {
  return [0, 1, 2].map((i) =>
    Math.round(top[i]! * w + base[i]! * (1 - w)),
  ) as Rgb;
}

function hex(c: Rgb): string {
  return `#${c.map((n) => Math.max(0, Math.min(255, n)).toString(16).padStart(2, "0")).join("")}`;
}

/**
 * Mermaid's theme variables for a palette. Mermaid derives what it is not told
 * from a few base colours, which on a dark surface gave near-black pie slices,
 * white Gantt bars under white text and edges the colour of the background; so
 * every colour a diagram type draws with is set here, from the theme:
 * - fills are the accent or a series colour mixed into the surface, text on
 *   them is the theme's text;
 * - lines and edges are the text at 70% over the surface;
 * - marks that carry their own label (pie slices, chart series) are the series
 *   colours at full strength, labelled in the surface colour, which reads on
 *   them in light and dark themes alike since the syntax palette contrasts
 *   with the surface.
 */
export function mermaidThemeVariables(
  p: DiagramPalette,
): Record<string, unknown> {
  const bg = p.background;
  const fg = hex(p.text);
  const tint = (c: Rgb, w: number) => hex(mix(c, bg, w));
  const line = tint(p.text, 0.7);
  const faint = tint(p.text, 0.25);
  const node = tint(p.accent, 0.22);
  // A mark carries its label in the surface colour, so it has to stand apart
  // from the surface as text would: a mark too close to it (the accent purple
  // on a dark surface) is pushed away from it until it does.
  // A theme may give two tokens the same colour (nord's accent is one of its
  // syntax colours): two slices of one pie must not look alike.
  const marks = p.series
    .map((c) => awayFrom(c, bg, MARK_CONTRAST))
    .filter(
      (c, i, all) => all.findIndex((o) => distance(o, c) < DISTINCT) === i,
    );
  const series = (i: number) => marks[i % marks.length]!;
  const vars: Record<string, unknown> = {
    darkMode: p.dark,
    fontFamily: p.font,
    fontSize: "14px",
    background: hex(bg),
    textColor: fg,
    titleColor: fg,
    lineColor: line,
    defaultLinkColor: line,
    arrowheadColor: line,
    // Flowchart, class, state, ER.
    primaryColor: node,
    mainBkg: node,
    primaryTextColor: fg,
    primaryBorderColor: hex(p.accent),
    nodeBorder: hex(p.accent),
    nodeTextColor: fg,
    secondaryColor: tint(series(1), 0.22),
    secondaryTextColor: fg,
    secondaryBorderColor: hex(series(1)),
    tertiaryColor: tint(series(2), 0.18),
    tertiaryTextColor: fg,
    tertiaryBorderColor: hex(series(2)),
    clusterBkg: tint(p.text, 0.05),
    clusterBorder: faint,
    edgeLabelBackground: hex(bg),
    labelBackground: hex(bg),
    attributeBackgroundColorOdd: tint(p.text, 0.04),
    attributeBackgroundColorEven: tint(p.text, 0.09),
    // Sequence.
    actorBkg: node,
    actorBorder: hex(p.accent),
    actorTextColor: fg,
    actorLineColor: line,
    signalColor: fg,
    signalTextColor: fg,
    labelBoxBkgColor: node,
    labelBoxBorderColor: hex(p.accent),
    labelTextColor: fg,
    loopTextColor: fg,
    activationBkgColor: tint(p.accent, 0.35),
    activationBorderColor: hex(p.accent),
    sequenceNumberColor: hex(bg),
    noteBkgColor: tint(series(3), 0.22),
    noteTextColor: fg,
    noteBorderColor: hex(series(3)),
    // Gantt.
    sectionBkgColor: tint(p.text, 0.06),
    altSectionBkgColor: hex(bg),
    sectionBkgColor2: tint(p.text, 0.06),
    gridColor: faint,
    taskBkgColor: tint(p.accent, 0.4),
    taskBorderColor: hex(p.accent),
    taskTextColor: fg,
    taskTextLightColor: fg,
    taskTextDarkColor: fg,
    taskTextOutsideColor: fg,
    taskTextClickableColor: fg,
    activeTaskBkgColor: tint(series(1), 0.45),
    activeTaskBorderColor: hex(series(1)),
    doneTaskBkgColor: tint(p.text, 0.2),
    doneTaskBorderColor: faint,
    critBkgColor: tint(series(6), 0.45),
    critBorderColor: hex(series(6)),
    todayLineColor: hex(series(6)),
    // Pie.
    pieTitleTextColor: fg,
    pieSectionTextColor: hex(bg),
    pieLegendTextColor: fg,
    pieStrokeColor: hex(bg),
    pieOuterStrokeColor: faint,
    pieOpacity: "1",
    // Quadrant chart.
    quadrant1Fill: tint(series(0), 0.18),
    quadrant2Fill: tint(series(1), 0.18),
    quadrant3Fill: tint(series(2), 0.18),
    quadrant4Fill: tint(series(3), 0.18),
    quadrant1TextFill: fg,
    quadrant2TextFill: fg,
    quadrant3TextFill: fg,
    quadrant4TextFill: fg,
    quadrantPointFill: hex(p.accent),
    quadrantPointTextFill: fg,
    quadrantTitleFill: fg,
    quadrantXAxisTextFill: fg,
    quadrantYAxisTextFill: fg,
    quadrantInternalBorderStrokeFill: faint,
    quadrantExternalBorderStrokeFill: line,
    // Requirement.
    requirementBackground: node,
    requirementBorderColor: hex(p.accent),
    requirementTextColor: fg,
    relationColor: line,
    relationLabelBackground: hex(bg),
    relationLabelColor: fg,
  };
  for (let i = 0; i < 12; i++) {
    const c = series(i);
    // Pie slices and chart marks: the colour itself.
    vars[`pie${i + 1}`] = hex(c);
    // Mindmap, timeline, kanban and journey sections: a tinted fill under the theme's text.
    vars[`cScale${i}`] = tint(c, 0.35);
    vars[`cScalePeer${i}`] = hex(c);
    vars[`cScaleLabel${i}`] = fg;
    // Git graph branches.
    if (i < 8) {
      vars[`git${i}`] = hex(c);
      vars[`gitBranchLabel${i}`] = hex(bg);
      vars[`gitInv${i}`] = hex(bg);
    }
    // Class / state fill types.
    if (i < 8) vars[`fillType${i}`] = tint(c, 0.25);
  }
  vars.commitLabelColor = fg;
  vars.commitLabelBackground = tint(p.text, 0.1);
  vars.tagLabelColor = fg;
  vars.tagLabelBackground = node;
  vars.tagLabelBorder = hex(p.accent);
  vars.xyChart = {
    backgroundColor: hex(bg),
    titleColor: fg,
    xAxisLabelColor: fg,
    xAxisTitleColor: fg,
    xAxisTickColor: line,
    xAxisLineColor: line,
    yAxisLabelColor: fg,
    yAxisTitleColor: fg,
    yAxisTickColor: line,
    yAxisLineColor: line,
    plotColorPalette: marks.map(hex).join(", "),
  };
  return vars;
}

function mermaidConfig(p: DiagramPalette | null, dark: boolean) {
  return {
    startOnLoad: false,
    securityLevel: "strict" as const,
    // Mermaid's own defaults, spelled out: a reply cannot ask for more.
    maxTextSize: 50000,
    maxEdges: 500,
    // SVG text, not HTML in a foreignObject: the picture is drawn by an <img>
    // and downloaded as a file, and both read plain SVG best.
    htmlLabels: false,
    flowchart: { htmlLabels: false },
    theme: p
      ? ("base" as const)
      : dark
        ? ("dark" as const)
        : ("default" as const),
    // Mermaid sets no anchor on the text of a mindmap's round root with SVG
    // labels, so its name starts at the centre and runs out of the circle.
    themeCSS: ".mindmap-node.section-root text { text-anchor: middle; }",
    themeVariables: p ? mermaidThemeVariables(p) : { darkMode: dark },
  };
}

/** Recent pictures by theme and source, so a row scrolled back into view does not run Mermaid again. */
const CACHE_LIMIT = 64;
const cache = new Map<string, RenderedPicture>();

export function cachedPicture(
  kind: PictureKind,
  source: string,
  theme: string,
): RenderedPicture | undefined {
  const key = cacheKey(kind, source, theme);
  const hit = cache.get(key);
  if (hit) {
    cache.delete(key);
    cache.set(key, hit);
  }
  return hit;
}

function remember(key: string, picture: RenderedPicture) {
  cache.set(key, picture);
  while (cache.size > CACHE_LIMIT) cache.delete(cache.keys().next().value!);
}

function cacheKey(kind: PictureKind, source: string, theme: string): string {
  // An SVG block draws the same in every theme.
  return `${kind}\u0000${kind === "svg" ? "" : theme}\u0000${source}`;
}

/** Mermaid's configuration is global: one diagram at a time. */
let queue: Promise<unknown> = Promise.resolve();
let renderSeq = 0;

export function renderPicture(
  kind: PictureKind,
  source: string,
  theme: UiThemeMode,
): Promise<RenderedPicture> {
  const key = cacheKey(kind, source, theme);
  const hit = cache.get(key);
  if (hit) return Promise.resolve(hit);
  if (kind === "svg") {
    try {
      const picture = standaloneSvg(source);
      remember(key, picture);
      return Promise.resolve(picture);
    } catch (err) {
      return Promise.reject(err);
    }
  }
  const job = queue.then(async () => {
    const again = cache.get(key);
    if (again) return again;
    const mermaid = await loadMermaid();
    // The palette is read from the page when the job runs, not when it was
    // queued: a job for a theme the page has already left would draw in the
    // new colours and file the picture under the old theme.
    if (readAppliedUiTheme() !== theme)
      throw new Error("the theme changed before the diagram was drawn");
    mermaid.initialize(
      mermaidConfig(diagramPalette(theme), !LIGHT_THEMES.has(theme)),
    );
    const id = `coddy-mermaid-${++renderSeq}`;
    try {
      // parse() names the error without leaving Mermaid's error picture behind.
      await mermaid.parse(source);
      const { svg } = await mermaid.render(id, source);
      const picture = standaloneSvg(svg);
      if (readAppliedUiTheme() === theme) remember(key, picture);
      return picture;
    } finally {
      // render() works in a temporary element it does not always remove on failure.
      document.getElementById(id)?.remove();
      document.getElementById(`d${id}`)?.remove();
    }
  });
  queue = job.catch(() => undefined);
  return job;
}

/** For tests: forget every rendered picture. */
export function clearPictureCache() {
  cache.clear();
}
