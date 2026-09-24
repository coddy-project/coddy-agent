import { afterEach, expect, test, vi } from "vitest";
import { initTelegramMiniApp, isTelegramMiniApp } from "./telegramMiniApp";

afterEach(() => {
  delete window.Telegram;
  delete window.__coddyTelegramLaunchURL;
  delete document.documentElement.dataset.telegramMiniApp;
  document.documentElement.style.removeProperty(
    "--coddy-telegram-viewport-height",
  );
  document.documentElement.style.removeProperty(
    "--coddy-telegram-stable-height",
  );
  document.documentElement.style.removeProperty("--coddy-telegram-safe-top");
  document.documentElement.style.removeProperty("--coddy-telegram-safe-bottom");
  document.documentElement.style.removeProperty("--coddy-telegram-safe-left");
  document.documentElement.style.removeProperty("--coddy-telegram-safe-right");
});

test("an SDK object without launch data does not classify an ordinary browser", () => {
  expect(isTelegramMiniApp({ initData: "" }, "")).toBe(false);
  expect(isTelegramMiniApp(undefined, "#/s/sess_1?history=1")).toBe(false);
});

test("either SDK initData or Telegram launch URL markers identify a Mini App", () => {
  expect(isTelegramMiniApp({ initData: "auth_date=1&hash=x" }, "")).toBe(true);
  expect(
    isTelegramMiniApp(undefined, "?tgWebAppStartParam=chat#/s/sess_1"),
  ).toBe(true);
  expect(
    isTelegramMiniApp(
      undefined,
      "#tgWebAppData=auth_date%3D1&tgWebAppVersion=8.0",
    ),
  ).toBe(true);
  expect(isTelegramMiniApp(undefined, "#tgWebAppVersion=8.0")).toBe(true);
  expect(isTelegramMiniApp(undefined, "?tgWebAppStartParam=#/s/sess_1")).toBe(
    false,
  );
});

test("boot applies Telegram viewport and safe area, then updates on Telegram events", () => {
  const callbacks = new Map<string, () => void>();
  const ready = vi.fn();
  const webApp = {
    initData: "auth_date=1",
    viewportHeight: 520,
    viewportStableHeight: 510,
    contentSafeAreaInset: { top: 20, bottom: 12, left: 5, right: 7 },
    ready,
    onEvent: vi.fn((name: string, callback: () => void) =>
      callbacks.set(name, callback),
    ),
  };
  window.Telegram = { WebApp: webApp };
  initTelegramMiniApp();

  const root = document.documentElement;
  expect(root.dataset.telegramMiniApp).toBe("true");
  expect(root.style.getPropertyValue("--coddy-telegram-viewport-height")).toBe(
    "520px",
  );
  expect(root.style.getPropertyValue("--coddy-telegram-stable-height")).toBe(
    "510px",
  );
  expect(root.style.getPropertyValue("--coddy-telegram-safe-top")).toBe("20px");
  expect(root.style.getPropertyValue("--coddy-telegram-safe-left")).toBe("5px");
  expect(root.style.getPropertyValue("--coddy-telegram-safe-right")).toBe("7px");
  expect(ready).toHaveBeenCalledOnce();

  webApp.viewportHeight = 390;
  webApp.viewportStableHeight = 400;
  callbacks.get("viewportChanged")?.();
  expect(root.style.getPropertyValue("--coddy-telegram-viewport-height")).toBe(
    "390px",
  );
  expect(root.style.getPropertyValue("--coddy-telegram-stable-height")).toBe(
    "400px",
  );
});

test("a URL marker still enables layout when the SDK has no initData", () => {
  window.__coddyTelegramLaunchURL = "#tgWebAppData=auth_date%3D1";
  initTelegramMiniApp();
  expect(document.documentElement.dataset.telegramMiniApp).toBe("true");
});
