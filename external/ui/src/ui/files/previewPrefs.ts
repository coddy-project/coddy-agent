/**
 * The files the Files window can show either as their source or as what they
 * draw, and which of the two this browser chose for each kind.
 *
 * An SVG is a picture first, a .canvas file its board first and an HTML file
 * its source first; the window's menu switches a kind to the other view, and
 * the choice holds for every file of that kind. Presentation only, so it lives in a cookie like the diff view
 * (changes/diffViewPrefs.ts) and says nothing to the server.
 */

export type PreviewKind = "svg" | "html" | "canvas";

export type PreviewPrefs = Record<PreviewKind, boolean>;

export const DEFAULT_PREVIEW_PREFS: PreviewPrefs = {
  svg: true,
  html: false,
  canvas: true,
};

const COOKIE = "coddy_files_preview";
const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

const KIND_BY_EXTENSION: Record<string, PreviewKind> = {
  svg: "svg",
  html: "html",
  htm: "html",
  xhtml: "html",
  canvas: "canvas",
};

/** The kind of preview a path has by its extension, or null for none. */
export function previewKindOf(path: string): PreviewKind | null {
  const name = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return null;
  return KIND_BY_EXTENSION[name.slice(dot + 1)] ?? null;
}

/**
 * The cookie lists the kinds shown as what they draw ("svg,html", or empty for
 * none), and the kinds drawn by default that this browser switched to their
 * source ("!canvas"); without it, or when it cannot be read, the defaults
 * hold.
 */
export function readPreviewPrefs(): PreviewPrefs {
  if (typeof document === "undefined") return { ...DEFAULT_PREVIEW_PREFS };
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (!s.startsWith(`${COOKIE}=`)) continue;
    let value: string;
    try {
      value = decodeURIComponent(s.slice(COOKIE.length + 1));
    } catch {
      return { ...DEFAULT_PREVIEW_PREFS };
    }
    const kinds = new Set(value.split(",").map((k) => k.trim()));
    // A cookie written before a kind existed says nothing about it.
    return {
      svg: kinds.has("svg"),
      html: kinds.has("html"),
      canvas: !kinds.has("!canvas"),
    };
  }
  return { ...DEFAULT_PREVIEW_PREFS };
}

export function writePreviewPrefs(prefs: PreviewPrefs): void {
  if (typeof document === "undefined") return;
  const kinds: string[] = (["svg", "html"] as const).filter((k) => prefs[k]);
  if (!prefs.canvas) kinds.push("!canvas");
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${COOKIE}=${encodeURIComponent(kinds.join(","))}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}
