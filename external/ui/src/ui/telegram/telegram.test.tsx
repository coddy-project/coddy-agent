import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { useState } from "react";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { Composer } from "../chat/Composer";
import { ImageLightbox } from "../components/ImageLightbox";
import { CODDY_UI_THEME_COOKIE } from "../theme/themeCookie";
import {
  createTelegramBridge,
  parentOrigin,
  TELEGRAM_WEB_ORIGIN,
  versionAtLeast,
} from "./bridge";
import {
  backButtonWanted,
  pressBack,
  TELEGRAM_BACK_LAYERS,
} from "./backButton";
import {
  captureTelegramLaunch,
  parseLaunchAddress,
  resetTelegramLaunchForTests,
  TELEGRAM_LAUNCH_STORAGE_KEY,
  telegramLaunch,
} from "./launch";
import {
  followTelegramTheme,
  initTelegramMiniApp,
  isDarkColor,
} from "./miniApp";

const here = dirname(fileURLToPath(import.meta.url));

const DATA = "query_id=q1&user=%7B%22id%22%3A4242%7D&auth_date=1&hash=abc";
const DARK = JSON.stringify({ bg_color: "#212121", text_color: "#ffffff" });
const LIGHT = JSON.stringify({ bg_color: "#ffffff", text_color: "#000000" });

function launchHash(extra: Record<string, string> = {}): string {
  const p = new URLSearchParams({
    tgWebAppData: DATA,
    tgWebAppVersion: "10.1",
    tgWebAppPlatform: "ios",
    tgWebAppThemeParams: DARK,
    ...extra,
  });
  return p.toString();
}

function go(url: string) {
  window.history.replaceState(null, "", url);
}

function clearThemeCookie() {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
}

type Posted = { type: string; data: unknown };

/** installProxy stands in for the native app: what the page posts, and a way to answer it. */
function installProxy(): Posted[] {
  const posted: Posted[] = [];
  (
    window as unknown as { TelegramWebviewProxy: unknown }
  ).TelegramWebviewProxy = {
    postEvent: (type: string, data: string) =>
      posted.push({ type, data: JSON.parse(data) }),
  };
  return posted;
}

function receive(type: string, data: unknown) {
  (
    window as unknown as {
      Telegram: { WebView: { receiveEvent: (t: string, d: unknown) => void } };
    }
  ).Telegram.WebView.receiveEvent(type, data);
}

let dispose: (() => void) | null = null;

beforeEach(() => {
  go("/");
  window.sessionStorage.clear();
  resetTelegramLaunchForTests();
  clearThemeCookie();
});

afterEach(() => {
  cleanup();
  dispose?.();
  dispose = null;
  delete (window as unknown as { TelegramWebviewProxy?: unknown })
    .TelegramWebviewProxy;
  delete (window as unknown as { Telegram?: unknown }).Telegram;
  delete (window as unknown as { TelegramGameProxy?: unknown })
    .TelegramGameProxy;
  delete (window as unknown as { TelegramGameProxy_receiveEvent?: unknown })
    .TelegramGameProxy_receiveEvent;
  document.documentElement.dataset.theme = "dark";
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("launch parameters", () => {
  test("a launch is tgWebAppData or tgWebAppVersion, in the fragment or the query", () => {
    expect(parseLaunchAddress("", `#${launchHash()}`).launch).toMatchObject({
      initData: DATA,
      version: "10.1",
      platform: "ios",
      themeParams: { bg_color: "#212121" },
    });
    expect(parseLaunchAddress("", "#tgWebAppVersion=7.0").launch?.version).toBe(
      "7.0",
    );
    expect(parseLaunchAddress("?tgWebAppVersion=6.0", "").launch?.version).toBe(
      "6.0",
    );
    expect(parseLaunchAddress("", "#/s/sess_1?history=1").launch).toBeNull();
    // A start parameter alone can be typed into any address.
    expect(parseLaunchAddress("?tgWebAppStartParam=x", "").launch).toBeNull();
    expect(parseLaunchAddress("", "#tgWebAppVersion=").launch).toBeNull();
  });

  test("the address keeps its own route and loses Telegram's parameters", () => {
    const after = parseLaunchAddress(
      "?lang=ru",
      `#/s/sess_1?history=1&${launchHash()}`,
    );
    expect(after.launch?.version).toBe("10.1");
    expect(after.cleanSearch).toBe("?lang=ru");
    expect(after.cleanHash).toBe("#/s/sess_1?history=1");
    expect(after.changed).toBe(true);
    const bare = parseLaunchAddress("", `#${launchHash()}`);
    expect(bare.cleanHash).toBe("");
    const ordinary = parseLaunchAddress("?lang=ru", "#/settings");
    expect(ordinary.changed).toBe(false);
  });

  test("the bot's link and a session start parameter open that conversation", () => {
    const link = parseLaunchAddress("?session=sess_abc", `#${launchHash()}`);
    expect(link.session).toBe("sess_abc");
    expect(link.cleanSearch).toBe("");
    expect(link.cleanHash).toBe("#/s/sess_abc");
    const direct = parseLaunchAddress(
      "?tgWebAppStartParam=sess_def",
      `#${launchHash()}`,
    );
    expect(direct.session).toBe("sess_def");
    expect(direct.cleanHash).toBe("#/s/sess_def");
    expect(
      parseLaunchAddress("?tgWebAppStartParam=promo", `#${launchHash()}`)
        .session,
    ).toBe("");
    // A group gets the link as a plain button: it opens the browser, no launch.
    const browser = parseLaunchAddress("?session=sess_abc", "");
    expect(browser.launch).toBeNull();
    expect(browser.cleanHash).toBe("#/s/sess_abc");
    // Outside a launch a start parameter is just a query key.
    expect(parseLaunchAddress("?tgWebAppStartParam=sess_def", "").session).toBe(
      "",
    );
    // Only the alphabet of a session id is taken; the rest is dropped.
    for (const bad of ["../etc", "sess_1<script>", "a b", "x".repeat(257)]) {
      const parsed = parseLaunchAddress(
        `?session=${encodeURIComponent(bad)}`,
        "#/settings",
      );
      expect(parsed.session).toBe("");
      expect(parsed.cleanSearch).toBe("");
      expect(parsed.cleanHash).toBe("#/settings");
    }
  });

  test("capture keeps the launch for the tab and cleans the address before the router reads it", () => {
    go(`/?session=sess_abc#${launchHash()}`);
    const launch = captureTelegramLaunch(window);
    expect(launch?.version).toBe("10.1");
    expect(window.location.search).toBe("");
    expect(window.location.hash).toBe("#/s/sess_abc");
    expect(window.location.href).not.toContain("tgWebAppData");
    expect(
      JSON.parse(
        window.sessionStorage.getItem(TELEGRAM_LAUNCH_STORAGE_KEY) ?? "{}",
      ).initData,
    ).toBe(DATA);

    // A reload inside the Mini App: the address has only the route now.
    resetTelegramLaunchForTests();
    go("/#/s/sess_abc");
    expect(captureTelegramLaunch(window)?.initData).toBe(DATA);
    expect(telegramLaunch()?.version).toBe("10.1");
  });

  test("a browser that refuses storage still loads, and the launch lives for the page", () => {
    vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    go(`/#${launchHash()}`);
    expect(captureTelegramLaunch(window)?.version).toBe("10.1");
    expect(telegramLaunch()?.initData).toBe(DATA);
    resetTelegramLaunchForTests();
    go("/");
    expect(captureTelegramLaunch(window)).toBeNull();
  });

  test("an ordinary browser is not a Mini App and its address is left alone", () => {
    go("/?lang=en#/s/sess_1?history=1");
    const replace = vi.spyOn(window.history, "replaceState");
    expect(captureTelegramLaunch(window)).toBeNull();
    expect(replace).not.toHaveBeenCalled();
    expect(window.location.href).toContain("?lang=en#/s/sess_1?history=1");
  });
});

describe("bridge", () => {
  test("the native apps' proxy carries events out and receiveEvent brings them in", () => {
    const posted = installProxy();
    const bridge = createTelegramBridge(window);
    dispose = () => bridge.dispose();
    const seen: unknown[] = [];
    bridge.on("viewport_changed", (d) => seen.push(d));
    bridge.post("web_app_ready");
    bridge.post("web_app_setup_back_button", { is_visible: true });
    receive("viewport_changed", { height: 500, is_state_stable: true });
    expect(posted).toEqual([
      { type: "web_app_ready", data: "" },
      { type: "web_app_setup_back_button", data: { is_visible: true } },
    ]);
    expect(seen).toEqual([{ height: 500, is_state_stable: true }]);
    // The older entry points of the clients reach the same handlers.
    (
      window as unknown as {
        TelegramGameProxy_receiveEvent: (t: string, d: unknown) => void;
      }
    ).TelegramGameProxy_receiveEvent("viewport_changed", { height: 400 });
    expect(seen).toHaveLength(2);
  });

  test("in a web client's iframe events go to the parent, and only the parent is heard", () => {
    const parentPost = vi.fn();
    const fakeParent = { postMessage: parentPost } as unknown as Window;
    vi.spyOn(window, "parent", "get").mockReturnValue(fakeParent);
    vi.spyOn(document, "referrer", "get").mockReturnValue(
      "http://127.0.0.1:19890/chat",
    );
    const bridge = createTelegramBridge(window);
    dispose = () => bridge.dispose();
    expect(JSON.parse(parentPost.mock.calls[0]?.[0] as string)).toEqual({
      eventType: "iframe_ready",
      eventData: { reload_supported: true },
    });
    expect(parentPost.mock.calls[0]?.[1]).toBe("http://127.0.0.1:19890");
    bridge.post("web_app_expand");
    expect(JSON.parse(parentPost.mock.calls[1]?.[0] as string)).toEqual({
      eventType: "web_app_expand",
      eventData: "",
    });
    expect(parentPost.mock.calls[1]?.[1]).toBe("http://127.0.0.1:19890");

    const seen: unknown[] = [];
    bridge.on("theme_changed", (d) => seen.push(d));
    const msg = JSON.stringify({
      eventType: "theme_changed",
      eventData: { theme_params: { bg_color: "#ffffff" } },
    });
    window.dispatchEvent(
      new MessageEvent("message", {
        data: msg,
        source: fakeParent,
        origin: "http://127.0.0.1:19890",
      }),
    );
    window.dispatchEvent(
      new MessageEvent("message", {
        data: msg,
        source: window,
        origin: "http://127.0.0.1:19890",
      }),
    );
    window.dispatchEvent(
      new MessageEvent("message", {
        data: msg,
        source: fakeParent,
        origin: "https://elsewhere.example",
      }),
    );
    window.dispatchEvent(
      new MessageEvent("message", {
        data: JSON.stringify({
          eventType: "set_custom_style",
          eventData: "body{display:none}",
        }),
        source: fakeParent,
        origin: "http://127.0.0.1:19890",
      }),
    );
    expect(seen).toEqual([{ theme_params: { bg_color: "#ffffff" } }]);
  });

  test("the parent's origin is the browser's record of it, else the page the frame came from, else Telegram Web", () => {
    const framedBy = (ancestors: string[] | undefined, referrer: string) =>
      ({
        location: { ancestorOrigins: ancestors },
        document: { referrer },
      }) as unknown as Window;
    expect(
      parentOrigin(
        framedBy(["https://web.telegram.org"], "http://127.0.0.1:19890/"),
      ),
    ).toBe("https://web.telegram.org");
    expect(
      parentOrigin(framedBy(undefined, "http://127.0.0.1:19890/chat?x=1")),
    ).toBe("http://127.0.0.1:19890");
    expect(parentOrigin(framedBy([], ""))).toBe(TELEGRAM_WEB_ORIGIN);
    expect(parentOrigin(framedBy(["null"], "not an address"))).toBe(
      TELEGRAM_WEB_ORIGIN,
    );
    expect(TELEGRAM_WEB_ORIGIN).toBe("https://web.telegram.org");
  });

  test("versions compare part by part, and an unknown one is the oldest", () => {
    expect(versionAtLeast("10.1", "7.7")).toBe(true);
    expect(versionAtLeast("7.7", "7.10")).toBe(false);
    expect(versionAtLeast("7.10", "7.10")).toBe(true);
    expect(versionAtLeast("", "6.1")).toBe(false);
    expect(versionAtLeast("8", "8.0")).toBe(true);
  });
});

describe("the Mini App window", () => {
  test("outside Telegram nothing is installed and nothing is sent", () => {
    const posted = installProxy();
    captureTelegramLaunch(window);
    expect(initTelegramMiniApp(window, document)).toBeNull();
    expect(posted).toEqual([]);
    expect(document.documentElement.dataset.telegramMiniApp).toBeUndefined();
    expect(
      (window as unknown as { Telegram?: unknown }).Telegram,
    ).toBeUndefined();
  });

  test("the start sequence: requests, expand, swipes off, colours, back button, ready last", () => {
    const posted = installProxy();
    go(`/#${launchHash()}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    expect(document.documentElement.dataset.telegramMiniApp).toBe("true");
    const types = posted.map((p) => p.type);
    for (const want of [
      "web_app_request_viewport",
      "web_app_request_safe_area",
      "web_app_request_content_safe_area",
      "web_app_request_theme",
      "web_app_expand",
      "web_app_setup_swipe_behavior",
      "web_app_setup_back_button",
    ]) {
      expect(types).toContain(want);
    }
    expect(types.at(-1)).toBe("web_app_ready");
    expect(
      posted.find((p) => p.type === "web_app_setup_swipe_behavior")?.data,
    ).toEqual({ allow_vertical_swipe: false });
    expect(types.indexOf("web_app_expand")).toBeLessThan(
      types.indexOf("web_app_ready"),
    );
  });

  test("an old client is not sent what it does not know", () => {
    const posted = installProxy();
    go(`/#${launchHash({ tgWebAppVersion: "6.0" })}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    const types = posted.map((p) => p.type);
    expect(types).not.toContain("web_app_setup_swipe_behavior");
    expect(types).not.toContain("web_app_set_header_color");
    expect(types).not.toContain("web_app_set_bottom_bar_color");
    expect(types).not.toContain("web_app_setup_back_button");
    expect(types).toContain("web_app_expand");
    expect(types.at(-1)).toBe("web_app_ready");
  });

  test("viewport events set the visible, stable and hidden heights; insets are summed", () => {
    installProxy();
    vi.stubGlobal("innerHeight", 800);
    go(`/#${launchHash()}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    const root = document.documentElement;
    const v = (name: string) => root.style.getPropertyValue(name);
    expect(v("--coddy-telegram-hidden-bottom")).toBe("0px");

    receive("viewport_changed", {
      height: 440,
      is_state_stable: true,
      is_expanded: false,
    });
    expect(v("--coddy-telegram-viewport-height")).toBe("440px");
    expect(v("--coddy-telegram-stable-height")).toBe("440px");
    expect(v("--coddy-telegram-hidden-bottom")).toBe("360px");

    // A drag in progress moves the visible height, not the stable one.
    receive("viewport_changed", {
      height: 600,
      is_state_stable: false,
      is_expanded: false,
    });
    expect(v("--coddy-telegram-viewport-height")).toBe("600px");
    expect(v("--coddy-telegram-stable-height")).toBe("440px");

    receive("viewport_changed", {
      height: 800,
      is_state_stable: true,
      is_expanded: true,
    });
    expect(v("--coddy-telegram-hidden-bottom")).toBe("0px");

    receive("safe_area_changed", { top: 47, bottom: 34, left: 0, right: 0 });
    receive("content_safe_area_changed", {
      top: 46,
      bottom: 0,
      left: 0,
      right: 0,
    });
    expect(v("--coddy-telegram-safe-top")).toBe("93px");
    expect(v("--coddy-telegram-safe-bottom")).toBe("34px");
  });

  test("the header takes the theme's colours, again when the theme changes", async () => {
    const posted = installProxy();
    document.documentElement.style.setProperty(
      "--coddy-canvas-gradient-top",
      "#0f0f10",
    );
    document.documentElement.style.setProperty(
      "--coddy-canvas-gradient-bottom",
      "#0b0b0c",
    );
    go(`/#${launchHash()}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    expect(
      posted.find((p) => p.type === "web_app_set_header_color")?.data,
    ).toEqual({ color: "#0f0f10" });
    expect(
      posted.find((p) => p.type === "web_app_set_background_color")?.data,
    ).toEqual({ color: "#0b0b0c" });
    expect(
      posted.find((p) => p.type === "web_app_set_bottom_bar_color")?.data,
    ).toEqual({ color: "#0b0b0c" });

    document.documentElement.style.setProperty(
      "--coddy-canvas-gradient-top",
      "#ffffff",
    );
    document.documentElement.dataset.theme = "light";
    await new Promise((r) => setTimeout(r, 0));
    const headers = posted.filter((p) => p.type === "web_app_set_header_color");
    expect(headers.at(-1)?.data).toEqual({ color: "#ffffff" });
    document.documentElement.style.removeProperty(
      "--coddy-canvas-gradient-top",
    );
    document.documentElement.style.removeProperty(
      "--coddy-canvas-gradient-bottom",
    );
  });

  test("the theme follows Telegram until the user picks one", () => {
    installProxy();
    go(`/#${launchHash({ tgWebAppThemeParams: LIGHT })}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    expect(document.documentElement.dataset.theme).toBe("light");
    receive("theme_changed", { theme_params: { bg_color: "#212121" } });
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.cookie).not.toContain(CODDY_UI_THEME_COOKIE);

    // The user picks a theme in Appearance, which writes the cookie; the next
    // night mode of Telegram does not undo it.
    document.cookie = `${CODDY_UI_THEME_COOKIE}=nord; Path=/`;
    document.documentElement.dataset.theme = "nord";
    receive("theme_changed", { theme_params: { bg_color: "#ffffff" } });
    expect(document.documentElement.dataset.theme).toBe("nord");
    followTelegramTheme({ bg_color: "#ffffff" });
    expect(document.documentElement.dataset.theme).toBe("nord");
  });

  test("a change of what Telegram hides is announced as a resize, once per change", () => {
    installProxy();
    vi.stubGlobal("innerHeight", 800);
    go(`/#${launchHash()}`);
    captureTelegramLaunch(window);
    const resizes = vi.fn();
    window.addEventListener("resize", resizes);
    try {
      dispose = initTelegramMiniApp(window, document);
      expect(resizes).not.toHaveBeenCalled();
      receive("viewport_changed", {
        height: 440,
        is_state_stable: true,
        is_expanded: false,
      });
      expect(resizes).toHaveBeenCalledTimes(1);
      receive("viewport_changed", {
        height: 440,
        is_state_stable: true,
        is_expanded: false,
      });
      receive("safe_area_changed", { top: 0, bottom: 34, left: 0, right: 0 });
      expect(resizes).toHaveBeenCalledTimes(1);
      receive("viewport_changed", {
        height: 800,
        is_state_stable: true,
        is_expanded: true,
      });
      expect(resizes).toHaveBeenCalledTimes(2);
    } finally {
      window.removeEventListener("resize", resizes);
    }
  });

  test("brightness decides dark and light the way the SDK does", () => {
    expect(isDarkColor("#212121")).toBe(true);
    expect(isDarkColor("#ffffff")).toBe(false);
    expect(isDarkColor("#2481cc")).toBe(false);
    expect(isDarkColor("nonsense")).toBe(false);
  });
});

describe("the back button", () => {
  test("it is wanted off the start screen and over a layer", () => {
    go("/");
    expect(backButtonWanted(document)).toBe(false);
    go("/#/s/sess_1");
    expect(backButtonWanted(document)).toBe(true);
    go("/#/settings");
    expect(backButtonWanted(document)).toBe(true);
    go("/");
    const scrim = document.createElement("div");
    scrim.className = "mode-menu-backdrop";
    document.body.appendChild(scrim);
    expect(backButtonWanted(document)).toBe(true);
    expect(TELEGRAM_BACK_LAYERS).toContain(".confirm-dialog-backdrop");
  });

  test("a press is an Escape for whatever answers it", () => {
    go("/#/s/sess_1");
    const onKey = vi.fn((ev: KeyboardEvent) => ev.preventDefault());
    document.addEventListener("keydown", onKey, true);
    try {
      expect(pressBack(window, document)).toBe("escape");
      expect(onKey).toHaveBeenCalledOnce();
      expect(window.location.hash).toBe("#/s/sess_1");
    } finally {
      document.removeEventListener("keydown", onKey, true);
    }
  });

  test("an Escape nothing claimed leaves the conversation and never reaches the question card", () => {
    go("/#/s/sess_1");
    const questionCard = vi.fn((ev: KeyboardEvent) => {
      if (!ev.defaultPrevented) ev.preventDefault();
    });
    window.addEventListener("keydown", questionCard);
    try {
      expect(pressBack(window, document)).toBe("home");
      expect(questionCard).not.toHaveBeenCalled();
      expect(window.location.hash).toBe("");
    } finally {
      window.removeEventListener("keydown", questionCard);
    }
  });

  test("a dialog that stops the key on window answers the press, and the next keyboard Escape is left alone", () => {
    go("/#/s/sess_1");
    let dialogOpen = true;
    const dialog = vi.fn((ev: KeyboardEvent) => {
      if (ev.key === "Escape" && dialogOpen) {
        ev.preventDefault();
        ev.stopImmediatePropagation();
        dialogOpen = false;
      }
    });
    window.addEventListener("keydown", dialog, true);
    const questionCard = vi.fn();
    window.addEventListener("keydown", questionCard);
    try {
      expect(pressBack(window, document)).toBe("escape");
      expect(dialog).toHaveBeenCalledOnce();
      expect(window.location.hash).toBe("#/s/sess_1");
      // A real Escape from a keyboard afterwards reaches the page as always:
      // no listener of the press is left behind to take it.
      document.body.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
      expect(questionCard).toHaveBeenCalledOnce();
      expect(window.location.hash).toBe("#/s/sess_1");
    } finally {
      window.removeEventListener("keydown", dialog, true);
      window.removeEventListener("keydown", questionCard);
    }
  });

  // Telegram Web's Back is a button of the parent page: a click on it leaves
  // the frame's focus on body, and so does a tap on a sheet's title or on a
  // picture. A layer that heard Escape only with the focus inside would let
  // the press through to leaving the conversation.
  test("a press closes the composer's picker sheet wherever the focus is", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }));
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string) => {
        const path = new URL(String(input), "http://x").pathname;
        const items =
          path === "/coddy/slash-commands"
            ? [{ name: "review", description: "review skill" }]
            : [];
        return Promise.resolve({
          ok: true,
          json: async () => ({ items, has_more: false, page: 1 }),
        });
      }),
    );
    function Docked() {
      const [value, setValue] = useState("");
      return (
        <Composer
          value={value}
          isEmpty={false}
          sessionId="sess_1"
          mode="agent"
          modes={["agent", "plan"]}
          onModeChange={() => {}}
          onChange={setValue}
          onSend={() => {}}
        />
      );
    }
    go("/#/s/sess_1");
    render(<Docked />);
    const field = screen.getByRole("textbox", { name: "Message" });
    field.focus();
    fireEvent.change(field, {
      target: { value: "/rev", selectionStart: 4, selectionEnd: 4 },
    });
    await waitFor(() =>
      expect(document.querySelector(".slash-menu--sheet")).not.toBeNull(),
    );
    field.blur();
    expect(pressBack(window, document)).toBe("escape");
    await waitFor(() =>
      expect(document.querySelector(".slash-menu--sheet")).toBeNull(),
    );
    expect(window.location.hash).toBe("#/s/sess_1");
  });

  test("a press closes the image viewer wherever the focus is", async () => {
    go("/#/s/sess_1");
    const onClose = vi.fn();
    render(<ImageLightbox src="/p.png" alt="A picture" onClose={onClose} />);
    (document.activeElement as HTMLElement).blur();
    expect(pressBack(window, document)).toBe("escape");
    await new Promise((r) => setTimeout(r, 0));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(window.location.hash).toBe("#/s/sess_1");
  });

  test("the client hears when the button should show, and a press is answered", async () => {
    const posted = installProxy();
    go(`/#${launchHash()}`);
    captureTelegramLaunch(window);
    dispose = initTelegramMiniApp(window, document);
    const visibility = () =>
      posted
        .filter((p) => p.type === "web_app_setup_back_button")
        .map((p) => (p.data as { is_visible: boolean }).is_visible);
    expect(visibility()).toEqual([false]);
    go("/#/s/sess_1");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(visibility()).toEqual([false, true]);
    receive("back_button_pressed", "");
    await new Promise((r) => setTimeout(r, 0));
    expect(window.location.hash).toBe("");
    expect(visibility()).toEqual([false, true, false]);
  });
});

describe("telegram.css", () => {
  const css = readFileSync(join(here, "telegram.css"), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );
  const styles = readFileSync(join(here, "../../styles.css"), "utf8");
  const indexHtml = readFileSync(join(here, "../../index.html"), "utf8");

  test("every rule is scoped to a Mini App and to the stacked shell", () => {
    const selectors = [...css.matchAll(/([^{}]+)\{[^{}]*\}/g)].map((m) =>
      (m[1] ?? "").trim(),
    );
    expect(selectors.length).toBeGreaterThan(5);
    for (const sel of selectors) {
      for (const part of sel.split(/,(?![^(]*\))/)) {
        expect(part.trim()).toMatch(/^html\[data-telegram-mini-app="true"\]/);
      }
    }
    expect(css).not.toMatch(/@media\s*\(min-width/);
  });

  test("the page never clips its own overflow: the stacked shell's sticky chrome needs body to scroll the viewport", () => {
    expect(css).not.toMatch(/overflow-x/);
  });

  test("the docked composer and the sheets are lifted by the keyboard or the hidden part, whichever is more", () => {
    expect(css).toMatch(
      /--coddy-telegram-lift:\s*max\(\s*var\(--coddy-keyboard-inset, 0px\),\s*var\(--coddy-telegram-hidden-bottom, 0px\)\s*\)/,
    );
    const rule = (sel: RegExp) =>
      css.match(new RegExp(sel.source + String.raw`[^{]*\{([^}]*)\}`))?.[1] ??
      "";
    // The composer keeps its bottom on the keyboard inset, and the block over
    // the transcript - whose height ChatScreen measures as the transcript's
    // reserve - grows by what Telegram hides beyond the keyboard.
    const composer = rule(/\.chat-bottom:has\(\.composer-wrap-docked\) \{/);
    expect(composer).not.toMatch(/(^|[^-])bottom:/);
    // A block of its own, not padding: the reserve is read by a ResizeObserver,
    // which a change of padding does not wake.
    const inner = rule(
      /\.chat-bottom:has\(\.composer-wrap-docked\)\s+\.chat-bottom-inner::after/,
    );
    expect(inner).toMatch(
      /height:\s*max\(\s*0px,\s*var\(--coddy-telegram-hidden-bottom, 0px\)\s*-\s*var\(--coddy-keyboard-inset, 0px\)\s*\)/,
    );
    expect(inner).toMatch(/display:\s*block/);
    const chatScreen = readFileSync(
      join(here, "../chat/ChatScreen.tsx"),
      "utf8",
    );
    expect(chatScreen).toMatch(
      /className="chat-bottom-inner" ref=\{composerHostRef\}/,
    );
    expect(rule(/\.mode-menu--sheet,/)).toMatch(
      /bottom:\s*var\(--coddy-telegram-lift\)/,
    );
    // Sheets stay sheets: nothing turns them into centred panels.
    expect(css).not.toMatch(/translateY\(-50%\)/);
  });

  test("the top inset is set where the stacked shell sets it", () => {
    expect(css).toMatch(
      /html\[data-telegram-mini-app="true"\] \{[^}]*--coddy-mobile-bar-h:/,
    );
    expect(css).not.toMatch(/--coddy-mobile-top-inset:/);
  });

  test("nothing of Telegram is left in styles.css or index.html", () => {
    expect(styles).not.toMatch(/telegram/i);
    expect(indexHtml).not.toMatch(/telegram/i);
    expect(indexHtml).not.toMatch(/<script[^>]+src="https?:/);
  });
});
