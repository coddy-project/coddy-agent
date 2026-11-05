/**
 * UI defaults a deployment picks for every browser: `GET /coddy/info` carries
 * the server's `CODDY_UI_THEME` and `CODDY_UI_WALLPAPER` environment
 * variables under `ui`, and the client applies them while the person has made
 * no choice of their own (no theme cookie, no stored wallpaper). The answer
 * is fetched once and shared.
 */

import {
  UI_THEME_IDS,
  readUiThemeCookie,
  type UiThemeMode,
} from "./themeCookie";
import { applyUiTheme } from "./uiTheme";

export interface UiDefaults {
  theme?: string;
  wallpaper?: string;
}

type InfoFetcher = (
  input: string,
) => Promise<{ ok: boolean; json?: () => Promise<unknown> }>;

let defaultsPromise: Promise<UiDefaults> | undefined;

/** Read the `ui` member of a `coddy.info` answer into the two fields it may name. */
export function parseUiDefaults(info: unknown): UiDefaults {
  const out: UiDefaults = {};
  if (typeof info !== "object" || info === null) return out;
  const ui = (info as { ui?: unknown }).ui;
  if (typeof ui !== "object" || ui === null) return out;
  const theme = (ui as { theme?: unknown }).theme;
  const wallpaper = (ui as { wallpaper?: unknown }).wallpaper;
  if (typeof theme === "string") out.theme = theme;
  if (typeof wallpaper === "string") out.wallpaper = wallpaper;
  return out;
}

/**
 * The deployment's defaults, fetched once for the page. A failed or refused
 * read answers empty defaults and is remembered, so a broken server is not
 * asked again on every boot path.
 */
export function loadUiDefaults(
  fetcher: InfoFetcher = fetch as unknown as InfoFetcher,
): Promise<UiDefaults> {
  if (!defaultsPromise) {
    defaultsPromise = fetcher("/coddy/info")
      .then(async (resp) => {
        if (!resp.ok || !resp.json) return {};
        try {
          return parseUiDefaults(await resp.json());
        } catch {
          return {};
        }
      })
      .catch(() => ({}));
  }
  return defaultsPromise;
}

/** Drops the cached read; for tests. */
export function resetUiDefaultsCache(): void {
  defaultsPromise = undefined;
}

/**
 * Applies the server's default theme only while the browser holds no theme
 * cookie: an explicit pick always wins, and a name the client does not know
 * (a newer server's theme, a typo) leaves the current theme alone. The
 * choice is not written to the cookie, so the deployment's default keeps
 * applying until the person picks a theme themselves.
 */
export function applyServerThemeDefaults(defaults: UiDefaults): void {
  const theme = defaults.theme;
  if (theme === undefined) return;
  if (readUiThemeCookie() !== null) return;
  if (!(UI_THEME_IDS as string[]).includes(theme)) return;
  applyUiTheme(theme as UiThemeMode);
}
