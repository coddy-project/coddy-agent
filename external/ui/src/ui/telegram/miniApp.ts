/**
 * The web UI inside Telegram as a Mini App (issue #320).
 *
 * `launch.ts` decides whether this tab is a Mini App; this module configures
 * the window when it is, and does nothing at all otherwise. In order: the
 * theme follows Telegram's until the user picks one in Appearance; the CSS
 * variables of `telegram.css` get the visible height, the stable height, the
 * part of the WebView Telegram does not show and the safe area; the client is
 * asked for its viewport, safe areas and theme; the app is expanded (a half
 * open app is where issue #320 came from) with vertical swipes off, so pulling
 * the transcript does not fold it; Telegram's header, background and bottom bar
 * take the theme's canvas colours, again on every theme change; the back button
 * follows `backButton.ts`; and `web_app_ready` goes last, so Telegram drops its
 * placeholder over a page that already has its colours.
 */

import "./telegram.css";

import { readUiThemeCookie } from "../theme/themeCookie";
import { applyUiTheme } from "../theme/uiTheme";
import { installTelegramBackButton } from "./backButton";
import {
  createTelegramBridge,
  type TelegramBridge,
  versionAtLeast,
} from "./bridge";
import {
  captureTelegramLaunch,
  telegramLaunch,
  type TelegramLaunch,
} from "./launch";

type Insets = { top: number; bottom: number; left: number; right: number };

const NO_INSETS: Insets = { top: 0, bottom: 0, left: 0, right: 0 };

function px(n: number): string {
  return `${Math.max(0, Math.round(Number.isFinite(n) ? n : 0))}px`;
}

function readInsets(data: unknown): Insets {
  const d = (data && typeof data === "object" ? data : {}) as Record<
    string,
    unknown
  >;
  const n = (v: unknown) =>
    typeof v === "number" && Number.isFinite(v) && v > 0 ? v : 0;
  return {
    top: n(d.top),
    bottom: n(d.bottom),
    left: n(d.left),
    right: n(d.right),
  };
}

/**
 * isDarkColor follows the SDK's rule for colorScheme: a #rrggbb background
 * darker than the middle of the HSP brightness scale is a dark theme.
 */
export function isDarkColor(hex: string): boolean {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m || !m[1]) {
    return false;
  }
  const v = Number.parseInt(m[1], 16);
  const r = (v >> 16) & 255;
  const g = (v >> 8) & 255;
  const b = v & 255;
  return Math.sqrt(0.299 * r * r + 0.587 * g * g + 0.114 * b * b) < 120;
}

/**
 * followTelegramTheme applies Telegram's light or dark theme while the user has
 * not picked one: a pick in Appearance writes the cookie and wins from then on.
 * Nothing is written here.
 */
export function followTelegramTheme(themeParams: Record<string, string>): void {
  if (readUiThemeCookie() !== null) {
    return;
  }
  const bg = themeParams.bg_color;
  if (!bg) {
    return;
  }
  applyUiTheme(isDarkColor(bg) ? "dark" : "light");
}

/** canvasColor reads one of the theme's canvas tokens as #rrggbb, or "". */
function canvasColor(doc: Document, token: string): string {
  const raw = getComputedStyle(doc.documentElement)
    .getPropertyValue(token)
    .trim();
  return /^#[0-9a-f]{6}$/i.test(raw) ? raw.toLowerCase() : "";
}

/** postThemeColors gives Telegram's header, background and bottom bar the theme's colours. */
function postThemeColors(
  bridge: TelegramBridge,
  launch: TelegramLaunch,
  doc: Document,
): void {
  const top = canvasColor(doc, "--coddy-canvas-gradient-top");
  const bottom = canvasColor(doc, "--coddy-canvas-gradient-bottom");
  if (top && versionAtLeast(launch.version, "6.9")) {
    bridge.post("web_app_set_header_color", { color: top });
  }
  if (bottom && versionAtLeast(launch.version, "6.1")) {
    bridge.post("web_app_set_background_color", { color: bottom });
  }
  if (bottom && versionAtLeast(launch.version, "7.10")) {
    bridge.post("web_app_set_bottom_bar_color", { color: bottom });
  }
}

/**
 * initTelegramMiniApp configures the Mini App window when this tab is one and
 * returns what undoes it (for tests); outside Telegram it returns null and has
 * touched nothing.
 */
export function initTelegramMiniApp(
  win: Window = window,
  doc: Document = document,
): (() => void) | null {
  const launch = telegramLaunch() ?? captureTelegramLaunch(win);
  if (!launch) {
    return null;
  }
  const root = doc.documentElement;
  root.dataset.telegramMiniApp = "true";
  const bridge = createTelegramBridge(win);

  followTelegramTheme(launch.themeParams);

  // Heights: until the client says otherwise the WebView is all visible.
  let visible = 0;
  let stable = 0;
  let safe: Insets = NO_INSETS;
  let content: Insets = NO_INSETS;
  const layoutHeight = () => win.innerHeight;
  const visualHeight = () => win.visualViewport?.height || win.innerHeight;
  let lastHidden = -1;
  const apply = () => {
    const layout = layoutHeight();
    const shown =
      visible > 0 ? Math.min(visible, visualHeight()) : visualHeight();
    const steady = Math.min(stable > 0 ? stable : layout, layout);
    root.style.setProperty("--coddy-telegram-viewport-height", px(shown));
    root.style.setProperty("--coddy-telegram-stable-height", px(steady));
    // The part of the WebView at its bottom that Telegram does not show: the
    // lower half of a half-open app. A keyboard that overlays the page is the
    // keyboard inset's to count (ChatScreen), and the CSS takes the larger.
    const hidden = Math.max(0, Math.round(layout - steady));
    root.style.setProperty("--coddy-telegram-hidden-bottom", px(hidden));
    // The content safe area is measured inside the device's (fullscreen: the
    // status bar, then Telegram's own buttons), so what to keep clear is the sum.
    root.style.setProperty(
      "--coddy-telegram-safe-top",
      px(safe.top + content.top),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-bottom",
      px(safe.bottom + content.bottom),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-left",
      px(safe.left + content.left),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-right",
      px(safe.right + content.right),
    );
    if (hidden !== lastHidden) {
      const first = lastHidden < 0;
      lastHidden = hidden;
      // The WebView keeps its size when Telegram shows less of it, so nothing
      // fires a resize: what measures itself against the window (the sheets
      // over the docked composer) is told here, once the lift has moved.
      if (!first) {
        win.dispatchEvent(new Event("resize"));
      }
    }
  };
  apply();

  const offs: Array<() => void> = [];
  offs.push(
    bridge.on("viewport_changed", (data) => {
      const d = (data && typeof data === "object" ? data : {}) as Record<
        string,
        unknown
      >;
      if (
        typeof d.height === "number" &&
        Number.isFinite(d.height) &&
        d.height > 0
      ) {
        visible = d.height;
        if (d.is_state_stable) {
          stable = d.height;
        }
      }
      apply();
    }),
    bridge.on("safe_area_changed", (data) => {
      safe = readInsets(data);
      apply();
    }),
    bridge.on("content_safe_area_changed", (data) => {
      content = readInsets(data);
      apply();
    }),
    bridge.on("theme_changed", (data) => {
      const params = (
        data && typeof data === "object"
          ? (data as Record<string, unknown>).theme_params
          : null
      ) as Record<string, string> | null;
      if (params && typeof params === "object") {
        followTelegramTheme(params);
      }
    }),
  );
  win.addEventListener("resize", apply);
  win.visualViewport?.addEventListener("resize", apply);

  bridge.post("web_app_request_viewport");
  bridge.post("web_app_request_safe_area");
  bridge.post("web_app_request_content_safe_area");
  bridge.post("web_app_request_theme");
  bridge.post("web_app_expand");
  if (versionAtLeast(launch.version, "7.7")) {
    bridge.post("web_app_setup_swipe_behavior", {
      allow_vertical_swipe: false,
    });
  }
  postThemeColors(bridge, launch, doc);
  const themeWatch = new MutationObserver(() =>
    postThemeColors(bridge, launch, doc),
  );
  themeWatch.observe(root, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  const disposeBack = versionAtLeast(launch.version, "6.1")
    ? installTelegramBackButton(bridge, win, doc)
    : () => {};
  bridge.post("web_app_ready");

  return () => {
    disposeBack();
    themeWatch.disconnect();
    for (const off of offs) {
      off();
    }
    win.removeEventListener("resize", apply);
    win.visualViewport?.removeEventListener("resize", apply);
    bridge.dispose();
    delete root.dataset.telegramMiniApp;
    for (const name of [
      "--coddy-telegram-viewport-height",
      "--coddy-telegram-stable-height",
      "--coddy-telegram-hidden-bottom",
      "--coddy-telegram-safe-top",
      "--coddy-telegram-safe-bottom",
      "--coddy-telegram-safe-left",
      "--coddy-telegram-safe-right",
    ]) {
      root.style.removeProperty(name);
    }
  };
}
