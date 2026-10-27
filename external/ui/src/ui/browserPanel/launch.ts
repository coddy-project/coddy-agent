/**
 * The side panel of Coddy for Browser shows this web UI in a frame. The panel
 * loads it with two query parameters: `coddy_panel`, a one-time ticket the
 * extension's endpoint issued for the extension key, which signs the frame in
 * (POST /coddy/auth/browser), and `coddy_panel_origin`, the panel's own
 * origin, the only one the frame talks to (bridge.ts).
 *
 * They are read once, before anything else reads the address (capture.ts is
 * imported first by main.tsx), kept in `sessionStorage` for the frame - a
 * reload is still the panel's - and taken out of the address, so the router
 * and everything that stores the address never see the ticket. The ticket is
 * good once: it is handed out once (takeBrowserPanelTicket) and forgotten.
 */

export type BrowserPanelLaunch = {
  /** The one-time sign-in ticket, "" once taken. */
  ticket: string;
  /** The panel's origin: chrome-extension://<id> or moz-extension://<uuid>. */
  origin: string;
};

export const BROWSER_PANEL_STORAGE_KEY = "coddy.browserPanel.launch";
export const PANEL_TICKET_PARAM = "coddy_panel";
export const PANEL_ORIGIN_PARAM = "coddy_panel_origin";

/**
 * An extension's origin: a Chromium extension's id is 32 letters a to p, a
 * Firefox extension's is a UUID of this installation.
 */
const EXTENSION_ORIGIN =
  /^(chrome-extension:\/\/[a-p]{32}|moz-extension:\/\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
/** A ticket as the endpoint issues one: 64 hex digits. */
const TICKET = /^[a-f0-9]{64}$/;

let current: BrowserPanelLaunch | null = null;

/** browserPanelLaunch is the panel this frame belongs to, or null. */
export function browserPanelLaunch(): BrowserPanelLaunch | null {
  return current;
}

function save(win: Window, launch: BrowserPanelLaunch): void {
  try {
    win.sessionStorage.setItem(
      BROWSER_PANEL_STORAGE_KEY,
      JSON.stringify(launch),
    );
  } catch {
    /* a frame without storage stays the panel's until it reloads */
  }
}

function readStored(win: Window): BrowserPanelLaunch | null {
  try {
    const raw = win.sessionStorage.getItem(BROWSER_PANEL_STORAGE_KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<BrowserPanelLaunch>;
    if (typeof v.origin !== "string" || !EXTENSION_ORIGIN.test(v.origin))
      return null;
    return {
      origin: v.origin,
      ticket:
        typeof v.ticket === "string" && TICKET.test(v.ticket) ? v.ticket : "",
    };
  } catch {
    return null;
  }
}

/**
 * captureBrowserPanelLaunch reads the panel's parameters out of the address of
 * win, keeps them for the frame and takes them out of the address. A frame
 * that is not the top window is the only one that may be a panel.
 */
export function captureBrowserPanelLaunch(
  win: Window = window,
): BrowserPanelLaunch | null {
  const params = new URLSearchParams(win.location.search);
  const named =
    params.has(PANEL_TICKET_PARAM) || params.has(PANEL_ORIGIN_PARAM);
  let launch: BrowserPanelLaunch | null = null;
  if (named) {
    const origin = params.get(PANEL_ORIGIN_PARAM) || "";
    const ticket = params.get(PANEL_TICKET_PARAM) || "";
    if (EXTENSION_ORIGIN.test(origin) && win.parent !== win) {
      launch = { origin, ticket: TICKET.test(ticket) ? ticket : "" };
      save(win, launch);
    }
    params.delete(PANEL_TICKET_PARAM);
    params.delete(PANEL_ORIGIN_PARAM);
    const search = params.toString();
    win.history.replaceState(
      win.history.state,
      "",
      `${win.location.pathname}${search ? `?${search}` : ""}${win.location.hash}`,
    );
  }
  current = launch ?? (win.parent !== win ? readStored(win) : null);
  if (current) win.document.documentElement.dataset.browserPanel = "true";
  return current;
}

/** takeBrowserPanelTicket hands the sign-in ticket out once. */
export function takeBrowserPanelTicket(win: Window = window): string {
  if (!current?.ticket) return "";
  const ticket = current.ticket;
  current = { ...current, ticket: "" };
  save(win, current);
  return ticket;
}

/** resetBrowserPanelLaunchForTests forgets the panel this page belongs to. */
export function resetBrowserPanelLaunchForTests(): void {
  current = null;
}
