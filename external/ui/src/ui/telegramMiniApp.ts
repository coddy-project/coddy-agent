type TelegramInsets = {
  top?: number;
  right?: number;
  bottom?: number;
  left?: number;
};

type TelegramWebApp = {
  initData?: string;
  viewportHeight?: number;
  viewportStableHeight?: number;
  contentSafeAreaInset?: TelegramInsets;
  safeAreaInset?: TelegramInsets;
  ready?: () => void;
  onEvent?: (name: string, callback: () => void) => void;
};

declare global {
  interface Window {
    Telegram?: { WebApp?: TelegramWebApp };
    __coddyTelegramLaunchURL?: string;
  }
}

/** Launch markers and SDK data affect presentation only; neither authenticates a user. */
export function isTelegramMiniApp(
  webApp: TelegramWebApp | undefined,
  launchURL: string,
): boolean {
  if (typeof webApp?.initData === "string" && webApp.initData.length > 0)
    return true;
  return /(?:^|[?#&])tgWebApp(?:Data|StartParam|Version)=[^&#]+/.test(
    launchURL,
  );
}

function inset(value: number | undefined): string {
  return `${typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0}px`;
}

/** Set the visible Telegram WebView bounds before React renders, then track changes. */
export function initTelegramMiniApp(): void {
  if (typeof window === "undefined") return;
  const webApp = window.Telegram?.WebApp;
  const launchURL =
    window.__coddyTelegramLaunchURL ??
    window.location.search + window.location.hash;
  if (!isTelegramMiniApp(webApp, launchURL)) return;

  const root = document.documentElement;
  root.dataset.telegramMiniApp = "true";
  const syncViewport = () => {
    const browserHeight = window.visualViewport?.height || window.innerHeight;
    const height = webApp?.viewportHeight
      ? Math.min(webApp.viewportHeight, browserHeight)
      : browserHeight;
    const stableHeight = webApp?.viewportStableHeight || height;
    if (height > 0 && Number.isFinite(height)) {
      root.style.setProperty("--coddy-telegram-viewport-height", `${height}px`);
    }
    if (stableHeight > 0 && Number.isFinite(stableHeight)) {
      root.style.setProperty(
        "--coddy-telegram-stable-height",
        `${Math.min(stableHeight, browserHeight)}px`,
      );
    }
    const safe = webApp?.safeAreaInset;
    const contentSafe = webApp?.contentSafeAreaInset;
    root.style.setProperty(
      "--coddy-telegram-safe-top",
      inset(Math.max(safe?.top ?? 0, contentSafe?.top ?? 0)),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-bottom",
      inset(Math.max(safe?.bottom ?? 0, contentSafe?.bottom ?? 0)),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-left",
      inset(Math.max(safe?.left ?? 0, contentSafe?.left ?? 0)),
    );
    root.style.setProperty(
      "--coddy-telegram-safe-right",
      inset(Math.max(safe?.right ?? 0, contentSafe?.right ?? 0)),
    );
  };
  syncViewport();
  webApp?.onEvent?.("viewportChanged", syncViewport);
  webApp?.onEvent?.("safeAreaChanged", syncViewport);
  webApp?.onEvent?.("contentSafeAreaChanged", syncViewport);
  window.visualViewport?.addEventListener("resize", syncViewport);
  window.addEventListener("resize", syncViewport);
  webApp?.ready?.();
}
