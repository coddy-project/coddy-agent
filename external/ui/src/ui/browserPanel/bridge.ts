/**
 * The frame's half of the bridge to the side panel of Coddy for Browser.
 * Before a turn is sent, the web UI asks the panel for the tab the person
 * writes from; the panel gives that tab to the turn's chat (its tab group) and
 * answers with it, and the turn carries it as metadata.browser. Messages go to
 * the panel's origin only and are taken only from the parent window at that
 * origin. The panel can also ask the frame to show a chat of this server: the
 * one that holds the tab the person looks at (startPanelNavigation).
 */
import { browserPanelLaunch } from "./launch";

export const WEBUI_SOURCE = "coddy-web-ui";
export const PANEL_SOURCE = "coddy-for-browser";

/** The tab a turn is written from, as the server reads metadata.browser. */
export type PanelTab = {
  browser_id: string;
  tab_id: number;
  url: string;
  title: string;
};

/** How long the panel has to answer; a turn is not held up longer. */
export const PANEL_ANSWER_MS = 3000;

let nextRequest = 1;

function panelTab(v: unknown): PanelTab | null {
  if (!v || typeof v !== "object") return null;
  const t = v as Record<string, unknown>;
  if (
    typeof t.browser_id !== "string" ||
    typeof t.tab_id !== "number" ||
    !Number.isInteger(t.tab_id) ||
    t.tab_id <= 0
  )
    return null;
  return {
    browser_id: t.browser_id,
    tab_id: t.tab_id,
    url: typeof t.url === "string" ? t.url : "",
    title: typeof t.title === "string" ? t.title : "",
  };
}

/**
 * panelTabForTurn asks the panel for the tab of a turn of sessionId. It
 * answers null outside the panel, when the page in front of the person is not
 * one Coddy may use, and when the panel does not answer in time: the turn is
 * sent either way.
 */
export function panelTabForTurn(
  sessionId: string,
  title: string,
  win: Window = window,
  timeoutMs: number = PANEL_ANSWER_MS,
): Promise<PanelTab | null> {
  const launch = browserPanelLaunch();
  if (!launch || !sessionId || win.parent === win) return Promise.resolve(null);
  const requestId = `turn-${nextRequest++}`;
  return new Promise((resolve) => {
    const done = (tab: PanelTab | null) => {
      win.clearTimeout(timer);
      win.removeEventListener("message", onMessage);
      resolve(tab);
    };
    const onMessage = (ev: MessageEvent) => {
      if (ev.source !== win.parent || ev.origin !== launch.origin) return;
      const msg = ev.data as Record<string, unknown> | null;
      if (
        !msg ||
        msg.source !== PANEL_SOURCE ||
        msg.type !== "turn" ||
        msg.requestId !== requestId
      )
        return;
      done(panelTab(msg.tab));
    };
    const timer = win.setTimeout(() => done(null), timeoutMs);
    win.addEventListener("message", onMessage);
    win.parent.postMessage(
      {
        source: WEBUI_SOURCE,
        type: "turn",
        requestId,
        sessionId,
        title: title.slice(0, 60),
      },
      launch.origin,
    );
  });
}

/** The colours of the page the panel paints its own strip with. */
export type PanelTheme = {
  scheme: "dark" | "light";
  bg: string;
  surface: string;
  border: string;
  text: string;
  muted: string;
  accent: string;
};

/** Where each colour of PanelTheme comes from in the web UI's tokens. */
const THEME_TOKENS: Record<Exclude<keyof PanelTheme, "scheme">, string> = {
  bg: "--coddy-canvas-gradient-top",
  surface: "--coddy-glass-panel-bg",
  border: "--coddy-glass-panel-border",
  text: "--text",
  muted: "--muted",
  accent: "--accent",
};

/**
 * schemeOf reads whether a computed colour is a dark one: rgb() or rgba(),
 * or color(srgb r g b) with channels from 0 to 1, which Chromium answers for
 * a colour written with color-mix().
 */
export function schemeOf(color: string): "dark" | "light" {
  const legacy = /rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/.exec(color);
  const srgb = /color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)/.exec(color);
  const m = legacy ?? srgb;
  if (!m) return "dark";
  const scale = legacy ? 255 : 1;
  const [r, g, b] = [m[1], m[2], m[3]].map((v) => Number(v) / scale) as [
    number,
    number,
    number,
  ];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b < 0.5 ? "dark" : "light";
}

/**
 * panelTheme resolves the web UI's tokens to computed colours, rgb() or
 * color(srgb ...): a token may be written with var() or color-mix(), and the
 * panel takes nothing but colours from the frame.
 */
export function panelTheme(doc: Document = document): PanelTheme {
  const win = doc.defaultView ?? window;
  const probe = doc.createElement("span");
  probe.style.display = "none";
  (doc.body ?? doc.documentElement).appendChild(probe);
  const colours = {} as Record<Exclude<keyof PanelTheme, "scheme">, string>;
  try {
    for (const [name, token] of Object.entries(THEME_TOKENS) as [
      Exclude<keyof PanelTheme, "scheme">,
      string,
    ][]) {
      probe.style.color = "";
      probe.style.color = `var(${token})`;
      colours[name] = win.getComputedStyle(probe).color;
    }
  } finally {
    probe.remove();
  }
  return { scheme: schemeOf(colours.bg), ...colours };
}

/**
 * startPanelTheme tells the panel the colours of this page, now and whenever
 * the theme changes, so the panel's own strip above the frame looks the same.
 * Outside the panel it does nothing.
 */
export function startPanelTheme(win: Window = window): () => void {
  const launch = browserPanelLaunch();
  if (!launch || win.parent === win) return () => {};
  const doc = win.document;
  let last = "";
  const post = () => {
    const theme = panelTheme(doc);
    const key = JSON.stringify(theme);
    if (key === last) return;
    last = key;
    win.parent.postMessage(
      { source: WEBUI_SOURCE, type: "theme", theme },
      launch.origin,
    );
  };
  post();
  // The frame's own MutationObserver, so a test can hand in a window of its own.
  const Observer =
    (win as Window & { MutationObserver?: typeof MutationObserver })
      .MutationObserver ?? MutationObserver;
  const observer = new Observer(post);
  observer.observe(doc.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => observer.disconnect();
}

/** A chat of this server, as the panel names one: a chat of another Coddy
 * process (`<node>/<session id>`) is not one this web UI can open. */
const SESSION_ID = /^sess_[A-Za-z0-9_-]{1,128}$/;

/**
 * startPanelNavigation lets the panel open a chat of this server in the frame:
 * on `{type: "open", sessionId}` the address becomes `#/s/<sessionId>`, so a
 * chat that works in the person's tab shows its turn live. Only the parent
 * window at the panel's origin is heard, only a session id is taken, and only
 * while local() says the web UI shows the panel's own server.
 */
export function startPanelNavigation(
  local: () => boolean,
  win: Window = window,
): () => void {
  const launch = browserPanelLaunch();
  if (!launch || win.parent === win) return () => {};
  const onMessage = (ev: MessageEvent) => {
    if (ev.source !== win.parent || ev.origin !== launch.origin) return;
    const msg = ev.data as Record<string, unknown> | null;
    if (!msg || msg.source !== PANEL_SOURCE || msg.type !== "open") return;
    if (typeof msg.sessionId !== "string" || !SESSION_ID.test(msg.sessionId))
      return;
    if (!local()) return;
    win.location.hash = `#/s/${msg.sessionId}`;
  };
  win.addEventListener("message", onMessage);
  return () => win.removeEventListener("message", onMessage);
}
