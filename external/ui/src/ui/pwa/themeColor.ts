/**
 * Keeps <meta name="theme-color"> on the theme's canvas colour, so the title
 * bar of the installed app (and the browser's toolbar where it takes the
 * colour) follows the theme picked in Settings → Appearance rather than the
 * dark colour the manifest names for the first paint.
 *
 * Contract: DESIGN.md, "Installable app and notifications".
 */
import { subscribeAppliedUiTheme } from "../theme/uiTheme";

const TOKEN = "--coddy-canvas-gradient-top";

function canvasColor(doc: Document): string {
  const raw = getComputedStyle(doc.documentElement)
    .getPropertyValue(TOKEN)
    .trim();
  return /^#[0-9a-f]{6}$/i.test(raw) ? raw.toLowerCase() : "";
}

/** applyThemeColor writes the canvas colour into the meta tag, creating it when absent. */
export function applyThemeColor(doc: Document = document): void {
  const color = canvasColor(doc);
  if (!color) {
    return;
  }
  let meta = doc.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
  if (!meta) {
    meta = doc.createElement("meta");
    meta.name = "theme-color";
    doc.head.appendChild(meta);
  }
  if (meta.content !== color) {
    meta.content = color;
  }
}

/** followThemeColor applies the colour now and on every theme change. */
export function followThemeColor(doc: Document = document): () => void {
  applyThemeColor(doc);
  return subscribeAppliedUiTheme(() => applyThemeColor(doc));
}
