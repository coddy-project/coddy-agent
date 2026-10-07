/**
 * Telegram's launch parameters (core.telegram.org/bots/webapps).
 *
 * A Telegram client that opens the web UI as a Mini App puts them into the
 * fragment of the address - `#tgWebAppData=...&tgWebAppVersion=...`, or after a
 * fragment the address already had, `#<route>?tgWebAppData=...`, the form the
 * official SDK parses - and a start parameter into the query
 * (`tgWebAppStartParam`). The fragment is also this SPA's router, so the
 * parameters are read once, before anything else reads the address (this
 * module is the first import of `main.tsx`), kept in `sessionStorage` for the
 * tab - a reload inside the Mini App is still a Mini App - and taken out of the
 * address with `history.replaceState`, so the router sees its own route and
 * nothing that stores the address (the environment switch, the Docs reader)
 * keeps the signed launch data.
 *
 * The bot's own link to a conversation carries the session in the query,
 * `?session=<id>`, because the fragment is Telegram's; it is honoured with or
 * without a launch, since a group gets the link as an ordinary button that
 * opens the browser. Inside a launch, a start parameter that is a session id
 * (`t.me/<bot>?startapp=sess_...`) is read the same way. Only the alphabet of a
 * session id is taken; anything else is dropped from the address and opens the
 * start screen.
 *
 * Nothing here is an identity: the launch data is kept as Telegram signed it
 * for a server that may check it, and the SPA never trusts it.
 */

import { appNavHrefSession } from "../scheduler/hashRoute";

export type TelegramLaunch = {
  /** The raw launch data (`tgWebAppData`), exactly as Telegram signed it. */
  initData: string;
  /** The Bot API version of the client (`tgWebAppVersion`). */
  version: string;
  /** The client's platform (`tgWebAppPlatform`). */
  platform: string;
  /** The client's theme (`tgWebAppThemeParams`), colours as `#rrggbb`. */
  themeParams: Record<string, string>;
  /** The start parameter of a direct link (`tgWebAppStartParam`). */
  startParam: string;
};

export const TELEGRAM_LAUNCH_STORAGE_KEY = "coddy.telegram.launch";

/** The query parameter the bot's link names a session with. */
export const SESSION_QUERY_PARAM = "session";

/** A session id as the server accepts one (internal/swarm ValidateSessionID). */
const SESSION_ID = /^[A-Za-z0-9_-]{1,256}$/;

/** A start parameter that names a session: only the ids the server mints. */
const START_PARAM_SESSION = /^sess_[A-Za-z0-9_-]{1,251}$/;

let current: TelegramLaunch | null = null;

/** telegramLaunch is the launch this tab was opened with, or null outside Telegram. */
export function telegramLaunch(): TelegramLaunch | null {
  return current;
}

/** resetTelegramLaunchForTests forgets the captured launch. */
export function resetTelegramLaunchForTests(): void {
  current = null;
}

function isTelegramKey(key: string): boolean {
  return key.startsWith("tgWebApp");
}

function parseThemeParams(raw: string | null): Record<string, string> {
  if (!raw) {
    return {};
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === "string") {
        out[k] = v;
      }
    }
    return out;
  } catch {
    return {};
  }
}

export type ParsedLaunchAddress = {
  /** The launch, when the address carries one. */
  launch: TelegramLaunch | null;
  /** The session the address asks to open, "" for none. */
  session: string;
  /** The address without the launch parameters and the session parameter. */
  cleanSearch: string;
  cleanHash: string;
  /** Whether anything was taken out (the address has to be rewritten). */
  changed: boolean;
};

/**
 * parseLaunchAddress reads a launch and a session out of the query and the
 * fragment of an address, and says what the address is without them. A launch
 * is a non-empty `tgWebAppData` or `tgWebAppVersion`: a start parameter alone
 * is not one, since anyone can type it into an address.
 */
export function parseLaunchAddress(
  search: string,
  hash: string,
): ParsedLaunchAddress {
  const query = new URLSearchParams(search.replace(/^\?/, ""));
  let fragment = hash.replace(/^#/, "");
  let route = fragment;
  let fragmentParams = new URLSearchParams();
  const q = fragment.indexOf("?");
  if (q >= 0) {
    route = fragment.slice(0, q);
    fragmentParams = new URLSearchParams(fragment.slice(q + 1));
  } else if (fragment.includes("=")) {
    route = "";
    fragmentParams = new URLSearchParams(fragment);
  }
  const value = (key: string): string =>
    (fragmentParams.get(key) ?? query.get(key) ?? "").trim();

  const initData = value("tgWebAppData");
  const version = value("tgWebAppVersion");
  const launch: TelegramLaunch | null =
    initData || version
      ? {
          initData,
          version,
          platform: value("tgWebAppPlatform"),
          themeParams: parseThemeParams(
            fragmentParams.get("tgWebAppThemeParams") ??
              query.get("tgWebAppThemeParams"),
          ),
          startParam: value("tgWebAppStartParam"),
        }
      : null;

  const startParam = value("tgWebAppStartParam");
  let session = (query.get(SESSION_QUERY_PARAM) ?? "").trim();
  if (!SESSION_ID.test(session)) {
    session = "";
  }
  if (!session && launch && START_PARAM_SESSION.test(startParam)) {
    session = startParam;
  }

  let changed = false;
  for (const key of [...query.keys()]) {
    if (isTelegramKey(key) || key === SESSION_QUERY_PARAM) {
      query.delete(key);
      changed = true;
    }
  }
  for (const key of [...fragmentParams.keys()]) {
    if (isTelegramKey(key)) {
      fragmentParams.delete(key);
      changed = true;
    }
  }
  const rest = fragmentParams.toString();
  if (session) {
    fragment = appNavHrefSession(session).replace(/^#/, "");
    changed = true;
  } else {
    fragment = rest ? `${route}?${rest}` : route;
  }
  const cleanSearch = query.toString();
  return {
    launch,
    session,
    cleanSearch: cleanSearch ? `?${cleanSearch}` : "",
    cleanHash: fragment ? `#${fragment}` : "",
    changed,
  };
}

function readStoredLaunch(win: Window): TelegramLaunch | null {
  try {
    const raw = win.sessionStorage.getItem(TELEGRAM_LAUNCH_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<TelegramLaunch>;
    if (!parsed || (!parsed.initData && !parsed.version)) {
      return null;
    }
    return {
      initData: String(parsed.initData ?? ""),
      version: String(parsed.version ?? ""),
      platform: String(parsed.platform ?? ""),
      themeParams:
        parsed.themeParams && typeof parsed.themeParams === "object"
          ? (parsed.themeParams as Record<string, string>)
          : {},
      startParam: String(parsed.startParam ?? ""),
    };
  } catch {
    return null;
  }
}

/**
 * captureTelegramLaunch reads the launch out of the address of win, keeps it
 * for the tab, takes the launch parameters and the session parameter out of
 * the address, and returns the launch this tab is a Mini App of - the one in
 * the address, else the one kept by an earlier load of the tab - or null.
 */
export function captureTelegramLaunch(
  win: Window = window,
): TelegramLaunch | null {
  const parsed = parseLaunchAddress(win.location.search, win.location.hash);
  let launch = parsed.launch;
  if (launch) {
    try {
      win.sessionStorage.setItem(
        TELEGRAM_LAUNCH_STORAGE_KEY,
        JSON.stringify(launch),
      );
    } catch {
      /* a tab without storage stays a Mini App until it reloads */
    }
  } else {
    launch = readStoredLaunch(win);
  }
  if (parsed.changed) {
    win.history.replaceState(
      win.history.state,
      "",
      `${win.location.pathname}${parsed.cleanSearch}${parsed.cleanHash}`,
    );
  }
  current = launch;
  return launch;
}
